package sandbox

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func writeAdapter(t *testing.T, rootfs, name string, content []byte, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(rootfs, "opt", "orva", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAdapterSupportsSDK(t *testing.T) {
	now := time.Now()
	marker := []byte("// orva-sdk-proto:1\n")
	rest := bytes.Repeat([]byte("x"), 8192)

	t.Run("marker present in header", func(t *testing.T) {
		root := t.TempDir()
		writeAdapter(t, root, "adapter.js", append(append([]byte{}, marker...), rest...), now)
		writeAdapter(t, root, "adapter.py", append([]byte("# orva-sdk-proto:1\n"), rest...), now)
		if !adapterSupportsSDK(root, Node) || !adapterSupportsSDK(root, Python) {
			t.Fatal("marker in the first bytes not detected")
		}
	})
	t.Run("marker absent", func(t *testing.T) {
		root := t.TempDir()
		writeAdapter(t, root, "adapter.js", rest, now)
		if adapterSupportsSDK(root, Node) {
			t.Fatal("adapter without the marker reported as supporting the channel")
		}
	})
	t.Run("other protocol version", func(t *testing.T) {
		root := t.TempDir()
		writeAdapter(t, root, "adapter.js", []byte("// orva-sdk-proto:2\n"), now)
		if adapterSupportsSDK(root, Node) {
			t.Fatal("proto:2 must not enable a v1 channel")
		}
	})
	t.Run("marker after 4 KiB is ignored", func(t *testing.T) {
		root := t.TempDir()
		writeAdapter(t, root, "adapter.js", append(bytes.Repeat([]byte("x"), sdkMarkerWindow), marker...), now)
		if adapterSupportsSDK(root, Node) {
			t.Fatal("marker beyond the 4 KiB window must not count")
		}
	})
	t.Run("marker straddling the window edge is ignored", func(t *testing.T) {
		root := t.TempDir()
		pad := bytes.Repeat([]byte("x"), sdkMarkerWindow-5)
		writeAdapter(t, root, "adapter.js", append(pad, marker...), now)
		if adapterSupportsSDK(root, Node) {
			t.Fatal("a truncated marker must not count")
		}
	})
	t.Run("absent unreadable and unknown", func(t *testing.T) {
		root := t.TempDir()
		if adapterSupportsSDK(root, Node) {
			t.Fatal("missing adapter reported as supporting")
		}
		p := writeAdapter(t, root, "adapter.js", marker, now)
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		if adapterSupportsSDK(root, Node) {
			t.Fatal("unreadable adapter reported as supporting")
		}
		if err := os.MkdirAll(filepath.Join(root, "opt", "orva", "adapter.py"), 0o755); err != nil {
			t.Fatal(err)
		}
		if adapterSupportsSDK(root, Python) {
			t.Fatal("a directory is not an adapter")
		}
		if adapterSupportsSDK(root, Language("ruby")) {
			t.Fatal("unknown language reported as supporting")
		}
	})
	t.Run("rechecks content permissions and replacement", func(t *testing.T) {
		root := t.TempDir()
		same := func(m string) []byte { return []byte("// orva-sdk-proto:" + m + "\n") }
		p := writeAdapter(t, root, "adapter.js", same("1"), now)
		if !adapterSupportsSDK(root, Node) {
			t.Fatal("want true")
		}
		// A repeated lookup still sees the installed adapter.
		if !adapterSupportsSDK(root, Node) {
			t.Fatal("repeated lookup changed")
		}
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		if adapterSupportsSDK(root, Node) {
			t.Fatal("previous support bypassed unreadable permissions")
		}
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		// A same-size rewrite with restored mtime must also be re-read.
		writeAdapter(t, root, "adapter.js", same("2"), now)
		if adapterSupportsSDK(root, Node) {
			t.Fatal("previous support bypassed changed marker")
		}
		// Same size, newer mtime: the file is re-read.
		writeAdapter(t, root, "adapter.js", same("2"), now.Add(time.Hour))
		if adapterSupportsSDK(root, Node) {
			t.Fatal("mtime change did not invalidate the cache")
		}
		// Different size, restored mtime.
		writeAdapter(t, root, "adapter.js", append(same("1"), '\n'), now.Add(time.Hour))
		if !adapterSupportsSDK(root, Node) {
			t.Fatal("size change did not invalidate the cache")
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if adapterSupportsSDK(root, Node) {
			t.Fatal("previous support bypassed missing adapter")
		}
	})
}

// TestDispatchSitesAttachExecution fails when a production file calls
// Worker.Dispatch/DispatchEx without attaching sandbox.WithExecution: without
// it the channel handler sees Exec with no identity, and execution-scoped ops
// would be attributed to nothing. Every site must attach one per call.
func TestDispatchSitesAttachExecution(t *testing.T) {
	root := filepath.Join("..", "..", "..", "backend", "internal")
	call := regexp.MustCompile(`Worker\.(Dispatch|DispatchEx)\(`)
	attach := regexp.MustCompile(`sandbox\.WithExecution\(`)
	sites := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "sandbox" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		calls, attaches := len(call.FindAll(src, -1)), len(attach.FindAll(src, -1))
		sites += calls
		if calls > attaches {
			t.Errorf("%s: %d Dispatch/DispatchEx call(s) but %d sandbox.WithExecution attachment(s)", path, calls, attaches)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sites != 7 {
		t.Errorf("found %d dispatch call sites, expected the 7 known ones; update this test and attach WithExecution to any new site", sites)
	}
}
