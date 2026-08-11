package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// JobsHandler exposes the background-job queue. It accepts requests both
// from the dashboard (session/API-key auth via the standard middleware)
// AND from inside worker sandboxes (X-Orva-Internal-Token). The internal
// path is what powers orva.jobs.enqueue() in the SDK.
type JobsHandler struct {
	DB       *database.Database
	Registry *registry.Registry
	SDKAuth  *sdkauth.Authenticator
}

// sdkCaller returns immutable caller attribution from a verified scoped SDK
// credential. Public session/API-key requests have no SDK caller.
func (h *JobsHandler) sdkCaller(r *http.Request) string {
	caller, _ := h.SDKAuth.Verify(r.Header.Get("X-Orva-Internal-Token"))
	return caller
}

type enqueueRequest struct {
	FunctionID               string          `json:"function_id"`
	FunctionName             string          `json:"function_name"`
	Payload                  json.RawMessage `json:"payload"`
	ScheduledAt              *time.Time      `json:"scheduled_at,omitempty"`
	MaxAttempts              int             `json:"max_attempts,omitempty"`
	IdempotencyKey           string          `json:"idempotency_key,omitempty"`
	IdempotencyWindowSeconds int             `json:"idempotency_window_seconds,omitempty"`
}

// Enqueue handles POST /api/v1/jobs.
func (h *JobsHandler) Enqueue(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	callerFnID := h.sdkCaller(r)
	callerTraceID := ""
	callerSpanID := ""
	if callerFnID != "" {
		var active bool
		callerTraceID, callerSpanID, _, active = h.SDKAuth.TraceContext(
			r.Header.Get("X-Orva-Execution-Id"), callerFnID,
		)
		if !active {
			respond.Error(w, http.StatusForbidden, "SDK_SCOPE_VIOLATION",
				"SDK request is not associated with an active caller execution", reqID)
			return
		}
	}

	body, err := readBoundedBody(r.Body, 1<<20)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", err.Error(), reqID)
		return
	}
	var req enqueueRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}

	// Resolve function_id from either field. Adapter SDK sends function_name;
	// dashboard sends function_id directly.
	fnID := req.FunctionID
	if fnID == "" && req.FunctionName != "" {
		fn, err := h.DB.GetFunctionByName(req.FunctionName)
		if err != nil {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found: "+req.FunctionName, reqID)
			return
		}
		fnID = fn.ID
	}
	if fnID == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "function_id or function_name is required", reqID)
		return
	}
	if _, err := h.DB.GetFunction(fnID); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	payload := []byte(req.Payload)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	job := &database.Job{
		FunctionID:               fnID,
		Payload:                  payload,
		MaxAttempts:              req.MaxAttempts,
		IdempotencyKey:           req.IdempotencyKey,
		IdempotencyWindowSeconds: req.IdempotencyWindowSeconds,
	}
	if req.ScheduledAt != nil {
		job.ScheduledAt = req.ScheduledAt.UTC()
	}
	// v0.5 trace context: when a function inside a sandbox enqueues this
	// job, the SDK forwards the caller's trace headers. We persist them on
	// the job row so the eventual execution lands in the same trace as
	// whatever enqueued it. Empty headers (dashboard or external API
	// caller) leave these blank → the picked-up job becomes a new root.
	job.TraceID = callerTraceID
	job.ParentSpanID = callerSpanID
	job.EnqueuedByFunctionID = callerFnID
	if err := h.DB.EnqueueJob(job); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "enqueue failed: "+err.Error(), reqID)
		return
	}
	if job.Replayed {
		w.Header().Set("X-Idempotency-Replayed", "true")
	}
	respond.JSON(w, http.StatusCreated, job)
}

// List handles GET /api/v1/jobs?status=...&function_id=...&limit=...
func (h *JobsHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	status := r.URL.Query().Get("status")
	fnID := r.URL.Query().Get("function_id")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	jobs, err := h.DB.ListJobs(status, fnID, limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "list failed: "+err.Error(), reqID)
		return
	}
	if jobs == nil {
		jobs = []*database.Job{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// Get handles GET /api/v1/jobs/{id}.
func (h *JobsHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	job, err := h.DB.GetJob(id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "job not found", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, job)
}

// Retry handles POST /api/v1/jobs/{id}/retry. Resets a terminal job back
// to pending so the next scheduler tick picks it up.
func (h *JobsHandler) Retry(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if _, err := h.DB.GetJob(id); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "job not found", reqID)
		return
	}
	if err := h.DB.RetryJob(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "retry failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "pending", "id": id})
}

// Delete handles DELETE /api/v1/jobs/{id}.
func (h *JobsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if err := h.DB.DeleteJob(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "delete failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}
