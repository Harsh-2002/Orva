package handlers

import (
	"encoding/json"
	"net/http"
	"time"

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

// userSpanRequest is the wire shape the runtime SDK sends. The trace ID,
// parent span ID, and execution ID ride as headers (already set by the
// proxy when the function started), so the request body only carries
// what's user-supplied — the span's name, timing, and attributes.
type userSpanRequest struct {
	Name         string          `json:"name"`
	StartedAt    time.Time       `json:"started_at"`
	DurationMS   int64           `json:"duration_ms"`
	Status       string          `json:"status,omitempty"`        // "ok" | "error"
	ErrorMessage string          `json:"error_message,omitempty"` // populated when status="error"
	Attributes   json.RawMessage `json:"attributes,omitempty"`    // arbitrary JSON
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

	body, err := readBoundedBody(r.Body, 1<<16)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req userSpanRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if req.Name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	if req.StartedAt.IsZero() {
		req.StartedAt = time.Now().UTC()
	}

	// Compute offset_ms relative to the parent execution row. If the row
	// hasn't landed yet (the user span is recorded before the execution
	// row's final insert), we fall back to "now since some reasonable
	// origin" — the dashboard renders these defensively.
	offset := int64(0)
	offset = req.StartedAt.Sub(executionStart).Milliseconds()
	if offset < 0 {
		offset = 0
	}

	span := &database.UserSpan{
		TraceID:      traceID,
		ParentSpanID: parentSpanID,
		ExecutionID:  execID,
		Name:         req.Name,
		StartedAt:    req.StartedAt.UTC(),
		DurationMS:   req.DurationMS,
		Status:       req.Status,
		ErrorMessage: req.ErrorMessage,
		OffsetMS:     offset,
	}
	if len(req.Attributes) > 0 {
		span.Attributes = string(req.Attributes)
	}
	h.DB.AsyncInsertUserSpan(span)

	respond.JSON(w, http.StatusAccepted, map[string]string{
		"status": "queued",
		"id":     span.ID,
	})
}
