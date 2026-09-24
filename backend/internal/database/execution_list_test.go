package database

import (
	"fmt"
	"testing"
)

func seedListExecution(t *testing.T, db *Database, id, functionID, status string, second int) {
	t.Helper()
	if _, err := db.write.Exec(`INSERT OR IGNORE INTO functions (id, name, runtime)
		VALUES (?, ?, 'node')`, functionID, functionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`INSERT INTO executions (id, function_id, status, started_at)
		VALUES (?, ?, ?, ?)`, id, functionID, status,
		fmt.Sprintf("2026-09-24 00:00:%02d", second)); err != nil {
		t.Fatal(err)
	}
}

func TestListExecutionsRecentSuccessPage(t *testing.T) {
	db := newTestDB(t)
	seedListExecution(t, db, "first", "fn-a", "error", 1)
	seedListExecution(t, db, "second", "fn-a", "success", 2)
	seedListExecution(t, db, "third", "fn-a", "success", 3)
	seedListExecution(t, db, "other-function", "fn-b", "success", 4)

	for _, tc := range []struct {
		name      string
		params    ListExecutionsParams
		wantIDs   []string
		wantTotal int
	}{
		{"global", ListExecutionsParams{Status: "success", Limit: 2},
			[]string{"other-function", "third"}, 3},
		{"function", ListExecutionsParams{FunctionID: "fn-a", Status: "success", Limit: 2},
			[]string{"third", "second"}, 2},
		{"all-available", ListExecutionsParams{FunctionID: "fn-b", Status: "success", Limit: 5},
			[]string{"other-function"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := db.ListExecutions(tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if result.Total != tc.wantTotal || len(result.Executions) != len(tc.wantIDs) {
				t.Fatalf("got total=%d rows=%d, want total=%d rows=%d",
					result.Total, len(result.Executions), tc.wantTotal, len(tc.wantIDs))
			}
			for i, want := range tc.wantIDs {
				if result.Executions[i].ID != want {
					t.Errorf("row %d: got %q, want %q", i, result.Executions[i].ID, want)
				}
			}
		})
	}
}

func TestListExecutionsSuccessFallsBackWhenRecentPageIsMixed(t *testing.T) {
	db := newTestDB(t)
	seedListExecution(t, db, "success-old", "fn-a", "success", 1)
	seedListExecution(t, db, "success-new", "fn-a", "success", 2)
	seedListExecution(t, db, "error-newest", "fn-a", "error", 3)

	result, err := db.ListExecutions(ListExecutionsParams{Status: "success", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || len(result.Executions) != 2 ||
		result.Executions[0].ID != "success-new" || result.Executions[1].ID != "success-old" {
		t.Fatalf("mixed recent page returned wrong successes: %+v", result)
	}

	// Offset and rare statuses retain the original filtered query semantics.
	offset, err := db.ListExecutions(ListExecutionsParams{Status: "success", Limit: 1, Offset: 1})
	if err != nil || len(offset.Executions) != 1 || offset.Executions[0].ID != "success-old" {
		t.Fatalf("offset result=%+v, err=%v", offset, err)
	}
	errors, err := db.ListExecutions(ListExecutionsParams{Status: "error", Limit: 2})
	if err != nil || len(errors.Executions) != 1 || errors.Executions[0].ID != "error-newest" {
		t.Fatalf("rare status result=%+v, err=%v", errors, err)
	}
}
