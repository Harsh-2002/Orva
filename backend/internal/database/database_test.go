package database

import (
	"path/filepath"
	"testing"
	"time"
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

	// Verify seed data — bump this when migrations.go gains new rows.
	var count int
	db.read.QueryRow("SELECT COUNT(*) FROM system_config").Scan(&count)
	if count != 21 {
		t.Errorf("expected 21 system config rows, got %d", count)
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

func TestFixtureCRUD(t *testing.T) {
	db := newTestDB(t)

	fn := &Function{
		ID: "fn_fixt12345678", Name: "fixture-test", Runtime: "node22",
		Entrypoint: "handler.js", TimeoutMS: 30000, MemoryMB: 128,
		CPUs: 0.5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}

	// Insert
	f := &Fixture{
		FunctionID:  fn.ID,
		Name:        "hello",
		Method:      "GET",
		Path:        "/",
		HeadersJSON: `{"X-Foo":"bar"}`,
		Body:        []byte(`{"name":"world"}`),
	}
	if err := db.InsertFixture(f); err != nil {
		t.Fatal(err)
	}
	if f.ID == "" {
		t.Errorf("expected ID to be auto-assigned")
	}

	// UNIQUE(function_id, name) — duplicate name should fail.
	dup := &Fixture{FunctionID: fn.ID, Name: "hello", Method: "POST", Path: "/", HeadersJSON: `{}`}
	if err := db.InsertFixture(dup); err == nil {
		t.Error("expected UNIQUE conflict on duplicate name")
	}

	// Get by name
	got, err := db.GetFixtureByName(fn.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "GET" || got.Path != "/" {
		t.Errorf("unexpected fixture fields: %+v", got)
	}

	// List
	rows, err := db.ListFixtures(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 fixture, got %d", len(rows))
	}

	// Upsert via UpsertFixture should overwrite method/path/body but
	// keep id and created_at.
	saved, err := db.UpsertFixture(&Fixture{
		FunctionID: fn.ID, Name: "hello", Method: "POST", Path: "/echo",
		HeadersJSON: `{"X-Bar":"baz"}`, Body: []byte("hi"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID != got.ID {
		t.Errorf("upsert should keep id; got %s vs original %s", saved.ID, got.ID)
	}
	if saved.Method != "POST" || saved.Path != "/echo" {
		t.Errorf("upsert did not overwrite: %+v", saved)
	}

	// Delete (idempotent)
	if err := db.DeleteFixture(fn.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteFixture(fn.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetFixtureByName(fn.ID, "hello"); err == nil {
		t.Error("expected ErrFixtureNotFound after delete")
	}

	// Cascade: deleting the function removes its fixtures.
	if err := db.InsertFixture(&Fixture{
		FunctionID: fn.ID, Name: "again", Method: "GET", Path: "/", HeadersJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	db.write.Exec("PRAGMA foreign_keys = ON")
	if err := db.DeleteFunction(fn.ID); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.ListFixtures(fn.ID)
	if len(rows) != 0 {
		t.Errorf("expected fixtures to cascade-delete with the function, got %d rows", len(rows))
	}
}

// TestInboundWebhookCRUD covers v0.4 C2a — list/insert/update/delete
// of inbound webhook trigger rows + verifies that deleting the owning
// function cascades through the FK.
func TestInboundWebhookCRUD(t *testing.T) {
	db := newTestDB(t)

	fn := &Function{
		ID: "fn_inb12345678", Name: "inbound-test", Runtime: "node22",
		Entrypoint: "handler.js", TimeoutMS: 30000, MemoryMB: 128,
		CPUs: 0.5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}

	hook := &InboundWebhook{
		FunctionID:      fn.ID,
		Name:            "github-deploys",
		Secret:          NewInboundWebhookSecret(),
		SignatureHeader: "X-Hub-Signature-256",
		SignatureFormat: "github",
		Active:          true,
	}
	if err := db.InsertInboundWebhook(hook); err != nil {
		t.Fatal(err)
	}
	if hook.ID == "" {
		t.Errorf("expected ID to be auto-assigned")
	}

	got, err := db.GetInboundWebhook(hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SignatureFormat != "github" || got.Name != "github-deploys" {
		t.Errorf("unexpected fields: %+v", got)
	}
	if got.Secret != hook.Secret {
		t.Errorf("secret should round-trip; got %q vs %q", got.Secret, hook.Secret)
	}

	// Reject unknown format on insert.
	bad := &InboundWebhook{
		FunctionID: fn.ID, Name: "x",
		Secret: "abc", SignatureFormat: "made-up",
	}
	if err := db.InsertInboundWebhook(bad); err == nil {
		t.Error("expected unknown signature_format to fail insert")
	}

	// Update name + flip active off.
	got.Name = "renamed"
	got.Active = false
	if err := db.UpdateInboundWebhook(got); err != nil {
		t.Fatal(err)
	}
	got2, _ := db.GetInboundWebhook(hook.ID)
	if got2.Name != "renamed" || got2.Active {
		t.Errorf("update did not stick: %+v", got2)
	}

	rows, err := db.ListInboundWebhooksForFunction(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}

	// Cascade: deleting the function removes its inbound webhooks.
	db.write.Exec("PRAGMA foreign_keys = ON")
	if err := db.DeleteFunction(fn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetInboundWebhook(hook.ID); err == nil {
		t.Error("expected inbound webhook to cascade-delete with the function")
	}
}

// TestJobScheduledAtFiltering covers the v0.4 C2b feature: a job
// scheduled in the future MUST NOT be claimed before its time, and a
// job whose scheduled_at is in the past MUST be claimed immediately.
// The scheduled_at column already exists; this test pins the
// ClaimDueJobs filter behaviour against regression.
func TestJobScheduledAtFiltering(t *testing.T) {
	db := newTestDB(t)

	fn := &Function{
		ID: "fn_jobsched12345", Name: "job-sched-test", Runtime: "node22",
		Entrypoint: "handler.js", TimeoutMS: 30000, MemoryMB: 64,
		CPUs: 0.5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	future := &Job{
		FunctionID:  fn.ID,
		Payload:     []byte("{}"),
		ScheduledAt: now.Add(2 * time.Hour),
	}
	past := &Job{
		FunctionID:  fn.ID,
		Payload:     []byte("{}"),
		ScheduledAt: now.Add(-1 * time.Minute),
	}
	if err := db.EnqueueJob(future); err != nil {
		t.Fatal(err)
	}
	if err := db.EnqueueJob(past); err != nil {
		t.Fatal(err)
	}

	claimed, err := db.ClaimDueJobs(now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected exactly 1 due job (the past one), got %d", len(claimed))
	}
	if claimed[0].ID != past.ID {
		t.Errorf("claimed wrong job: got %s, want %s", claimed[0].ID, past.ID)
	}

	// Advance the clock — now the future job should also be due.
	claimed2, err := db.ClaimDueJobs(now.Add(3*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed2) != 1 || claimed2[0].ID != future.ID {
		t.Fatalf("expected future job to be due after clock advance; got %+v", claimed2)
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
