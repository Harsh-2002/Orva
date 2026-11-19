package artifactfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
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

func TestWriteRetainedBuildSource(t *testing.T) {
	root := t.TempDir()
	source := []byte("original source bytes")
	for name, body := range map[string][]byte{BuildSourceName: source, "compiled.js": []byte("generated")} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := fixtureManifest(t, root)
	l := &Lease{store: &Store{boundKind: KindCode, limits: Limits{Entries: 16, PathBytes: 4096, FileBytes: 4096, ManifestBytes: 4096, Bytes: 4096}},
		rec: &record{reserved: 4096, durable: persistentRecord{Manifest: m}}, path: root, id: Identity{Kind: KindCode}}
	var out bytes.Buffer
	digest := fmt.Sprintf("%x", sha256.Sum256(source))
	if err := l.WriteRetainedBuildSource(t.Context(), &out, digest); err != nil || !bytes.Equal(out.Bytes(), source) {
		t.Fatalf("retained source = %q, %v", out.Bytes(), err)
	}
	if err := l.WriteRetainedBuildSource(t.Context(), io.Discard, fmt.Sprintf("%x", sha256.Sum256(nil))); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("incorrect association digest accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "compiled.js"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.WriteRetainedBuildSource(t.Context(), io.Discard, digest); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("changed tree accepted: %v", err)
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
	gz, err := gzip.NewReader(bytes.NewReader(a.Bytes()))
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
	var raw bytes.Buffer
	if err := l.WriteBuildSourceTar(t.Context(), &raw); err != nil {
		t.Fatal(err)
	}
	gz, err = gzip.NewReader(&a)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(gz)
	if err := errors.Join(err, gz.Close()); err != nil || !bytes.Equal(decoded, raw.Bytes()) {
		t.Fatalf("gzip and raw projections differ: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.WriteBuildSource(t.Context(), io.Discard); err == nil {
		t.Fatal("changed tree exported")
	}
}
