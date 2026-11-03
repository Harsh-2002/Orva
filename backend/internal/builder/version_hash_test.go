package builder

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeStamp(t *testing.T, rootfs, runtime, stamp string) {
	t.Helper()
	dir := filepath.Join(rootfs, runtime)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RuntimeStampFile), []byte(stamp+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Dependencies are built into the version directory, so the hash that names that
// directory has to cover the interpreter. Otherwise the same source redeployed after
// a runtime upgrade hits the "already built" fast path and keeps native addons that
// were compiled for the old interpreter, with no way to rebuild short of editing the
// source.
func TestVersionHashCoversTheInterpreter(t *testing.T) {
	const archive = "3b5d5c3712955042212316173ccf37be800d8d1a7bd6ba5b3a0a5e8d3d3a9f01"

	node26 := versionHash(archive, "node v26.13.0")

	if node26 == archive {
		t.Fatal("the version hash must not equal the bare archive hash, or it cannot carry the interpreter")
	}
	if versionHash(archive, "node v26.13.0") != node26 {
		t.Error("the hash must be deterministic: same source and interpreter must hit the cache")
	}
	if versionHash(archive, "node v24.22.1") == node26 {
		t.Error("the same source on a different Node must not share a version directory")
	}
	if versionHash(archive, "python 3.15.2") == node26 {
		t.Error("the same source for node and for python must not share a version directory")
	}
	if versionHash(archive+"0", "node v26.13.0") == node26 {
		t.Error("different source must give a different hash")
	}
	if len(node26) != 64 || strings.Trim(node26, "0123456789abcdef") != "" {
		t.Errorf("version hash must stay a 64-char lowercase hex digest (it becomes a path component): %q", node26)
	}
}

// The identity is what is installed, not what this binary expects: a rootfs that
// is stale or unstamped must not be mistaken for a current one.
func TestRuntimeIdentityIsReadFromTheInstalledRootfs(t *testing.T) {
	rootfs := t.TempDir()
	b := &Builder{RootfsDir: rootfs}

	if got := b.runtimeIdentity("node"); got != "node unstamped" {
		t.Errorf("a rootfs with no stamp must report itself as unstamped, got %q", got)
	}
	unstamped := versionHash("aa", b.runtimeIdentity("node"))

	writeStamp(t, rootfs, "node", "node v26.13.0")
	writeStamp(t, rootfs, "python", "python 3.15.2")
	if got := b.runtimeIdentity("node"); got != "node v26.13.0" {
		t.Errorf("node identity = %q, want the stamp contents", got)
	}
	if got := b.runtimeIdentity("python"); got != "python 3.15.2" {
		t.Errorf("python identity = %q, want the stamp contents", got)
	}
	if versionHash("aa", b.runtimeIdentity("node")) == unstamped {
		t.Error("stamping a rootfs must change the version hash, so the first deploy afterwards rebuilds")
	}

	writeStamp(t, rootfs, "node", "node v26.14.0")
	if got := b.runtimeIdentity("node"); got != "node v26.14.0" {
		t.Errorf("a changed stamp must change the identity, got %q", got)
	}

	if err := os.WriteFile(filepath.Join(rootfs, "node", RuntimeStampFile), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := b.runtimeIdentity("node"); got != "node unstamped" {
		t.Errorf("a blank stamp is no stamp, got %q", got)
	}
}

// pip runs on the rootfs's own interpreter, so it must pick wheels with its own
// tags. A pinned --platform manylinux2014 made numpy, pandas, scipy, pillow,
// pyarrow and torch invisible, or silently installed an older release.
func TestPipInstallDoesNotPinPlatformOrInterpreter(t *testing.T) {
	argv := pipInstallArgv()

	for _, pinned := range []string{"--platform", "--python-version", "--implementation", "--abi"} {
		if slices.Contains(argv, pinned) {
			t.Errorf("pip argv pins %s; let pip detect it from the interpreter it runs on: %v", pinned, argv)
		}
	}
	if !slices.Contains(argv, "--only-binary=:all:") {
		t.Errorf("pip must stay wheel-only so setup.py never runs: %v", argv)
	}
	if !slices.Contains(argv, "-t") {
		t.Errorf("dependencies must install into the code directory: %v", argv)
	}
}
