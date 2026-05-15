package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
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

// TestArchiveCarriesManifestAndKeys verifies the v1 archive format:
// manifest.json at the root with sha256 of every file + the keys
// (.master.key and .admin-key) make it into keys/master.key and
// keys/admin.key. End-to-end test of the snapshot promise — restore on
// a fresh host can read function_secrets because the master key rode
// along.
func TestArchiveCarriesManifestAndKeys(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "orva.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Lay down the two key files the snapshot is supposed to capture.
	masterKey := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	if err := os.WriteFile(filepath.Join(srcDir, ".master.key"), masterKey, 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}
	adminKey := []byte("orva_admintestkey1234")
	if err := os.WriteFile(filepath.Join(srcDir, ".admin-key"), adminKey, 0o600); err != nil {
		t.Fatalf("write admin.key: %v", err)
	}

	snapPath := filepath.Join(srcDir, "snap.db")
	if err := SnapshotDB(db.WriteDB(), snapPath); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var buf bytes.Buffer
	if err := ArchiveTo(&buf, srcDir, snapPath); err != nil {
		t.Fatalf("archive: %v", err)
	}
	_ = db.Close()

	// Walk the archive and collect what's in it.
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	have := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		b, _ := io.ReadAll(tr)
		have[hdr.Name] = b
	}

	// Manifest must exist.
	manifestBytes, ok := have["manifest.json"]
	if !ok {
		t.Fatalf("manifest.json missing from archive; got %v", keysOf(have))
	}
	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.FormatVersion != FormatVersion {
		t.Errorf("format_version=%d want %d", m.FormatVersion, FormatVersion)
	}
	if !m.ContainsKeys {
		t.Errorf("manifest.contains_keys = false, want true")
	}
	if _, ok := m.Files["keys/master.key"]; !ok {
		t.Errorf("manifest.files missing keys/master.key entry: %v", keysOf(m.Files))
	}
	if _, ok := m.Files["keys/admin.key"]; !ok {
		t.Errorf("manifest.files missing keys/admin.key entry: %v", keysOf(m.Files))
	}
	if _, ok := m.Files["orva.db"]; !ok {
		t.Errorf("manifest.files missing orva.db entry")
	}

	// Keys content must match.
	if !bytes.Equal(have["keys/master.key"], masterKey) {
		t.Errorf("master.key in archive doesn't match source")
	}
	if !bytes.Equal(have["keys/admin.key"], adminKey) {
		t.Errorf("admin.key in archive doesn't match source")
	}

	// Restore into a fresh dir and verify the keys land at the right
	// paths with tight perms.
	dstDir := t.TempDir()
	if err := RestoreFrom(bytes.NewReader(archiveBytes(t, srcDir, snapPath)), dstDir); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dstDir, ".master.key"))
	if err != nil {
		t.Fatalf("read restored master.key: %v", err)
	}
	if !bytes.Equal(got, masterKey) {
		t.Errorf("restored master.key bytes don't match")
	}
	got, err = os.ReadFile(filepath.Join(dstDir, ".admin-key"))
	if err != nil {
		t.Fatalf("read restored admin.key: %v", err)
	}
	if !bytes.Equal(got, adminKey) {
		t.Errorf("restored admin.key bytes don't match")
	}
}

// TestRestoreRejectsTamperedManifest produces a valid archive, then
// flips one byte of orva.db inside it. RestoreFrom must reject the
// archive on manifest verification before touching live state.
func TestRestoreRejectsTamperedManifest(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "orva.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	snapPath := filepath.Join(srcDir, "snap.db")
	if err := SnapshotDB(db.WriteDB(), snapPath); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var buf bytes.Buffer
	if err := ArchiveTo(&buf, srcDir, snapPath); err != nil {
		t.Fatalf("archive: %v", err)
	}
	_ = db.Close()

	// Rewrite the archive: change orva.db's bytes but keep the
	// manifest unchanged. The manifest's sha256 will no longer match.
	tampered := rewriteArchive(t, buf.Bytes(), "orva.db", func(b []byte) []byte {
		// Flip the last byte. SQLite header has a magic prefix, so
		// changing trailing bytes preserves the gross structure and
		// the integrity check might miss it — but the sha256 won't.
		if len(b) == 0 {
			return b
		}
		c := append([]byte{}, b...)
		c[len(c)-1] ^= 0xFF
		return c
	})

	err = RestoreFrom(bytes.NewReader(tampered), t.TempDir())
	if err == nil {
		t.Fatal("restore accepted tampered archive")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("expected sha256 mismatch error, got: %v", err)
	}
}

// keysOf returns the sorted keys of a map for stable error printing.
func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// archiveBytes is a helper to produce a fresh archive (since the previous
// test consumed the buffer).
func archiveBytes(t *testing.T, srcDir, snapPath string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := ArchiveTo(&buf, srcDir, snapPath); err != nil {
		t.Fatalf("archive: %v", err)
	}
	return buf.Bytes()
}

// rewriteArchive walks a gzip-tar in memory and substitutes the named
// file's contents using the given transform. Other entries pass through
// unchanged. Manifest.json is left alone so the resulting archive has a
// manifest that no longer matches the (changed) file — exactly the
// tamper scenario we want to reject.
func rewriteArchive(t *testing.T, in []byte, targetName string, transform func([]byte) []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var out bytes.Buffer
	gzw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gzw)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		body, _ := io.ReadAll(tr)
		if hdr.Name == targetName {
			body = transform(body)
			hdr.Size = int64(len(body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write hdr: %v", err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return out.Bytes()
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
