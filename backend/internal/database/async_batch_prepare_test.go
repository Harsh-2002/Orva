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
