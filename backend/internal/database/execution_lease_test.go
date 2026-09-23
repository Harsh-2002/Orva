package database

import (
	"context"
	"errors"
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
