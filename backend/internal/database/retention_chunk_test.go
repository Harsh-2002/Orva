package database

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// seedExecutionsAt inserts n executions (ids prefix+i) with started_at fixed at
// ts, each with one row in every executionChildTables table, in one transaction.
func seedExecutionsAt(t *testing.T, db *Database, prefix string, n int, ts string) {
	t.Helper()
	if _, err := db.write.Exec(`INSERT OR IGNORE INTO functions (id, name, runtime) VALUES ('fn-retention','fn-retention','node')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ex, err := tx.Prepare(`INSERT INTO executions (id, function_id, status, started_at) VALUES (?, 'fn-retention', 'success', ?)`)
	if err != nil {
		t.Fatal(err)
	}
	lg, _ := tx.Prepare(`INSERT INTO execution_logs (execution_id, stdout, stderr) VALUES (?, 'o', 'e')`)
	rq, _ := tx.Prepare(`INSERT INTO execution_requests (execution_id, method, path, headers_json, body, captured_at) VALUES (?, 'POST', '/', '{}', 'x', 0)`)
	sp, _ := tx.Prepare(`INSERT INTO user_spans (id, trace_id, parent_span_id, execution_id, name, started_at, duration_ms) VALUES (?, 't', '', ?, 'w', datetime('now'), 1)`)
	le, _ := tx.Prepare(`INSERT INTO execution_log_entries (execution_id, ts, level, message) VALUES (?, datetime('now'), 'info', 'm')`)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%07d", prefix, i)
		for _, err := range []error{
			func() error { _, e := ex.Exec(id, ts); return e }(),
			func() error { _, e := lg.Exec(id); return e }(),
			func() error { _, e := rq.Exec(id); return e }(),
			func() error { _, e := sp.Exec("s"+id, id); return e }(),
			func() error { _, e := le.Exec(id); return e }(),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// orphans counts child rows with no parent execution, plus executions that
// lost children while still existing; both must always be zero.
func assertExecutionsConsistent(t *testing.T, db *Database) {
	t.Helper()
	for _, tbl := range executionChildTables {
		var orphan, missing int
		if err := db.read.QueryRow(`SELECT COUNT(*) FROM ` + tbl + ` c WHERE NOT EXISTS (SELECT 1 FROM executions e WHERE e.id = c.execution_id)`).Scan(&orphan); err != nil {
			t.Fatal(err)
		}
		if err := db.read.QueryRow(`SELECT COUNT(*) FROM executions e WHERE NOT EXISTS (SELECT 1 FROM ` + tbl + ` c WHERE c.execution_id = e.id)`).Scan(&missing); err != nil {
			t.Fatal(err)
		}
		if orphan != 0 || missing != 0 {
			t.Fatalf("%s: %d orphan children, %d executions missing children", tbl, orphan, missing)
		}
	}
}

func tableCount(t *testing.T, db *Database, table string) int {
	t.Helper()
	var n int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Rows strictly older than the cutoff go (across several batches); a row exactly
// at the cutoff and newer rows stay; an execution with no children is handled.
func TestPurgeExecutionsEquivalenceAcrossBatches(t *testing.T) {
	db := newTestDB(t)
	const cutoff = "2024-06-01 00:00:00"
	seedExecutionsAt(t, db, "old", 2*purgeChunk+37, "2024-05-31 23:59:59")
	seedExecutionsAt(t, db, "edge", 3, cutoff)
	seedExecutionsAt(t, db, "new", 40, "2024-06-02 00:00:00")
	// childless old execution
	if _, err := db.write.Exec(`INSERT INTO executions (id, function_id, status, started_at) VALUES ('bare','fn-retention','success','2000-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	removed, err := db.purgeExecutions(context.Background(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(2*purgeChunk + 37 + 1); removed != want { // the old rows plus the childless one
		t.Fatalf("purge reported %d removed, want %d", removed, want)
	}
	if got := countExecutions(t, db); got != 43 {
		t.Fatalf("executions left = %d, want 43 (edge + new)", got)
	}
	var old int
	_ = db.read.QueryRow(`SELECT COUNT(*) FROM executions WHERE id LIKE 'old%' OR id = 'bare'`).Scan(&old)
	if old != 0 {
		t.Fatalf("%d expired executions survived", old)
	}
	for _, tbl := range executionChildTables {
		if got := tableCount(t, db, tbl); got != 43 {
			t.Fatalf("%s has %d rows, want 43", tbl, got)
		}
	}
	assertExecutionsConsistent(t, db)
}

// A purge cancelled between batches leaves a consistent database, and a later
// full run finishes the job.
func TestPurgeExecutionsCancelLeavesConsistentState(t *testing.T) {
	db := newTestDB(t)
	const total = 6 * purgeChunk
	seedExecutionsAt(t, db, "old", total, "2020-01-01 00:00:00")
	seedExecutionsAt(t, db, "new", 10, time.Now().UTC().Format("2006-01-02 15:04:05"))

	ctx, cancel := context.WithCancel(context.Background())
	// The watcher must not call t.Fatal (it can outlive the test) and is always
	// awaited before the test returns.
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			var n int
			if db.read.QueryRow(`SELECT COUNT(*) FROM executions`).Scan(&n) != nil || n <= total { // some batches have gone
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	removed, err := db.PurgeOldExecutions(ctx, 30)
	close(stop)
	<-done
	if err == nil || ctx.Err() == nil {
		t.Skipf("purge finished before it could be cancelled (err=%v)", err)
	}
	left := countExecutions(t, db)
	if left <= 10 || left >= total+10 {
		t.Fatalf("expected a partial purge, %d executions left", left)
	}
	if int(removed) != total+10-left {
		t.Fatalf("PurgeOldExecutions reported %d removed, but %d rows actually went", removed, total+10-left)
	}
	assertExecutionsConsistent(t, db)

	if _, err := db.PurgeOldExecutions(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	if got := countExecutions(t, db); got != 10 {
		t.Fatalf("after the follow-up run %d executions remain, want 10", got)
	}
	assertExecutionsConsistent(t, db)
}

func TestPurgeAlreadyCancelledDeletesNothing(t *testing.T) {
	db := newTestDB(t)
	seedExecutionsAt(t, db, "old", 20, "2020-01-01 00:00:00")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.PurgeOldExecutions(ctx, 30); err == nil {
		t.Fatal("a cancelled context must return an error")
	}
	if got := countExecutions(t, db); got != 20 {
		t.Fatalf("%d executions left, want 20", got)
	}
	assertExecutionsConsistent(t, db)
}

// probeWriterWaits writes through the writer connection every ~2 ms until fn
// returns, and returns each write's wait.
func probeWriterWaits(t *testing.T, db *Database, fn func() error) []time.Duration {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	var waits []time.Duration
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return waits
		default:
			start := time.Now()
			if err := db.SetSystemConfig("probe", "x"); err != nil {
				t.Fatal(err)
			}
			waits = append(waits, time.Since(start))
			time.Sleep(2 * time.Millisecond)
		}
	}
}

func assertWriterStayedResponsive(t *testing.T, what string, waits []time.Duration) {
	t.Helper()
	if len(waits) < 5 {
		t.Fatalf("only %d probes landed during the %s; it did not yield the writer", len(waits), what)
	}
	sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
	if m := waits[len(waits)/2]; m > 250*time.Millisecond {
		t.Fatalf("median write wait %s during the %s", m, what)
	}
	if w := waits[len(waits)-1]; w > time.Second {
		t.Fatalf("a write waited %s during the %s; chunks must keep holds short", w, what)
	}
}

func TestPurgeExecutionsDoesNotMonopoliseTheWriter(t *testing.T) {
	db := newTestDB(t)
	seedExecutionsAt(t, db, "old", 4000, "2020-01-01 00:00:00")
	waits := probeWriterWaits(t, db, func() error {
		_, err := db.PurgeOldExecutions(context.Background(), 30)
		return err
	})
	if got := countExecutions(t, db); got != 0 {
		t.Fatalf("%d executions left", got)
	}
	assertWriterStayedResponsive(t, "purge", waits)
}

// The id-selection query must be an index range search on started_at.
func TestPurgeExecutionIDQueryUsesTheStartedAtIndex(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.read.Query(`EXPLAIN QUERY PLAN SELECT id FROM executions WHERE started_at < ? LIMIT 500`, "2020-01-01 00:00:00")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, "idx_executions_started") || !strings.Contains(joined, "started_at<") || strings.Contains(joined, "SCAN") {
		t.Fatalf("id query plan is not an index range search: %s", joined)
	}
}

func seedExpiredKV(t *testing.T, db *Database, n int) {
	t.Helper()
	if _, err := db.write.Exec(`INSERT OR IGNORE INTO functions (id, name, runtime) VALUES ('fn-kv','fn-kv','node')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	st, err := tx.Prepare(`INSERT INTO kv_store (function_id, key, value, expires_at) VALUES ('fn-kv', ?, x'00', ?)`)
	if err != nil {
		t.Fatal(err)
	}
	past, future := time.Now().Add(-time.Hour).UTC(), time.Now().Add(time.Hour).UTC()
	for i := 0; i < n; i++ {
		if _, err := st.Exec(fmt.Sprintf("exp%07d", i), past); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 30; i++ {
		if _, err := st.Exec(fmt.Sprintf("live%03d", i), future); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO kv_store (function_id, key, value) VALUES ('fn-kv', ?, x'00')`, fmt.Sprintf("forever%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestKVSweepExpiredEquivalenceAcrossBatches(t *testing.T) {
	db := newTestDB(t)
	seedExpiredKV(t, db, 2*purgeChunk+11)
	n, err := db.KVSweepExpired(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2*purgeChunk+11 {
		t.Fatalf("removed %d, want %d", n, 2*purgeChunk+11)
	}
	if got := tableCount(t, db, "kv_store"); got != 60 {
		t.Fatalf("%d keys left, want the 30 live + 30 non-expiring", got)
	}
	if n, err := db.KVSweepExpired(context.Background()); err != nil || n != 0 {
		t.Fatalf("second sweep removed %d (err %v)", n, err)
	}
}

func TestKVSweepExpiredDoesNotMonopoliseTheWriter(t *testing.T) {
	db := newTestDB(t)
	seedExpiredKV(t, db, 8000)
	waits := probeWriterWaits(t, db, func() error { _, err := db.KVSweepExpired(context.Background()); return err })
	if got := tableCount(t, db, "kv_store"); got != 60 {
		t.Fatalf("%d keys left, want 60", got)
	}
	assertWriterStayedResponsive(t, "kv sweep", waits)
}

func TestKVSweepExpiredHonoursCancel(t *testing.T) {
	db := newTestDB(t)
	seedExpiredKV(t, db, 3*purgeChunk)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.KVSweepExpired(ctx); err == nil {
		t.Fatal("a cancelled context must stop the sweep")
	}
	if got := tableCount(t, db, "kv_store"); got != 3*purgeChunk+60 {
		t.Fatalf("a cancelled sweep changed the table: %d rows", got)
	}
	if n, err := db.KVSweepExpired(context.Background()); err != nil || n != int64(3*purgeChunk) {
		t.Fatalf("follow-up sweep removed %d (err %v)", n, err)
	}
}

// The kv chunk query must use the partial expiry index rather than scan the table.
func TestKVSweepChunkQueryUsesTheExpiryIndex(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.read.Query(`EXPLAIN QUERY PLAN SELECT rowid FROM kv_store WHERE expires_at IS NOT NULL AND expires_at <= ? LIMIT 500`, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if joined := strings.Join(plan, " | "); !strings.Contains(joined, "idx_kv_expires") || strings.Contains(joined, "SCAN") {
		t.Fatalf("kv chunk plan is not an index search: %s", joined)
	}
}
