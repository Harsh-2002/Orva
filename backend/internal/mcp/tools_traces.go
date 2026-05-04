package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/server/handlers"
)

// SpanRow is the per-span shape returned by get_trace. Mirrors the REST
// SpanView in handlers/traces.go but kept independent so MCP tool
// schemas don't get tangled with HTTP types.
type SpanRow struct {
	ExecutionID      string `json:"execution_id"`
	SpanID           string `json:"span_id"`
	ParentSpanID     string `json:"parent_span_id,omitempty"`
	FunctionID       string `json:"function_id"`
	FunctionName     string `json:"function_name,omitempty"`
	ParentFunctionID string `json:"parent_function_id,omitempty"`
	Trigger          string `json:"trigger,omitempty"`
	Status           string `json:"status"`
	StatusCode       int    `json:"status_code,omitempty"`
	ColdStart        bool   `json:"cold_start"`
	IsOutlier        bool   `json:"is_outlier,omitempty"`
	BaselineP95MS    int64  `json:"baseline_p95_ms,omitempty"`
	StartedAt        string `json:"started_at"`
	DurationMS       int64  `json:"duration_ms,omitempty"`
	OffsetMS         int64  `json:"offset_ms"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

type GetTraceInput struct {
	TraceID string `json:"trace_id" jsonschema:"the trace_id (e.g. tr_abc...)"`
}

type GetTraceOutput struct {
	TraceID         string    `json:"trace_id"`
	RootSpanID      string    `json:"root_span_id,omitempty"`
	RootFunctionID  string    `json:"root_function_id,omitempty"`
	Trigger         string    `json:"trigger,omitempty"`
	StartedAt       string    `json:"started_at"`
	TotalDurationMS int64     `json:"total_duration_ms"`
	Status          string    `json:"status"`
	HasOutlier      bool      `json:"has_outlier"`
	SpanCount       int       `json:"span_count"`
	Spans           []SpanRow `json:"spans"`
}

type ListTracesInput struct {
	FunctionID  string `json:"function_id,omitempty" jsonschema:"filter to traces whose root span is this function"`
	Status      string `json:"status,omitempty"     jsonschema:"success or error"`
	OutlierOnly bool   `json:"outlier_only,omitempty" jsonschema:"only return traces flagged as outliers"`
	Since       string `json:"since,omitempty"      jsonschema:"ISO8601 lower bound on root.started_at"`
	Until       string `json:"until,omitempty"      jsonschema:"ISO8601 upper bound on root.started_at"`
	Limit       int    `json:"limit,omitempty"      jsonschema:"default 50, max 200"`
}

type RootSpanRow struct {
	TraceID        string `json:"trace_id"`
	RootSpanID     string `json:"root_span_id"`
	RootFunctionID string `json:"root_function_id"`
	FunctionName   string `json:"function_name,omitempty"`
	Trigger        string `json:"trigger,omitempty"`
	StartedAt      string `json:"started_at"`
	DurationMS     int64  `json:"duration_ms,omitempty"`
	Status         string `json:"status"`
	StatusCode     int    `json:"status_code,omitempty"`
	IsOutlier      bool   `json:"is_outlier"`
}

type ListTracesOutput struct {
	Traces []RootSpanRow `json:"traces"`
	Count  int           `json:"count"`
}

type GetFunctionBaselineInput struct {
	FunctionID string `json:"function_id" jsonschema:"the function id (UUID) or friendly name"`
}

func registerTraceTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name: "get_trace",
				Title: "Get Trace",
				Description: "Return the full causal tree for a trace. Each span is one execution row; spans are ordered by started_at ascending so the root is first. Use this after list_traces or after spotting a slow request.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetTraceInput) (*mcpsdk.CallToolResult, GetTraceOutput, error) {
				if in.TraceID == "" {
					return nil, GetTraceOutput{}, fmt.Errorf("trace_id required")
				}
				execs, err := deps.DB.ListByTraceID(in.TraceID)
				if err != nil {
					return nil, GetTraceOutput{}, fmt.Errorf("list trace: %w", err)
				}
				if len(execs) == 0 {
					return nil, GetTraceOutput{}, fmt.Errorf("no spans found for trace %s", in.TraceID)
				}
				view := handlers.BuildTraceViewForMCP(in.TraceID, execs, deps.Registry)
				out := GetTraceOutput{
					TraceID:         view.TraceID,
					RootSpanID:      view.RootSpanID,
					RootFunctionID:  view.RootFunctionID,
					Trigger:         view.Trigger,
					StartedAt:       view.StartedAt,
					TotalDurationMS: view.TotalDurationMS,
					Status:          view.Status,
					HasOutlier:      view.HasOutlier,
					SpanCount:       view.SpanCount,
					Spans:           make([]SpanRow, len(view.Spans)),
				}
				for i, sp := range view.Spans {
					out.Spans[i] = SpanRow{
						ExecutionID:      sp.ExecutionID,
						SpanID:           sp.SpanID,
						ParentSpanID:     sp.ParentSpanID,
						FunctionID:       sp.FunctionID,
						FunctionName:     sp.FunctionName,
						ParentFunctionID: sp.ParentFunctionID,
						Trigger:          sp.Trigger,
						Status:           sp.Status,
						StatusCode:       sp.StatusCode,
						ColdStart:        sp.ColdStart,
						IsOutlier:        sp.IsOutlier,
						BaselineP95MS:    sp.BaselineP95MS,
						StartedAt:        sp.StartedAt,
						DurationMS:       sp.DurationMS,
						OffsetMS:         sp.OffsetMS,
						ErrorMessage:     sp.ErrorMessage,
					}
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name: "list_traces",
				Title: "List Traces",
				Description: "List recent root spans (one entry per trace). Filter by function, status, time range, or outlier flag. Pair with get_trace to drill into a specific causal chain.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListTracesInput) (*mcpsdk.CallToolResult, ListTracesOutput, error) {
				params := database.ListRootSpansParams{
					FunctionID:  resolveFnIDForBaseline(deps, in.FunctionID),
					Status:      in.Status,
					OutlierOnly: in.OutlierOnly,
					Since:       in.Since,
					Until:       in.Until,
					Limit:       in.Limit,
				}
				if params.Limit <= 0 || params.Limit > 200 {
					params.Limit = 50
				}
				roots, err := deps.DB.ListRootSpans(params)
				if err != nil {
					return nil, ListTracesOutput{}, fmt.Errorf("list traces: %w", err)
				}
				rows := make([]RootSpanRow, 0, len(roots))
				for _, e := range roots {
					var name string
					if deps.Registry != nil {
						if fn, err := deps.Registry.Get(e.FunctionID); err == nil {
							name = fn.Name
						}
					}
					var dur int64
					if e.DurationMS != nil {
						dur = *e.DurationMS
					}
					var sc int
					if e.StatusCode != nil {
						sc = *e.StatusCode
					}
					rows = append(rows, RootSpanRow{
						TraceID:        e.TraceID,
						RootSpanID:     e.SpanID,
						RootFunctionID: e.FunctionID,
						FunctionName:   name,
						Trigger:        e.Trigger,
						StartedAt:      e.StartedAt.Format("2006-01-02T15:04:05.000Z07:00"),
						DurationMS:     dur,
						Status:         e.Status,
						StatusCode:     sc,
						IsOutlier:      e.IsOutlier,
					})
				}
				return nil, ListTracesOutput{Traces: rows, Count: len(rows)}, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name: "get_function_baseline",
				Title: "Get Function Baseline",
				Description: "Return the rolling P95/P99/mean latency baseline for a function plus the current sample count. baseline drives the outlier flag on each execution.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetFunctionBaselineInput) (*mcpsdk.CallToolResult, metrics.BaselineSummary, error) {
				if in.FunctionID == "" {
					return nil, metrics.BaselineSummary{}, fmt.Errorf("function_id required")
				}
				fnID := resolveFnIDForBaseline(deps, in.FunctionID)
				if deps.Metrics == nil {
					return nil, metrics.BaselineSummary{FunctionID: fnID, WindowSize: 100}, nil
				}
				return nil, deps.Metrics.Baselines.Summary(fnID), nil
			},
		)
	})
}

// resolveFnIDForBaseline accepts either fn_xxx or a friendly name and
// returns the canonical id. Falls back to the input when neither lookup
// matches; the baseline endpoint will simply return zero counts.
func resolveFnIDForBaseline(deps Deps, idOrName string) string {
	if idOrName == "" {
		return ""
	}
	if deps.Registry != nil {
		if fn, err := deps.Registry.Get(idOrName); err == nil {
			return fn.ID
		}
	}
	if fn, err := deps.DB.GetFunctionByName(idOrName); err == nil {
		return fn.ID
	}
	return idOrName
}
