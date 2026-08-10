package builder

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// mkTree creates dir with one file in it, so the directory is non-empty and
// has a measurable size.
func mkTree(t *testing.T, dir string, bytes int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, bytes), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBuildCacheIsScopedToOneFunction is the invariant that stops someone
// turning this into a shared directory later. A shared installer cache is an
// arbitrary-code-execution channel between functions (npm's packument and its
// tarball share one URL-keyed cacache with no keyed integrity check; pip's
// http cache has none at all), so the cache handed to a build step must be
// derived from the id of the function being built and from nothing else.
func TestBuildCacheIsScopedToOneFunction(t *testing.T) {
	data := t.TempDir()
	b := &Builder{DataDir: data}

	cfgA := b.buildConfigFor(buildStep{fnID: "fn-a", codeDir: "/code/a", language: sandbox.Node})
	cfgB := b.buildConfigFor(buildStep{fnID: "fn-b", codeDir: "/code/b", language: sandbox.Node})

	wantA := filepath.Join(data, "build-cache", "fn-a")
	if cfgA.CacheDir != wantA {
		t.Fatalf("cache dir = %q, want %q", cfgA.CacheDir, wantA)
	}
	if cfgA.CacheDir == cfgB.CacheDir {
		t.Fatalf("two functions must never share a cache dir (both got %q)", cfgA.CacheDir)
	}
	// Never inside the deployable artifact tree.
	if strings.Contains(cfgA.CacheDir, filepath.Join(data, "functions")) {
		t.Errorf("cache must not live under functions/: %q", cfgA.CacheDir)
	}
	// Preparing it is what makes the mount usable, so it must exist by now.
	if _, err := os.Stat(cfgA.CacheDir); err != nil {
		t.Fatalf("cache dir not created: %v", err)
	}
}

// TestBuildCacheDisabledWithoutIdentity: no function id (or no data dir) means
// no persistent cache — the pre-cache behaviour, which is what keeps unit
// tests and any caller without an id working.
func TestBuildCacheDisabledWithoutIdentity(t *testing.T) {
	if got := (&Builder{DataDir: t.TempDir()}).buildConfigFor(buildStep{}); got.CacheDir != "" {
		t.Errorf("no fn id must mean no persistent cache, got %q", got.CacheDir)
	}
	if got := (&Builder{}).buildConfigFor(buildStep{fnID: "fn-a"}); got.CacheDir != "" {
		t.Errorf("no data dir must mean no persistent cache, got %q", got.CacheDir)
	}
}

// TestFnScopedPathRefusesEscape covers the only new code that removes
// directories recursively. A malformed id must never turn a purge into a
// delete of something else.
func TestFnScopedPathRefusesEscape(t *testing.T) {
	data := t.TempDir()
	for _, id := range []string{
		"", ".", "..", "../..", "../other", "a/b", "/abs", `back\slash`,
		"..\x00", ".hidden", "sub/../escape", "./x",
	} {
		t.Run("id="+id, func(t *testing.T) {
			if _, err := BuildCacheDir(data, id); !errors.Is(err, ErrUnsafeFunctionID) {
				t.Errorf("BuildCacheDir(%q): want ErrUnsafeFunctionID, got %v", id, err)
			}
			if _, err := FunctionDir(data, id); !errors.Is(err, ErrUnsafeFunctionID) {
				t.Errorf("FunctionDir(%q): want ErrUnsafeFunctionID, got %v", id, err)
			}
			if err := PurgeBuildCache(data, id); !errors.Is(err, ErrUnsafeFunctionID) {
				t.Errorf("PurgeBuildCache(%q): want ErrUnsafeFunctionID, got %v", id, err)
			}
			if err := PurgeFunctionFiles(data, id); !errors.Is(err, ErrUnsafeFunctionID) {
				t.Errorf("PurgeFunctionFiles(%q): want ErrUnsafeFunctionID, got %v", id, err)
			}
		})
	}
	// An empty data dir is equally refused: joining onto "" would resolve
	// relative to the process working directory.
	if _, err := BuildCacheDir("", "019df200-7b00-7e00-9c00-aab1cd2e3f40"); !errors.Is(err, ErrUnsafeFunctionID) {
		t.Errorf("empty data dir must be refused, got %v", err)
	}
}

// TestPurgeFunctionFilesRemovesOnlyThatFunction: deleting a function must
// reclaim its code tree and its cache — and touch nothing else. Before this,
// functions/<id>/ was never removed at all and leaked permanently.
func TestPurgeFunctionFilesRemovesOnlyThatFunction(t *testing.T) {
	data := t.TempDir()
	mkTree(t, filepath.Join(data, "functions", "keep", "versions", "abc"), 32)
	mkTree(t, filepath.Join(data, "functions", "drop", "versions", "abc"), 32)
	mkTree(t, filepath.Join(data, "build-cache", "keep", "npm"), 32)
	mkTree(t, filepath.Join(data, "build-cache", "drop", "npm"), 32)
	if err := os.WriteFile(filepath.Join(data, "orva.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PurgeFunctionFiles(data, "drop"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	for _, gone := range []string{
		filepath.Join(data, "functions", "drop"),
		filepath.Join(data, "build-cache", "drop"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be gone, stat err = %v", gone, err)
		}
	}
	for _, kept := range []string{
		filepath.Join(data, "functions", "keep", "versions", "abc", "blob"),
		filepath.Join(data, "build-cache", "keep", "npm", "blob"),
		filepath.Join(data, "orva.db"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s must survive: %v", kept, err)
		}
	}
	// Purging a function that has nothing on disk is success, not an error.
	if err := PurgeFunctionFiles(data, "never-deployed"); err != nil {
		t.Errorf("purging an absent function must be a no-op, got %v", err)
	}
}

// TestPrepareBuildCacheStampsMtime: the directory's own mtime only moves when
// a direct child changes, so without an explicit stamp it reads as creation
// time. Both the LRU eviction order and the in-flight-build guard key off it.
func TestPrepareBuildCacheStampsMtime(t *testing.T) {
	data := t.TempDir()
	dir, err := PrepareBuildCache(data, "fn-a")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareBuildCache(data, "fn-a"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Fatalf("a build must stamp its cache as used; mtime is %s", info.ModTime())
	}
}

// TestSweepBuildScratch: RunBuild's deferred cleanup only covers a live
// process. A crash mid-install leaks the whole npm working set under
// build-tmp/ permanently, so boot sweeps it.
func TestSweepBuildScratch(t *testing.T) {
	data := t.TempDir()
	mkTree(t, filepath.Join(data, "build-tmp", "build-123", "home"), 64)
	mkTree(t, filepath.Join(data, "build-tmp", "build-456"), 64)
	mkTree(t, filepath.Join(data, "build-tmp", "not-a-build"), 64)

	SweepBuildScratch(data)

	for _, gone := range []string{"build-123", "build-456"} {
		if _, err := os.Stat(filepath.Join(data, "build-tmp", gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been swept, stat err = %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "build-tmp", "not-a-build")); err != nil {
		t.Errorf("only build-* dirs are ours to remove: %v", err)
	}
	// Nothing to sweep must not panic or create anything.
	SweepBuildScratch(filepath.Join(data, "does-not-exist"))
}

// TestEvictIdleBuildCachesSpareInFlight: the low-disk lever drops rebuildable
// caches, but never one a build may still be writing into.
func TestEvictIdleBuildCachesSpareInFlight(t *testing.T) {
	data := t.TempDir()
	idle := filepath.Join(data, "build-cache", "old")
	busy := filepath.Join(data, "build-cache", "new")
	mkTree(t, filepath.Join(idle, "npm"), 4096)
	mkTree(t, filepath.Join(busy, "npm"), 4096)
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(idle, old, old); err != nil {
		t.Fatal(err)
	}

	if freed := EvictIdleBuildCaches(data); freed < 4096 {
		t.Errorf("freed = %d, want at least the idle cache's 4096 bytes", freed)
	}
	if _, err := os.Stat(idle); !os.IsNotExist(err) {
		t.Errorf("idle cache should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(busy); err != nil {
		t.Errorf("a cache touched inside the grace window must survive: %v", err)
	}
}

func TestBuildCacheUsageBytes(t *testing.T) {
	data := t.TempDir()
	if got := BuildCacheUsageBytes(data); got != 0 {
		t.Errorf("empty data dir usage = %d, want 0", got)
	}
	mkTree(t, filepath.Join(data, "build-cache", "a", "npm"), 1024)
	mkTree(t, filepath.Join(data, "build-cache", "b", "pip"), 2048)
	if got := BuildCacheUsageBytes(data); got < 3072 {
		t.Errorf("usage = %d, want >= 3072", got)
	}
}
