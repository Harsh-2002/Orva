package database

import (
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
// migration moves it and restores the authored name.
func TestBackfillSplitsCompiledEntrypoint(t *testing.T) {
	db := newTestDB(t)

	stamped := insertTestFunction(t, db, "legacy-ts")
	stamped.Entrypoint = "dist/handler.js"
	if err := db.UpdateFunction(stamped); err != nil {
		t.Fatalf("UpdateFunction: %v", err)
	}
	// A plain function must not be touched: no directory, never compiled.
	plain := insertTestFunction(t, db, "legacy-js")

	db.backfillAuthoredEntrypoint()

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

	untouched, err := db.GetFunction(plain.ID)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if untouched.Entrypoint != "handler.js" || untouched.RunEntrypoint != "" {
		t.Errorf("a never-compiled function was rewritten: entrypoint=%q run=%q",
			untouched.Entrypoint, untouched.RunEntrypoint)
	}
}
