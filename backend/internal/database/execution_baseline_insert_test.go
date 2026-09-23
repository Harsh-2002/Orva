package database

import (
	"testing"
	"time"
)

func TestAsyncInsertExecutionFinalPersistsBaselineInInsert(t *testing.T) {
	db := newTestDB(t)
	if err := db.InsertFunction(&Function{
		ID: "fn_baseline", Name: "baseline-insert", Runtime: "python",
		Entrypoint: "handler.py", TimeoutMS: 5000, MemoryMB: 64,
		CPUs: 0.5, NetworkMode: "none", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	p95 := int64(42)
	db.AsyncInsertExecutionFinal(&Execution{
		ID: "exec_baseline", FunctionID: "fn_baseline", Status: "success",
		IsOutlier: true, BaselineP95MS: &p95,
	}, 100, 200, "", 2)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := db.GetExecution("exec_baseline")
		if err == nil && got != nil {
			if !got.IsOutlier || got.BaselineP95MS == nil || *got.BaselineP95MS != p95 {
				t.Fatalf("persisted baseline = outlier:%t p95:%v", got.IsOutlier, got.BaselineP95MS)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("execution did not commit: %+v", db.WriterStats())
}
