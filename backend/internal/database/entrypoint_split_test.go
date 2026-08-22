package database

import (
	"os"
	"path/filepath"
	"testing"
)

// functions.entrypoint used to carry two meanings. A TypeScript build stamped
// tsc's output onto it, so the handler.ts the operator wrote was gone from the
// row. Four readers grew private heuristics to guess it back (the validator,
// the build-cache resolver, the diff, and GetSource, which never got one and so
// served compiled JavaScript in the editor), and re-deploying from the
// dashboard failed on a path nobody had typed.
//
// entrypoint is now the authored file and the pipeline never writes it;
// run_entrypoint is the build output, and empty means the two are the same.
func TestEntrypointKeepsTheAuthoredFileSeparateFromTheBuildOutput(t *testing.T) {
	db := newTestDB(t)
	fn := insertTestFunction(t, db, "ts-fn")
	fn.Entrypoint = "handler.ts"
	fn.RunEntrypoint = "dist/handler.js"
	if err := db.UpdateFunction(fn); err != nil {
		t.Fatalf("UpdateFunction: %v", err)
	}

	got, err := db.GetFunction(fn.ID)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if got.Entrypoint != "handler.ts" {
		t.Errorf("entrypoint = %q, want handler.ts: the authored file must survive a build", got.Entrypoint)
	}
	if got.RunEntrypoint != "dist/handler.js" {
		t.Errorf("run_entrypoint = %q, want dist/handler.js", got.RunEntrypoint)
	}
}

// Rollback restores every other per-version fact from the snapshot, and the
// compiled path is one: a version built before an outDir change runs from a
// different directory than the current one.
func TestSnapshotCarriesRunEntrypoint(t *testing.T) {
	fn := &Function{
		Entrypoint: "handler.ts", RunEntrypoint: "dist/handler.js",
		EnvVars: map[string]string{}, MemoryMB: 128, CPUs: 0.5,
	}
	snap := SnapshotFromFunction(fn)
	if snap.RunEntrypoint != "dist/handler.js" {
		t.Fatalf("snapshot run_entrypoint = %q, want dist/handler.js", snap.RunEntrypoint)
	}

	// It deliberately does NOT participate in equality. Rollback derives the
	// compiled path from the promoted version's directory rather than restoring
	// it from here, so a snapshot written before the field existed holds "" and
	// comparing it would report a spurious difference on every legacy row.
	// Same code hash means same version directory means same compiled output.
	other := SnapshotFromFunction(&Function{
		Entrypoint: "handler.ts", RunEntrypoint: "build/handler.js",
		EnvVars: map[string]string{}, MemoryMB: 128, CPUs: 0.5,
	})
	if !snap.Equal(other) {
		t.Error("snapshots differing only in run_entrypoint compared unequal; a legacy row would never register as a no-op")
	}
}

// Rows written before the split hold the compiled path in entrypoint. The
// migration moves it and restores the authored name -- but only where the
// TypeScript source is actually on disk.
//
// The shape alone is not enough evidence: "a path with a directory and a .js
// extension" also describes an ordinary JavaScript function whose operator
// chose src/handler.js. An earlier version of this backfill matched on the
// string and would have rewritten those rows to point at a handler.ts that has
// never existed, failing the next deploy on a path nobody typed.
func TestSplitCompiledEntrypointsUsesDiskEvidence(t *testing.T) {
	db := newTestDB(t)
	dataDir := t.TempDir()

	// A real TypeScript function: entrypoint was stamped with tsc's output, and
	// handler.ts + tsconfig.json are sitting in its current/ directory.
	stamped := insertTestFunction(t, db, "legacy-ts")
	stamped.Entrypoint = "dist/handler.js"
	if err := db.UpdateFunction(stamped); err != nil {
		t.Fatalf("UpdateFunction: %v", err)
	}
	tsDir := filepath.Join(dataDir, "functions", stamped.ID, "current")
	if err := os.MkdirAll(tsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"handler.ts", "tsconfig.json"} {
		if err := os.WriteFile(filepath.Join(tsDir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A JavaScript function that simply keeps its handler in a subdirectory.
	// Same string shape, no TypeScript anywhere. It must not be touched.
	nested := insertTestFunction(t, db, "plain-js-nested")
	nested.Entrypoint = "src/handler.js"
	if err := db.UpdateFunction(nested); err != nil {
		t.Fatalf("UpdateFunction: %v", err)
	}
	jsDir := filepath.Join(dataDir, "functions", nested.ID, "current", "src")
	if err := os.MkdirAll(jsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jsDir, "handler.js"), []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A plain function with no directory at all: never compiled, never touched.
	plain := insertTestFunction(t, db, "legacy-js")

	db.SplitCompiledEntrypoints(dataDir)

	got, err := db.GetFunction(stamped.ID)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if got.Entrypoint != "handler.ts" {
		t.Errorf("entrypoint = %q, want handler.ts", got.Entrypoint)
	}
	if got.RunEntrypoint != "dist/handler.js" {
		t.Errorf("run_entrypoint = %q, want dist/handler.js", got.RunEntrypoint)
	}

	keptJS, err := db.GetFunction(nested.ID)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if keptJS.Entrypoint != "src/handler.js" || keptJS.RunEntrypoint != "" {
		t.Errorf("a JavaScript function with a nested entrypoint was rewritten: entrypoint=%q run=%q -- it has no handler.ts and the next deploy would fail validation",
			keptJS.Entrypoint, keptJS.RunEntrypoint)
	}

	untouched, err := db.GetFunction(plain.ID)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if untouched.Entrypoint != "handler.js" || untouched.RunEntrypoint != "" {
		t.Errorf("a never-compiled function was rewritten: entrypoint=%q run=%q",
			untouched.Entrypoint, untouched.RunEntrypoint)
	}
}
