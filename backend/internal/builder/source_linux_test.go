//go:build linux

package builder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSourceTextStaysInSealedRoot(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "code")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"outside": "outside", "code/source": "authored"} {
		if err := os.WriteFile(filepath.Join(parent, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../outside", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if got, err := readSourceText(root, "source", 8); err != nil || got != "authored" {
		t.Fatalf("inside source: %q, %v", got, err)
	}
	for name, limit := range map[string]int64{"link": 8, "../outside": 8, "source": 7, ".": 8} {
		if got, err := readSourceText(root, name, limit); err == nil || got != "" {
			t.Fatalf("%s/%d returned content outside source constraints", name, limit)
		}
	}
}
