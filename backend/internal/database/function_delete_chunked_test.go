package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// seedFnData creates fnID with nExec executions (every executionChildTables
// row each), nKV kv rows, nJobs jobs and one deployment with nLogs build lines.
func seedFnData(t *testing.T, db *Database, fnID string, nExec, nKV, nJobs, nLogs int) {
	t.Helper()
	seedFunctionRow(t, db, fnID)
	tx, err := db.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	prep := func(q string) *sql.Stmt {
		st, err := tx.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}
	ex := prep(`INSERT INTO executions (id, function_id, status, started_at) VALUES (?, ?, 'success', datetime('now'))`)
	lg := prep(`INSERT INTO execution_logs (execution_id, stdout, stderr) VALUES (?, 'o', 'e')`)
	rq := prep(`INSERT INTO execution_requests (execution_id, function_id, method, path, headers_json, body, captured_at) VALUES (?, ?, 'POST', '/', '{}', 'x', 0)`)
	sp := prep(`INSERT INTO user_spans (id, trace_id, parent_span_id, execution_id, function_id, name, started_at, duration_ms) VALUES (?, 't', '', ?, ?, 'w', datetime('now'), 1)`)
	le := prep(`INSERT INTO execution_log_entries (execution_id, function_id, ts, level, message) VALUES (?, ?, datetime('now'), 'info', 'm')`)
	kv := prep(`INSERT INTO kv_store (function_id, key, value) VALUES (?, ?, x'00')`)
	jb := prep(`INSERT INTO jobs (id, function_id, payload, status, scheduled_at) VALUES (?, ?, x'00', 'pending', datetime('now'))`)
	run := func(st *sql.Stmt, args ...any) {
		if _, err := st.Exec(args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < nExec; i++ {
		id := fmt.Sprintf("%s-e%06d", fnID, i)
		run(ex, id, fnID)
		run(lg, id)
		run(rq, id, fnID)
		run(sp, "s"+id, id, fnID)
		run(le, id, fnID)
	}
	for i := 0; i < nKV; i++ {
		run(kv, fnID, fmt.Sprintf("k%d", i))
	}
	for i := 0; i < nJobs; i++ {
		run(jb, fmt.Sprintf("%s-j%d", fnID, i), fnID)
	}
	dep := "dep-" + fnID
	if _, err := tx.Exec(`INSERT INTO deployments (id, function_id, version, status) VALUES (?, ?, 1, 'succeeded')`, dep, fnID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nLogs; i++ {
		if _, err := tx.Exec(`INSERT INTO build_logs (deployment_id, seq, stream, line) VALUES (?, ?, 'stdout', 'l')`, dep, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// fnRowCounts counts every row that belongs to fnID, by table.
func fnRowCounts(t *testing.T, db *Database, fnID string) map[string]int {
	t.Helper()
	q := map[string]string{
		"functions":             "SELECT COUNT(*) FROM functions WHERE id = ?1",
		"executions":            "SELECT COUNT(*) FROM executions WHERE function_id = ?1",
		"execution_logs":        "SELECT COUNT(*) FROM execution_logs WHERE execution_id LIKE ?1 || '-e%'",
		"execution_requests":    "SELECT COUNT(*) FROM execution_requests WHERE function_id = ?1",
		"user_spans":            "SELECT COUNT(*) FROM user_spans WHERE function_id = ?1",
		"execution_log_entries": "SELECT COUNT(*) FROM execution_log_entries WHERE function_id = ?1",
		"kv_store":              "SELECT COUNT(*) FROM kv_store WHERE function_id = ?1",
		"jobs":                  "SELECT COUNT(*) FROM jobs WHERE function_id = ?1",
		"deployments":           "SELECT COUNT(*) FROM deployments WHERE function_id = ?1",
		"build_logs":            "SELECT COUNT(*) FROM build_logs WHERE deployment_id = 'dep-' || ?1",
	}
	out := make(map[string]int, len(q))
	for name, query := range q {
		out[name] = countQuery(t, db, query, fnID)
	}
	return out
}

func fnStatus(t *testing.T, db *Database, id string) (string, bool) {
	t.Helper()
	var s string
	err := db.read.QueryRow("SELECT status FROM functions WHERE id = ?", id).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return s, true
}

func TestDeleteFunctionEquivalenceAcrossBatches(t *testing.T) {
	db := newTestDB(t)
	seedFnData(t, db, "fn-big", 2*purgeChunk+37, 2*purgeChunk+5, purgeChunk+3, purgeChunk+9)
	seedFnData(t, db, "fn-keep", 20, 10, 5, 7)
	want := fnRowCounts(t, db, "fn-keep")

	if err := db.DeleteFunction(context.Background(), "fn-big"); err != nil {
		t.Fatal(err)
	}
	for table, n := range fnRowCounts(t, db, "fn-big") {
		if n != 0 {
			t.Errorf("%s kept %d rows of the deleted function", table, n)
		}
	}
	got := fnRowCounts(t, db, "fn-keep")
	for table, n := range want {
		if got[table] != n {
			t.Errorf("%s: untouched function has %d rows, want %d", table, got[table], n)
		}
	}
	if _, ok := db.liveFunctions.Load("fn-big"); ok {
		t.Error("deleted function still cached as live")
	}
	assertExecutionsConsistent(t, db)
}

func TestDeleteFunctionMissingIDIsNil(t *testing.T) {
	db := newTestDB(t)
	if err := db.DeleteFunction(context.Background(), "fn-nope"); err != nil {
		t.Fatalf("missing id: %v", err)
	}
	seedFnData(t, db, "fn-twice", 3, 1, 1, 1)
	for i := 0; i < 2; i++ {
		if err := db.DeleteFunction(context.Background(), "fn-twice"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
}

func TestDeleteFunctionDoesNotMonopoliseTheWriter(t *testing.T) {
	db := newTestDB(t)
	const nExec, nKV, nJobs = 3500, 5000, 2500
	seedFnData(t, db, "fn-ref", nExec, nKV, nJobs, 0)
	seedFnData(t, db, "fn-big", nExec, nKV, nJobs, 0)

	// What the old code did: one transaction holding the writer for everything.
	start := time.Now()
	tx, err := db.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"DELETE FROM execution_logs WHERE execution_id IN (SELECT id FROM executions WHERE function_id = 'fn-ref')",
		"DELETE FROM execution_requests WHERE function_id = 'fn-ref'",
		"DELETE FROM user_spans WHERE function_id = 'fn-ref'",
		"DELETE FROM execution_log_entries WHERE function_id = 'fn-ref'",
		"DELETE FROM functions WHERE id = 'fn-ref'",
	} {
		if _, err := tx.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	unbounded := time.Since(start)

	waits := probeWriterWaits(t, db, func() error {
		return db.DeleteFunction(context.Background(), "fn-big")
	})
	assertWriterStayedResponsive(t, "function delete", waits)
	// Self-calibrating: slow or -race hosts scale both sides alike.
	if w := waits[len(waits)-1]; unbounded > 100*time.Millisecond && w > unbounded/3 {
		t.Fatalf("worst write wait %s vs %s for one unbounded transaction", w, unbounded)
	}
	if fnRowCounts(t, db, "fn-big")["executions"] != 0 {
		t.Fatal("executions survived")
	}
}

func TestDeleteFunctionCancelLeavesDeletingAndResumes(t *testing.T) {
	db := newTestDB(t)
	const total = 5 * purgeChunk
	seedFnData(t, db, "fn-cancel", total, 0, 0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			var n int
			if db.read.QueryRow(`SELECT COUNT(*) FROM executions WHERE function_id = 'fn-cancel'`).Scan(&n) != nil || n < total {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	err := db.DeleteFunction(ctx, "fn-cancel")
	close(stop)
	<-done
	if err == nil {
		t.Skip("delete finished before it could be cancelled")
	}
	if ctx.Err() == nil { // the driver may word an interrupted statement differently
		t.Fatalf("delete failed without being cancelled: %v", err)
	}
	if s, ok := fnStatus(t, db, "fn-cancel"); !ok || s != FunctionStatusDeleting {
		t.Fatalf("after cancel status = %q (exists %v), want deleting", s, ok)
	}
	if left := fnRowCounts(t, db, "fn-cancel")["executions"]; left >= total {
		t.Fatalf("expected some executions deleted, %d left", left)
	}
	assertExecutionsConsistent(t, db)

	if err := db.DeleteFunction(context.Background(), "fn-cancel"); err != nil {
		t.Fatal(err)
	}
	for table, n := range fnRowCounts(t, db, "fn-cancel") {
		if n != 0 {
			t.Errorf("%s kept %d rows after the second call", table, n)
		}
	}
}

func TestFilterDeletedDiscardsJobsOfDeletingFunction(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "fn-dying")
	seedFunctionRow(t, db, "fn-alive")
	if _, err := db.write.Exec(`UPDATE functions SET status = 'deleting' WHERE id = 'fn-dying'`); err != nil {
		t.Fatal(err)
	}
	batch := []writeJob{
		{functionID: "fn-dying", lease: &ExecutionLease{}},
		{functionID: "fn-alive", lease: &ExecutionLease{}},
		{functionID: "fn-dying", lease: &ExecutionLease{}},
	}
	keep, discarded, err := db.writer.filterDeleted(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if discarded != 2 || len(keep) != 1 || keep[0].functionID != "fn-alive" {
		t.Fatalf("discarded=%d keep=%d, want 2 and the alive job only", discarded, len(keep))
	}
	if _, ok := db.liveFunctions.Load("fn-dying"); ok {
		t.Fatal("a deleting function was resurrected in liveFunctions")
	}
	if _, ok := db.liveFunctions.Load("fn-alive"); !ok {
		t.Fatal("a live function was not cached")
	}
}

func TestUpdateFunctionRefusesDeletingFunction(t *testing.T) {
	db := newTestDB(t)
	fn := &Function{ID: "fn-upd", Name: "fn-upd", Runtime: "node", Entrypoint: "handler.js", Status: "active",
		EnvVars: map[string]string{}}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	fn.Description = "changed"
	if err := db.UpdateFunction(fn); err != nil {
		t.Fatalf("normal update: %v", err)
	}
	if _, err := db.write.Exec(`UPDATE functions SET status = 'deleting' WHERE id = 'fn-upd'`); err != nil {
		t.Fatal(err)
	}
	fn.Status = "active"
	if err := db.UpdateFunction(fn); !errors.Is(err, ErrFunctionDeleting) {
		t.Fatalf("err = %v, want ErrFunctionDeleting", err)
	}
	if s, _ := fnStatus(t, db, "fn-upd"); s != FunctionStatusDeleting {
		t.Fatalf("status revived to %q", s)
	}
	missing := &Function{ID: "fn-missing", EnvVars: map[string]string{}}
	if err := db.UpdateFunction(missing); !errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrFunctionDeleting) {
		t.Fatalf("missing function err = %v, want sql.ErrNoRows", err)
	}
}

func TestResumeFunctionDeletes(t *testing.T) {
	db := newTestDB(t)
	seedFnData(t, db, "fn-r1", 700, 5, 5, 5)
	seedFnData(t, db, "fn-r2", 3, 0, 0, 0)
	seedFnData(t, db, "fn-active", 4, 1, 1, 1)
	if _, err := db.write.Exec(`UPDATE functions SET status = 'deleting' WHERE id IN ('fn-r1','fn-r2')`); err != nil {
		t.Fatal(err)
	}
	n, err := db.ResumeFunctionDeletes(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("resumed %d, err %v; want 2", n, err)
	}
	for _, id := range []string{"fn-r1", "fn-r2"} {
		for table, c := range fnRowCounts(t, db, id) {
			if c != 0 {
				t.Errorf("%s: %s kept %d rows", id, table, c)
			}
		}
	}
	if fnRowCounts(t, db, "fn-active")["executions"] != 4 {
		t.Fatal("a non-deleting function was touched")
	}
	if n, err := db.ResumeFunctionDeletes(context.Background()); err != nil || n != 0 {
		t.Fatalf("second resume = %d, %v; want 0", n, err)
	}
}

// Every batch query must be an index search, never a table scan.
func TestDeleteFunctionBatchQueryPlans(t *testing.T) {
	db := newTestDB(t)
	cases := []struct{ query, index string }{
		{"SELECT id FROM executions WHERE function_id = ? LIMIT 500", "idx_executions_function"},
		{"SELECT rowid FROM execution_requests WHERE function_id = ? LIMIT 500", "idx_execution_requests_function_id"},
		{"SELECT rowid FROM user_spans WHERE function_id = ? LIMIT 500", "idx_user_spans_function_id"},
		{"SELECT rowid FROM execution_log_entries WHERE function_id = ? LIMIT 500", "idx_execution_log_entries_function_id"},
		{"SELECT rowid FROM kv_store WHERE function_id = ? LIMIT 500", "sqlite_autoindex_kv_store"},
		{"SELECT rowid FROM jobs WHERE function_id = ? LIMIT 500", "idx_jobs_fn"},
		{"SELECT rowid FROM build_logs WHERE deployment_id IN (SELECT id FROM deployments WHERE function_id = ?) LIMIT 500", "idx_deployments_fn"},
	}
	for _, c := range cases {
		rows, err := db.read.Query("EXPLAIN QUERY PLAN "+c.query, "fn")
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		sort.Strings(plan)
		joined := strings.Join(plan, " | ")
		if !strings.Contains(joined, c.index) || strings.Contains(joined, "SCAN") && !strings.Contains(joined, "USING") {
			t.Errorf("%s\n  plan: %s\n  want an index search on %s", c.query, joined, c.index)
		}
	}
}
