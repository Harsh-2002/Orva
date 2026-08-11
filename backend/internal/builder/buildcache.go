package builder

// Per-function dependency caches.
//
// Dependency installs run in a jail whose /tmp is a throwaway directory, so
// npm's cacache and pip's http cache died with every build and each deploy
// re-downloaded every dependency. This file owns the persistent replacement:
// one cache directory PER FUNCTION at <dataDir>/build-cache/<fnID>/{npm,pip}.
//
// Per-function is the whole design, not an implementation detail. A cache
// shared across functions is an arbitrary-code-execution channel between them:
// npm stores the packument in the same cacache as the tarball, keyed by URL and
// guarded only by an unkeyed corruption checksum, so a forged packument whose
// dist.integrity matches a poisoned tarball installs the poison on a plain
// `npm install` (the cached cache-control headers are attacker-controlled too,
// so npm never revalidates). pip's http cache is URL-keyed with no content
// verification on read at all. Since `npm install` runs whatever postinstall
// script a package ships, one function with a bad dependency could poison every
// later build of every other function. Do not "simplify" this into a shared
// directory.
//
// It deliberately does NOT live under functions/<id>/: that tree is the
// deployable artifact (snapshotted, GC'd by version, served from `current`),
// and a mutable multi-hundred-MB cache has no business inside it.
//
// The cache is bounded three ways, because a cache with no bound is strictly
// worse than no cache: an age sweep, a global size cap evicting whole
// per-function directories LRU, and an explicit purge (on delete, and via
// DELETE /api/v1/functions/{id}/build-cache — the only clean recovery from a
// build that installed something malicious).

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// buildCacheDirName / functionsDirName / buildScratchDirName are the three
	// per-function trees under the data dir.
	buildCacheDirName   = "build-cache"
	functionsDirName    = "functions"
	buildScratchDirName = "build-tmp"

	// BuildCacheMaxAgeKey bounds how long an unused per-function cache is kept.
	// 0 disables the age sweep (the size cap still applies).
	BuildCacheMaxAgeKey     = "build_cache_max_age_days"
	DefaultBuildCacheMaxAge = 14

	// BuildCacheMaxMBKey caps the total size of all per-function caches.
	// 0 disables caching's size bound; -1 style values are treated as 0.
	BuildCacheMaxMBKey     = "build_cache_max_mb"
	DefaultBuildCacheMaxMB = 2048
)

// buildCacheGrace protects a cache that a build may still be using. Every
// build stamps its cache dir's mtime on the way in (see PrepareBuildCache), so
// "modified recently" means "possibly in use". Eviction also tries the
// function lock; this is the second line of defence for callers without one.
const buildCacheGrace = 15 * time.Minute

// ErrUnsafeFunctionID rejects an id that could escape its parent directory.
// Everything in this file removes directories recursively, so the id → path
// conversion is the one place that has to be paranoid.
var ErrUnsafeFunctionID = errors.New("unsafe function id for a filesystem path")

// fnScopedPath joins <dataDir>/<sub>/<fnID> and refuses any id that is not a
// single, plain path element. A function id is a UUIDv7 in practice; this
// guard is what makes that a property of the code rather than of the caller.
func fnScopedPath(dataDir, sub, fnID string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", fmt.Errorf("%w: empty data dir", ErrUnsafeFunctionID)
	}
	if fnID == "" ||
		strings.ContainsAny(fnID, `/\`+"\x00") ||
		strings.ContainsRune(fnID, os.PathSeparator) ||
		strings.HasPrefix(fnID, ".") ||
		filepath.Base(fnID) != fnID ||
		filepath.Clean(fnID) != fnID {
		return "", fmt.Errorf("%w: %q", ErrUnsafeFunctionID, fnID)
	}
	return filepath.Join(dataDir, sub, fnID), nil
}

// BuildCacheDir returns the per-function cache directory. It does not create it.
func BuildCacheDir(dataDir, fnID string) (string, error) {
	return fnScopedPath(dataDir, buildCacheDirName, fnID)
}

// FunctionDir returns the per-function code tree (versions/ + `current`).
func FunctionDir(dataDir, fnID string) (string, error) {
	return fnScopedPath(dataDir, functionsDirName, fnID)
}

// PrepareBuildCache creates the function's cache directory and stamps its
// mtime as "in use now". The stamp is load-bearing twice over: the directory's
// own mtime otherwise only moves when a direct child is added (so it would
// read as creation time, not last use), and both the LRU eviction order and
// the in-flight-build guard key off it.
//
// Returns "" with no error when caching is not configured (empty dataDir),
// which callers pass straight through as "no persistent cache".
func PrepareBuildCache(dataDir, fnID string) (string, error) {
	if dataDir == "" || fnID == "" {
		return "", nil
	}
	dir, err := BuildCacheDir(dataDir, fnID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", fmt.Errorf("create build cache dir: %w", err)
	}
	// MkdirAll honours the umask; chmod is what actually grants the mode. Same
	// 0777 as the build scratch dir, for the ORVA_DISABLE_USERNS=1 case where
	// nsjail may run the build under a different uid.
	if err := os.Chmod(dir, 0o777); err != nil {
		return "", fmt.Errorf("prepare build cache dir: %w", err)
	}
	now := time.Now()
	if err := os.Chtimes(dir, now, now); err != nil {
		// Non-fatal: a cache that cannot be stamped is still usable, it just
		// looks older than it is to the sweeper.
		slog.Debug("build cache: touch failed", "dir", dir, "err", err)
	}
	return dir, nil
}

// PurgeBuildCache removes one function's cache. Missing is success.
func PurgeBuildCache(dataDir, fnID string) error {
	dir, err := BuildCacheDir(dataDir, fnID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// PurgeFunctionFiles removes every on-disk trace of a function: its code tree
// (versions/ + `current`) and its build cache. Called after the DB row is
// gone. Deleting a function used to leave functions/<id>/ behind forever —
// the GC only ever walks functions that still exist in the database, so an
// orphaned tree was never revisited.
func PurgeFunctionFiles(dataDir, fnID string) error {
	fnDir, err := FunctionDir(dataDir, fnID)
	if err != nil {
		return err
	}
	var errs []error
	if err := os.RemoveAll(fnDir); err != nil {
		errs = append(errs, fmt.Errorf("remove function dir: %w", err))
	}
	if err := PurgeBuildCache(dataDir, fnID); err != nil {
		errs = append(errs, fmt.Errorf("remove build cache: %w", err))
	}
	return errors.Join(errs...)
}

// SweepBuildScratch removes leftover build-* scratch directories at boot.
// RunBuild defers its own cleanup, which covers a live process and nothing
// else: a crash or a kill -9 mid-install leaks the whole npm working set
// (tens of MB per in-flight build) under <dataDir>/build-tmp permanently.
func SweepBuildScratch(dataDir string) {
	if dataDir == "" {
		return
	}
	base := filepath.Join(dataDir, buildScratchDirName)
	entries, err := os.ReadDir(base)
	if err != nil {
		return // no scratch base yet — nothing to sweep
	}
	swept, freed := 0, int64(0)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "build-") {
			continue
		}
		path := filepath.Join(base, e.Name())
		size := dirSizeBytes(path)
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("build scratch sweep: remove failed", "path", path, "err", err)
			continue
		}
		swept++
		freed += size
	}
	if swept > 0 {
		slog.Info("swept stale build scratch dirs", "count", swept, "freed_mb", freed/(1024*1024))
	}
}

// cacheEntry is one per-function cache directory as the sweeper sees it.
type cacheEntry struct {
	fnID  string
	path  string
	mtime time.Time
	bytes int64
}

// listBuildCaches enumerates the per-function caches. sizeOf lets the GC
// memoize sizes across ticks so an untouched cache is not re-walked.
func listBuildCaches(dataDir string, sizeOf func(fnID, path string, mtime time.Time) int64) []cacheEntry {
	if dataDir == "" {
		// Joining onto "" would resolve relative to the working directory.
		return nil
	}
	root := filepath.Join(dataDir, buildCacheDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	if sizeOf == nil {
		sizeOf = func(_, path string, _ time.Time) int64 { return dirSizeBytes(path) }
	}
	out := make([]cacheEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, cacheEntry{
			fnID:  e.Name(),
			path:  path,
			mtime: info.ModTime(),
			bytes: sizeOf(e.Name(), path, info.ModTime()),
		})
	}
	return out
}

// evictBuildCache removes one cache directory, refusing when the function lock
// cannot be taken (a build of that function is running and npm may be reading
// from the very directory we would delete). tryLock may be nil.
func evictBuildCache(e cacheEntry, tryLock func(fnID string) (release func(), ok bool)) bool {
	if tryLock != nil {
		release, ok := tryLock(e.fnID)
		if !ok {
			return false
		}
		defer release()
	}
	if err := os.RemoveAll(e.path); err != nil {
		slog.Warn("build cache: evict failed", "path", e.path, "err", err)
		return false
	}
	return true
}

// dirSizeBytes sums the apparent size of a tree. Errors are skipped rather
// than propagated: a partially-readable cache should still be measurable
// enough to be evicted.
func dirSizeBytes(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // unreadable entries just don't count
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// BuildCacheUsageBytes is the total size of every per-function cache.
func BuildCacheUsageBytes(dataDir string) int64 {
	var total int64
	for _, e := range listBuildCaches(dataDir, nil) {
		total += e.bytes
	}
	return total
}

// EvictIdleBuildCaches drops every cache not touched within buildCacheGrace
// and returns the bytes reclaimed. This is the low-disk lever: a build cache
// is pure optimisation, so it must never be the reason a deploy fails for want
// of space. Caches a build may still be using are left alone.
// EvictIdleBuildCaches reclaims caches no build has touched within
// buildCacheGrace. tryLock is the per-function try-lock; passing nil evicts
// without it, which is only safe when no build can be running.
//
// The mtime grace alone is NOT sufficient protection: PrepareBuildCache stamps
// the directory once at build START and never again, so an install running
// longer than the grace looks idle while it is actively using the cache. This
// runs on the deploy path, where concurrent builds of other functions are
// normal, so it must hold the lock as the GC does.
func EvictIdleBuildCaches(dataDir string, tryLock func(fnID string) (func(), bool)) int64 {
	cutoff := time.Now().Add(-buildCacheGrace)
	var freed int64
	for _, e := range listBuildCaches(dataDir, nil) {
		if e.mtime.After(cutoff) {
			continue
		}
		if evictBuildCache(e, tryLock) {
			freed += e.bytes
		}
	}
	if freed > 0 {
		slog.Info("build caches evicted to reclaim disk", "freed_mb", freed/(1024*1024))
	}
	return freed
}
