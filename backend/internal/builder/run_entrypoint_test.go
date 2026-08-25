package builder

import (
	"os"
	"path/filepath"
	"testing"
)

// A deployment snapshot written before run_entrypoint existed carries no value
// for it. Rollback used to apply that absence as an empty string, which points a
// compiled TypeScript version back at its .ts source — Node cannot execute that,
// so every invocation of the rolled-back version died with WORKER_CRASHED.
//
// The compiled output is in the version directory regardless of what any row
// remembers, so the run entrypoint is derived from disk.
func TestRunEntrypointForDerivesFromTheVersionOnDisk(t *testing.T) {
	dataDir := t.TempDir()
	const fnID, hash = "01a02abf-351c-76ae-937a-90016387aaf1", "3f786850e387550fdab836ed7e6dc881de23001b1bd4e88e08c1a9b5a2b1c0d9"

	version := filepath.Join(dataDir, "functions", fnID, "versions", hash)
	if err := os.MkdirAll(filepath.Join(version, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(version, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("handler.ts", "export async function handler() {}")
	write("tsconfig.json", `{"compilerOptions":{"outDir":"dist"}}`)
	write("dist/handler.js", "exports.handler = async () => {}")

	if got := RunEntrypointFor(dataDir, fnID, hash, "handler.ts"); got != "dist/handler.js" {
		t.Errorf("RunEntrypointFor = %q, want dist/handler.js: a compiled version must run its build output", got)
	}
}

// Empty means "runs the authored file". A plain JavaScript version compiles
// nothing, so it must not be handed a dist/ path that does not exist.
func TestRunEntrypointForIsEmptyWhenNothingWasCompiled(t *testing.T) {
	dataDir := t.TempDir()
	const fnID, hash = "01a02abf-351c-76ae-937a-90016387aaf2", "3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d"

	version := filepath.Join(dataDir, "functions", fnID, "versions", hash)
	if err := os.MkdirAll(version, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(version, "handler.js"), []byte("exports.handler=async()=>{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := RunEntrypointFor(dataDir, fnID, hash, "handler.js"); got != "" {
		t.Errorf("RunEntrypointFor = %q, want empty: nothing was compiled", got)
	}
}

// A tsconfig whose output is missing (an interrupted or failed compile) must not
// produce a path to a file that is not there.
func TestRunEntrypointForIgnoresAMissingBuildOutput(t *testing.T) {
	dataDir := t.TempDir()
	const fnID, hash = "01a02abf-351c-76ae-937a-90016387aaf3", "2e7d2c03a9507ae265ecf5b5356885a53393a2029d241394997265a1a25aefc6"

	version := filepath.Join(dataDir, "functions", fnID, "versions", hash)
	if err := os.MkdirAll(version, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(version, "tsconfig.json"), []byte(`{"compilerOptions":{"outDir":"dist"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := RunEntrypointFor(dataDir, fnID, hash, "handler.ts"); got != "" {
		t.Errorf("RunEntrypointFor = %q, want empty: dist/handler.js does not exist", got)
	}
}
