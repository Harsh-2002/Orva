package database

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
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
	if v, err := db.KVIncr("fn_kvincr12345", "n", 1, nil); err != nil || v != 1 {
		t.Fatalf("first incr: v=%d err=%v (want 1, nil)", v, err)
	}
	if v, err := db.KVIncr("fn_kvincr12345", "n", 5, nil); err != nil || v != 6 {
		t.Fatalf("second incr: v=%d err=%v (want 6, nil)", v, err)
	}
	if v, err := db.KVIncr("fn_kvincr12345", "n", -2, nil); err != nil || v != 4 {
		t.Fatalf("negative incr: v=%d err=%v (want 4, nil)", v, err)
	}

	// A non-integer value cannot be incremented.
	if err := db.KVPut("fn_kvincr12345", "s", []byte(`"abc"`), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.KVIncr("fn_kvincr12345", "s", 1, nil); err == nil {
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
	if ok, _, err := db.KVCAS("fn_kvcas123456", "lock", nil, []byte(`"held"`), nil); err != nil || !ok {
		t.Fatalf("insert-if-absent: ok=%v err=%v (want true, nil)", ok, err)
	}
	// expected=nil on an existing key fails (already held), returns current.
	ok, current, err := db.KVCAS("fn_kvcas123456", "lock", nil, []byte(`"other"`), nil)
	if err != nil || ok {
		t.Fatalf("insert-if-absent on existing: ok=%v err=%v (want false, nil)", ok, err)
	}
	if string(current) != `"held"` {
		t.Errorf("expected current=%q, got %q", `"held"`, string(current))
	}
	// Matching expected swaps.
	if ok, _, err := db.KVCAS("fn_kvcas123456", "lock", []byte(`"held"`), []byte(`"taken"`), nil); err != nil || !ok {
		t.Fatalf("matching cas: ok=%v err=%v (want true, nil)", ok, err)
	}
	// Stale expected fails and returns the new current.
	ok, current, err = db.KVCAS("fn_kvcas123456", "lock", []byte(`"held"`), []byte(`"z"`), nil)
	if err != nil || ok {
		t.Fatalf("stale cas: ok=%v err=%v (want false, nil)", ok, err)
	}
	if string(current) != `"taken"` {
		t.Errorf("expected current=%q, got %q", `"taken"`, string(current))
	}
}

func TestKVValidationBoundaries(t *testing.T) {
	db := newTestDB(t)
	kvTestFn(t, db, "fn_kvlimits123")

	key256 := strings.Repeat("界", 256)
	if err := db.KVPut("fn_kvlimits123", key256, []byte(`null`), nil); err != nil {
		t.Fatalf("256-character key rejected: %v", err)
	}
	if err := db.KVPut("fn_kvlimits123", key256+"x", []byte(`null`), nil); err == nil {
		t.Fatal("257-character key accepted")
	}
	value64KiB := []byte(`"` + strings.Repeat("x", KVMaxValueBytes-2) + `"`)
	if err := db.KVPut("fn_kvlimits123", "max", value64KiB, nil); err != nil {
		t.Fatalf("64 KiB value rejected: %v", err)
	}
	if err := db.KVPut("fn_kvlimits123", "too-big", append(value64KiB, ' '), nil); err == nil {
		t.Fatal("value larger than 64 KiB accepted")
	}
	if err := db.KVPut("fn_kvlimits123", "bad-json", []byte(`{"broken"`), nil); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}

func TestKVTTLOmitClearAndRefresh(t *testing.T) {
	db := newTestDB(t)
	kvTestFn(t, db, "fn_kvttl123456")
	ttl100 := 100
	if err := db.KVPut("fn_kvttl123456", "key", []byte(`1`), &ttl100); err != nil {
		t.Fatal(err)
	}
	first, err := db.KVGet("fn_kvttl123456", "key")
	if err != nil || first.ExpiresAt == nil {
		t.Fatalf("initial expiry missing: entry=%v err=%v", first, err)
	}
	if err := db.KVPut("fn_kvttl123456", "key", []byte(`2`), nil); err != nil {
		t.Fatal(err)
	}
	preserved, _ := db.KVGet("fn_kvttl123456", "key")
	if preserved.ExpiresAt == nil || !preserved.ExpiresAt.Equal(*first.ExpiresAt) {
		t.Fatalf("omitted TTL did not preserve expiry: first=%v next=%v", first.ExpiresAt, preserved.ExpiresAt)
	}
	ttl200 := 200
	if err := db.KVPut("fn_kvttl123456", "key", []byte(`3`), &ttl200); err != nil {
		t.Fatal(err)
	}
	refreshed, _ := db.KVGet("fn_kvttl123456", "key")
	if refreshed.ExpiresAt == nil || !refreshed.ExpiresAt.After(*preserved.ExpiresAt) {
		t.Fatalf("positive TTL did not refresh expiry: old=%v new=%v", preserved.ExpiresAt, refreshed.ExpiresAt)
	}
	zero := 0
	if err := db.KVPut("fn_kvttl123456", "key", []byte(`4`), &zero); err != nil {
		t.Fatal(err)
	}
	cleared, _ := db.KVGet("fn_kvttl123456", "key")
	if cleared.ExpiresAt != nil {
		t.Fatalf("explicit zero did not clear expiry: %v", cleared.ExpiresAt)
	}
	negative := -1
	if err := db.KVPut("fn_kvttl123456", "key", []byte(`5`), &negative); err == nil {
		t.Fatal("negative TTL accepted")
	}
}

func TestKVBatchRollsBackDatabaseFailure(t *testing.T) {
	db := newTestDB(t)
	kvTestFn(t, db, "fn_kvbatch1234")
	if _, err := db.write.Exec(`CREATE TRIGGER reject_boom BEFORE INSERT ON kv_store WHEN NEW.key = 'boom' BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	ops := []KVBatchOp{
		{Op: "put", Key: "first", Value: []byte(`1`)},
		{Op: "put", Key: "boom", Value: []byte(`2`)},
	}
	if _, err := db.KVBatch("fn_kvbatch1234", ops); err == nil {
		t.Fatal("batch database failure was not returned")
	}
	if _, err := db.KVGet("fn_kvbatch1234", "first"); !errors.Is(err, ErrKVNotFound) {
		t.Fatalf("first write survived rollback: %v", err)
	}
	if got := db.KVMetrics().Rollbacks; got != 1 {
		t.Fatalf("rollback metric=%d, want 1", got)
	}
}

func TestKVConcurrentIncrements(t *testing.T) {
	db := newTestDB(t)
	kvTestFn(t, db, "fn_kvconcurrent")
	const increments = 1000
	errs := make(chan error, increments)
	var wg sync.WaitGroup
	for i := 0; i < increments; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.KVIncr("fn_kvconcurrent", "counter", 1, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entry, err := db.KVGet("fn_kvconcurrent", "counter")
	if err != nil {
		t.Fatal(err)
	}
	if string(entry.Value) != "1000" {
		t.Fatalf("counter=%s, want 1000", entry.Value)
	}
}

func TestKVContextCancellation(t *testing.T) {
	db := newTestDB(t)
	kvTestFn(t, db, "fn_kvcancel1234")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.KVPutContext(ctx, "fn_kvcancel1234", "key", []byte(`1`), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("KVPutContext err=%v, want context.Canceled", err)
	}
	stats := db.KVMetrics()
	if stats.Operations[1].Timeouts != 1 {
		t.Fatalf("put timeouts=%d, want 1", stats.Operations[1].Timeouts)
	}
}

func TestKVPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	fn := &Function{
		ID: "fn_kvpersist123", Name: "fn-kv-persist", Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 64, CPUs: 0.5, EnvVars: map[string]string{},
		NetworkMode: "none", Status: "active",
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	if err := db.KVPut("fn_kvpersist123", "key", []byte(`{"ok":true}`), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	entry, err := db.KVGet("fn_kvpersist123", "key")
	if err != nil {
		t.Fatal(err)
	}
	if string(entry.Value) != `{"ok":true}` {
		t.Fatalf("persisted entry=%s", entry.Value)
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
