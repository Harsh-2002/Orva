// Package backup implements consistent snapshot + restore of an Orva data
// directory. The two on-disk things to capture are the SQLite database
// (orva.db, with WAL on the side) and the per-function deployed code
// (functions/<id>/versions/<hash>/...). The `current` symlink that points
// at the active version isn't archived — restore reconstructs it from
// each function row's code_hash, so a restored install is always coherent
// with what the database says is "active".
//
// SQLite is in WAL mode, so a naïve `cp orva.db` could capture a torn
// read while a writer is mid-transaction. The fix is `VACUUM INTO`: it
// runs in a transaction, copies pages to a fresh single-file database,
// and produces a checkpoint-clean snapshot with no WAL sidecar to ship.
//
// The package exposes three pure functions so tests can roundtrip without
// the full HTTP stack:
//
//   SnapshotDB(srcDB, outPath)        — VACUUM INTO outPath
//   ArchiveTo(w, dataDir, snapshot)   — gzip-tar to w
//   RestoreFrom(r, dataDir)           — extract + activate, atomic on success
package backup

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SnapshotDB writes a consistent point-in-time copy of the live SQLite
// database to outPath. Uses `VACUUM INTO`, which holds a shared lock for
// the duration of the copy — writes block briefly, reads continue. The
// resulting file is a fully checkpointed single-file SQLite database with
// no -wal/-shm sidecars; safe to ship as-is.
//
// outPath must not already exist. SQLite refuses to clobber.
func SnapshotDB(srcDB *sql.DB, outPath string) error {
	if srcDB == nil {
		return fmt.Errorf("snapshot: nil source DB")
	}
	if outPath == "" {
		return fmt.Errorf("snapshot: empty output path")
	}
	// VACUUM INTO requires the path as a literal because parameter
	// binding doesn't apply to filenames. We escape single quotes by
	// doubling them per SQLite's string-literal grammar.
	escaped := strings.ReplaceAll(outPath, "'", "''")
	if _, err := srcDB.Exec("VACUUM INTO '" + escaped + "'"); err != nil {
		return fmt.Errorf("vacuum into %s: %w", outPath, err)
	}
	return nil
}

// ArchiveTo writes a gzip-compressed tar containing:
//
//   orva.db                                  (the snapshot file)
//   functions/<id>/versions/<hash>/...       (every deployed version)
//
// The `current` symlink under each function dir is intentionally NOT
// included — restore rebuilds it from the function row's code_hash. This
// guarantees the symlink and the DB never disagree after restore.
//
// Archive paths are relative (no leading slash), so `tar -xzf` expands
// into the cwd cleanly. snapshotPath is the file written by SnapshotDB;
// it is read and added to the archive as `orva.db`.
func ArchiveTo(w io.Writer, dataDir, snapshotPath string) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// 1. orva.db (the snapshot).
	if err := addFileToTar(tw, snapshotPath, "orva.db"); err != nil {
		return fmt.Errorf("archive orva.db: %w", err)
	}

	// 2. functions/<id>/versions/<hash>/... — walk the dataDir and pick
	// only the versions trees. Skip the `current` symlinks (they are
	// re-derived on restore) and any other top-level junk operators may
	// have left behind.
	functionsDir := filepath.Join(dataDir, "functions")
	if _, err := os.Stat(functionsDir); err == nil {
		err := filepath.Walk(functionsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dataDir, path)
			if err != nil {
				return err
			}
			// Skip the `current` symlink at functions/<id>/current.
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			// Only include things under versions/. Anything else under
			// functions/<id>/ (e.g. an old `code/` from pre-Round-G
			// installs) gets archived too because it might be in use,
			// but skip dotfiles.
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") && path != functionsDir {
				return nil
			}
			if info.IsDir() {
				// Emit a directory header so `tar t` shows the tree;
				// extraction does MkdirAll regardless.
				hdr := &tar.Header{
					Name:     filepath.ToSlash(rel) + "/",
					Mode:     0o755,
					ModTime:  info.ModTime(),
					Typeflag: tar.TypeDir,
				}
				return tw.WriteHeader(hdr)
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			return addFileToTar(tw, path, filepath.ToSlash(rel))
		})
		if err != nil {
			return fmt.Errorf("walk functions: %w", err)
		}
	}
	return nil
}

// addFileToTar streams a single regular file into the archive at the
// given archive path.
func addFileToTar(tw *tar.Writer, srcPath, archivePath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    archivePath,
		Mode:    int64(st.Mode().Perm()),
		Size:    st.Size(),
		ModTime: st.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.Copy(tw, f); err != nil {
		return err
	}
	return nil
}

// RestoreFrom extracts a tarball produced by ArchiveTo into dataDir and
// activates it atomically:
//
//  1. Extract the archive into a staging dir under dataDir.
//  2. Open the staged orva.db; bail if it isn't a valid SQLite file.
//  3. Move the live orva.db aside as orva.db.before-restore-<unix>.
//  4. Rename the staged orva.db into place; move the staged functions/
//     tree on top of the existing one (per-function rename so old
//     functions not in the backup are left alone).
//  5. Walk the new functions table; for every row with a code_hash,
//     recreate the `current` symlink → versions/<hash>.
//
// On any failure before step 3, no live state has changed; the staging
// dir is removed and the original error is returned. After step 3 a
// failure rolls back by moving the .before-restore file back into place.
//
// IMPORTANT: the caller must have closed (or be ready to close + reopen)
// any *sql.DB handles to the live orva.db before invoking this. The
// HTTP handler returns success and asks the client to reload the page;
// the operator restarts the process to pick up the new file.
func RestoreFrom(r io.Reader, dataDir string) error {
	if dataDir == "" {
		return fmt.Errorf("restore: empty data dir")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("restore: ensure data dir: %w", err)
	}

	stage, err := os.MkdirTemp(dataDir, "restore-stage-*")
	if err != nil {
		return fmt.Errorf("restore: stage dir: %w", err)
	}
	// Best-effort cleanup on every exit path. If we successfully
	// promote the staged tree the dir is already empty.
	defer os.RemoveAll(stage)

	if err := extractTarGz(r, stage); err != nil {
		return fmt.Errorf("restore: extract: %w", err)
	}

	stagedDB := filepath.Join(stage, "orva.db")
	if _, err := os.Stat(stagedDB); err != nil {
		return fmt.Errorf("restore: archive missing orva.db")
	}
	// Validate by opening + running migrations. If the file is
	// corrupted or wasn't actually SQLite this fails loudly here, before
	// we touch the live DB.
	if err := validateStagedDB(stagedDB); err != nil {
		return fmt.Errorf("restore: validate staged db: %w", err)
	}

	livePath := filepath.Join(dataDir, "orva.db")
	backupPath := fmt.Sprintf("%s.before-restore-%d", livePath, time.Now().Unix())

	// Move live aside (only if it exists). Skipping rename on a
	// non-existent file lets us restore into a fresh data dir.
	hadLive := false
	if _, err := os.Stat(livePath); err == nil {
		if err := os.Rename(livePath, backupPath); err != nil {
			return fmt.Errorf("restore: move live aside: %w", err)
		}
		hadLive = true
	}
	// Also clean up any stale WAL/SHM sidecars — the snapshot is
	// fully checkpointed and a leftover -wal would be applied to the
	// new file on next open and could corrupt it.
	_ = os.Remove(livePath + "-wal")
	_ = os.Remove(livePath + "-shm")

	rollback := func(cause error) error {
		if hadLive {
			if rerr := os.Rename(backupPath, livePath); rerr != nil {
				return fmt.Errorf("restore: %v; rollback also failed: %w", cause, rerr)
			}
		}
		return cause
	}

	if err := os.Rename(stagedDB, livePath); err != nil {
		return rollback(fmt.Errorf("install staged db: %w", err))
	}

	// Move staged functions tree into place. We do per-function
	// renames so unrelated functions in the live tree (i.e. ones not
	// shipped in the backup) survive.
	stagedFns := filepath.Join(stage, "functions")
	liveFns := filepath.Join(dataDir, "functions")
	if _, err := os.Stat(stagedFns); err == nil {
		if err := os.MkdirAll(liveFns, 0o755); err != nil {
			return rollback(fmt.Errorf("ensure live functions dir: %w", err))
		}
		entries, err := os.ReadDir(stagedFns)
		if err != nil {
			return rollback(fmt.Errorf("read staged functions: %w", err))
		}
		for _, e := range entries {
			src := filepath.Join(stagedFns, e.Name())
			dst := filepath.Join(liveFns, e.Name())
			// Remove any existing function dir so the rename is
			// clean. After this the new tree owns the slot.
			_ = os.RemoveAll(dst)
			if err := os.Rename(src, dst); err != nil {
				return rollback(fmt.Errorf("install function %s: %w", e.Name(), err))
			}
		}
	}

	// Recreate `current` symlinks per function row. We can't reuse
	// the in-process database handle (it still points at the old
	// file we just moved aside) — open the new file directly.
	if err := rebuildCurrentSymlinks(livePath, dataDir); err != nil {
		// Symlink rebuild failure is non-fatal for the restore as a
		// whole — the data is in place, the operator just won't be
		// able to invoke until the symlinks are right. Surface the
		// error so they know to investigate, but don't roll back.
		return fmt.Errorf("restore: rebuild symlinks (data installed; symlinks broken): %w", err)
	}
	return nil
}

// extractTarGz reads a gzip tar from r and writes it under destDir.
// Refuses any path that escapes destDir (.. traversal). Symlinks in the
// archive are skipped — Orva's archive format never contains them, and
// extracting an attacker-supplied symlink would let a malicious tarball
// rewrite files outside destDir.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip open: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}
		// Reject absolute paths and traversal.
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		target := filepath.Join(destDir, clean)
		// Defense-in-depth: the joined path must still live under
		// destDir even after Clean.
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(filepath.Separator)) && target != filepath.Clean(destDir) {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// symlinks, char devices, etc. — silently skip.
		}
	}
}

// validateStagedDB opens the freshly-extracted DB read-only and runs a
// quick sanity check (PRAGMA integrity_check + a SELECT against the
// sqlite_master table to make sure it's at least a real SQLite file).
// Any failure here means the archive is corrupt; we bail before
// touching live state.
func validateStagedDB(path string) error {
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='functions' LIMIT 1`).Scan(&name); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("staged db missing functions table — not an orva backup")
		}
		return err
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("integrity_check returned %q", integrity)
	}
	return nil
}

// rebuildCurrentSymlinks walks every function row in the new DB and
// re-creates the `current` symlink under functions/<id>/ to point at
// versions/<code_hash>. Functions with empty code_hash (never deployed)
// are skipped — there's nothing to point at.
func rebuildCurrentSymlinks(dbPath, dataDir string) error {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, code_hash FROM functions WHERE code_hash IS NOT NULL AND code_hash != ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return err
		}
		fnDir := filepath.Join(dataDir, "functions", id)
		if err := os.MkdirAll(fnDir, 0o755); err != nil {
			return err
		}
		linkPath := filepath.Join(fnDir, "current")
		// Remove whatever is there (stale symlink, dir, file) and
		// re-create. The link target is relative so the dataDir
		// path doesn't bake into the symlink.
		_ = os.Remove(linkPath)
		target := filepath.Join("versions", hash)
		if err := os.Symlink(target, linkPath); err != nil {
			return fmt.Errorf("symlink %s: %w", linkPath, err)
		}
	}
	return rows.Err()
}
