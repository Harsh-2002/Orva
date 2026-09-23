package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExecutionLeaseRejectsBeforeWorkWhenWriterCapacityIsReserved(t *testing.T) {
	db := &Database{}
	db.writer = newAsyncWriter(db)
	leases := make([]*ExecutionLease, 0, cap(db.writer.criticalSlots))
	for range cap(db.writer.criticalSlots) {
		lease, err := db.ReserveExecution(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := db.ReserveExecution(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("full writer reservation should reject before work: %v", err)
	}
	leases[0].Cancel()
	lease, err := db.ReserveExecution(context.Background())
	if err != nil {
		t.Fatalf("capacity did not recover after cancellation: %v", err)
	}
	lease.Cancel()
	for _, lease := range leases[1:] {
		lease.Cancel()
	}
	if got := len(db.writer.criticalSlots); got != cap(db.writer.criticalSlots) {
		t.Fatalf("leaked writer reservations: %d free of %d", got, cap(db.writer.criticalSlots))
	}
}

func TestBulkExecutionInsertIsolatesBadRowAndReturnsEveryLease(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-bulk")
	db.writer.stop(5 * time.Second)
	db.writer = newAsyncWriter(db)

	for _, id := range []string{"exec-bulk-a", "exec-bulk-a", "exec-bulk-b"} {
		lease, err := db.ReserveExecution(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := db.AsyncInsertExecutionFinal(&Execution{
			ID: id, FunctionID: "fn-bulk", Status: "success",
		}, 1, 200, "", 2, lease); err != nil {
			t.Fatal(err)
		}
		lease.Cancel()
	}
	db.writer.start()
	if !waitFor(t, 5*time.Second, func() bool {
		return countQuery(t, db, "SELECT COUNT(*) FROM executions WHERE function_id = ?", "fn-bulk") == 2 &&
			db.WriterStats().CriticalFailures == 1 &&
			len(db.writer.criticalSlots) == cap(db.writer.criticalSlots)
	}) {
		t.Fatalf("bulk fallback lost a valid row or leaked a lease: %+v", db.WriterStats())
	}
	if countQuery(t, db, "SELECT COUNT(*) FROM executions WHERE id = ?", "exec-bulk-b") != 1 {
		t.Fatal("valid row after a duplicate was not persisted")
	}
}

func TestBulkExecutionInsertPersistsWholeBatch(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-bulk-ok")
	db.writer.stop(5 * time.Second)
	db.writer = newAsyncWriter(db)
	for i := 0; i < 200; i++ {
		lease, err := db.ReserveExecution(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := db.AsyncInsertExecutionFinal(&Execution{
			ID: fmt.Sprintf("exec-bulk-%03d", i), FunctionID: "fn-bulk-ok", Status: "success",
		}, 1, 200, "", 2, lease); err != nil {
			t.Fatal(err)
		}
		lease.Cancel()
	}
	db.writer.start()
	if !waitFor(t, 5*time.Second, func() bool {
		return countQuery(t, db, "SELECT COUNT(*) FROM executions WHERE function_id = ?", "fn-bulk-ok") == 200 &&
			len(db.writer.criticalSlots) == cap(db.writer.criticalSlots)
	}) {
		t.Fatalf("bulk insert did not persist every row or return leases: %+v", db.WriterStats())
	}
}

func TestBulkInsertSQLRepeatsOnlyMarkedValuesTuple(t *testing.T) {
	statement := "INSERT INTO executions(id, finished_at) VALUES (?, CURRENT_TIMESTAMP)"
	bulk := bulkInsertSQL(statement, 3)
	if strings.Count(bulk, "CURRENT_TIMESTAMP") != 3 || strings.Count(bulk, "?") != 3 {
		t.Fatalf("unexpected bulk SQL: %q", bulk)
	}
	if bulkInsertSQL("SELECT 1", 3) != "" || bulkInsertSQL(statement, 1) != "" {
		t.Fatal("unrecognized or single-row SQL should not be bulked")
	}
}

func TestBulkActivityRowsPreserveOperatorFeed(t *testing.T) {
	db := newTestDB(t)
	db.writer.stop(5 * time.Second)
	db.writer = newAsyncWriter(db)
	for i := 0; i < 200; i++ {
		db.InsertActivity(ActivityRow{
			TS: int64(i + 1), Source: "web", ActorType: "anon",
			Method: "GET", Path: "/fn/bulk", Status: 200,
		})
	}
	first := <-db.writer.activity
	if !first.bulkInsert {
		t.Fatal("operator activity rows were not marked for grouped INSERT")
	}
	db.writer.activity <- first
	db.writer.start()
	if !waitFor(t, 5*time.Second, func() bool {
		return countQuery(t, db, "SELECT COUNT(*) FROM activity_log") == 200
	}) {
		t.Fatalf("grouped activity insert lost rows: %+v", db.WriterStats())
	}
}

func TestExecutionLeaseReturnsOnlyAfterFinalRowCommit(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-reserved")
	db.writer.stop(5 * time.Second)
	db.writer = newAsyncWriter(db)
	lease, err := db.ReserveExecution(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AsyncInsertExecutionFinal(&Execution{
		ID: "exec-reserved", FunctionID: "fn-reserved", Status: "success",
	}, 1, 200, "", 2, lease); err != nil {
		t.Fatal(err)
	}
	lease.Cancel() // handler defer must not release a job already owned by the writer
	if got := len(db.writer.criticalSlots); got != cap(db.writer.criticalSlots)-1 {
		t.Fatalf("completion slot released before commit: %d free", got)
	}
	db.writer.start()
	if !waitFor(t, 5*time.Second, func() bool {
		return countQuery(t, db, "SELECT COUNT(*) FROM executions WHERE id = ?", "exec-reserved") == 1 &&
			len(db.writer.criticalSlots) == cap(db.writer.criticalSlots)
	}) {
		t.Fatalf("committed execution did not return its slot: %+v", db.WriterStats())
	}
}
