package database

import (
	"testing"
	"time"
)

func kvTestFn(t *testing.T, db *Database, id string) {
	t.Helper()
	fn := &Function{
		ID: id, Name: id, Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 64, CPUs: 0.5,
		EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
}

// TestKVIncr covers the atomic counter the CLI `kv incr` and MCP kv_incr both
// drive: create-at-delta, accumulate (incl. negative), and the not-an-integer
// guard.
func TestKVIncr(t *testing.T) {
	db := newTestDB(t)
	kvTestFn(t, db, "fn_kvincr12345")

	// Fresh key is created at the delta.
	if v, err := db.KVIncr("fn_kvincr12345", "n", 1, 0); err != nil || v != 1 {
		t.Fatalf("first incr: v=%d err=%v (want 1, nil)", v, err)
	}
	if v, err := db.KVIncr("fn_kvincr12345", "n", 5, 0); err != nil || v != 6 {
		t.Fatalf("second incr: v=%d err=%v (want 6, nil)", v, err)
	}
	if v, err := db.KVIncr("fn_kvincr12345", "n", -2, 0); err != nil || v != 4 {
		t.Fatalf("negative incr: v=%d err=%v (want 4, nil)", v, err)
	}

	// A non-integer value cannot be incremented.
	if err := db.KVPut("fn_kvincr12345", "s", []byte(`"abc"`), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.KVIncr("fn_kvincr12345", "s", 1, 0); err == nil {
		t.Error("expected incr on a non-integer value to error")
	}
}

// TestKVCAS covers compare-and-swap: insert-if-absent (expected=nil), match,
// and the precondition-miss path that returns the current value (the lock /
// optimistic-concurrency primitive the CLI `kv cas` exposes).
func TestKVCAS(t *testing.T) {
	db := newTestDB(t)
	kvTestFn(t, db, "fn_kvcas123456")

	// Insert-if-absent: expected=nil on a missing key swaps.
	if ok, _, err := db.KVCAS("fn_kvcas123456", "lock", nil, []byte(`"held"`), 0); err != nil || !ok {
		t.Fatalf("insert-if-absent: ok=%v err=%v (want true, nil)", ok, err)
	}
	// expected=nil on an existing key fails (already held), returns current.
	ok, current, err := db.KVCAS("fn_kvcas123456", "lock", nil, []byte(`"other"`), 0)
	if err != nil || ok {
		t.Fatalf("insert-if-absent on existing: ok=%v err=%v (want false, nil)", ok, err)
	}
	if string(current) != `"held"` {
		t.Errorf("expected current=%q, got %q", `"held"`, string(current))
	}
	// Matching expected swaps.
	if ok, _, err := db.KVCAS("fn_kvcas123456", "lock", []byte(`"held"`), []byte(`"taken"`), 0); err != nil || !ok {
		t.Fatalf("matching cas: ok=%v err=%v (want true, nil)", ok, err)
	}
	// Stale expected fails and returns the new current.
	ok, current, err = db.KVCAS("fn_kvcas123456", "lock", []byte(`"held"`), []byte(`"z"`), 0)
	if err != nil || ok {
		t.Fatalf("stale cas: ok=%v err=%v (want false, nil)", ok, err)
	}
	if string(current) != `"taken"` {
		t.Errorf("expected current=%q, got %q", `"taken"`, string(current))
	}
}

// TestCountDeploymentsForFunction pins the COUNT(*) added so the deployments
// list response can carry a truncation signal.
func TestCountDeploymentsForFunction(t *testing.T) {
	db := newTestDB(t)
	kvTestFn(t, db, "fn_depcount123")

	if n, err := db.CountDeploymentsForFunction("fn_depcount123"); err != nil || n != 0 {
		t.Fatalf("empty count: n=%d err=%v (want 0, nil)", n, err)
	}

	now := time.Now().UTC()
	for i, id := range []string{"dep_count00001", "dep_count00002", "dep_count00003"} {
		d := &Deployment{
			ID: id, FunctionID: "fn_depcount123", Version: int64(i + 1),
			Status: "succeeded", Phase: "done", SubmittedAt: now.Add(time.Duration(i) * time.Second),
		}
		if err := db.InsertDeployment(d); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.CountDeploymentsForFunction("fn_depcount123"); err != nil || n != 3 {
		t.Fatalf("count: n=%d err=%v (want 3, nil)", n, err)
	}
}
