package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// unmigratedDB returns a schema-migrated database with the UUIDv7 marker
// removed, so a test can seed legacy ids and then drive MigrateToUUIDv7
// itself. Mirrors the fixture in TestMigrationPopulatedDB.
func unmigratedDB(t *testing.T) (*Database, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(
		`DELETE FROM system_config WHERE key = ?`, migrationMarkerKey,
	); err != nil {
		t.Fatal(err)
	}
	return db, dir
}

func mustExecT(t *testing.T, db *Database, query string, args ...any) {
	t.Helper()
	if _, err := db.write.Exec(query, args...); err != nil {
		t.Fatalf("seed: %v\nquery: %s", err, query)
	}
}

// TestMigrationRewritesChannelFunctions is the regression for the bug that
// bricked upgrades: channel_functions declares a hard FK to functions(id)
// but was missing from the hand-maintained child list, so the rewrite left
// it dangling, foreign_key_check refused the commit, and the process exited
// 1 on every boot thereafter.
//
// Fails on the pre-fix code with "integrity check failed".
func TestMigrationRewritesChannelFunctions(t *testing.T) {
	db, _ := unmigratedDB(t)

	const legacyFnID = "fn_channelbound1"
	const legacyChanID = "chn_test12345678"

	mustExecT(t, db, `INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, ?, ?)`,
		legacyFnID, "bound-func", "python", "handler.py")
	mustExecT(t, db, `INSERT INTO channels (id, name, token_hash, token_prefix) VALUES (?, ?, ?, ?)`,
		legacyChanID, "prod", "fakehash", "orva_chn_")
	mustExecT(t, db, `INSERT INTO channel_functions (channel_id, function_id) VALUES (?, ?)`,
		legacyChanID, legacyFnID)

	if err := db.MigrateToUUIDv7(); err != nil {
		t.Fatalf("migration failed with a channel binding present: %v", err)
	}

	var newFnID, boundFnID string
	if err := db.read.QueryRow(`SELECT id FROM functions`).Scan(&newFnID); err != nil {
		t.Fatal(err)
	}
	if err := db.read.QueryRow(`SELECT function_id FROM channel_functions`).Scan(&boundFnID); err != nil {
		t.Fatal(err)
	}
	if !ids.IsUUID(newFnID) {
		t.Fatalf("function id not rewritten: %q", newFnID)
	}
	if boundFnID != newFnID {
		t.Fatalf("channel_functions.function_id = %q, want the rewritten id %q",
			boundFnID, newFnID)
	}
}

// TestRewritePlanCoversEveryDeclaredFK asserts the plan is complete against
// the schema itself rather than against a list someone remembered to update.
// It needs no fixture and would have caught the channel_functions miss the
// day that table was added.
func TestRewritePlanCoversEveryDeclaredFK(t *testing.T) {
	db, _ := unmigratedDB(t)

	tx, err := db.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	parents := make(map[string]struct{}, len(rewrites))
	for _, rw := range rewrites {
		parents[strings.ToLower(rw.parentTable)+"."+strings.ToLower(rw.idColumn)] = struct{}{}
	}

	tables, err := userTables(tx)
	if err != nil {
		t.Fatal(err)
	}

	// Every declared FK whose parent column we rewrite must resolve to a
	// child ref the migration will actually update.
	var missing []string
	for _, rw := range rewrites {
		refs, err := resolveChildRefs(tx, rw)
		if err != nil {
			t.Fatalf("resolve %s.%s: %v", rw.parentTable, rw.idColumn, err)
		}
		declared, err := declaredChildRefs(tx, rw.parentTable, rw.idColumn)
		if err != nil {
			t.Fatal(err)
		}
		have := make(map[childRef]struct{}, len(refs))
		for _, r := range refs {
			have[r] = struct{}{}
		}
		for _, d := range declared {
			if _, ok := have[d]; !ok {
				missing = append(missing,
					rw.parentTable+"."+rw.idColumn+" <- "+d.table+"."+d.column)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("rewrite plan misses %d declared FK(s):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}

	// And the inverse direction: no table in the schema may declare an FK to
	// a parent column that the plan never rewrites at all. That would mean a
	// new id-bearing parent was introduced without being added here.
	var unplanned []string
	for _, tbl := range tables {
		declaredFor, err := declaredParentsOf(tx, tbl)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range declaredFor {
			key := strings.ToLower(p.table) + "." + strings.ToLower(p.column)
			if _, ok := parents[key]; ok {
				continue
			}
			// Parents whose ids are not prefix-typed storage ids are fine.
			if !idBearingParent(p.table) {
				continue
			}
			unplanned = append(unplanned, tbl+" -> "+key)
		}
	}
	if len(unplanned) > 0 {
		sort.Strings(unplanned)
		t.Fatalf("schema declares FKs to %d unplanned parent column(s):\n  %s",
			len(unplanned), strings.Join(unplanned, "\n  "))
	}
}

// parentRef names a parent column that some table's FK points at.
type parentRef struct{ table, column string }

// declaredParentsOf returns every parent column that `table` declares an FK
// to. Test-local because production code only ever needs the inverse
// direction (children of a given parent).
func declaredParentsOf(tx *sql.Tx, table string) ([]parentRef, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA foreign_key_list(%q)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []parentRef
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		to := asString(vals[4])
		if to == "" {
			to = "id"
		}
		out = append(out, parentRef{table: asString(vals[2]), column: to})
	}
	return out, rows.Err()
}

// idBearingParent reports whether a parent table's key is one of Orva's
// generated storage ids (as opposed to a natural key like users.id or a
// content hash).
func idBearingParent(table string) bool {
	switch strings.ToLower(table) {
	case "functions", "deployments", "executions", "event_subscriptions",
		"cron_schedules", "jobs", "webhook_deliveries", "fixtures",
		"inbound_webhooks", "api_keys", "oauth_clients", "oauth_access_tokens",
		"channels":
		return true
	}
	return false
}

// TestMigrationRewritesSoftReferences covers the columns that hold a copy of
// a rewritten id but carry no declared FK — foreign_key_check is blind to
// them, so a miss here is silent rather than a boot failure.
func TestMigrationRewritesSoftReferences(t *testing.T) {
	db, _ := unmigratedDB(t)

	const legacyFnID = "fn_softref123456"
	const legacyParentFnID = "fn_softrefparent"
	const legacyExecID = "exec_softref1"

	mustExecT(t, db, `INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, ?, ?)`,
		legacyFnID, "child-func", "python", "handler.py")
	mustExecT(t, db, `INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, ?, ?)`,
		legacyParentFnID, "parent-func", "python", "handler.py")
	mustExecT(t, db, `INSERT INTO executions (id, function_id, status, started_at, parent_function_id) VALUES (?, ?, 'success', datetime('now'), ?)`,
		legacyExecID, legacyFnID, legacyParentFnID)
	mustExecT(t, db, `INSERT INTO user_spans (id, trace_id, parent_span_id, execution_id, name, started_at, duration_ms) VALUES (?, ?, ?, ?, ?, datetime('now'), 5)`,
		"span_softref1", "trace1", "parent1", legacyExecID, "span-a")
	mustExecT(t, db, `INSERT INTO jobs (id, function_id, payload, status, scheduled_at, enqueued_by_function_id) VALUES (?, ?, '{}', 'pending', datetime('now'), ?)`,
		"job_softref1", legacyFnID, legacyParentFnID)

	// Prove the seeds actually landed — a vacuous pass is exactly how the
	// equivalent execution-child list drifted before (see retention_test.go).
	assertCount(t, db, `SELECT COUNT(*) FROM executions WHERE parent_function_id = ?`, 1, legacyParentFnID)
	assertCount(t, db, `SELECT COUNT(*) FROM user_spans WHERE execution_id = ?`, 1, legacyExecID)
	assertCount(t, db, `SELECT COUNT(*) FROM jobs WHERE enqueued_by_function_id = ?`, 1, legacyParentFnID)

	if err := db.MigrateToUUIDv7(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	var newParentID string
	if err := db.read.QueryRow(
		`SELECT id FROM functions WHERE name = 'parent-func'`).Scan(&newParentID); err != nil {
		t.Fatal(err)
	}
	var newExecID string
	if err := db.read.QueryRow(`SELECT id FROM executions`).Scan(&newExecID); err != nil {
		t.Fatal(err)
	}

	assertCount(t, db, `SELECT COUNT(*) FROM executions WHERE parent_function_id = ?`, 1, newParentID)
	assertCount(t, db, `SELECT COUNT(*) FROM user_spans WHERE execution_id = ?`, 1, newExecID)
	assertCount(t, db, `SELECT COUNT(*) FROM jobs WHERE enqueued_by_function_id = ?`, 1, newParentID)

	// And nothing may still hold a legacy value.
	assertCount(t, db, `SELECT COUNT(*) FROM executions WHERE parent_function_id = ?`, 0, legacyParentFnID)
	assertCount(t, db, `SELECT COUNT(*) FROM user_spans WHERE execution_id = ?`, 0, legacyExecID)
	assertCount(t, db, `SELECT COUNT(*) FROM jobs WHERE enqueued_by_function_id = ?`, 0, legacyParentFnID)
}

func assertCount(t *testing.T, db *Database, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := db.read.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v\n%s", err, query)
	}
	if got != want {
		t.Fatalf("count = %d, want %d\nquery: %s\nargs: %v", got, want, query, args)
	}
}

// TestMigrationRenamesFunctionDirs is the data-loss regression. The rewrite
// moves functions.id, but each function's source lives at
// <dataDir>/functions/<id>/ and every call site builds that path from the
// database id. Without the rename the code is unreachable, and the build
// GC then deletes it as an orphan.
//
// Fails on the pre-fix code: the directory keeps its legacy name.
func TestMigrationRenamesFunctionDirs(t *testing.T) {
	db, dir := unmigratedDB(t)

	const legacyFnID = "fn_ondisk1234567"
	mustExecT(t, db, `INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, ?, ?)`,
		legacyFnID, "on-disk", "node", "handler.js")

	// Lay down a realistic tree: versions/<hash>/ plus the current symlink
	// target, so we can prove the whole subtree moves, not just the top dir.
	srcDir := filepath.Join(dir, "functions", legacyFnID, "versions", "abc123")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const handler = "module.exports = async () => ({ok:true});\n"
	if err := os.WriteFile(filepath.Join(srcDir, "handler.js"), []byte(handler), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := db.MigrateToUUIDv7(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if err := db.ReconcileFunctionDirs(dir); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	var newFnID string
	if err := db.read.QueryRow(`SELECT id FROM functions`).Scan(&newFnID); err != nil {
		t.Fatal(err)
	}
	if newFnID == legacyFnID {
		t.Fatal("function id was not rewritten; fixture is wrong")
	}

	// The code must be reachable under the id the running server will use.
	moved := filepath.Join(dir, "functions", newFnID, "versions", "abc123", "handler.js")
	got, err := os.ReadFile(moved)
	if err != nil {
		t.Fatalf("function source not reachable at the new id: %v", err)
	}
	if string(got) != handler {
		t.Fatalf("source content changed: %q", string(got))
	}
	if _, err := os.Stat(filepath.Join(dir, "functions", legacyFnID)); !os.IsNotExist(err) {
		t.Fatal("legacy directory still present after reconcile")
	}

	// The pending record must be cleared, so the GC is unblocked.
	if db.HasPendingFunctionDirRenames() {
		t.Fatal("pending rename record not cleared after a successful reconcile")
	}
}

// TestReconcileIsIdempotentAndResumable proves the crash-safety property:
// the record survives until every pair is applied, and re-running is a
// no-op rather than a second, destructive rename.
func TestReconcileIsIdempotentAndResumable(t *testing.T) {
	db, dir := unmigratedDB(t)

	const legacyFnID = "fn_resume1234567"
	mustExecT(t, db, `INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, ?, ?)`,
		legacyFnID, "resume", "node", "handler.js")
	src := filepath.Join(dir, "functions", legacyFnID)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := db.MigrateToUUIDv7(); err != nil {
		t.Fatal(err)
	}
	if !db.HasPendingFunctionDirRenames() {
		t.Fatal("expected a pending rename record between migrate and reconcile")
	}

	for i := 0; i < 3; i++ {
		if err := db.ReconcileFunctionDirs(dir); err != nil {
			t.Fatalf("reconcile pass %d: %v", i, err)
		}
	}

	var newFnID string
	if err := db.read.QueryRow(`SELECT id FROM functions`).Scan(&newFnID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "functions", newFnID, "marker")); err != nil {
		t.Fatalf("content lost across repeated reconciles: %v", err)
	}
}

// TestReconcileToleratesFunctionsWithNoCodeOnDisk — a function row can exist
// with no directory (created but never deployed). That must not block the
// record from clearing, or the GC stays disabled forever.
func TestReconcileToleratesFunctionsWithNoCodeOnDisk(t *testing.T) {
	db, dir := unmigratedDB(t)

	mustExecT(t, db, `INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, ?, ?)`,
		"fn_nocode12345678", "never-deployed", "node", "handler.js")

	if err := db.MigrateToUUIDv7(); err != nil {
		t.Fatal(err)
	}
	if err := db.ReconcileFunctionDirs(dir); err != nil {
		t.Fatalf("reconcile should tolerate a missing directory: %v", err)
	}
	if db.HasPendingFunctionDirRenames() {
		t.Fatal("record not cleared when a function had no code on disk")
	}
}
