package builder

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// GC prunes archived version directories beyond a configurable retention
// count. Always preserves the version that `current` resolves to,
// regardless of the retention setting — operators can never accidentally
// GC the actively-serving code.
//
// GC is intentionally conservative:
//   - sleeps the full interval before the first pass (avoids deleting on
//     boot if the operator just restarted to inspect old versions)
//   - reads the retention count fresh on each tick so operators can tune
//     it without restarting
//   - skips scratch dirs (".tmp.<rand>") so an in-flight build is never
//     touched
//
// The same tick also bounds the per-function dependency caches
// (build_cache_max_age_days / build_cache_max_mb) and reclaims function
// directories whose function no longer exists.
type GC struct {
	dataDir string
	db      *database.Database

	// FnLock is the per-function mutex shared with the build queue. Optional;
	// when set, nothing that a build could be writing is removed while that
	// build holds the lock. The GC only ever TRIES the lock — it must not
	// block a whole tick behind a long npm install.
	FnLock func(fnID string) *sync.Mutex

	// cacheSizes memoizes the measured size of each per-function cache,
	// keyed by fn id. Walking a multi-hundred-MB npm cacache on every tick
	// would be wasteful, and PrepareBuildCache stamps the directory's mtime
	// on every build, so an unchanged mtime means an unchanged size.
	cacheSizes map[string]cacheSizeMemo

	// listLimit is the page size used to enumerate functions. Overridden in
	// tests to exercise the truncated-listing guard.
	listLimit int
}

type cacheSizeMemo struct {
	mtime time.Time
	bytes int64
}

// NewGC returns a GC bound to the data dir + DB.
func NewGC(dataDir string, db *database.Database) *GC {
	return &GC{dataDir: dataDir, db: db, cacheSizes: map[string]cacheSizeMemo{}, listLimit: 10000}
}

// Run is the long-running goroutine. Stops cleanly on ctx cancel.
func (g *GC) Run(ctx context.Context) {
	intervalSec := g.db.GetSystemConfigInt("gc_interval_seconds", 300)
	if intervalSec < 30 {
		intervalSec = 30
	}
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	slog.Info("version gc started", "interval_s", intervalSec)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.tick(ctx)
		}
	}
}

func (g *GC) tick(ctx context.Context) {
	keep := g.db.GetSystemConfigInt("versions_to_keep", 5)
	if keep < 1 {
		keep = 1
	}

	limit := g.listLimit
	if limit <= 0 {
		limit = 10000
	}
	res, err := g.db.ListFunctions(database.ListFunctionsParams{Limit: limit})
	if err != nil {
		slog.Warn("version gc: list functions failed", "err", err)
		return
	}

	pruned := 0
	live := make(map[string]struct{}, len(res.Functions))
	for _, fn := range res.Functions {
		live[fn.ID] = struct{}{}
		if ctx.Err() != nil {
			return
		}
		pruned += g.pruneFunction(fn.ID, keep)
	}
	if pruned > 0 {
		slog.Info("version gc tick", "pruned", pruned, "kept_per_fn", keep)
	}

	// The listing is the authority for "which functions still exist", so both
	// sweeps below are only safe on a COMPLETE listing. A truncated page would
	// make every function past the limit look deleted.
	complete := res.Total <= len(res.Functions)
	if ctx.Err() != nil || !complete {
		if !complete {
			slog.Warn("gc: function listing truncated, skipping orphan sweeps",
				"listed", len(res.Functions), "total", res.Total)
		}
		return
	}
	g.sweepBuildCaches(ctx, live)
	g.sweepOrphanFunctionDirs(ctx, live)
}

// tryFnLock takes the per-function lock without blocking. The bool is false
// when a build holds it, which the callers read as "leave this one alone".
func (g *GC) tryFnLock(fnID string) (func(), bool) {
	if g.FnLock == nil {
		return func() {}, true
	}
	lk := g.FnLock(fnID)
	if !lk.TryLock() {
		return nil, false
	}
	return lk.Unlock, true
}

// sweepBuildCaches enforces the three bounds on the per-function dependency
// caches: they are dropped when their function is gone, when they have gone
// unused for build_cache_max_age_days, and — oldest-first, whole directories
// at a time — when the total exceeds build_cache_max_mb.
//
// Whole directories, never individual files: npm's cacache and pip's http
// cache both carry an index that would then reference content that is no
// longer there, which is a worse state than an empty cache.
func (g *GC) sweepBuildCaches(ctx context.Context, live map[string]struct{}) {
	maxAgeDays := g.db.GetSystemConfigInt(BuildCacheMaxAgeKey, DefaultBuildCacheMaxAge)
	maxMB := g.db.GetSystemConfigInt(BuildCacheMaxMBKey, DefaultBuildCacheMaxMB)

	caches := listBuildCaches(g.dataDir, g.cachedSize)
	if len(caches) == 0 {
		return
	}

	now := time.Now()
	graceCutoff := now.Add(-buildCacheGrace)
	ageCutoff := now.AddDate(0, 0, -maxAgeDays)

	evicted, freed := 0, int64(0)
	kept := make([]cacheEntry, 0, len(caches))
	for _, e := range caches {
		if ctx.Err() != nil {
			return
		}
		orphan := false
		if _, ok := live[e.fnID]; !ok {
			// The function is gone. Delete purges the cache directly; this
			// catches the cases it could not — a crash between the two, or a
			// row removed while the daemon was down.
			orphan = true
		}
		stale := maxAgeDays > 0 && e.mtime.Before(ageCutoff)
		if !orphan && !stale {
			kept = append(kept, e)
			continue
		}
		if e.mtime.After(graceCutoff) {
			kept = append(kept, e) // stamped recently — a build may be writing it
			continue
		}
		if evictBuildCache(e, g.tryFnLock) {
			delete(g.cacheSizes, e.fnID)
			evicted++
			freed += e.bytes
			continue
		}
		kept = append(kept, e)
	}

	// Size cap: evict least-recently-used whole caches until under the cap.
	if maxMB > 0 {
		var total int64
		for _, e := range kept {
			total += e.bytes
		}
		capBytes := int64(maxMB) * 1024 * 1024
		if total > capBytes {
			// Oldest first — mtime is stamped at the start of every build, so
			// this is genuine least-recently-used order.
			sort.Slice(kept, func(i, j int) bool { return kept[i].mtime.Before(kept[j].mtime) })
			for _, e := range kept {
				if total <= capBytes || ctx.Err() != nil {
					break
				}
				if e.mtime.After(graceCutoff) {
					continue // a build may be writing into it right now
				}
				if !evictBuildCache(e, g.tryFnLock) {
					continue
				}
				delete(g.cacheSizes, e.fnID)
				total -= e.bytes
				evicted++
				freed += e.bytes
			}
			if total > capBytes {
				slog.Warn("build cache still over cap after eviction",
					"used_mb", total/(1024*1024), "cap_mb", maxMB)
			}
		}
	}

	if evicted > 0 {
		slog.Info("build cache gc", "evicted", evicted, "freed_mb", freed/(1024*1024),
			"max_age_days", maxAgeDays, "max_mb", maxMB)
	}
}

// cachedSize returns the memoized size when the directory has not been touched
// since it was measured. Caches inside the grace window are always re-measured
// and never memoized: a build may be actively growing one, so a size captured
// mid-install would otherwise stick forever.
func (g *GC) cachedSize(fnID, path string, mtime time.Time) int64 {
	if g.cacheSizes == nil {
		g.cacheSizes = map[string]cacheSizeMemo{}
	}
	if time.Since(mtime) < buildCacheGrace {
		return dirSizeBytes(path)
	}
	if memo, ok := g.cacheSizes[fnID]; ok && memo.mtime.Equal(mtime) {
		return memo.bytes
	}
	size := dirSizeBytes(path)
	g.cacheSizes[fnID] = cacheSizeMemo{mtime: mtime, bytes: size}
	return size
}

// sweepOrphanFunctionDirs removes functions/<id>/ trees whose function no
// longer exists. Delete now removes them inline; this reclaims what leaked
// before that fix (deleting a function never touched the disk, and this GC
// only ever walked functions still present in the database, so the tree was
// orphaned permanently).
//
// Two guards, because this removes user code: the per-function lock must be
// free, and the tree must not have been written to recently — which covers the
// window between the function listing above and this scan, where a function
// created in between would look like an orphan.
func (g *GC) sweepOrphanFunctionDirs(ctx context.Context, live map[string]struct{}) {
	root := filepath.Join(g.dataDir, functionsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-buildCacheGrace)
	removed, freed := 0, int64(0)
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if !e.IsDir() {
			continue
		}
		if _, ok := live[e.Name()]; ok {
			continue
		}
		path := filepath.Join(root, e.Name())
		if lastWrite(path).After(cutoff) {
			continue // young enough that a build could be creating it
		}
		release, ok := g.tryFnLock(e.Name())
		if !ok {
			continue
		}
		size := dirSizeBytes(path)
		err := os.RemoveAll(path)
		release()
		if err != nil {
			slog.Warn("orphan function dir: remove failed", "path", path, "err", err)
			continue
		}
		removed++
		freed += size
	}
	if removed > 0 {
		slog.Info("removed orphaned function dirs", "count", removed, "freed_mb", freed/(1024*1024))
	}
}

// lastWrite is the newest of a function dir's own mtime and its versions/
// subdirectory's. A directory's mtime only moves when a DIRECT child changes,
// so functions/<id>/ looks untouched during a redeploy — versions/ is where
// the new directory actually appears.
func lastWrite(fnDir string) time.Time {
	var newest time.Time
	for _, p := range []string{fnDir, filepath.Join(fnDir, "versions")} {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

// pruneFunction returns how many version dirs were removed for this fn.
func (g *GC) pruneFunction(fnID string, keep int) int {
	versionsDir := filepath.Join(g.dataDir, "functions", fnID, "versions")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return 0
	}

	type candidate struct {
		name  string
		mtime time.Time
	}
	cands := make([]candidate, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip in-flight scratch dirs.
		if strings.Contains(e.Name(), ".tmp.") {
			continue
		}
		// Skip half-built versions (no .orva-ready marker). They're either
		// scratch debris or a crashed build; either way leave them — a
		// later boot's defer cleanup or a manual sweep handles them.
		if _, err := os.Stat(filepath.Join(versionsDir, e.Name(), ".orva-ready")); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cands = append(cands, candidate{name: e.Name(), mtime: info.ModTime()})
	}

	if len(cands) <= keep {
		return 0
	}

	// Active hash always survives, regardless of mtime ordering.
	activeHash := ResolveActiveHash(g.dataDir, fnID)

	// Sort newest first; keep the top `keep`, prune the rest (minus active).
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })

	pruned := 0
	for i, c := range cands {
		if i < keep {
			continue
		}
		if c.name == activeHash {
			continue // active always survives even if mtime is old
		}
		path := filepath.Join(versionsDir, c.name)
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("version gc: remove failed", "path", path, "err", err)
			continue
		}
		slog.Info("version gc'd", "fn", fnID, "hash", c.name[:min(12, len(c.name))])
		pruned++
	}
	return pruned
}
