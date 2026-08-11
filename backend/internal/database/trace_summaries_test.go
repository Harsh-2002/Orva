package database

import (
	"testing"
	"time"
)

func insertTraceTestFunction(t *testing.T, db *Database, id, name string) {
	t.Helper()
	err := db.InsertFunction(&Function{
		ID: id, Name: name, Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 128, CPUs: 0.5,
		EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func insertTraceExecution(t *testing.T, db *Database, id, fnID, traceID, spanID, parentID, status string, started time.Time, duration int64, cold, outlier bool) {
	t.Helper()
	coldInt, outlierInt := 0, 0
	if cold {
		coldInt = 1
	}
	if outlier {
		outlierInt = 1
	}
	statusCode := 200
	if status == "error" {
		statusCode = 500
	}
	_, err := db.write.Exec(`
		INSERT INTO executions (
			id, function_id, status, cold_start, duration_ms, status_code,
			container_id, started_at, finished_at, trace_id, span_id,
			parent_span_id, trigger, is_outlier
		) VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, 'http', ?)`,
		id, fnID, status, coldInt, duration, statusCode, started,
		started.Add(time.Duration(duration)*time.Millisecond), traceID, spanID,
		nullableString(parentID), outlierInt,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestListTraceSummariesAggregatesAndFindsExternalLocalRoot(t *testing.T) {
	db := newTestDB(t)
	insertTraceTestFunction(t, db, "fn_root", "gateway")
	insertTraceTestFunction(t, db, "fn_child", "worker")
	start := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	insertTraceExecution(t, db, "ex_root", "fn_root", "tr_external", "sp_root", "upstream_parent", "success", start, 50, false, false)
	insertTraceExecution(t, db, "ex_child", "fn_child", "tr_external", "sp_child", "sp_root", "error", start.Add(10*time.Millisecond), 100, true, true)
	_, err := db.write.Exec(`
		INSERT INTO user_spans (
			id, trace_id, parent_span_id, execution_id, name, started_at,
			duration_ms, status, error_message, offset_ms
		) VALUES ('us_1', 'tr_external', 'sp_child', 'ex_child', 'parse', ?, 200, 'error', 'bad payload', 20)`,
		start.Add(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListTraceSummaries(ListTraceSummariesParams{Function: "worker", Status: "error", OutlierOnly: true, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one matching trace, got %d", len(rows))
	}
	got := rows[0]
	if got.RootSpanID != "sp_root" || got.ExternalParentSpanID != "upstream_parent" {
		t.Fatalf("wrong local root: %#v", got)
	}
	if got.RootFunctionID != "fn_root" || got.FunctionName != "gateway" {
		t.Fatalf("wrong root identity: %#v", got)
	}
	if got.Status != "error" || got.StatusCode != 500 || !got.IsOutlier {
		t.Fatalf("trace-wide failure/outlier not aggregated: %#v", got)
	}
	if got.DurationMS != 220 || got.SpanCount != 3 || got.ErrorCount != 2 || got.ColdStartCount != 1 {
		t.Fatalf("trace-wide timing/counts mismatch: %#v", got)
	}
}

func TestTraceCursorContinuityWithTiedTimestamps(t *testing.T) {
	db := newTestDB(t)
	insertTraceTestFunction(t, db, "fn_cursor", "cursor")
	start := time.Date(2026, 8, 11, 11, 0, 0, 123000000, time.UTC)
	for _, traceID := range []string{"tr_a", "tr_b", "tr_c"} {
		insertTraceExecution(t, db, "ex_"+traceID, "fn_cursor", traceID, "sp_"+traceID, "", "success", start, 10, false, false)
	}

	first, err := db.ListTraceSummaries(ListTraceSummariesParams{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].TraceID != "tr_c" || first[1].TraceID != "tr_b" {
		t.Fatalf("unexpected first page: %#v", first)
	}
	cursor := EncodeTraceCursor(first[1].StartedAt, first[1].TraceID)
	before, traceID, legacy, err := DecodeTraceCursor(cursor)
	if err != nil || legacy {
		t.Fatalf("decode opaque cursor: legacy=%v err=%v", legacy, err)
	}
	second, err := db.ListTraceSummaries(ListTraceSummariesParams{
		Limit: 2, BeforeStartedAt: before, BeforeTraceID: traceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].TraceID != "tr_a" {
		t.Fatalf("cursor skipped or duplicated tied trace: %#v", second)
	}

	legacyValue := start.Format(time.RFC3339Nano)
	legacyStarted, legacyTrace, isLegacy, err := DecodeTraceCursor(legacyValue)
	if err != nil || !isLegacy || legacyStarted != legacyValue || legacyTrace != "" {
		t.Fatalf("legacy cursor rejected: %q %q %v %v", legacyStarted, legacyTrace, isLegacy, err)
	}
	if _, _, _, err := DecodeTraceCursor("not-a-cursor"); err == nil {
		t.Fatal("expected malformed cursor rejection")
	}
}
