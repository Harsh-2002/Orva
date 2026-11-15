package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// SpansHandler accepts user-defined spans emitted from orva.trace.span()
// inside a sandbox. Authentication is the function-scoped SDK credential
// (same as KV and F2F). Insertion is fire-and-forget — the handler
// returns 202 Accepted immediately and the async writer commits the row
// alongside any other batched writes.
type SpansHandler struct {
	DB      *database.Database
	SDKAuth *sdkauth.Authenticator
}

func (h *SpansHandler) authorize(w http.ResponseWriter, r *http.Request) (string, bool) {
	caller, err := h.SDKAuth.Verify(r.Header.Get("X-Orva-Internal-Token"))
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid SDK credential", r.Header.Get("X-Request-ID"))
		return "", false
	}
	return caller, true
}

// Create handles POST /api/v1/_internal/spans. Returns 202 with the
// generated span ID — the actual DB write is batched async.
func (h *SpansHandler) Create(w http.ResponseWriter, r *http.Request) {
	callerFnID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	execID := r.Header.Get("X-Orva-Execution-Id")
	if execID == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			"missing execution context header", reqID)
		return
	}
	traceID, parentSpanID, executionStart, owned := h.SDKAuth.TraceContext(execID, callerFnID)
	if !owned {
		respond.Error(w, http.StatusForbidden, "SDK_SCOPE_VIOLATION",
			"span execution is not active for the calling function", reqID)
		return
	}

	body, err := readBoundedBody(r.Body, MaxUserSpanBody)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req UserSpanRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	res, serr := CreateUserSpan(h.DB, callerFnID, execID, traceID, parentSpanID, executionStart, &req)
	if serr != nil {
		serr.write(w, reqID)
		return
	}
	respond.JSON(w, http.StatusAccepted, res)
}
