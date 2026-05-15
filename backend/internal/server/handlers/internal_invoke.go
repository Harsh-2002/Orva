package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/trace"
)

// InternalInvokeHandler is the F2F (function-to-function) entrypoint.
// User code inside a sandbox calls orva.invoke("other-fn", payload); the
// adapter POSTs here. We bypass the public auth layer (worker holds the
// internal token) and the per-function rate limiter (loops are bounded
// by call depth, not request rate).
type InternalInvokeHandler struct {
	DB            *database.Database
	Registry      *registry.Registry
	Pool          *pool.Manager
	Metrics       *metrics.Metrics
	InternalToken string

	// MaxCallDepth caps how many invoke()s can be nested before the call
	// chain is rejected. Without this, a function recursively invoking
	// itself would deadlock the warm pool. Default 8.
	MaxCallDepth int
}

const defaultMaxCallDepth = 8

// Invoke handles POST /api/v1/_internal/invoke/{name}. The path uses the
// friendly function name (NOT the fn_<id> form) so user code stays
// readable: orva.invoke("resize-image", payload).
func (h *InternalInvokeHandler) Invoke(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	// Per-process internal token. Constant-time compare so timing leaks
	// don't help an external attacker who somehow reached this endpoint.
	got := r.Header.Get("X-Orva-Internal-Token")
	if h.InternalToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.InternalToken)) != 1 {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid internal token", reqID)
		return
	}

	name := r.PathValue("name")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "function name is required", reqID)
		return
	}

	// Call-depth guard. The adapter forwards the inbound depth header
	// when it makes a nested invoke() so we can refuse cycles before
	// the pool runs out of workers.
	depth := 0
	if v := r.Header.Get("X-Orva-Call-Depth"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			depth = n
		}
	}
	maxDepth := h.MaxCallDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxCallDepth
	}
	if depth+1 > maxDepth {
		respond.Error(w, http.StatusInsufficientStorage, "MAX_CALL_DEPTH",
			"call depth exceeds max ("+strconv.Itoa(maxDepth)+")", reqID)
		return
	}

	// Resolve name → function. Look up via the DB (single SQL hit) so
	// brand-new functions are visible immediately without registry warm.
	fn, err := h.DB.GetFunctionByName(name)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND",
			"function not found: "+name, reqID)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}

	timeout := time.Duration(fn.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// v0.5 trace context. The SDK forwards X-Orva-Trace-Id /
	// X-Orva-Span-Id from the caller's env (set by proxy.Forward when the
	// caller was started). traceID may be empty for legacy calls; we
	// generate a fresh root in that case so this F2F still produces a
	// usable single-span trace. parentSpanID = caller's span = our parent.
	traceID := r.Header.Get("X-Orva-Trace-Id")
	parentSpanID := r.Header.Get("X-Orva-Span-Id")
	if traceID == "" {
		traceID = trace.NewTraceID()
	}
	spanID := trace.NewSpanID()
	callerFnID := r.Header.Get("X-Orva-Caller-Function")

	acq, err := h.Pool.Acquire(ctx, fn.ID)
	if err != nil {
		respond.Error(w, http.StatusServiceUnavailable, "POOL_ERROR",
			"pool acquire: "+err.Error(), reqID)
		return
	}
	var reqErr error
	defer func() { h.Pool.Release(fn.ID, acq.Worker, reqErr) }()

	// Generate an execution ID so this F2F call shows up in the executions
	// log AND in the trace tree as a distinct span. Without this, F2F
	// children would be invisible to ops tooling.
	execID := ids.New()

	// Synthesize the event in the same shape the public /fn/ handler
	// builds so user code can't tell the difference (which is the point).
	// Trace headers reach the user code so the SDK can forward them on
	// any nested invokes.
	event := map[string]any{
		"method": "POST",
		"path":   "/",
		"headers": map[string]string{
			"content-type":          "application/json",
			"x-orva-trigger":        "f2f",
			"x-orva-call-depth":     strconv.Itoa(depth + 1),
			"x-orva-function-id":    fn.ID,
			"x-orva-execution-id":   execID,
			"x-orva-trace-id":       traceID,
			"x-orva-span-id":        spanID,
		},
		"body": string(body),
	}
	eventJSON, _ := json.Marshal(event)

	start := time.Now()
	respJSON, _, err := acq.Worker.Dispatch(ctx, eventJSON)
	durationMS := time.Since(start).Milliseconds()
	if err != nil {
		reqErr = err
		errMsg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "function timed out"
		}
		// Record the failed F2F as a span so the trace tree shows the
		// failure point even when the parent succeeded.
		h.DB.AsyncInsertExecutionFinal(
			&database.Execution{
				ID: execID, FunctionID: fn.ID, Status: "error", ColdStart: acq.ColdStart,
				TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID,
				Trigger: "f2f", ParentFunctionID: callerFnID,
				StartedAt: start,
			},
			durationMS, http.StatusBadGateway, errMsg, 0,
		)
		if h.Metrics != nil {
			h.Metrics.Baselines.FinalizeExecution(h.DB, execID, fn.ID, "error", acq.ColdStart, durationMS)
		}
		respond.Error(w, http.StatusBadGateway, "INVOKE_FAILED", errMsg, reqID)
		return
	}

	// Successful F2F: best-effort parse of the worker envelope to derive
	// status code + response size for the span. The body shape is
	// {"statusCode":N,"headers":{...},"body":"..."}; on parse failure we
	// fall back to status=200, size=0 — the span still records.
	var env struct {
		StatusCode int    `json:"statusCode"`
		Body       string `json:"body"`
	}
	statusCode := 200
	respSize := len(respJSON)
	if json.Unmarshal(respJSON, &env) == nil {
		if env.StatusCode > 0 {
			statusCode = env.StatusCode
		}
		respSize = len(env.Body)
	}
	execStatus := "success"
	if statusCode >= 500 {
		execStatus = "error"
	}
	h.DB.AsyncInsertExecutionFinal(
		&database.Execution{
			ID: execID, FunctionID: fn.ID, Status: execStatus, ColdStart: acq.ColdStart,
			TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID,
			Trigger: "f2f", ParentFunctionID: callerFnID,
			StartedAt: start,
		},
		durationMS, statusCode, "", respSize,
	)
	if h.Metrics != nil {
		h.Metrics.Baselines.FinalizeExecution(h.DB, execID, fn.ID, execStatus, acq.ColdStart, durationMS)
	}

	// Pass through the worker's response verbatim so callers see exactly
	// the {statusCode, headers, body} shape that public callers see.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respJSON)
}

// InvokeStream handles POST /api/v1/_internal/invoke/{name}/stream. Behaves
// like Invoke but streams the response body bytes back to the SDK in
// chunked-transfer order. The inner envelope's statusCode lands as the
// outer HTTP status; the inner headers ride as `X-Orva-Inner-<Name>:
// <value>` header pairs so the SDK can surface them if needed. The body
// itself is raw — the SDK's async iterator yields each Uint8Array chunk.
//
// Errors before any chunk is written produce a normal JSON error
// response. Errors mid-stream cut the connection (no trailer) — the SDK
// surfaces them as an `Aborted` exception on the iterator.
func (h *InternalInvokeHandler) InvokeStream(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	got := r.Header.Get("X-Orva-Internal-Token")
	if h.InternalToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.InternalToken)) != 1 {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid internal token", reqID)
		return
	}

	name := r.PathValue("name")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "function name is required", reqID)
		return
	}

	depth := 0
	if v := r.Header.Get("X-Orva-Call-Depth"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			depth = n
		}
	}
	maxDepth := h.MaxCallDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxCallDepth
	}
	if depth+1 > maxDepth {
		respond.Error(w, http.StatusInsufficientStorage, "MAX_CALL_DEPTH",
			"call depth exceeds max ("+strconv.Itoa(maxDepth)+")", reqID)
		return
	}

	fn, err := h.DB.GetFunctionByName(name)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND",
			"function not found: "+name, reqID)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}

	timeout := time.Duration(fn.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	traceID := r.Header.Get("X-Orva-Trace-Id")
	parentSpanID := r.Header.Get("X-Orva-Span-Id")
	if traceID == "" {
		traceID = trace.NewTraceID()
	}
	spanID := trace.NewSpanID()
	callerFnID := r.Header.Get("X-Orva-Caller-Function")

	acq, err := h.Pool.Acquire(ctx, fn.ID)
	if err != nil {
		respond.Error(w, http.StatusServiceUnavailable, "POOL_ERROR",
			"pool acquire: "+err.Error(), reqID)
		return
	}
	var reqErr error
	defer func() { h.Pool.Release(fn.ID, acq.Worker, reqErr) }()

	execID := ids.New()
	event := map[string]any{
		"method": "POST",
		"path":   "/",
		"headers": map[string]string{
			"content-type":        "application/json",
			"x-orva-trigger":      "f2f",
			"x-orva-call-depth":   strconv.Itoa(depth + 1),
			"x-orva-function-id":  fn.ID,
			"x-orva-execution-id": execID,
			"x-orva-trace-id":     traceID,
			"x-orva-span-id":      spanID,
			"x-orva-stream":       "1",
		},
		"body": string(body),
	}
	eventJSON, _ := json.Marshal(event)

	start := time.Now()
	dres, err := acq.Worker.DispatchEx(ctx, eventJSON)
	if err != nil {
		reqErr = err
		errMsg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "function timed out"
		}
		h.DB.AsyncInsertExecutionFinal(
			&database.Execution{
				ID: execID, FunctionID: fn.ID, Status: "error", ColdStart: acq.ColdStart,
				TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID,
				Trigger: "f2f", ParentFunctionID: callerFnID,
				StartedAt: start,
			},
			time.Since(start).Milliseconds(), http.StatusBadGateway, errMsg, 0,
		)
		respond.Error(w, http.StatusBadGateway, "INVOKE_FAILED", errMsg, reqID)
		return
	}

	// Single-frame (non-streaming) response: write headers + body in one go.
	if !dres.Streaming {
		statusCode := dres.StatusCode
		if statusCode == 0 {
			statusCode = 200
		}
		for k, v := range dres.Headers {
			w.Header().Set("X-Orva-Inner-"+k, v)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(dres.Body))
		durationMS := time.Since(start).Milliseconds()
		execStatus := "success"
		if statusCode >= 500 {
			execStatus = "error"
		}
		h.DB.AsyncInsertExecutionFinal(
			&database.Execution{
				ID: execID, FunctionID: fn.ID, Status: execStatus, ColdStart: acq.ColdStart,
				TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID,
				Trigger: "f2f", ParentFunctionID: callerFnID,
				StartedAt: start,
			},
			durationMS, statusCode, "", len(dres.Body),
		)
		if h.Metrics != nil {
			h.Metrics.Baselines.FinalizeExecution(h.DB, execID, fn.ID, execStatus, acq.ColdStart, durationMS)
		}
		return
	}

	// Streaming path. Write the inner statusCode + headers first, then
	// stream chunks straight from NextFrame to the wire. The ResponseWriter
	// is flushed after each chunk so SDK clients see progress (the http
	// pipeline handles chunked-transfer encoding automatically).
	statusCode := dres.StatusCode
	if statusCode == 0 {
		statusCode = 200
	}
	for k, v := range dres.Headers {
		w.Header().Set("X-Orva-Inner-"+k, v)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Orva-Streamed", "1")
	w.WriteHeader(statusCode)
	flusher, _ := w.(http.Flusher)

	totalBytes := 0
	for {
		kind, chunk, ferr := dres.NextFrame(ctx)
		if ferr != nil {
			reqErr = ferr
			break
		}
		if kind == "end" {
			break
		}
		if len(chunk) > 0 {
			if _, werr := w.Write(chunk); werr != nil {
				reqErr = werr
				break
			}
			totalBytes += len(chunk)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	durationMS := time.Since(start).Milliseconds()
	execStatus := "success"
	if reqErr != nil || statusCode >= 500 {
		execStatus = "error"
	}
	errMsg := ""
	if reqErr != nil {
		errMsg = reqErr.Error()
	}
	h.DB.AsyncInsertExecutionFinal(
		&database.Execution{
			ID: execID, FunctionID: fn.ID, Status: execStatus, ColdStart: acq.ColdStart,
			TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID,
			Trigger: "f2f", ParentFunctionID: callerFnID,
			StartedAt: start,
		},
		durationMS, statusCode, errMsg, totalBytes,
	)
	if h.Metrics != nil {
		h.Metrics.Baselines.FinalizeExecution(h.DB, execID, fn.ID, execStatus, acq.ColdStart, durationMS)
	}
}
