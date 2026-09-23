package database

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDeleteFunctionDiscardsQueuedExecutionChildren(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-delete-queued")
	db.writer.stop(5 * time.Second)
	db.writer = newAsyncWriter(db)

	db.AsyncInsertExecutionFinal(&Execution{
		ID: "exec-delete-queued", FunctionID: "fn-delete-queued", Status: "success",
	}, 1, 200, "", 2)
	db.AsyncInsertExecutionRequest(&ExecutionRequest{
		ExecutionID: "exec-delete-queued", FunctionID: "fn-delete-queued",
		Method: "GET", Path: "/", HeadersJSON: "{}", CapturedAt: time.Now().UnixMilli(),
	})
	db.AsyncInsertUserSpan(&UserSpan{
		FunctionID: "fn-delete-queued", ExecutionID: "exec-delete-queued",
		TraceID: "tr-delete-queued", ParentSpanID: "sp-parent", Name: "work",
		StartedAt: time.Now(),
	})
	db.AsyncInsertLogEntry(&LogEntry{
		FunctionID: "fn-delete-queued", ExecutionID: "exec-delete-queued",
		TS: time.Now(), Level: "info", Message: "work",
	})
	db.AsyncInsertExecutionLog(&ExecutionLog{
		FunctionID: "fn-delete-queued", ExecutionID: "exec-delete-queued",
		Stdout: "work",
	})
	if err := db.DeleteFunction("fn-delete-queued"); err != nil {
		t.Fatal(err)
	}
	db.writer.start()
	if !waitFor(t, 5*time.Second, func() bool {
		return db.WriterStats().DeletedWrites == 5 && db.WriterStats().CriticalBytes == 0 &&
			db.WriterStats().TelemetryBytes == 0
	}) {
		t.Fatalf("writer did not discard deleted function jobs: %+v", db.WriterStats())
	}
	for _, table := range append([]string{"executions"}, executionChildTables...) {
		if got := countQuery(t, db, "SELECT COUNT(*) FROM "+table+" WHERE "+
			map[bool]string{true: "id", false: "execution_id"}[table == "executions"]+" = ?", "exec-delete-queued"); got != 0 {
			t.Errorf("%s kept %d deleted-function rows", table, got)
		}
	}
	if got := db.WriterStats().CriticalFailures; got != 0 {
		t.Errorf("deleted-function execution counted as critical failure: %d", got)
	}
}

func TestMigrationAddsFunctionOwnershipAfterLegacyCaptureRebuild(t *testing.T) {
	children := make(map[string]bool, len(executionChildTables))
	for _, table := range executionChildTables {
		children[table] = true
	}
	for _, table := range executionOwnedChildTables {
		if !children[table] {
			t.Fatalf("owned table %s is absent from executionChildTables", table)
		}
	}
	db, err := New(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.write.Exec(`CREATE TABLE execution_requests (
		execution_id TEXT PRIMARY KEY REFERENCES executions(id),
		method TEXT NOT NULL, path TEXT NOT NULL, headers_json TEXT NOT NULL,
		body BLOB, truncated INTEGER NOT NULL DEFAULT 0, captured_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, table := range executionOwnedChildTables {
		var count int
		if err := db.read.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'function_id'`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s missing function ownership column after migration", table)
		}
	}
}

func TestDeleteFunctionRemovesChildrenWithoutParentExecution(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-delete-orphan")
	db.AsyncInsertExecutionRequest(&ExecutionRequest{
		ExecutionID: "exec-delete-orphan", FunctionID: "fn-delete-orphan",
		Method: "GET", Path: "/", HeadersJSON: "{}", CapturedAt: time.Now().UnixMilli(),
	})
	db.AsyncInsertUserSpan(&UserSpan{
		FunctionID: "fn-delete-orphan", ExecutionID: "exec-delete-orphan",
		TraceID: "tr-delete-orphan", ParentSpanID: "sp-parent", Name: "work",
		StartedAt: time.Now(),
	})
	db.AsyncInsertLogEntry(&LogEntry{
		FunctionID: "fn-delete-orphan", ExecutionID: "exec-delete-orphan",
		TS: time.Now(), Level: "info", Message: "work",
	})
	if !waitFor(t, 5*time.Second, func() bool {
		for _, table := range executionOwnedChildTables {
			if countQuery(t, db, "SELECT COUNT(*) FROM "+table+" WHERE execution_id = ?", "exec-delete-orphan") != 1 {
				return false
			}
		}
		return true
	}) {
		t.Fatal("pre-parent telemetry did not commit")
	}
	if err := db.DeleteFunction("fn-delete-orphan"); err != nil {
		t.Fatal(err)
	}
	for _, table := range executionOwnedChildTables {
		if got := countQuery(t, db, "SELECT COUNT(*) FROM "+table+" WHERE execution_id = ?", "exec-delete-orphan"); got != 0 {
			t.Errorf("%s kept %d parentless rows", table, got)
		}
	}
}

func TestDeleteFunctionConcurrentWithExecutionEnqueues(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-delete-race")
	const jobs = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range jobs {
			db.AsyncInsertExecutionFinal(&Execution{
				ID:         fmt.Sprintf("exec-delete-race-%d", i),
				FunctionID: "fn-delete-race", Status: "success",
			}, 1, 200, "", 2)
		}
	}()
	if err := db.DeleteFunction("fn-delete-race"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !waitFor(t, 5*time.Second, func() bool { return db.WriterStats().CriticalBytes == 0 }) {
		t.Fatalf("critical queue did not drain: %+v", db.WriterStats())
	}
	if got := db.WriterStats().CriticalFailures; got != 0 {
		t.Fatalf("concurrent deletion caused %d critical failures", got)
	}
	if got := countQuery(t, db, "SELECT COUNT(*) FROM executions WHERE function_id = ?", "fn-delete-race"); got != 0 {
		t.Fatalf("deleted function has %d execution rows", got)
	}
}
