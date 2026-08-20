package database

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func countQuery(t *testing.T, db *Database, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.read.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v\n%s", err, q)
	}
	return n
}

func seedFunctionRow(t *testing.T, db *Database, id string) {
	t.Helper()
	if _, err := db.write.Exec(
		`INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, 'node', 'handler.js')`,
		id, "fn-"+id,
	); err != nil {
		t.Fatalf("seed function: %v", err)
	}
}

// TestOneBadStatementDoesNotDestroyItsBatch is the data-loss regression.
// batchMax is 50 and a failing statement used to roll back the whole
// transaction with nothing but a counter bump, so a single FK violation --
// the realistic trigger being a function deleted while it had an invocation
// in flight -- took up to 49 unrelated execution records with it.
func TestOneBadStatementDoesNotDestroyItsBatch(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-live")

	// One poisoned job (FK to a function that does not exist) surrounded by
	// good ones, all inside a single batch.
	const good = 8
	for i := 0; i < good; i++ {
		if err := db.AsyncExecCritical(context.Background(),
			`INSERT INTO executions (id, function_id, status, started_at) VALUES (?, 'fn-live', 'success', datetime('now'))`,
			fmt.Sprintf("exec-good-%d", i),
		); err != nil {
			t.Fatal(err)
		}
		if i == good/2 {
			if err := db.AsyncExecCritical(context.Background(),
				`INSERT INTO executions (id, function_id, status, started_at) VALUES (?, 'fn-deleted', 'success', datetime('now'))`,
				"exec-orphan",
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	if !waitFor(t, 5*time.Second, func() bool {
		return countQuery(t, db, `SELECT COUNT(*) FROM executions WHERE function_id = 'fn-live'`) == good
	}) {
		got := countQuery(t, db, `SELECT COUNT(*) FROM executions WHERE function_id = 'fn-live'`)
		t.Fatalf("only %d of %d valid execution rows survived a poisoned batch", got, good)
	}

	// The poisoned row itself must be shed, not retried forever.
	if !waitFor(t, 5*time.Second, func() bool { return db.writer.shed.Load() >= 1 }) {
		t.Error("the permanently-failing statement was never shed")
	}
	if n := countQuery(t, db, `SELECT COUNT(*) FROM executions WHERE id = 'exec-orphan'`); n != 0 {
		t.Errorf("orphan row was written anyway: %d", n)
	}
}

// TestBatchSurvivesTheWriteConnectionBeingHeld is the VACUUM regression.
// The write pool is MaxOpenConns(1), and system.go runs VACUUM on it. The
// old writer gave the whole commit 5s and DISCARDED the batch when that
// expired, so every execution record, log and replay capture written during
// a multi-minute compaction was thrown away with only a counter to show.
//
// No VACUUM needed to reproduce it: holding the single write connection is
// the same condition.
func TestBatchSurvivesTheWriteConnectionBeingHeld(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-live")

	conn, err := db.write.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Take a write lock so the writer genuinely cannot proceed.
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO functions (id, name, runtime, entrypoint) VALUES ('holder','holder','node','handler.js')`,
	); err != nil {
		t.Fatal(err)
	}

	if err := db.AsyncExecCritical(context.Background(),
		`INSERT INTO executions (id, function_id, status, started_at) VALUES (?, 'fn-live', 'success', datetime('now'))`,
		"exec-during-stall",
	); err != nil {
		t.Fatal(err)
	}

	// Give the writer long enough to have flushed (and, on the old code, to
	// have given up and dropped the batch).
	time.Sleep(300 * time.Millisecond)

	// Release the connection.
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	if !waitFor(t, 10*time.Second, func() bool {
		return countQuery(t, db, `SELECT COUNT(*) FROM executions WHERE id = 'exec-during-stall'`) == 1
	}) {
		t.Fatal("the execution record queued while the write connection was busy " +
			"was never written; it was dropped instead of retried")
	}
}

// TestCloseDoesNotPanicWithProducersStillRunning — Close used to signal the
// writer by closing its channels, before waiting for producers. Anything
// still enqueueing then sent on a closed channel and panicked, which a
// select does not prevent: a send to a closed channel is ready, not blocked.
// The live trigger is a cron that overruns Scheduler.Stop's 5s grace.
func TestCloseDoesNotPanicWithProducersStillRunning(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-live")

	// The producer must still be running WHEN Close lands, or there is no
	// race to observe. Run it for a fixed window and close in the middle,
	// then keep producing well past the close.
	stop := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// A panic on either of these fails the test process, which is
			// the entire point of the test.
			_ = db.AsyncExecCritical(context.Background(),
				`INSERT INTO executions (id, function_id, status, started_at) VALUES (?, 'fn-live', 'success', datetime('now'))`,
				fmt.Sprintf("exec-racing-%d", i))
			db.AsyncExecTelemetry(
				`INSERT INTO activity_log (ts, source, actor_type, actor_id, actor_label, action) VALUES (datetime('now'), 'test', 'anon', '', '', 'x')`)
			time.Sleep(time.Millisecond)
		}
	}()

	<-started
	time.Sleep(30 * time.Millisecond)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Keep producing against the closed database for a while: this is the
	// window a cron overrunning Scheduler.Stop's grace period occupies.
	time.Sleep(80 * time.Millisecond)
	close(stop)
	<-done
}

// TestQueuesAreBoundedByBytes — the queues held 1024 jobs each with no
// regard for size, while a single job can carry a captured request body up
// to replay_capture_max_bytes (1 MiB). A stalled writer could therefore
// retain roughly a gigabyte before backpressure engaged.
func TestQueuesAreBoundedByBytes(t *testing.T) {
	db := &Database{}
	db.writer = newAsyncWriter(db)

	big := make([]byte, 1<<20)
	db.writer.criticalBytes.Store(maxCriticalQueueBytes + 1)

	err := db.AsyncExecCritical(context.Background(), "INSERT INTO t VALUES (?)", string(big))
	if err == nil {
		t.Error("critical enqueue accepted past the byte budget")
	}
	if db.writer.timeouts.Load() == 0 {
		t.Error("byte-budget rejection was not counted")
	}

	db.writer.telemetryBytes.Store(maxTelemetryQueueBytes + 1)
	before := db.writer.dropped.Load()
	db.AsyncExecTelemetry("INSERT INTO t VALUES (?)", string(big))
	if db.writer.dropped.Load() == before {
		t.Error("telemetry enqueue past the byte budget was not dropped")
	}
}

// TestJobBytesCountsPayload — the bound is only meaningful if the estimate
// tracks the actual payload.
func TestJobBytesCountsPayload(t *testing.T) {
	small := jobBytes("INSERT INTO t VALUES (?)", []any{"x"})
	large := jobBytes("INSERT INTO t VALUES (?)", []any{string(make([]byte, 1<<20))})
	// The small case already carries one byte, so the delta is 1 MiB - 1.
	if large-small < (1<<20)-16 {
		t.Errorf("payload not counted: small=%d large=%d", small, large)
	}
	if b := jobBytes("SELECT 1", []any{[]byte("abcd")}); b < 4 {
		t.Errorf("[]byte payload not counted: %d", b)
	}
}
