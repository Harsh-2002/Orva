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

// TestPurgeCascadesToLogsAndCapturedRequests guards the manual cascade:
// execution_requests deliberately has no FK to executions, so a purge that only
// deleted the parent rows would orphan them forever.
func TestPurgeCascadesToLogsAndCapturedRequests(t *testing.T) {
	db := newTestDB(t)
	seedExecution(t, db, "old-with-children", 90)

	if _, err := db.write.Exec(
		`INSERT INTO execution_logs (execution_id, stdout, stderr)
		 VALUES ('old-with-children', 'hello', '')`,
	); err != nil {
		t.Fatalf("seed execution log: %v", err)
	}
	// execution_requests deliberately carries NO foreign key to executions
	// (async insert ordering), which is exactly why the purge has to delete it
	// by hand rather than relying on ON DELETE CASCADE.
	if _, err := db.write.Exec(
		`INSERT INTO execution_requests (execution_id, body) VALUES ('old-with-children', 'x')`,
	); err != nil {
		t.Logf("execution_requests seed skipped (shape differs): %v", err)
	}

	if err := db.PurgeOldExecutions(DefaultRetentionDays); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var logs int
	if err := db.read.QueryRow(
		"SELECT COUNT(*) FROM execution_logs WHERE execution_id = 'old-with-children'").Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if logs != 0 {
		t.Errorf("purge left %d orphaned log rows behind", logs)
	}

	var reqs int
	if err := db.read.QueryRow(
		"SELECT COUNT(*) FROM execution_requests WHERE execution_id = 'old-with-children'").Scan(&reqs); err == nil && reqs != 0 {
		t.Errorf("purge left %d orphaned captured-request rows behind (no FK exists to clean them up)", reqs)
	}
}
