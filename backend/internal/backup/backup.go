// Package backup implements consistent point-in-time snapshot + restore
// of an Orva data directory. The snapshot is the entire operator-visible
// state: SQLite database, per-function deployed code, encryption keys.
// One file on disk → one file in the archive; restore is byte-faithful.
//
// On-disk things that get captured:
//
//	orva.db                                   the SQLite database (VACUUM INTO'd)
//	keys/master.key                           AES key that decrypts function secrets
//	keys/admin.key                            bootstrap admin API key
//	functions/<id>/versions/<hash>/...        deployed code for every function
//
// On-disk things deliberately omitted:
//
//	functions/<id>/current                    symlink — rebuilt on restore from
//	                                          each function row's code_hash so DB
//	                                          and disk can never disagree
//	-wal / -shm                               WAL sidecars — the snapshot is fully
//	                                          checkpointed and a stale WAL on
//	                                          restore would corrupt the new DB
//
// SQLite is in WAL mode, so a naïve `cp orva.db` could capture a torn read
// while a writer is mid-transaction. `VACUUM INTO` runs in a transaction,
// copies pages to a fresh single-file database, and produces a
// checkpoint-clean snapshot with no WAL sidecar to ship.
//
// Format
//
// Every archive carries a `manifest.json` at the root listing format_version,
// the producing Orva binary's version, created_at (RFC3339 UTC), and a
// sha256 + size per file. Restore validates every file's checksum against
// the manifest BEFORE touching live state — a half-uploaded or
// bit-rotted archive is rejected, not partially applied.
//
// The package exposes pure functions so tests can roundtrip without the
// full HTTP stack:
//
//	SnapshotDB(srcDB, outPath)        — VACUUM INTO outPath
//	ArchiveTo(w, dataDir, snapshot)   — gzip-tar to w with manifest
//	RestoreFrom(r, dataDir)           — extract + verify + activate atomic
package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/version"
)

// FormatVersion is the on-disk archive layout version. Bump on any
// breaking change (new required field, layout change). The restore path
// REFUSES to restore an archive whose format_version is greater than this
// constant — that's the "newer Orva produced a snapshot you can't read"
// guard. Older format_versions are still accepted (backward-compatible
// reads).
const FormatVersion = 1

// ErrBadArchive is returned by RestoreFrom when the supplied bytes are
// not a valid manifest-bearing tar.gz produced by ArchiveTo. The HTTP
// layer maps this to 400 BAD_ARCHIVE so the client knows the request
// body — not the server — is at fault.
var ErrBadArchive = errors.New("malformed archive")

// ErrIncompatibleFormat is returned when the archive's format_version is
// newer than this binary's FormatVersion. Operators see this when they
// try to restore a newer snapshot on an older binary.
var ErrIncompatibleFormat = errors.New("archive format newer than this binary supports")

// Manifest describes the snapshot's contents. Embedded as manifest.json
// at the root of the tarball. Restore parses + validates this before
// touching any live state, so a corrupt or truncated archive can't
// half-overwrite the running system.
type Manifest struct {
	FormatVersion int                   `json:"format_version"`
	OrvaVersion   string                `json:"orva_version"`
	CreatedAt     string                `json:"created_at"` // RFC3339 UTC
	FunctionCount int                   `json:"function_count"`
	ContainsKeys  bool                  `json:"contains_keys"`
	Files         map[string]FileDigest `json:"files"`
}

// FileDigest records the size and sha256 of one archived file. The
// manifest's Files map is keyed by archive-relative path (e.g.
// "orva.db", "keys/master.key", "functions/fn_abc/versions/h1/handler.js").
type FileDigest struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"` // hex
}

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

// ArchiveTo writes a gzip-compressed, manifest-bearing tar containing:
//
//	manifest.json                            (sha256 inventory + metadata)
//	orva.db                                  (the snapshot file)
//	keys/master.key                          (if present at dataDir/.master.key)
//	keys/admin.key                           (if present at dataDir/.admin-key)
//	functions/<id>/versions/<hash>/...       (every deployed version)
//
// Strategy: two-pass. First pass walks the dataDir collecting paths +
// sizes + sha256s into a manifest. Second pass streams the manifest
// followed by every file. This costs one extra read of every file but
// guarantees the manifest's checksums match exactly what got archived
// (a single-pass approach where we compute checksums while streaming
// would put the manifest at the END of the tar, forcing restore to read
// the whole archive before validating it).
func ArchiveTo(w io.Writer, dataDir, snapshotPath string) error {
	plan, err := planArchive(dataDir, snapshotPath)
	if err != nil {
		return err
	}

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// Stream manifest.json first so restore can read it without
	// scanning the whole archive.
	manifestBytes, err := json.MarshalIndent(plan.manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeTarBytes(tw, "manifest.json", manifestBytes, time.Now().UTC()); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	// Then stream every planned file in deterministic order.
	for _, item := range plan.items {
		if item.isDir {
			hdr := &tar.Header{
				Name:     item.archivePath + "/",
				Mode:     0o755,
				ModTime:  item.modTime,
				Typeflag: tar.TypeDir,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			continue
		}
		if err := addFileToTar(tw, item.srcPath, item.archivePath); err != nil {
			return fmt.Errorf("archive %s: %w", item.archivePath, err)
		}
	}
	return nil
}

// archiveItem is one entry in the archive plan. Directories ride along
// for `tar t` readability; the actual extraction does MkdirAll regardless.
type archiveItem struct {
	srcPath     string    // absolute path on disk
	archivePath string    // tar-relative path (no leading slash, forward slashes)
	isDir       bool
	modTime     time.Time
}

// archivePlan is the result of the first archive pass. items is the
// ordered file list to stream; manifest is the metadata to write at the
// top of the tarball.
type archivePlan struct {
	items    []archiveItem
	manifest Manifest
}

// planArchive walks the data dir and produces an ordered list of files
// to stream, plus a manifest with sha256 of every file. The single-pass
// would be cheaper but would put the manifest at the END of the tar; we
// pay the re-read cost to put it at the FRONT so restore can validate
// before unpacking.
func planArchive(dataDir, snapshotPath string) (*archivePlan, error) {
	plan := &archivePlan{
		manifest: Manifest{
			FormatVersion: FormatVersion,
			OrvaVersion:   version.Version,
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
			Files:         map[string]FileDigest{},
		},
	}

	// 1. orva.db — always present.
	if err := planFile(plan, snapshotPath, "orva.db"); err != nil {
		return nil, fmt.Errorf("plan orva.db: %w", err)
	}

	// 2. Encryption + admin keys — both optional but should normally
	// exist on a running install. Skipping silently is correct for
	// migration scenarios (e.g. backup taken on a host that lost its
	// .admin-key for some reason).
	masterKeyPath := filepath.Join(dataDir, ".master.key")
	if _, err := os.Stat(masterKeyPath); err == nil {
		if err := planFile(plan, masterKeyPath, "keys/master.key"); err != nil {
			return nil, fmt.Errorf("plan master.key: %w", err)
		}
	}
	adminKeyPath := filepath.Join(dataDir, ".admin-key")
	if _, err := os.Stat(adminKeyPath); err == nil {
		if err := planFile(plan, adminKeyPath, "keys/admin.key"); err != nil {
			return nil, fmt.Errorf("plan admin.key: %w", err)
		}
	}
	if _, hasKey := plan.manifest.Files["keys/master.key"]; hasKey {
		plan.manifest.ContainsKeys = true
	}

	// 3. functions/<id>/versions/<hash>/... — filtered to the IDs
	// present in the snapshot so orphaned on-disk trees don't ride
	// along (saves bytes and avoids re-creating orphans on restore).
	liveIDs, err := loadLiveFunctionIDs(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("load live fn ids: %w", err)
	}
	plan.manifest.FunctionCount = len(liveIDs)
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
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") && path != functionsDir {
				return nil
			}
			if id, ok := fnIDFromRel(rel); ok {
				if _, live := liveIDs[id]; !live {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			if info.IsDir() {
				plan.items = append(plan.items, archiveItem{
					srcPath:     path,
					archivePath: filepath.ToSlash(rel),
					isDir:       true,
					modTime:     info.ModTime(),
				})
				return nil
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			return planFile(plan, path, filepath.ToSlash(rel))
		})
		if err != nil {
			return nil, fmt.Errorf("walk functions: %w", err)
		}
	}
	return plan, nil
}

// planFile reads the file's size + sha256 and appends both an items
// entry and a manifest entry. Used for orva.db, the keys, and every
// regular file under functions/.
func planFile(plan *archivePlan, srcPath, archivePath string) error {
	st, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", srcPath)
	}
	digest, err := sha256File(srcPath)
	if err != nil {
		return err
	}
	plan.items = append(plan.items, archiveItem{
		srcPath:     srcPath,
		archivePath: archivePath,
		modTime:     st.ModTime(),
	})
	plan.manifest.Files[archivePath] = FileDigest{
		Size:   st.Size(),
		SHA256: digest,
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, modTime time.Time) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(data)),
		ModTime: modTime,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// fnIDFromRel extracts the function ID component from an archive-relative
// path. Paths look like "functions" (the root, no id), or
// "functions/<id>/...". Returns ("", false) for the root and any path that
// isn't under functions/.
func fnIDFromRel(rel string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || parts[0] != "functions" {
		return "", false
	}
	id := parts[1]
	if id == "" {
		return "", false
	}
	return id, true
}

// loadLiveFunctionIDs reads the set of function IDs from the snapshot
// database. The snapshot is a fully-checkpointed SQLite file written by
// VACUUM INTO — opening it read-only is safe even while the live DB is
// taking writes.
func loadLiveFunctionIDs(snapshotPath string) (map[string]struct{}, error) {
	db, err := sql.Open("sqlite", snapshotPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id FROM functions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
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
//  2. Parse + validate manifest.json: format compatibility, every file's
//     sha256 matches what's on staging-disk after extraction.
//  3. Open the staged orva.db; bail if it isn't a valid SQLite file.
//  4. Move the live orva.db aside as orva.db.before-restore-<unix>.
//  5. Rename the staged orva.db into place; move the staged keys + functions/
//     trees on top of the existing ones.
//  6. Walk the new functions table; for every row with a code_hash,
//     recreate the `current` symlink → versions/<hash>.
//
// On any failure before step 4, no live state has changed; the staging
// dir is removed and the original error is returned. After step 4 a
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

	manifest, err := readAndVerifyManifest(stage)
	if err != nil {
		return err
	}

	stagedDB := filepath.Join(stage, "orva.db")
	if _, err := os.Stat(stagedDB); err != nil {
		return fmt.Errorf("%w: archive missing orva.db", ErrBadArchive)
	}
	// Validate by opening + integrity-check. If the file is corrupted
	// or wasn't actually SQLite this fails loudly here, before we touch
	// the live DB. Validation failures point at the supplied file, not
	// the server, so flag them as bad-archive too.
	if err := validateStagedDB(stagedDB); err != nil {
		return fmt.Errorf("%w: validate staged db: %v", ErrBadArchive, err)
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

	// Move keys into place (if the archive shipped them). Keys are
	// per-host and overwriting is the right thing — the operator
	// asked for a snapshot-faithful restore.
	stagedKeys := filepath.Join(stage, "keys")
	if _, err := os.Stat(stagedKeys); err == nil {
		moves := []struct{ src, dst string }{
			{filepath.Join(stagedKeys, "master.key"), filepath.Join(dataDir, ".master.key")},
			{filepath.Join(stagedKeys, "admin.key"), filepath.Join(dataDir, ".admin-key")},
		}
		for _, m := range moves {
			if _, err := os.Stat(m.src); err != nil {
				continue
			}
			// Tight perms — these files are secrets.
			if err := os.Rename(m.src, m.dst); err != nil {
				return rollback(fmt.Errorf("install %s: %w", filepath.Base(m.dst), err))
			}
			_ = os.Chmod(m.dst, 0o600)
		}
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

	_ = manifest // keep available in case we surface stats later
	return nil
}

// readAndVerifyManifest reads manifest.json from the staging dir and
// verifies every file's sha256 against the extracted bytes. Returns the
// parsed manifest on success. Failures (missing manifest, hash mismatch,
// format too new) are wrapped as ErrBadArchive / ErrIncompatibleFormat
// so the HTTP layer maps them to 400 cleanly.
//
// Legacy archives without a manifest.json are tolerated: this is the
// path a v1-format-aware binary takes when restoring a snapshot from
// before manifests existed. The cost is no checksum verification on
// those old archives — but they were the only contract at the time, so
// rejecting them outright would break operators mid-upgrade.
func readAndVerifyManifest(stage string) (*Manifest, error) {
	manifestPath := filepath.Join(stage, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Legacy archive — proceed without verification.
			return &Manifest{FormatVersion: 0, Files: map[string]FileDigest{}}, nil
		}
		return nil, fmt.Errorf("%w: read manifest: %v", ErrBadArchive, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: parse manifest: %v", ErrBadArchive, err)
	}
	if m.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("%w: archive format_version=%d, this binary supports up to %d",
			ErrIncompatibleFormat, m.FormatVersion, FormatVersion)
	}
	// Verify every file in the manifest exists on staged disk and its
	// sha256 + size match. Files in the tar that AREN'T in the
	// manifest are tolerated (forward-compat: a future format might
	// add files the older binary doesn't know about).
	for archivePath, want := range m.Files {
		stagedFile := filepath.Join(stage, filepath.FromSlash(archivePath))
		st, err := os.Stat(stagedFile)
		if err != nil {
			return nil, fmt.Errorf("%w: manifest lists %s but file missing", ErrBadArchive, archivePath)
		}
		if st.Size() != want.Size {
			return nil, fmt.Errorf("%w: %s size mismatch (manifest=%d on-disk=%d)",
				ErrBadArchive, archivePath, want.Size, st.Size())
		}
		got, err := sha256File(stagedFile)
		if err != nil {
			return nil, fmt.Errorf("%w: hash %s: %v", ErrBadArchive, archivePath, err)
		}
		if got != want.SHA256 {
			return nil, fmt.Errorf("%w: %s sha256 mismatch", ErrBadArchive, archivePath)
		}
	}
	return &m, nil
}

// extractTarGz reads a gzip tar from r and writes it under destDir.
// Refuses any path that escapes destDir (.. traversal). Symlinks in the
// archive are skipped — Orva's archive format never contains them, and
// extracting an attacker-supplied symlink would let a malicious tarball
// rewrite files outside destDir.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		// Wrap the gzip error so the HTTP layer can map it to 400
		// BAD_ARCHIVE — the client supplied a non-gzip body.
		return fmt.Errorf("%w: gzip open: %v", ErrBadArchive, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: tar read: %v", ErrBadArchive, err)
		}
		// Reject absolute paths and traversal.
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
			return fmt.Errorf("%w: unsafe path in archive: %s", ErrBadArchive, hdr.Name)
		}
		target := filepath.Join(destDir, clean)
		// Defense-in-depth: the joined path must still live under
		// destDir even after Clean.
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(filepath.Separator)) && target != filepath.Clean(destDir) {
			return fmt.Errorf("%w: unsafe path in archive: %s", ErrBadArchive, hdr.Name)
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
