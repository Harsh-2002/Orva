package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifiedEndpointResolvesRelativeLinksOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "actual"), nil, 0700); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"relative": "actual", "contained": "../bin/actual",
		"absolute": filepath.Join(root, "bin", "actual"),
		"host":     "/proc/self/exe", "escape": "../../outside",
		"cycle": "cycle", "file_parent": "actual/../actual",
	} {
		if err := os.Symlink(target, filepath.Join(root, "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"actual", "relative", "contained"} {
		path, err := resolveVerifiedEndpoint(root, "bin/"+name)
		if err != nil || path != filepath.Join(root, "bin", "actual") {
			t.Fatalf("contained endpoint %s: %q %v", name, path, err)
		}
	}
	for _, name := range []string{"absolute", "host", "escape", "cycle", "file_parent", "missing"} {
		if _, err := resolveVerifiedEndpoint(root, "bin/"+name); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", name)
		}
	}
}
