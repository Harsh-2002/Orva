package handlers

import (
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func int64Ptr(v int64) *int64 { return &v }
func intPtr(v int) *int       { return &v }

func TestBuildTraceViewUsesLocalRootAndTraceWideAggregates(t *testing.T) {
	start := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	execs := []*database.Execution{
		{
			ID: "ex_root", FunctionID: "fn_a", TraceID: "tr_1", SpanID: "sp_root",
			ParentSpanID: "external_parent", Trigger: "http", Status: "success",
			StatusCode: intPtr(200), DurationMS: int64Ptr(50), StartedAt: start,
		},
		{
			ID: "ex_child", FunctionID: "fn_b", TraceID: "tr_1", SpanID: "sp_child",
			ParentSpanID: "sp_root", ParentFunctionID: "fn_a", Trigger: "f2f",
			Status: "error", StatusCode: intPtr(500), DurationMS: int64Ptr(100),
			StartedAt: start.Add(10 * time.Millisecond), ColdStart: true,
			IsOutlier: true, BaselineP95MS: int64Ptr(30), ErrorMessage: "boom",
		},
	}
	userSpans := []*database.UserSpan{{
		ID: "us_1", ExecutionID: "ex_child", ParentSpanID: "sp_child",
		Name: "query", StartedAt: start.Add(20 * time.Millisecond),
		DurationMS: 200, Status: "error", ErrorMessage: "timeout",
	}}
	logs := []*database.LogEntry{{
		ID: 1, ExecutionID: "ex_child", SpanID: "sp_child",
		TS: start.Add(30 * time.Millisecond), Level: "error", Message: "failed",
	}}

	view := buildTraceView("tr_1", execs, userSpans, logs, nil)
	if view.RootSpanID != "sp_root" || view.ExternalParentSpanID != "external_parent" {
		t.Fatalf("wrong local root: %#v", view)
	}
	if view.Status != "error" || !view.HasOutlier || view.TotalDurationMS != 220 {
		t.Fatalf("wrong aggregate state: %#v", view)
	}
	if view.SpanCount != 3 || view.ErrorCount != 2 || view.ColdStartCount != 1 {
		t.Fatalf("wrong aggregate counts: %#v", view)
	}
	if len(view.UserSpans) != 1 || view.UserSpans[0].OffsetMS != 20 {
		t.Fatalf("wrong user span placement: %#v", view.UserSpans)
	}
	if len(view.LogEntries) != 1 || view.LogEntries[0].ExecutionID != "ex_child" {
		t.Fatalf("wrong log association: %#v", view.LogEntries)
	}
}
