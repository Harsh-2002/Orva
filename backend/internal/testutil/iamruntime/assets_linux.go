//go:build linux

// Package iamruntime owns hash-pinned runtime inputs for disposable VM tests.
package iamruntime

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
)

func check(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func verified(t testing.TB, path, expected string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	check(t, err)
	t.Cleanup(func() { _ = f.Close() })
	h := sha256.New()
	_, err = io.Copy(h, io.LimitReader(f, (1<<30)+1))
	check(t, err)
	if fmt.Sprintf("%x", h.Sum(nil)) != expected {
		t.Fatalf("runtime fixture checksum mismatch: %s", filepath.Base(path))
	}
	_, err = f.Seek(0, io.SeekStart)
	check(t, err)
	return f
}

func Nsjail(t testing.TB, dir string) string {
	t.Helper()
	f := verified(t, filepath.Join(dir, "nsjail"), "f8a398897ede1735c7eb8ca398e4886edd5926bfb9ab468d3011fecd22438d07")
	check(t, f.Close())
	return f.Name()
}

func Limits() artifactfs.Limits {
	return artifactfs.Limits{Generations: 4, Leases: 32, Staging: 1, Entries: 32768,
		PathBytes: 4096, LinkBytes: 4096, ManifestBytes: 64 << 20, Bytes: 1 << 30, FileBytes: 256 << 20}
}

func Seal(t testing.TB, store *artifactfs.Store, limits artifactfs.Limits, dir, lang string) artifactfs.Identity {
	t.Helper()
	conf := map[string][2]string{
		"node":   {"50885568c03e83f8b88960ea5cfdfe53f382a1bcc4af015435cedf0c8a39cec7", "6a85bea48a4ee93458ca1bb7bf071535928ee2a93ce5792670c9b1a151a0122c"},
		"python": {"870d854d966209cf1c7efa473582c7a1d4d9526551a1d5b9d5584d4c64eeed7b", "59a66da6b75c1394efd3e7f2b8719df23b0d5628e6ea326dd8425a6090077918"},
	}[lang]
	meta := verified(t, filepath.Join(dir, lang, "producer.json"), conf[0])
	var producer struct {
		Language, ArchiveSHA256, ManifestSHA256 string
		Manifest                                artifactfs.Manifest
	}
	check(t, json.NewDecoder(meta).Decode(&producer))
	if producer.Language != lang || producer.ArchiveSHA256 != conf[1] {
		t.Fatal("wrong fixture runtime recipe")
	}
	archive := verified(t, filepath.Join(dir, lang, "runtime.tar"), conf[1])
	stage, err := store.Begin(t.Context(), 400<<20)
	check(t, err)
	sealed := false
	defer func() {
		if !sealed {
			check(t, stage.Abort())
		}
	}()
	path, err := stage.Path()
	check(t, err)
	cmd := exec.CommandContext(t.Context(), "/usr/bin/tar", "-xp", "-f", "-", "-C", path)
	cmd.Stdin = archive
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unpack verified test runtime: %v: %s", err, out)
	}
	if lang == "python" {
		refreshPython(t, dir, path, &producer.Manifest)
		producer.ManifestSHA256, err = artifactfs.Digest(producer.Manifest, limits)
		check(t, err)
	}
	id, err := stage.Seal(t.Context(), artifactfs.KindRuntime, lang, producer.Manifest, producer.ManifestSHA256)
	check(t, err)
	sealed = true
	lease, err := store.Acquire(id)
	check(t, err)
	defer lease.Release()
	return lease.Identity()
}

func refreshPython(t testing.TB, dir, root string, manifest *artifactfs.Manifest) {
	t.Helper()
	const current = "41dee6721529405b096ab354b4e22ed0299b3d9dd1c7ce19f833095ff1493e81"
	const previous = "47a4ebef3baa183a461a3a04ea6dd331224049c6bc5b02b62fb86be988fda3bf"
	const dest = "opt/orva/orva.py"
	source := verified(t, filepath.Join(dir, "orva.py"), current)
	b, err := io.ReadAll(io.LimitReader(source, 1<<20))
	check(t, err)
	old := verified(t, filepath.Join(root, dest), previous)
	check(t, old.Close())
	for i := range manifest.Entries {
		e := &manifest.Entries[i]
		if e.Path == dest && e.Type == artifactfs.TypeFile && e.SHA256 == previous && e.HardlinkGroup == "" {
			e.SHA256, e.Size = current, int64(len(b))
			check(t, os.WriteFile(filepath.Join(root, dest), b, 0644))
			return
		}
	}
	t.Fatal("Python SDK not in verified runtime manifest")
}
