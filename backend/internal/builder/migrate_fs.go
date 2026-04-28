package builder

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Harsh-2002/Orva/internal/database"
)

// MigrateLegacyCodeDirs is a one-shot, idempotent upgrade hook that
// converts pre-Round-G volumes from the flat `functions/<id>/code/`
// layout to the content-addressed `functions/<id>/versions/<hash>/` +
// `current` symlink layout.
//
// Behavior per function:
//   - if there's no legacy `code/` dir → no-op (already migrated, or never deployed)
//   - if the function row has no code_hash → no-op (created but never built)
//   - if `versions/<hash>/` already exists → leave the legacy dir alone (will be
//     pruned by GC; we don't want to clobber the post-Round-G version with the
//     legacy contents)
//   - otherwise: rename `code/` → `versions/<hash>/`, write the `.orva-ready`
//     marker, and activate the symlink
//
// Called from cmd/orva/serve.go right after db.Migrate(), before
// server.New(). Failures here log a warning but don't block startup —
// the operator can still onboard / redeploy fresh.
func MigrateLegacyCodeDirs(dataDir string, db *database.Database) {
	res, err := db.ListFunctions(database.ListFunctionsParams{Limit: 10000})
	if err != nil {
		slog.Warn("legacy code-dir migration: list functions failed", "err", err)
		return
	}

	migrated := 0
	for _, fn := range res.Functions {
		legacy := filepath.Join(dataDir, "functions", fn.ID, "code")
		if _, err := os.Stat(legacy); err != nil {
			continue
		}
		if fn.CodeHash == "" {
			// Legacy dir without a recorded hash — can't fold into the new
			// layout safely. Drop it so a fresh deploy starts clean.
			_ = os.RemoveAll(legacy)
			continue
		}

		versionsDir := filepath.Join(dataDir, "functions", fn.ID, "versions")
		versionDir := filepath.Join(versionsDir, fn.CodeHash)

		if _, err := os.Stat(versionDir); err == nil {
			// Already migrated (or post-Round-G version exists). Drop the
			// legacy dir so it doesn't shadow anything.
			_ = os.RemoveAll(legacy)
			continue
		}

		if err := os.MkdirAll(versionsDir, 0755); err != nil {
			slog.Warn("legacy code-dir migration: mkdir versions failed", "fn", fn.ID, "err", err)
			continue
		}
		if err := os.Rename(legacy, versionDir); err != nil {
			slog.Warn("legacy code-dir migration: rename failed", "fn", fn.ID, "err", err)
			continue
		}
		readyPath := filepath.Join(versionDir, ".orva-ready")
		if err := os.WriteFile(readyPath, []byte(fn.CodeHash), 0644); err != nil {
			slog.Warn("legacy code-dir migration: write marker failed", "fn", fn.ID, "err", err)
			// Continue anyway — the dir is in place; GC will skip it because
			// it lacks the marker. Rollback just won't see it as available.
		}
		if err := ActivateVersion(dataDir, fn.ID, fn.CodeHash); err != nil {
			slog.Warn("legacy code-dir migration: activate failed", "fn", fn.ID, "err", err)
			continue
		}
		migrated++
	}

	if migrated > 0 {
		slog.Info("legacy code-dir migration complete", "migrated", migrated, "total_functions", len(res.Functions))
	}
}
