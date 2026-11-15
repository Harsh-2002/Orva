package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/executionlogs"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/trace"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// InternalInvokeHandler is the F2F (function-to-function) entrypoint.
// User code inside a sandbox calls orva.invoke("other-fn", payload); the
// adapter POSTs here. We bypass the public auth layer (worker holds the
// scoped SDK credential) and the per-function rate limiter (loops are bounded
// by call depth, not request rate).
type InternalInvokeHandler struct {
	DB       *database.Database
	Registry *registry.Registry
	Pool     *pool.Manager
	Metrics  *metrics.Metrics
	SDKAuth  *sdkauth.Authenticator

	// MaxCallDepth caps how many invoke()s can be nested before the call
	// chain is rejected. Without this, a function recursively invoking
	// itself would deadlock the warm pool. Default 8.
	MaxCallDepth int
}

const defaultMaxCallDepth = 8

// callerDepth returns the caller's host-recorded call depth and enforces the
// cap, writing the 507 itself when exceeded. Depth lives on the execution
// binding, never in a request header: a function can simply omit the header,
// which made the cap bypassable. X-Orva-Call-Depth is ignored entirely.
//
// Known residual: a function could leak one of its own execution ids to a
// sibling execution (e.g. via KV) and borrow that execution's lower depth.
// The planned per-dispatch SDK lease closes that.
func (h *InternalInvokeHandler) callerDepth(w http.ResponseWriter, r *http.Request, callerFnID, reqID string) (int, bool) {
	depth, ok := h.SDKAuth.ExecutionDepth(r.Header.Get("X-Orva-Execution-Id"), callerFnID)
	if !ok {
		respond.Error(w, http.StatusForbidden, "SDK_SCOPE_VIOLATION",
			"SDK request is not associated with an active caller execution", reqID)
		return 0, false
	}
	if ierr := h.CheckDepth(depth); ierr != nil {
		ierr.write(w, reqID)
		return 0, false
	}
	return depth, true
}

// F2FCaller is the host-recorded identity of the execution making an F2F call.
// It comes from the SDK credential's execution binding (HTTP) or the dispatch
// lease (SDK channel), never from request data.
type F2FCaller struct {
	FunctionID string
	TraceID    string
	SpanID     string
	Depth      int
}

// InvokeError is a failure of an F2F call carrying the exact HTTP status and
// envelope; the SDK channel reuses status/code/message verbatim.
type InvokeError struct {
	Status int
	Opts   respond.ErrorOpts
}

func (e *InvokeError) Error() string { return e.Opts.Code + ": " + e.Opts.Message }

func (e *InvokeError) write(w http.ResponseWriter, reqID string) {
	opts := e.Opts
	opts.RequestID = reqID
	respond.ErrorWithDetail(w, e.Status, opts)
}

func (h *InternalInvokeHandler) maxDepth() int {
	if h.MaxCallDepth > 0 {
		return h.MaxCallDepth
	}
	return defaultMaxCallDepth
}

// CheckDepth enforces the call-depth cap for a caller at callerDepth.
func (h *InternalInvokeHandler) CheckDepth(callerDepth int) *InvokeError {
	if m := h.maxDepth(); callerDepth+1 > m {
		return &InvokeError{Status: http.StatusInsufficientStorage, Opts: respond.ErrorOpts{Code: "MAX_CALL_DEPTH", Message: "call depth exceeds max (" + strconv.Itoa(m) + ")", RequestID: ""}}
	}
	return nil
}

// ResolveCallee finds an active callee by name.
func (h *InternalInvokeHandler) ResolveCallee(name string) (*database.Function, *InvokeError) {
	if name == "" {
		return nil, &InvokeError{Status: http.StatusBadRequest, Opts: respond.ErrorOpts{Code: "VALIDATION", Message: "function name is required", RequestID: ""}}
	}
	// Look up via the DB (single SQL hit) so brand-new functions are visible
	// immediately without registry warm.
	fn, err := h.DB.GetFunctionByName(name)
	if err != nil {
		return nil, &InvokeError{Status: http.StatusNotFound, Opts: respond.ErrorOpts{Code: "NOT_FOUND", Message: "function not found: " + name, RequestID: ""}}
	}
	if fn.Status != "active" {
		return nil, &InvokeError{Status: http.StatusConflict, Opts: respond.ErrorOpts{Code: "NOT_ACTIVE", Message: "function is not active", RequestID: ""}}
	}
	return fn, nil
}

func reserveExecutionCtx(parent context.Context, db *database.Database, reqID string) (*database.ExecutionLease, *InvokeError) {
	reserveCtx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	lease, err := db.ReserveExecution(reserveCtx)
	if err != nil {
		return nil, &InvokeError{Status: http.StatusTooManyRequests, Opts: respond.ErrorOpts{
			Code: "STORAGE_BACKPRESSURE", Message: "execution storage is catching up",
			RequestID: reqID, RetryAfterS: 1,
			Hint: "back off briefly and retry; no function code ran",
		}}
	}
	return lease, nil
}

func f2fEvent(fn *database.Function, execID string, c F2FCaller, traceID, spanID string, body []byte, stream bool) []byte {
	headers := map[string]string{
		"content-type":        "application/json",
		"x-orva-trigger":      "f2f",
		"x-orva-call-depth":   strconv.Itoa(c.Depth + 1),
		"x-orva-function-id":  fn.ID,
		"x-orva-execution-id": execID,
		"x-orva-trace-id":     traceID,
		"x-orva-span-id":      spanID,
	}
	if stream {
		headers["x-orva-stream"] = "1"
	}
	eventJSON, _ := json.Marshal(map[string]any{
		"method": "POST", "path": "/", "headers": headers, "body": string(body),
	})
	return eventJSON
}

func calleeTimeout(fn *database.Function) time.Duration {
	if t := time.Duration(fn.TimeoutMS) * time.Millisecond; t > 0 {
		return t
	}
	return 30 * time.Second
}

func (h *InternalInvokeHandler) recordMetrics(coldStart bool, durationMS int64, execID, fnID, status string) {
	if h.Metrics == nil {
		return
	}
	// Count the invocation, not just the baseline: these paths fed Baselines
	// but never the counters.
	h.Metrics.RecordInvocation(coldStart)
	h.Metrics.RecordDuration(time.Duration(durationMS) * time.Millisecond)
	h.Metrics.Baselines.FinalizeExecution(h.DB, execID, fnID, status, coldStart, durationMS)
}

// RunInvoke runs one buffered F2F call. parent bounds it: cancelling parent
// cancels the callee. It returns the worker envelope verbatim.
func (h *InternalInvokeHandler) RunInvoke(parent context.Context, c F2FCaller, fn *database.Function, body []byte, reqID string) ([]byte, *InvokeError) {
	if err := parent.Err(); err != nil {
		return nil, invokeErrorFrom(err, fn, reqID)
	}
	executionLease, ierr := reserveExecutionCtx(parent, h.DB, reqID)
	if ierr != nil {
		return nil, ierr
	}
	defer executionLease.Cancel()

	timeout := calleeTimeout(fn)
	// traceID may be empty for legacy calls; parentSpanID = caller's span.
	traceID := c.TraceID
	parentSpanID := c.SpanID
	spanID := trace.NewSpanID()

	acq, err := h.Pool.Acquire(parent, fn.ID)
	if err != nil {
		// The shared taxonomy distinguishes FUNCTION_BUSY, MEMORY_EXHAUSTED,
		// EGRESS_POLICY_UNAVAILABLE and TIMEOUT.
		return nil, invokeErrorFrom(err, fn, reqID)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var reqErr error
	defer func() { h.Pool.Release(acq, reqErr) }()

	// A distinct execution so the F2F child shows in the executions log and
	// the trace tree.
	execID := ids.New()
	start := time.Now()
	releaseExecution := h.SDKAuth.BindExecution(execID, fn.ID, traceID, spanID, start, c.Depth+1)
	defer releaseExecution()
	ctx = sandbox.WithExecution(ctx, sandbox.NewExecInfo(ctx, execID, traceID, spanID, c.Depth+1, start))

	respJSON, stderr, err := acq.Worker.Dispatch(ctx, f2fEvent(fn, execID, c, traceID, spanID, body, false))
	durationMS := time.Since(start).Milliseconds()
	// Raw logs have an execution FK, so publish the completion before its logs.
	defer func() { executionlogs.Persist(h.DB, stderr, fn.ID, execID, traceID, spanID) }()
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
				Trigger: "f2f", ParentFunctionID: c.FunctionID,
				StartedAt: start,
			},
			durationMS, http.StatusBadGateway, errMsg, 0, executionLease,
		)
		h.recordMetrics(acq.ColdStart, durationMS, execID, fn.ID, "error")
		return nil, &InvokeError{Status: http.StatusBadGateway, Opts: respond.ErrorOpts{Code: "INVOKE_FAILED", Message: errMsg, RequestID: reqID}}
	}

	// Best-effort parse of the worker envelope for the span's status + size.
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
			Trigger: "f2f", ParentFunctionID: c.FunctionID,
			StartedAt: start,
		},
		durationMS, statusCode, "", respSize, executionLease,
	)
	h.recordMetrics(acq.ColdStart, durationMS, execID, fn.ID, execStatus)
	return respJSON, nil
}

func invokeErrorFrom(err error, fn *database.Function, reqID string) *InvokeError {
	status, opts := invokeError(err, fn, reqID)
	return &InvokeError{Status: status, Opts: opts}
}

// F2FStream is an open streaming F2F call. Streaming=false means the callee
// answered with one frame: StatusCode/Headers/Body are complete and the call is
// already recorded and released. Otherwise the caller drives NextFrame until
// "end" or an error and then calls Finish exactly once (it is idempotent).
type F2FStream struct {
	Streaming  bool
	StatusCode int
	Headers    map[string]string
	Body       string

	h            *InternalInvokeHandler
	fn           *database.Function
	c            F2FCaller
	acq          *pool.AcquireResult
	ctx          context.Context
	cancel       context.CancelFunc
	dres         *sandbox.DispatchResult
	execID       string
	traceID      string
	spanID       string
	parentSpanID string
	start        time.Time
	lease        *database.ExecutionLease
	release      func()

	mu         sync.Mutex
	ended      bool
	totalBytes int
	reqErr     error
	once       sync.Once
}

// OpenStream dispatches a streaming F2F call and returns once the callee's
// response head (or complete single-frame response) is known.
func (h *InternalInvokeHandler) OpenStream(parent context.Context, c F2FCaller, fn *database.Function, body []byte, reqID string) (*F2FStream, *InvokeError) {
	if err := parent.Err(); err != nil {
		return nil, invokeErrorFrom(err, fn, reqID)
	}
	executionLease, ierr := reserveExecutionCtx(parent, h.DB, reqID)
	if ierr != nil {
		return nil, ierr
	}
	s := &F2FStream{h: h, fn: fn, c: c, lease: executionLease,
		traceID: c.TraceID, parentSpanID: c.SpanID, spanID: trace.NewSpanID()}

	acq, err := h.Pool.Acquire(parent, fn.ID)
	if err != nil {
		executionLease.Cancel()
		return nil, invokeErrorFrom(err, fn, reqID)
	}
	s.acq = acq
	s.ctx, s.cancel = context.WithTimeout(parent, calleeTimeout(fn))
	s.execID = ids.New()
	s.start = time.Now()
	s.release = h.SDKAuth.BindExecution(s.execID, fn.ID, s.traceID, s.spanID, s.start, c.Depth+1)
	s.ctx = sandbox.WithExecution(s.ctx, sandbox.NewExecInfo(s.ctx, s.execID, s.traceID, s.spanID, c.Depth+1, s.start))

	dres, err := acq.Worker.DispatchEx(s.ctx, f2fEvent(fn, s.execID, c, s.traceID, s.spanID, body, true))
	s.dres = dres
	if err != nil {
		s.reqErr = err
		errMsg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "function timed out"
		}
		s.insert("error", http.StatusBadGateway, errMsg, 0)
		s.teardown()
		return nil, &InvokeError{Status: http.StatusBadGateway, Opts: respond.ErrorOpts{Code: "INVOKE_FAILED", Message: errMsg, RequestID: reqID}}
	}
	s.StatusCode = dres.StatusCode
	if s.StatusCode == 0 {
		s.StatusCode = 200
	}
	s.Headers = dres.Headers
	if !dres.Streaming {
		s.Body = dres.Body
		s.ended = true
		execStatus := "success"
		if s.StatusCode >= 500 {
			execStatus = "error"
		}
		s.insert(execStatus, s.StatusCode, "", len(dres.Body))
		s.h.recordMetrics(acq.ColdStart, time.Since(s.start).Milliseconds(), s.execID, fn.ID, execStatus)
		s.teardown()
		return s, nil
	}
	s.Streaming = true
	return s, nil
}

func (s *F2FStream) insert(status string, code int, errMsg string, size int) {
	s.h.DB.AsyncInsertExecutionFinal(
		&database.Execution{
			ID: s.execID, FunctionID: s.fn.ID, Status: status, ColdStart: s.acq.ColdStart,
			TraceID: s.traceID, SpanID: s.spanID, ParentSpanID: s.parentSpanID,
			Trigger: "f2f", ParentFunctionID: s.c.FunctionID,
			StartedAt: s.start,
		},
		time.Since(s.start).Milliseconds(), code, errMsg, size, s.lease,
	)
}

// teardown releases everything in the order the HTTP handlers' defers did:
// logs, execution binding, pool worker, timeout ctx, storage reservation.
func (s *F2FStream) teardown() {
	if s.dres != nil {
		executionlogs.Persist(s.h.DB, s.dres.Stderr(), s.fn.ID, s.execID, s.traceID, s.spanID)
	}
	s.release()
	s.h.Pool.Release(s.acq, s.reqErr)
	s.cancel()
	s.lease.Cancel()
}

// NextFrame returns the next streaming frame ("chunk" or "end"). Safe against
// a concurrent Finish: Finish cancels the call first, which unblocks it.
func (s *F2FStream) NextFrame() (kind string, data []byte, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return "end", nil, nil
	}
	kind, data, err = s.dres.NextFrame(s.ctx)
	switch {
	case err != nil:
		s.reqErr = err
	case kind == "end":
		s.ended = true
	default:
		s.totalBytes += len(data)
	}
	return kind, data, err
}

// Finish records the call and releases its worker. abortErr non-nil means the
// stream is abandoned (client gone / closed early): the callee is cancelled.
// Idempotent.
func (s *F2FStream) Finish(abortErr error) {
	if !s.Streaming {
		return
	}
	s.once.Do(func() {
		if abortErr != nil {
			s.cancel() // unblocks an in-flight NextFrame
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.ended && s.reqErr == nil {
			// Drive the (cancelled) stream to a terminal state so the worker's
			// dispatch lock is released before the worker returns to the pool.
			for i := 0; i < 16; i++ {
				kind, _, err := s.dres.NextFrame(s.ctx)
				if err != nil {
					s.reqErr = err
					break
				}
				if kind == "end" {
					s.ended = true
					break
				}
			}
		}
		if s.reqErr == nil && abortErr != nil {
			s.reqErr = abortErr
		}
		execStatus := "success"
		errMsg := ""
		if s.reqErr != nil || s.StatusCode >= 500 {
			execStatus = "error"
		}
		if s.reqErr != nil {
			errMsg = s.reqErr.Error()
		}
		s.insert(execStatus, s.StatusCode, errMsg, s.totalBytes)
		s.h.recordMetrics(s.acq.ColdStart, time.Since(s.start).Milliseconds(), s.execID, s.fn.ID, execStatus)
		s.teardown()
	})
}

// Invoke handles POST /api/v1/_internal/invoke/{name}. The path uses the
// friendly function name (NOT the fn_<id> form) so user code stays
// readable: orva.invoke("resize-image", payload).
func (h *InternalInvokeHandler) Invoke(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	caller, name, ok := h.httpCaller(w, r, reqID)
	if !ok {
		return
	}
	fn, ierr := h.ResolveCallee(name)
	if ierr != nil {
		ierr.write(w, reqID)
		return
	}
	body, err := readBoundedBody(r.Body, 1<<20)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	respJSON, ierr := h.RunInvoke(r.Context(), caller, fn, body, reqID)
	if ierr != nil {
		ierr.write(w, reqID)
		return
	}
	// Pass through the worker's response verbatim so callers see exactly
	// the {statusCode, headers, body} shape that public callers see.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respJSON)
}

// httpCaller authenticates an SDK HTTP request and derives the caller identity
// and the (depth-checked) callee name, writing the error itself on failure.
func (h *InternalInvokeHandler) httpCaller(w http.ResponseWriter, r *http.Request, reqID string) (F2FCaller, string, bool) {
	callerFnID, authErr := h.SDKAuth.Verify(r.Header.Get("X-Orva-Internal-Token"))
	if authErr != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid SDK credential", reqID)
		return F2FCaller{}, "", false
	}
	traceID, spanID, _, active := h.SDKAuth.TraceContext(r.Header.Get("X-Orva-Execution-Id"), callerFnID)
	if !active {
		respond.Error(w, http.StatusForbidden, "SDK_SCOPE_VIOLATION",
			"SDK request is not associated with an active caller execution", reqID)
		return F2FCaller{}, "", false
	}
	name := r.PathValue("name")
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "function name is required", reqID)
		return F2FCaller{}, "", false
	}
	depth, depthOK := h.callerDepth(w, r, callerFnID, reqID)
	if !depthOK {
		return F2FCaller{}, "", false
	}
	return F2FCaller{FunctionID: callerFnID, TraceID: traceID, SpanID: spanID, Depth: depth}, name, true
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
	caller, name, ok := h.httpCaller(w, r, reqID)
	if !ok {
		return
	}
	fn, ierr := h.ResolveCallee(name)
	if ierr != nil {
		ierr.write(w, reqID)
		return
	}
	body, err := readBoundedBody(r.Body, 1<<20)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	s, ierr := h.OpenStream(r.Context(), caller, fn, body, reqID)
	if ierr != nil {
		ierr.write(w, reqID)
		return
	}
	for k, v := range s.Headers {
		w.Header().Set("X-Orva-Inner-"+k, v)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if !s.Streaming {
		w.WriteHeader(s.StatusCode)
		_, _ = w.Write([]byte(s.Body))
		return
	}
	// Write the inner statusCode + headers first, then stream chunks straight
	// to the wire, flushing each so SDK clients see progress.
	w.Header().Set("X-Orva-Streamed", "1")
	w.WriteHeader(s.StatusCode)
	flusher, _ := w.(http.Flusher)
	var abort error
	for {
		kind, chunk, ferr := s.NextFrame()
		if ferr != nil {
			break
		}
		if kind == "end" {
			break
		}
		if len(chunk) > 0 {
			if _, werr := w.Write(chunk); werr != nil {
				abort = werr
				break
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	s.Finish(abort)
}
