package database

import (
	"fmt"
	"testing"
	"time"
)

func TestAsyncWriterCommitRepeatedStatementAndFailureIsolation(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.write.Exec("CREATE TABLE batch_prepare_test (id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	const statement = "INSERT INTO batch_prepare_test (id, value) VALUES (?, ?)"
	batch := make([]writeJob, 0, 51)
	enqueuedAt := time.Now().Add(-time.Millisecond).UnixNano()
	for i := 0; i < 50; i++ {
		batch = append(batch, writeJob{sql: statement, args: []any{i, fmt.Sprint(i)}, enqueuedAt: enqueuedAt})
	}
	if retry := db.writer.commit(batch, writeCritical); len(retry) != 0 {
		t.Fatalf("repeated prepared statement left %d jobs for retry", len(retry))
	}
	var count int
	if err := db.read.QueryRow("SELECT COUNT(*) FROM batch_prepare_test").Scan(&count); err != nil || count != 50 {
		t.Fatalf("committed rows = %d, err = %v", count, err)
	}
	timing := db.WriterStats().Timing[writeCritical]
	if timing.Attempts != 1 || timing.CommittedJobs != 50 || timing.QueueWaitCount != 50 ||
		timing.QueueWaitNS == 0 || timing.StatementNS == 0 || timing.CommitNS == 0 {
		t.Fatalf("incomplete critical batch timings: %+v", timing)
	}

	// A duplicate key must still enter the savepoint recovery path, preserve
	// the following valid row, and report the one permanently bad job.
	failedBefore := db.WriterStats().CriticalFailures
	batch = []writeJob{
		{sql: statement, args: []any{50, "good"}},
		{sql: statement, args: []any{0, "duplicate"}},
		{sql: statement, args: []any{51, "after"}},
	}
	if retry := db.writer.commit(batch, writeCritical); len(retry) != 0 {
		t.Fatalf("duplicate key left %d jobs for retry", len(retry))
	}
	if err := db.read.QueryRow("SELECT COUNT(*) FROM batch_prepare_test").Scan(&count); err != nil || count != 52 {
		t.Fatalf("isolated rows = %d, err = %v", count, err)
	}
	if got := db.WriterStats().CriticalFailures; got != failedBefore+1 {
		t.Fatalf("critical failures = %d, want %d", got, failedBefore+1)
	}
}

func TestBulkGroupEndBoundsWideStatementsWithoutCappingNarrowBatches(t *testing.T) {
	wide := make([]writeJob, 200)
	for i := range wide {
		wide[i] = writeJob{sql: "INSERT INTO executions VALUES (?)", args: make([]any, 17), bulkInsert: true}
	}
	for start := 0; start < len(wide); start += 50 {
		end, binds := bulkGroupEnd(wide, start)
		if end != start+50 || binds != 850 {
			t.Fatalf("wide group from %d ended at %d with %d binds", start, end, binds)
		}
	}
	narrow := make([]writeJob, 200)
	for i := range narrow {
		narrow[i] = writeJob{sql: "INSERT INTO activity VALUES (?)", args: []any{i}, bulkInsert: true}
	}
	if end, binds := bulkGroupEnd(narrow, 0); end != 200 || binds != 200 {
		t.Fatalf("narrow batch ended at %d with %d binds", end, binds)
	}
	wide[1].sql = "INSERT INTO other VALUES (?)"
	if end, _ := bulkGroupEnd(wide, 0); end != 1 {
		t.Fatalf("group crossed SQL boundary: %d", end)
	}
}

func TestAsyncWriterWideBulkInsertRecoversAcrossGroupBoundary(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.write.Exec(`INSERT INTO functions (id, name, runtime, entrypoint)
		VALUES ('wide-batch-fn', 'wide-batch-fn', 'node', 'handler.js')`); err != nil {
		t.Fatal(err)
	}
	const statement = `INSERT INTO executions (
		id, function_id, status, cold_start, container_id,
		duration_ms, status_code, error_message, response_size,
		started_at, finished_at, trace_id, span_id, parent_span_id,
		trigger, parent_function_id, is_outlier, baseline_p95_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?)`
	batch := make([]writeJob, 101)
	for i := range batch {
		id := fmt.Sprintf("wide-batch-%03d", i)
		if i == 50 {
			id = "wide-batch-000" // First row of the second group is invalid.
		}
		batch[i] = writeJob{sql: statement, bulkInsert: true, args: []any{
			id, "wide-batch-fn", "success", 0, "worker", int64(1), 200,
			"", 1, time.Now().UTC(), "trace", id, nil, "http", nil, false, nil,
		}}
	}
	if retry := db.writer.commit(batch, writeCritical); len(retry) != 0 {
		t.Fatalf("wide batch left %d jobs for retry", len(retry))
	}
	var count int
	if err := db.read.QueryRow(`SELECT COUNT(*) FROM executions WHERE function_id = 'wide-batch-fn'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 100 || db.WriterStats().CriticalFailures != 1 {
		t.Fatalf("wide group recovery: rows=%d failures=%d, want 100 and 1", count, db.WriterStats().CriticalFailures)
	}
}
