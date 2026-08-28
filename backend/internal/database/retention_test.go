package database

import "testing"

// seedExecution writes an executions row with an explicit age in days, which is
// what retention keys off. The normal insert path stamps started_at itself, so
// the age is backdated directly.
func seedExecution(t *testing.T, db *Database, id string, ageDays int) {
	t.Helper()
	// executions.function_id is a real FK, so the parent must exist first.
	if _, err := db.write.Exec(
		`INSERT OR IGNORE INTO functions (id, name, runtime) VALUES ('fn-retention','fn-retention','node')`,
	); err != nil {
		t.Fatalf("seed parent function: %v", err)
	}
	_, err := db.write.Exec(
		`INSERT INTO executions (id, function_id, status, started_at)
		 VALUES (?, 'fn-retention', 'success', datetime('now', '-' || ? || ' days'))`,
		id, ageDays)
	if err != nil {
		t.Fatalf("seed execution %s: %v", id, err)
	}
}

func countExecutions(t *testing.T, db *Database) int {
	t.Helper()
	var n int
	if err := db.read.QueryRow("SELECT COUNT(*) FROM executions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestPurgeRemovesOnlyExpiredExecutions(t *testing.T) {
	db := newTestDB(t)
	seedExecution(t, db, "old-1", 60)
	seedExecution(t, db, "old-2", 31)
	seedExecution(t, db, "fresh-1", 5)
	seedExecution(t, db, "fresh-2", 0)

	if got := countExecutions(t, db); got != 4 {
		t.Fatalf("seed: want 4 rows, got %d", got)
	}
	if err := db.PurgeOldExecutions(DefaultRetentionDays); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if got := countExecutions(t, db); got != 2 {
		t.Fatalf("after purge: want the 2 in-window rows, got %d", got)
	}
	for _, id := range []string{"fresh-1", "fresh-2"} {
		var n int
		if err := db.read.QueryRow("SELECT COUNT(*) FROM executions WHERE id = ?", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s was purged but is inside the retention window", id)
		}
	}
}

// TestPurgeOnceIsDisabledByZero pins the escape hatch. Retention deletes
// user-visible diagnostic history, so an operator must be able to turn it off
// completely, and "0" must mean keep-everything rather than purge-everything.
func TestPurgeOnceIsDisabledByZero(t *testing.T) {
	db := newTestDB(t)
	seedExecution(t, db, "ancient", 3650)

	if err := db.SetSystemConfig(RetentionSettingKey, "0"); err != nil {
		t.Skipf("cannot set system config in this build: %v", err)
	}
	db.purgeOnce()

	if got := countExecutions(t, db); got != 1 {
		t.Fatalf("retention 0 must keep everything, got %d rows", got)
	}
}

// TestPurgeOnceUsesTheConfiguredWindow proves the setting is actually consulted
// rather than the default being hardcoded at the call site.
func TestPurgeOnceUsesTheConfiguredWindow(t *testing.T) {
	db := newTestDB(t)
	seedExecution(t, db, "day-10", 10)
	seedExecution(t, db, "day-1", 1)

	if err := db.SetSystemConfig(RetentionSettingKey, "5"); err != nil {
		t.Skipf("cannot set system config in this build: %v", err)
	}
	db.purgeOnce()

	if got := countExecutions(t, db); got != 1 {
		t.Fatalf("a 5-day window must drop the 10-day row and keep the 1-day row, got %d rows", got)
	}
}

// TestPurgeCascadesToEveryExecutionChildTable is the guard that the previous
// version of this test failed to be.
//
// Only execution_logs has ON DELETE CASCADE; the other three child tables are
// cleaned up by hand in PurgeOldExecutions. A table missing from that list is
// not merely un-purged — once the parent executions row is gone nothing can
// join the rows back, so they are permanently unreachable.
//
// The earlier version seeded execution_requests with only (execution_id, body),
// which violates three NOT NULL columns. The insert error was swallowed by
// t.Logf and the assertion was gated on the row count being non-zero, so it
// passed with nothing seeded and nothing checked — and that is exactly why
// user_spans and execution_log_entries were missed. Every seed here is a hard
// failure, and every table is asserted empty afterwards.
func TestPurgeCascadesToEveryExecutionChildTable(t *testing.T) {
	db := newTestDB(t)
	const id = "old-with-children"
	seedExecution(t, db, id, 90)

	seeds := map[string]string{
		"execution_logs": `INSERT INTO execution_logs (execution_id, stdout, stderr)
			VALUES ('` + id + `', 'out', 'err')`,
		"execution_requests": `INSERT INTO execution_requests
			(execution_id, method, path, headers_json, body, captured_at)
			VALUES ('` + id + `', 'POST', '/', '{}', 'x', 0)`,
		"user_spans": `INSERT INTO user_spans
			(id, trace_id, parent_span_id, execution_id, name, started_at, duration_ms)
			VALUES ('span-1', 'tr-1', '', '` + id + `', 'work', datetime('now'), 5)`,
		"execution_log_entries": `INSERT INTO execution_log_entries
			(execution_id, ts, level, message)
			VALUES ('` + id + `', datetime('now'), 'info', 'hello')`,
	}
	for table, stmt := range seeds {
		if _, err := db.write.Exec(stmt); err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}
	// Prove the seeds landed, so the post-purge assertion cannot pass vacuously.
	for table := range seeds {
		if n := countRows(t, db, table, id); n == 0 {
			t.Fatalf("seed %s inserted nothing; the assertion below would be vacuous", table)
		}
	}

	if err := db.PurgeOldExecutions(DefaultRetentionDays); err != nil {
		t.Fatalf("purge: %v", err)
	}

	for table := range seeds {
		if n := countRows(t, db, table, id); n != 0 {
			t.Errorf("purge left %d orphaned row(s) in %s — unreachable forever, "+
				"since the parent executions row is gone", n, table)
		}
	}
}

// TestEveryExecutionChildTableIsPurged fails when a new table keyed by
// execution_id is added to the schema without being added to the purge.
func TestEveryExecutionChildTableIsPurged(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.read.Query(`
		SELECT m.name FROM sqlite_master m
		JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND p.name = 'execution_id' AND m.name != 'executions'`)
	if err != nil {
		t.Skipf("cannot introspect schema in this build: %v", err)
	}
	defer rows.Close()

	covered := map[string]bool{}
	for _, t2 := range executionChildTables {
		covered[t2] = true
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if !covered[name] {
			t.Errorf("table %q is keyed by execution_id but is not in "+
				"executionChildTables; retention would orphan its rows permanently", name)
		}
	}
}

func countRows(t *testing.T, db *Database, table, execID string) int {
	t.Helper()
	var n int
	if err := db.read.QueryRow(
		"SELECT COUNT(*) FROM "+table+" WHERE execution_id = ?", execID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// seedExecutionChildren fills every executionChildTables row for execID. The
// inserts are hard failures so a later "table is empty" assertion can never
// pass vacuously — the trap that hid this class of bug in the purge test above.
func seedExecutionChildren(t *testing.T, db *Database, execID string) {
	t.Helper()
	stmts := map[string]string{
		"execution_logs": `INSERT INTO execution_logs (execution_id, stdout, stderr)
			VALUES (?, 'out', 'err')`,
		"execution_requests": `INSERT INTO execution_requests
			(execution_id, method, path, headers_json, body, captured_at)
			VALUES (?, 'POST', '/', '{}', 'x', 0)`,
		"user_spans": `INSERT INTO user_spans
			(id, trace_id, parent_span_id, execution_id, name, started_at, duration_ms)
			VALUES ('span-' || ?, 'tr-1', '', ?, 'work', datetime('now'), 5)`,
		"execution_log_entries": `INSERT INTO execution_log_entries
			(execution_id, ts, level, message)
			VALUES (?, datetime('now'), 'info', 'hello')`,
	}
	for table, stmt := range stmts {
		args := []any{execID}
		if table == "user_spans" {
			args = []any{execID, execID}
		}
		if _, err := db.write.Exec(stmt, args...); err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
		if n := countRows(t, db, table, execID); n == 0 {
			t.Fatalf("seed %s inserted nothing; later assertions would be vacuous", table)
		}
	}
}

func totalRows(t *testing.T, db *Database, table string) int {
	t.Helper()
	var n int
	if err := db.read.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestDeleteFunctionLeavesNoOrphanedChildRows is the guard DeleteFunction
// never had. ON DELETE CASCADE reaches executions and execution_logs; the
// other three child tables dropped their FK for async insert ordering, so
// deleting a function stranded their rows with nothing able to join them back.
func TestDeleteFunctionLeavesNoOrphanedChildRows(t *testing.T) {
	db := newTestDB(t)
	const execID = "exec-doomed"
	seedExecution(t, db, execID, 1) // creates parent function fn-retention
	seedExecutionChildren(t, db, execID)

	if err := db.DeleteFunction("fn-retention"); err != nil {
		t.Fatalf("delete function: %v", err)
	}

	if n := totalRows(t, db, "executions"); n != 0 {
		t.Errorf("executions: want 0 after the function was deleted, got %d", n)
	}
	for _, table := range executionChildTables {
		if n := totalRows(t, db, table); n != 0 {
			t.Errorf("%s kept %d row(s) whose function is gone — the parent "+
				"executions row went with the cascade, so nothing can ever "+
				"join these back", table, n)
		}
	}
}

// TestDeleteFunctionKeepsAnotherFunctionsRows pins the blast radius: the
// subselect is keyed on function_id, so a second function's children survive.
func TestDeleteFunctionKeepsAnotherFunctionsRows(t *testing.T) {
	db := newTestDB(t)
	seedExecution(t, db, "exec-doomed", 1)
	seedExecutionChildren(t, db, "exec-doomed")

	if _, err := db.write.Exec(
		`INSERT INTO functions (id, name, runtime) VALUES ('fn-keep','fn-keep','node')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(
		`INSERT INTO executions (id, function_id, status, started_at)
		 VALUES ('exec-keep', 'fn-keep', 'success', datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	seedExecutionChildren(t, db, "exec-keep")

	if err := db.DeleteFunction("fn-retention"); err != nil {
		t.Fatalf("delete function: %v", err)
	}

	for _, table := range executionChildTables {
		if n := countRows(t, db, table, "exec-keep"); n != 1 {
			t.Errorf("%s: fn-keep's row count is %d, want 1 — deleting one "+
				"function must not touch another's history", table, n)
		}
	}
}

// TestReclaimSweepsPreExistingOrphans covers the databases already damaged:
// child rows whose execution is gone, and execution rows whose function is
// gone. Both are seeded the only way they can occur — behind the FK, with
// foreign_keys off, exactly as the old code left them.
func TestReclaimSweepsPreExistingOrphans(t *testing.T) {
	db := newTestDB(t)
	seedExecution(t, db, "exec-orphaned-children", 1)
	seedExecutionChildren(t, db, "exec-orphaned-children")

	// A stranded executions row: FK off is how a pre-DSN-fix build deleted a
	// function without cascading, so it is how the damage is reproduced.
	if _, err := db.write.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(
		`INSERT INTO executions (id, function_id, status, started_at)
		 VALUES ('exec-no-function', 'fn-vanished', 'success', datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	// Drop the parent executions row only, leaving its children behind.
	if _, err := db.write.Exec(
		"DELETE FROM executions WHERE id = 'exec-orphaned-children'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}

	for _, table := range executionChildTables {
		if n := countRows(t, db, table, "exec-orphaned-children"); n != 1 {
			t.Fatalf("seed: %s has %d orphan rows, want 1", table, n)
		}
	}

	// The reclaim is a boot step; run it the way boot does. The marker has
	// already been written by newTestDB's Migrate, so clear it first —
	// which also proves the marker is what makes the sweep one-shot.
	if err := db.SetSystemConfig(orphanReclaimMarkerKey, ""); err != nil {
		t.Fatal(err)
	}
	db.reclaimOrphanedExecutionRows()

	for _, table := range executionChildTables {
		if n := countRows(t, db, table, "exec-orphaned-children"); n != 0 {
			t.Errorf("%s kept %d orphan row(s) after reclaim", table, n)
		}
	}
	var n int
	if err := db.read.QueryRow(
		"SELECT COUNT(*) FROM executions WHERE id = 'exec-no-function'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("executions row whose function is gone survived the reclaim")
	}

	// Idempotent: a second run is a no-op, and the marker is set.
	db.reclaimOrphanedExecutionRows()
	if got := db.GetSystemConfigString(orphanReclaimMarkerKey, ""); got != "true" {
		t.Errorf("marker = %q, want \"true\" after a clean pass", got)
	}
}

// TestReclaimKeepsLiveRows is the counterweight: the sweep must delete only
// rows whose parent is missing. A predicate inverted, or an anti-join against
// the wrong column, wipes the instance's entire execution history.
func TestReclaimKeepsLiveRows(t *testing.T) {
	db := newTestDB(t)
	seedExecution(t, db, "exec-live", 1)
	seedExecutionChildren(t, db, "exec-live")

	if err := db.SetSystemConfig(orphanReclaimMarkerKey, ""); err != nil {
		t.Fatal(err)
	}
	db.reclaimOrphanedExecutionRows()

	if n := totalRows(t, db, "executions"); n != 1 {
		t.Fatalf("reclaim deleted a live executions row: %d left, want 1", n)
	}
	for _, table := range executionChildTables {
		if n := countRows(t, db, table, "exec-live"); n != 1 {
			t.Errorf("%s: reclaim deleted a live row (%d left, want 1)", table, n)
		}
	}
}
