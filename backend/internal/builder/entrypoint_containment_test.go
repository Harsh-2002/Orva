package builder

import (
	"os"
	"path/filepath"
	"testing"
)

// The compiled entrypoint is derived by joining two operator-supplied strings
// onto a server-owned directory: the function row's entrypoint, and
// compilerOptions.outDir out of the tsconfig.json that shipped in the uploaded
// tarball. Neither was contained. The result is persisted as run_entrypoint and
// handed to the adapter as ORVA_ENTRYPOINT, so it names the file the sandbox
// executes — which is a strange thing to let a JSON field walk out of the
// version tree to choose.
//
// These tests plant the escape target on disk, so a version that resolves the
// join without containment finds it and reports it. That is the whole failure:
// the checks are stat()s, and a stat() that succeeds outside the tree is
// indistinguishable from one that succeeds inside it.

// tsVersionDir writes a published version directory and returns its path.
func tsVersionDir(t *testing.T, base string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(base, "version")
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A tsconfig asking for an outDir above its own directory used to be honoured
// verbatim: filepath.Join collapsed "../escape" against the version dir and the
// stat landed outside it, so the escaping path was returned as the entrypoint.
func TestResolveCachedEntrypointRefusesATraversingOutDir(t *testing.T) {
	base := t.TempDir()
	version := tsVersionDir(t, base, map[string]string{
		"handler.ts":    "export async function handler() {}",
		"tsconfig.json": `{"compilerOptions":{"outDir":"../escape"}}`,
	})
	// The escape target, sitting beside the version dir where "../escape"
	// resolves. It exists, so an uncontained stat() finds it.
	escape := filepath.Join(base, "escape")
	if err := os.MkdirAll(escape, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(escape, "handler.js"), []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := resolveCachedEntrypoint(version, "handler.ts")
	if got != "handler.ts" {
		t.Errorf("resolveCachedEntrypoint = %q, want handler.ts: an outDir of ../escape must not select a file outside the version directory", got)
	}
}

// Same escape, spelled as a symlink instead of a traversal. os.Stat follows the
// link and reports the target, so "dist" pointing anywhere on the box used to
// be enough; os.Root refuses a link that leaves its root.
func TestResolveCachedEntrypointRefusesAnOutDirSymlinkedOutOfTheTree(t *testing.T) {
	base := t.TempDir()
	version := tsVersionDir(t, base, map[string]string{
		"handler.ts":    "export async function handler() {}",
		"tsconfig.json": `{"compilerOptions":{"outDir":"dist"}}`,
	})
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "handler.js"), []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(version, "dist")); err != nil {
		t.Fatal(err)
	}

	got := resolveCachedEntrypoint(version, "handler.ts")
	if got != "handler.ts" {
		t.Errorf("resolveCachedEntrypoint = %q, want handler.ts: dist symlinked outside the version directory must not resolve", got)
	}
}

// A legacy row whose entrypoint traverses must not select a file outside the
// version directory either. The write-side gate (safepath.Validate) rejects
// these on the way in now, but rows predating it exist and storage is not the
// only way a value arrives — which is exactly why safepath documents an
// independent read-side gate.
//
// Two levels of traversal, not one: filepath.Join cleans its result, so
// "dist" + "../handler.js" collapses back to "handler.js" inside the tree and
// escapes nothing. It takes "../../" to clear the outDir and the version dir
// both.
func TestResolveCachedEntrypointRefusesATraversingEntrypoint(t *testing.T) {
	base := t.TempDir()
	version := tsVersionDir(t, base, map[string]string{
		"tsconfig.json": `{"compilerOptions":{"outDir":"dist"}}`,
	})
	// Where "<version>/dist/../../handler.js" lands: beside the version dir.
	if err := os.WriteFile(filepath.Join(base, "handler.js"), []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}

	const authored = "../../handler.ts"
	got := resolveCachedEntrypoint(version, authored)
	if got != authored {
		t.Errorf("resolveCachedEntrypoint = %q, want %q: a traversing entrypoint must not resolve outside the version directory", got, authored)
	}
}

// The version directory is named from a code hash. ActivateVersion has always
// rejected a non-digest before letting one become a path component, for stated
// traversal reasons; the derive-on-rollback path added later named the same
// directory the same way without the guard, so a hash spelled "../../../evil"
// pointed the whole lookup at a directory outside the function's tree.
//
// The escape target is planted and complete, so a version without the guard
// finds a real tsconfig and a real compiled file there and reports them.
func TestRunEntrypointForRejectsATraversingCodeHash(t *testing.T) {
	dataDir := t.TempDir()
	const fnID = "01a02abf-351c-76ae-937a-90016387aaf1"

	// "<dataDir>/functions/<fn>/versions/../../../evil" cleans to
	// "<dataDir>/evil". Verified rather than assumed: an escape planted at the
	// wrong depth makes this test pass against the unfixed code.
	evil := filepath.Join(dataDir, "evil")
	if err := os.MkdirAll(filepath.Join(evil, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"tsconfig.json":   `{"compilerOptions":{"outDir":"dist"}}`,
		"dist/handler.js": "//",
	} {
		if err := os.WriteFile(filepath.Join(evil, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got := RunEntrypointFor(dataDir, fnID, "../../../evil", "handler.ts"); got != "" {
		t.Errorf("RunEntrypointFor(hash=%q) = %q, want empty: only a sha256 digest may name a version directory", "../../../evil", got)
	}
}

// The same guard rejects every other spelling that is not a digest. These do
// not escape anywhere on their own — no directory exists at the paths they
// name — so unlike the test above they do not demonstrate the defect. They pin
// the guard's shape so a later "relax it slightly" cannot reopen the one that
// does.
func TestRunEntrypointForAcceptsOnlyASha256Digest(t *testing.T) {
	dataDir := t.TempDir()
	const fnID = "01a02abf-351c-76ae-937a-90016387aaf2"

	for _, hash := range []string{
		"not-a-hash",
		"3F786850E387550FDAB836ED7E6DC881DE23001B1BD4E88E08C1A9B5A2B1C0D9",  // uppercase
		"3f786850e387550fdab836ed7e6dc881de23001b1bd4e88e08c1a9b5a2b1c0",    // one byte short
		"3f786850e387550fdab836ed7e6dc881de23001b1bd4e88e08c1a9b5a2b1c0d9f", // one over
		"../..",
		"a/b",
	} {
		t.Run(hash, func(t *testing.T) {
			if got := RunEntrypointFor(dataDir, fnID, hash, "handler.ts"); got != "" {
				t.Errorf("RunEntrypointFor(hash=%q) = %q, want empty", hash, got)
			}
		})
	}
}
