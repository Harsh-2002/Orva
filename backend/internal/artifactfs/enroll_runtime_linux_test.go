package artifactfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnrollmentRuntimeCopy(t *testing.T) {
	base := newStore(t, testLimits())
	src := t.TempDir()
	d := filepath.Join(src, "etc", "alternatives")
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"awk.1.gz": "/usr/share/man/man1/mawk.1.gz", "which.de1.gz": "/usr/share/man/de/man1/which.debianutils.1.gz",
		"escape": "/bin/absent",
	} {
		if err := os.Symlink(target, filepath.Join(d, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sealInstalledRuntime(t.Context(), base, "node", src); err != ErrUnsupported {
		t.Fatalf("non-manual dangling link accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(d, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "retained"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := sealInstalledRuntime(t.Context(), base, "node", src)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := base.Acquire(id.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if _, err := os.Lstat(filepath.Join(lease.Path(), "etc", "alternatives", "awk.1.gz")); !os.IsNotExist(err) {
		t.Fatalf("dangling manual was copied: %v", err)
	}
	if _, err := os.Readlink(filepath.Join(d, "awk.1.gz")); err != nil {
		t.Fatalf("legacy manual link was changed: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(lease.Path(), "etc", "alternatives", "retained")); err != nil || string(raw) != "data" {
		t.Fatalf("unexpected retained file: %v", err)
	}
}
