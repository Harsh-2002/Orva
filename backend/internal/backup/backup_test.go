package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/database"
)

// TestSnapshotArchiveRestoreRoundtrip exercises the full backup +
// restore cycle: build a small data dir, snapshot the DB, archive it,
// then restore into a *fresh* empty data dir and verify both that the
// row reads back AND that the function's `current` symlink was
// reconstructed from the row's code_hash.
func TestSnapshotArchiveRestoreRoundtrip(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	dbPath := filepath.Join(srcDir, "orva.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Insert a function row with a known code_hash so we can verify
	// the symlink rebuild on restore.
	fn := &database.Function{
		ID:          "fn_backup_test",
		Name:        "backup-test",
		Runtime:     "node22",
		Entrypoint:  "handler.js",
		TimeoutMS:   30000,
		MemoryMB:    64,
		CPUs:        0.5,
		EnvVars:     map[string]string{},
		NetworkMode: "none",
		Status:      "active",
		CodeHash:    "abc123hash",
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatalf("insert function: %v", err)
	}

	// Lay down the on-disk version directory + an unrelated `current`
	// symlink that should NOT be carried into the archive — restore
	// has to rebuild it.
	versionDir := filepath.Join(srcDir, "functions", fn.ID, "versions", fn.CodeHash)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir versions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "handler.js"), []byte("export default () => 'ok'\n"), 0o644); err != nil {
		t.Fatalf("write code: %v", err)
	}
	// Pre-existing symlink in the source — confirms ArchiveTo skips it.
	if err := os.Symlink(filepath.Join("versions", fn.CodeHash), filepath.Join(srcDir, "functions", fn.ID, "current")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// 1. Snapshot.
	snapPath := filepath.Join(srcDir, "snap.db")
	if err := SnapshotDB(db.WriteDB(), snapPath); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}

	// 2. Archive.
	var buf bytes.Buffer
	if err := ArchiveTo(&buf, srcDir, snapPath); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if buf.Len() < 100 {
		t.Fatalf("archive suspiciously small: %d bytes", buf.Len())
	}

	// Close the source DB before restore so the file handles don't
	// race with the staging DB validation. (RestoreFrom doesn't
	// touch srcDir, but a real-world restore replaces the live DB —
	// we mimic that ordering here.)
	if err := db.Close(); err != nil {
		t.Fatalf("close source db: %v", err)
	}

	// 3. Restore into a fresh empty dir.
	if err := RestoreFrom(&buf, dstDir); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// 4. Verify the DB row is queryable.
	dstDB, err := database.New(filepath.Join(dstDir, "orva.db"))
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer dstDB.Close()
	got, err := dstDB.GetFunction(fn.ID)
	if err != nil {
		t.Fatalf("get restored function: %v", err)
	}
	if got.Name != fn.Name || got.CodeHash != fn.CodeHash {
		t.Fatalf("restored row mismatch: got name=%s hash=%s; want %s/%s",
			got.Name, got.CodeHash, fn.Name, fn.CodeHash)
	}

	// 5. Verify the version files were extracted.
	restoredCode := filepath.Join(dstDir, "functions", fn.ID, "versions", fn.CodeHash, "handler.js")
	if _, err := os.Stat(restoredCode); err != nil {
		t.Fatalf("restored code missing: %v", err)
	}

	// 6. Verify the `current` symlink was rebuilt to point at versions/<hash>.
	link := filepath.Join(dstDir, "functions", fn.ID, "current")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	expected := filepath.Join("versions", fn.CodeHash)
	if target != expected {
		t.Fatalf("symlink target = %q, want %q", target, expected)
	}

	// 7. Verify the original DB was preserved as a .before-restore-* file.
	// (Only present if dstDir had a live DB before restore — we used a
	// fresh dir so nothing should be aside; just make sure no spurious
	// backup file was created.)
	matches, _ := filepath.Glob(filepath.Join(dstDir, "orva.db.before-restore-*"))
	if len(matches) != 0 {
		t.Fatalf("unexpected before-restore backup in fresh dir: %v", matches)
	}
}

// TestRestoreRejectsTraversal feeds RestoreFrom an archive containing
// "../escape" — the extractor must refuse instead of writing outside
// the destination.
func TestRestoreRejectsTraversal(t *testing.T) {
	dataDir := t.TempDir()
	// Hand-craft a minimal gzip tar with an unsafe path. Easier than
	// pulling in an extra dep — we just need the extractTarGz check
	// to fire before any DB validation.
	var buf bytes.Buffer
	if err := writeBadArchive(&buf, "../escape", []byte("nope")); err != nil {
		t.Fatalf("build bad archive: %v", err)
	}
	err := RestoreFrom(&buf, dataDir)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("expected unsafe-path error, got %v", err)
	}
}
