package scheduler

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestJobsTickDoesNotClaimAttemptWhenExecutionStorageIsFull(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "orva.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f50"
	if err := db.InsertFunction(&database.Function{
		ID: fnID, Name: "storage-pressure-job", Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 64, CPUs: 0.5, NetworkMode: "none", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	job := &database.Job{FunctionID: fnID, Payload: []byte("{}")}
	if err := db.EnqueueJob(job); err != nil {
		t.Fatal(err)
	}

	leases := make([]*database.ExecutionLease, 0, db.WriterStats().CriticalCap)
	for range db.WriterStats().CriticalCap {
		lease, err := db.ReserveExecution(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	defer func() {
		for _, lease := range leases {
			lease.Cancel()
		}
	}()

	New(db, nil, t.TempDir(), nil).jobsTick(context.Background())
	got, err := db.GetJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || got.Attempts != 0 {
		t.Fatalf("storage pressure consumed job attempt without execution: status=%s attempts=%d", got.Status, got.Attempts)
	}
}
