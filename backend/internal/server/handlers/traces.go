package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// TracesHandler exposes the trace REST surface. A trace is a set of
// executions sharing the same trace_id; the root span is the one with a
// nil parent_span_id. The waterfall view in the UI is driven entirely
// from these endpoints — there is no separate spans table.
type TracesHandler struct {
	DB       *database.Database
	Registry *registry.Registry
	Metrics  *metrics.Metrics
}

// SpanView is a single span entry returned to the UI / MCP. We compute
// the "function name" client-side so the response stays cheap (no JOIN
// per span); when the function has been deleted the name is empty and
// the UI shows the function id instead.
type SpanView struct {
	ExecutionID      string  `json:"execution_id"`
	SpanID           string  `json:"span_id"`
	ParentSpanID     string  `json:"parent_span_id,omitempty"`
	FunctionID       string  `json:"function_id"`
	FunctionName     string  `json:"function_name,omitempty"`
	ParentFunctionID string  `json:"parent_function_id,omitempty"`
	Trigger          string  `json:"trigger,omitempty"`
	Status           string  `json:"status"`
	StatusCode       int     `json:"status_code,omitempty"`
	ColdStart        bool    `json:"cold_start"`
	IsOutlier        bool    `json:"is_outlier,omitempty"`
	BaselineP95MS    int64   `json:"baseline_p95_ms,omitempty"`
	StartedAt        string  `json:"started_at"`
	DurationMS       int64   `json:"duration_ms,omitempty"`
	ErrorMessage     string  `json:"error_message,omitempty"`
	OffsetMS         int64   `json:"offset_ms"` // ms since trace.started_at
}

// TraceView is the response for GET /api/v1/traces/{id}.
type TraceView struct {
	TraceID         string     `json:"trace_id"`
	RootSpanID      string     `json:"root_span_id,omitempty"`
	RootFunctionID  string     `json:"root_function_id,omitempty"`
	Trigger         string     `json:"trigger,omitempty"`
	StartedAt       string     `json:"started_at"`
	TotalDurationMS int64      `json:"total_duration_ms"`
	Status          string     `json:"status"`
	HasOutlier      bool       `json:"has_outlier"`
	SpanCount       int        `json:"span_count"`
	Spans           []SpanView `json:"spans"`
}

// GetTrace handles GET /api/v1/traces/{trace_id}. Pulls every execution
// row matching the trace_id (single indexed query), then enriches each
// row with the function name and computes per-span offset_ms relative to
// the earliest started_at so the UI waterfall renders without further
// math.
func (h *TracesHandler) GetTrace(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	traceID := r.PathValue("trace_id")
	if traceID == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "trace_id required", reqID)
		return
	}

	execs, err := h.DB.ListByTraceID(traceID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "list trace: "+err.Error(), reqID)
		return
	}
	if len(execs) == 0 {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "no spans for trace", reqID)
		return
	}

	out := buildTraceView(traceID, execs, h.Registry)
	respond.JSON(w, http.StatusOK, out)
}

// ListTraces handles GET /api/v1/traces?function_id=&since=&until=&status=&outlier_only=&limit=&before.
// The result list contains only ROOT spans; expand any of them via
// GET /api/v1/traces/{trace_id} for the full tree. before is an
// ISO8601 cursor: pass the started_at of the oldest row from the
// previous page to fetch the next.
func (h *TracesHandler) ListTraces(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	params := database.ListRootSpansParams{
		FunctionID:   q.Get("function_id"),
		Since:        q.Get("since"),
		Until:        q.Get("until"),
		Status:       q.Get("status"),
		OutlierOnly:  q.Get("outlier_only") == "1" || q.Get("outlier_only") == "true",
		Limit:        limit,
		BeforeCursor: q.Get("before"),
	}
	roots, err := h.DB.ListRootSpans(params)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "list traces: "+err.Error(), reqID)
		return
	}

	type rootSummary struct {
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
	out := make([]rootSummary, 0, len(roots))
	for _, e := range roots {
		var name string
		if h.Registry != nil {
			if fn, err := h.Registry.Get(e.FunctionID); err == nil {
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
		out = append(out, rootSummary{
			TraceID:        e.TraceID,
			RootSpanID:     e.SpanID,
			RootFunctionID: e.FunctionID,
			FunctionName:   name,
			Trigger:        e.Trigger,
			StartedAt:      e.StartedAt.Format(time.RFC3339Nano),
			DurationMS:     dur,
			Status:         e.Status,
			StatusCode:     sc,
			IsOutlier:      e.IsOutlier,
		})
	}

	// Cursor for the next page: started_at of the oldest row we returned.
	var nextCursor string
	if len(roots) == limit && len(roots) > 0 {
		nextCursor = roots[len(roots)-1].StartedAt.Format(time.RFC3339Nano)
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"traces":      out,
		"next_cursor": nextCursor,
		"limit":       limit,
	})
}

// GetFunctionBaseline handles GET /api/v1/functions/{id}/baseline. Always
// 200 with current snapshot (sample_count = 0 when no traffic yet).
func (h *TracesHandler) GetFunctionBaseline(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnIDOrName := r.PathValue("id")
	if fnIDOrName == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "id required", reqID)
		return
	}
	// Resolve name → id when needed (mirrors handlers/functions_helpers.go).
	fnID := fnIDOrName
	if h.Registry != nil {
		if fn, err := h.Registry.Get(fnIDOrName); err == nil {
			fnID = fn.ID
		} else if fn2, err2 := h.DB.GetFunctionByName(fnIDOrName); err2 == nil {
			fnID = fn2.ID
		}
	}
	if h.Metrics == nil {
		respond.JSON(w, http.StatusOK, metrics.BaselineSummary{FunctionID: fnID, WindowSize: 100})
		return
	}
	respond.JSON(w, http.StatusOK, h.Metrics.Baselines.Summary(fnID))
}

// BuildTraceViewForMCP is the exported entry-point so the MCP layer
// can reuse the same shaping logic without duplicating the offset/
// outlier walk. Same return type as the REST endpoint.
func BuildTraceViewForMCP(traceID string, execs []*database.Execution, reg *registry.Registry) TraceView {
	return buildTraceView(traceID, execs, reg)
}

// buildTraceView shapes the database rows into the API contract. Pure
// (no DB hits) so it's trivially testable.
func buildTraceView(traceID string, execs []*database.Execution, reg *registry.Registry) TraceView {
	if len(execs) == 0 {
		return TraceView{TraceID: traceID, Spans: []SpanView{}}
	}
	// execs is already sorted started_at ASC. The earliest is the trace's
	// reference point for offset_ms. The root span is the one with no
	// parent — usually the first row, but a misconfigured replay could
	// theoretically produce a different ordering.
	root := execs[0]
	for _, e := range execs {
		if e.ParentSpanID == "" {
			root = e
			break
		}
	}
	traceStart := execs[0].StartedAt

	// One trace lookup of function names. We resolve via the registry
	// (which caches by id); falls back to DB only on misses.
	nameOf := func(id string) string {
		if id == "" || reg == nil {
			return ""
		}
		if fn, err := reg.Get(id); err == nil {
			return fn.Name
		}
		return ""
	}

	spans := make([]SpanView, 0, len(execs))
	var hasOutlier bool
	var totalEnd time.Time
	traceStatus := "success"
	for _, e := range execs {
		var dur int64
		if e.DurationMS != nil {
			dur = *e.DurationMS
		}
		var sc int
		if e.StatusCode != nil {
			sc = *e.StatusCode
		}
		var baseP95 int64
		if e.BaselineP95MS != nil {
			baseP95 = *e.BaselineP95MS
		}
		offset := e.StartedAt.Sub(traceStart).Milliseconds()
		if offset < 0 {
			offset = 0
		}
		end := e.StartedAt.Add(time.Duration(dur) * time.Millisecond)
		if end.After(totalEnd) {
			totalEnd = end
		}
		if e.IsOutlier {
			hasOutlier = true
		}
		if e.Status == "error" && traceStatus == "success" {
			traceStatus = "error"
		}
		spans = append(spans, SpanView{
			ExecutionID:      e.ID,
			SpanID:           e.SpanID,
			ParentSpanID:     e.ParentSpanID,
			FunctionID:       e.FunctionID,
			FunctionName:     nameOf(e.FunctionID),
			ParentFunctionID: e.ParentFunctionID,
			Trigger:          e.Trigger,
			Status:           e.Status,
			StatusCode:       sc,
			ColdStart:        e.ColdStart,
			IsOutlier:        e.IsOutlier,
			BaselineP95MS:    baseP95,
			StartedAt:        e.StartedAt.Format(time.RFC3339Nano),
			DurationMS:       dur,
			ErrorMessage:     e.ErrorMessage,
			OffsetMS:         offset,
		})
	}
	totalMS := totalEnd.Sub(traceStart).Milliseconds()
	if totalMS < 0 {
		totalMS = 0
	}
	return TraceView{
		TraceID:         traceID,
		RootSpanID:      root.SpanID,
		RootFunctionID:  root.FunctionID,
		Trigger:         root.Trigger,
		StartedAt:       traceStart.Format(time.RFC3339Nano),
		TotalDurationMS: totalMS,
		Status:          traceStatus,
		HasOutlier:      hasOutlier,
		SpanCount:       len(spans),
		Spans:           spans,
	}
}
