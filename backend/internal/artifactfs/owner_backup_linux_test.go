package artifactfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupWitnessExclusion(t *testing.T) {
	witnessDir, backupDir := t.TempDir(), t.TempDir()
	witness, err := os.Create(filepath.Join(witnessDir, "witness"))
	if err != nil {
		t.Fatal(err)
	}
	defer witness.Close()
	for _, name := range []string{"snapshot", "alias"} {
		p := filepath.Join(backupDir, name)
		if name == "alias" {
			err = os.Link(witness.Name(), p)
		} else {
			err = os.WriteFile(p, nil, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := checkBackupPaths(witness.Name(), witness, []string{backupDir}, []string{filepath.Join(backupDir, "snapshot")}); err != nil {
		t.Fatal(err)
	}
	if err := checkBackupPaths(witness.Name(), witness, []string{witnessDir}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlapping root: %v", err)
	}
	if err := checkBackupPaths(witness.Name(), witness, []string{backupDir}, []string{filepath.Join(backupDir, "alias")}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("witness alias: %v", err)
	}
}

func TestWriteBuildSource(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	const code = "export default () => 42\n"
	path := filepath.Join(root, "handler.mjs")
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	m := fixtureManifest(t, root)
	l := &Lease{store: &Store{boundKind: KindCode, limits: Limits{Entries: 16, PathBytes: 4096, FileBytes: 4096, ManifestBytes: 4096, Bytes: 4096}},
		rec: &record{reserved: 4096, durable: persistentRecord{Manifest: m}}, path: root, id: Identity{Kind: KindCode}}
	var a, b bytes.Buffer
	for _, out := range []*bytes.Buffer{&a, &b} {
		if err := l.WriteBuildSource(t.Context(), out); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("source representation is not deterministic")
	}
	gz, err := gzip.NewReader(&a)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	header, err := tr.Next()
	if err != nil || header.Name != "handler.mjs" || header.Mode != 0640 ||
		header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
		t.Fatalf("unexpected source header: %+v (%v)", header, err)
	}
	got, err := io.ReadAll(tr)
	if err != nil || string(got) != code {
		t.Fatalf("source content mismatch: %v", err)
	}
	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected seal metadata: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.WriteBuildSource(t.Context(), io.Discard); err == nil {
		t.Fatal("changed tree exported")
	}
}
