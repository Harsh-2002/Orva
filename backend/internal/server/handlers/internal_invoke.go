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

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
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

	acq, err := h.Pool.Acquire(ctx, fn.ID)
	if err != nil {
		respond.Error(w, http.StatusServiceUnavailable, "POOL_ERROR",
			"pool acquire: "+err.Error(), reqID)
		return
	}
	var reqErr error
	defer func() { h.Pool.Release(fn.ID, acq.Worker, reqErr) }()

	// Synthesize the event in the same shape the public /fn/ handler
	// builds so user code can't tell the difference (which is the point).
	event := map[string]any{
		"method": "POST",
		"path":   "/",
		"headers": map[string]string{
			"content-type":          "application/json",
			"x-orva-trigger":        "f2f",
			"x-orva-call-depth":     strconv.Itoa(depth + 1),
			"x-orva-function-id":    fn.ID,
		},
		"body": string(body),
	}
	eventJSON, _ := json.Marshal(event)

	respJSON, _, err := acq.Worker.Dispatch(ctx, eventJSON)
	if err != nil {
		reqErr = err
		errMsg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "function timed out"
		}
		respond.Error(w, http.StatusBadGateway, "INVOKE_FAILED", errMsg, reqID)
		return
	}

	// Pass through the worker's response verbatim so callers see exactly
	// the {statusCode, headers, body} shape that public callers see.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respJSON)
}
