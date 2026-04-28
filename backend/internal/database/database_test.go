package database

import (
	"path/filepath"
	"testing"
)

func newTestDB(t *testing.T) *Database {
	t.Helper()
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrate(t *testing.T) {
	db := newTestDB(t)

	// Verify tables exist
	tables := []string{"functions", "executions", "execution_logs", "pool_config", "api_keys", "system_config"}
	for _, table := range tables {
		var name string
		err := db.read.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}

	// Verify seed data
	var count int
	db.read.QueryRow("SELECT COUNT(*) FROM system_config").Scan(&count)
	if count != 11 {
		t.Errorf("expected 11 system config rows, got %d", count)
	}
}

func TestFunctionCRUD(t *testing.T) {
	db := newTestDB(t)

	fn := &Function{
		ID:          "fn_test123456",
		Name:        "hello-world",
		Runtime:     "node22",
		Entrypoint:  "handler.js",
		TimeoutMS:   30000,
		MemoryMB:    128,
		CPUs:        0.5,
		EnvVars:     map[string]string{"FOO": "bar"},
		NetworkMode: "none",
		Status:      "created",
	}

	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}

	// Get by ID
	got, err := db.GetFunction("fn_test123456")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "hello-world" {
		t.Errorf("expected name hello-world, got %s", got.Name)
	}
	if got.EnvVars["FOO"] != "bar" {
		t.Errorf("expected env var FOO=bar, got %v", got.EnvVars)
	}

	// Get by name
	got, err = db.GetFunctionByName("hello-world")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "fn_test123456" {
		t.Errorf("expected id fn_test123456, got %s", got.ID)
	}

	// Update
	got.Status = "active"
	got.Version = 1
	got.Image = "orva-fn-test:v1"
	if err := db.UpdateFunction(got); err != nil {
		t.Fatal(err)
	}

	got, _ = db.GetFunction("fn_test123456")
	if got.Status != "active" || got.Version != 1 {
		t.Errorf("update failed: status=%s version=%d", got.Status, got.Version)
	}

	// List
	result, err := db.ListFunctions(ListFunctionsParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Errorf("expected 1 function, got %d", result.Total)
	}

	// Delete
	if err := db.DeleteFunction("fn_test123456"); err != nil {
		t.Fatal(err)
	}
	_, err = db.GetFunction("fn_test123456")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestExecutionCRUD(t *testing.T) {
	db := newTestDB(t)

	fn := &Function{
		ID: "fn_test123456", Name: "test-fn", Runtime: "node22",
		Entrypoint: "handler.js", TimeoutMS: 30000, MemoryMB: 128,
		CPUs: 0.5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	}
	db.InsertFunction(fn)

	exec := &Execution{
		ID:         "exec_test12345",
		FunctionID: "fn_test123456",
		Status:     "running",
		ColdStart:  true,
	}
	if err := db.InsertExecution(exec); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateExecution("exec_test12345", "success", 45, 200, "", 100); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetExecution("exec_test12345")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" || !got.ColdStart {
		t.Errorf("unexpected: status=%s cold_start=%v", got.Status, got.ColdStart)
	}
	if got.DurationMS == nil || *got.DurationMS != 45 {
		t.Errorf("expected duration 45, got %v", got.DurationMS)
	}

	// Logs
	log := &ExecutionLog{ExecutionID: "exec_test12345", Stdout: "hello\n", Stderr: ""}
	if err := db.InsertExecutionLog(log); err != nil {
		t.Fatal(err)
	}
	gotLog, err := db.GetExecutionLog("exec_test12345")
	if err != nil {
		t.Fatal(err)
	}
	if gotLog.Stdout != "hello\n" {
		t.Errorf("expected stdout 'hello\\n', got '%s'", gotLog.Stdout)
	}
}

func TestAPIKeyCRUD(t *testing.T) {
	db := newTestDB(t)

	key := &APIKey{
		ID:          "key_test123456",
		KeyHash:     "abc123hash",
		Name:        "test-key",
		Permissions: `["invoke","read","write","admin"]`,
	}
	if err := db.InsertAPIKey(key); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetAPIKeyByHash("abc123hash")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "test-key" {
		t.Errorf("expected name test-key, got %s", got.Name)
	}

	keys, err := db.ListAPIKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}

	if err := db.DeleteAPIKey("key_test123456"); err != nil {
		t.Fatal(err)
	}

	_, err = db.GetAPIKeyByHash("abc123hash")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestCascadeDelete(t *testing.T) {
	db := newTestDB(t)

	fn := &Function{
		ID: "fn_cascade1234", Name: "cascade-test", Runtime: "node22",
		Entrypoint: "handler.js", TimeoutMS: 30000, MemoryMB: 128,
		CPUs: 0.5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	}
	db.InsertFunction(fn)

	exec := &Execution{ID: "exec_cascade123", FunctionID: "fn_cascade1234", Status: "success"}
	db.InsertExecution(exec)
	db.InsertExecutionLog(&ExecutionLog{ExecutionID: "exec_cascade123", Stdout: "test"})

	// Enable foreign keys for this connection
	db.write.Exec("PRAGMA foreign_keys = ON")

	if err := db.DeleteFunction("fn_cascade1234"); err != nil {
		t.Fatal(err)
	}

	_, err := db.GetExecution("exec_cascade123")
	if err == nil {
		t.Error("expected execution to be cascade deleted")
	}

	_, err = db.GetExecutionLog("exec_cascade123")
	if err == nil {
		t.Error("expected execution log to be cascade deleted")
	}
}
