package builder

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
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
type GC struct {
	dataDir string
	db      *database.Database
}

// NewGC returns a GC bound to the data dir + DB.
func NewGC(dataDir string, db *database.Database) *GC {
	return &GC{dataDir: dataDir, db: db}
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

	res, err := g.db.ListFunctions(database.ListFunctionsParams{Limit: 10000})
	if err != nil {
		slog.Warn("version gc: list functions failed", "err", err)
		return
	}

	pruned := 0
	for _, fn := range res.Functions {
		if ctx.Err() != nil {
			return
		}
		pruned += g.pruneFunction(fn.ID, keep)
	}
	if pruned > 0 {
		slog.Info("version gc tick", "pruned", pruned, "kept_per_fn", keep)
	}
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
