package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// ReplayHandler powers POST /api/v1/executions/{id}/replay (v0.4 A3).
//
// We re-build the same event-JSON envelope the proxy assembles for a
// fresh /fn/ invocation, then dispatch via the warm pool. The result
// table records a brand-new execution row whose replay_of column points
// at the original — chaining replays is allowed (replay of a replay).
type ReplayHandler struct {
	DB       *database.Database
	Registry *registry.Registry
	Pool     *pool.Manager
	Metrics  *metrics.Metrics

	// PublishEvent fans the new execution out to the SSE hub so the
	// dashboard's InvocationsLog prepends the replay row without polling.
	PublishEvent func(eventType string, data any)
}

// adapterRequest mirrors the shape proxy.Forward serializes into stdin.
// Kept private so the redaction list lives entirely with proxy.go.
type adapterRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// adapterResponse mirrors what the worker writes back.
type adapterResponse struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

// Replay handles POST /api/v1/executions/{id}/replay.
//
// 404 — no captured request for that execution (capture was disabled at
//       the time, or the row was purged).
// 410 — the captured body was truncated; replay would be inaccurate, so
//       we refuse rather than silently corrupt downstream state.
// 502 — worker dispatch failed.
// 504 — the function timed out during the replay.
//
// On success the response carries the replayed body verbatim (the same
// shape the F2F handler returns) plus a JSON metadata header set so the
// frontend can open a fresh drawer for the new execution row.
func (h *ReplayHandler) Replay(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	origID := extractReplayExecID(r.URL.Path)
	if origID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST",
			"missing execution ID", reqID)
		return
	}

	captured, err := h.DB.GetExecutionRequest(origID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND",
				"no captured request for this execution (capture may have been disabled)", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL",
			"failed to load captured request: "+err.Error(), reqID)
		return
	}
	if captured.Truncated {
		respond.Error(w, http.StatusGone, "REQUEST_TRUNCATED",
			"captured request body was truncated at the configured cap; replay is refused because it would not match the original", reqID)
		return
	}

	// Find the original execution to learn its function_id. The function
	// the operator wants to replay against is always the one that ran
	// originally — we don't accept a function_id override on this path.
	origExec, err := h.DB.GetExecution(origID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND",
			"original execution not found", reqID)
		return
	}

	fn, err := h.Registry.Get(origExec.FunctionID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "FUNCTION_GONE",
			"function for this execution no longer exists", reqID)
		return
	}
	if fn.Status != "active" {
		respond.Error(w, http.StatusServiceUnavailable, "NOT_ACTIVE",
			"function is not active (status="+fn.Status+")", reqID)
		return
	}

	// Generate the new execution ID up-front so the captured event-JSON
	// carries the new id (function code that logs `execution_id` shows
	// the right one).
	suffix, err := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 12)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL",
			"failed to generate execution id", reqID)
		return
	}
	newExecID := "exec_" + suffix

	// Rebuild the headers map from the stored JSON. Sensitive headers
	// landed as the literal string "[REDACTED]" — those flow through to
	// the function unchanged. Operators that need to replay with a fresh
	// credential can do so by re-issuing the original request manually.
	rebuiltHeaders := map[string]string{}
	if captured.HeadersJSON != "" {
		_ = json.Unmarshal([]byte(captured.HeadersJSON), &rebuiltHeaders)
	}
	// Stamp the standard Orva-internal headers the proxy normally adds.
	rebuiltHeaders["x-orva-function-id"] = fn.ID
	rebuiltHeaders["x-orva-execution-id"] = newExecID
	rebuiltHeaders["x-orva-trigger"] = "replay"
	rebuiltHeaders["x-orva-timeout-ms"] = strconv.FormatInt(fn.TimeoutMS, 10)

	timeout := time.Duration(fn.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if h.Pool == nil {
		respond.Error(w, http.StatusServiceUnavailable, "POOL_ERROR",
			"pool manager not configured", reqID)
		return
	}

	if h.Metrics != nil {
		h.Metrics.ActiveRequests.Add(1)
		defer h.Metrics.ActiveRequests.Add(-1)
	}

	start := time.Now()
	acq, err := h.Pool.Acquire(ctx, fn.ID)
	if err != nil {
		respond.Error(w, http.StatusServiceUnavailable, "POOL_ERROR",
			"pool acquire: "+err.Error(), reqID)
		return
	}
	var reqErr error
	defer func() { h.Pool.Release(fn.ID, acq.Worker, reqErr) }()

	event := adapterRequest{
		Method:  captured.Method,
		Path:    captured.Path,
		Headers: rebuiltHeaders,
		Body:    string(captured.Body),
	}
	eventJSON, _ := json.Marshal(event)

	respJSON, stderr, err := acq.Worker.Dispatch(ctx, eventJSON)
	duration := time.Since(start)
	if h.Metrics != nil {
		h.Metrics.RecordDuration(duration)
		h.Metrics.RecordInvocation(acq.ColdStart)
	}

	if err != nil {
		reqErr = err
		errMsg := err.Error()
		if len(errMsg) > 1024 {
			errMsg = errMsg[:1024] + "…(truncated)"
		}
		h.DB.AsyncInsertExecutionFinalReplay(
			&database.Execution{
				ID:         newExecID,
				FunctionID: fn.ID,
				Status:     "error",
				ColdStart:  acq.ColdStart,
			},
			duration.Milliseconds(), 0, errMsg, 0, origID,
		)
		if len(stderr) > 0 {
			h.DB.AsyncInsertExecutionLog(&database.ExecutionLog{
				ExecutionID: newExecID,
				Stderr:      string(stderr),
			})
		}
		h.publishExecution(newExecID, fn, "error", 0, duration.Milliseconds(), 0, acq.ColdStart, origID)

		status := http.StatusBadGateway
		code := "REPLAY_FAILED"
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
			code = "TIMEOUT"
		}
		respond.Error(w, status, code, errMsg, reqID)
		return
	}

	var resp adapterResponse
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		reqErr = err
		respond.Error(w, http.StatusBadGateway, "INVALID_RESPONSE",
			"adapter returned invalid response: "+err.Error(), reqID)
		return
	}

	sc := resp.StatusCode
	if sc == 0 {
		sc = 200
	}
	execStatus := "success"
	if sc >= 500 {
		execStatus = "error"
	}
	h.DB.AsyncInsertExecutionFinalReplay(
		&database.Execution{
			ID:         newExecID,
			FunctionID: fn.ID,
			Status:     execStatus,
			ColdStart:  acq.ColdStart,
		},
		duration.Milliseconds(), sc, "", len(resp.Body), origID,
	)
	if len(stderr) > 0 {
		h.DB.AsyncInsertExecutionLog(&database.ExecutionLog{
			ExecutionID: newExecID,
			Stderr:      string(stderr),
		})
	}
	h.publishExecution(newExecID, fn, execStatus, sc, duration.Milliseconds(), len(resp.Body), acq.ColdStart, origID)

	// Pass the worker's headers through verbatim so a JSON function
	// stays JSON, plus an Orva-prefixed metadata pair so the dashboard
	// can swap the open drawer to the new execution without polling.
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("X-Orva-Execution-ID", newExecID)
	w.Header().Set("X-Orva-Replay-Of", origID)
	w.Header().Set("X-Orva-Duration-MS", strconv.FormatInt(duration.Milliseconds(), 10))
	w.WriteHeader(sc)
	_, _ = w.Write([]byte(resp.Body))
}

func (h *ReplayHandler) publishExecution(id string, fn *database.Function, status string, statusCode int, durationMS int64, responseSize int, coldStart bool, replayOf string) {
	if h.PublishEvent == nil {
		return
	}
	now := time.Now().UTC()
	h.PublishEvent("execution", map[string]any{
		"id":            id,
		"function_id":   fn.ID,
		"function_name": fn.Name,
		"status":        status,
		"status_code":   statusCode,
		"duration_ms":   durationMS,
		"response_size": responseSize,
		"cold_start":    coldStart,
		"started_at":    now.Format(time.RFC3339Nano),
		"finished_at":   now.Format(time.RFC3339Nano),
		"replay_of":     replayOf,
	})
}

// extractReplayExecID parses /api/v1/executions/{id}/replay.
func extractReplayExecID(path string) string {
	const prefix = "/api/v1/executions/"
	const suffix = "/replay"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	mid := strings.TrimPrefix(path, prefix)
	mid = strings.TrimSuffix(mid, suffix)
	if mid == "" || strings.Contains(mid, "/") {
		return ""
	}
	return mid
}
