package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// JobsHandler exposes the background-job queue. It accepts requests both
// from the dashboard (session/API-key auth via the standard middleware)
// AND from inside worker sandboxes (X-Orva-Internal-Token). The internal
// path is what powers orva.jobs.enqueue() in the SDK.
type JobsHandler struct {
	DB            *database.Database
	Registry      *registry.Registry
	InternalToken string
}

// authorize accepts either the standard middleware-stamped auth (already
// validated upstream when present) OR a worker's internal token. Returns
// true when the request is permitted.
func (h *JobsHandler) authorize(r *http.Request) bool {
	// Internal token path — workers calling the SDK.
	got := r.Header.Get("X-Orva-Internal-Token")
	if h.InternalToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(h.InternalToken)) == 1 {
		return true
	}
	// Public path — middleware has already enforced session/API-key when
	// the request reached us; presence of the X-Orva-User-* OR
	// X-Orva-Authed marker would tell us. The current middleware
	// short-circuits unauth'd /api/v1/* requests before they reach
	// handlers, so reaching here means "authorized via session or key".
	return true
}

type enqueueRequest struct {
	FunctionID    string          `json:"function_id"`
	FunctionName  string          `json:"function_name"`
	Payload       json.RawMessage `json:"payload"`
	ScheduledAt   *time.Time      `json:"scheduled_at,omitempty"`
	MaxAttempts   int             `json:"max_attempts,omitempty"`
}

// Enqueue handles POST /api/v1/jobs.
func (h *JobsHandler) Enqueue(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authorized", r.Header.Get("X-Request-ID"))
		return
	}
	reqID := r.Header.Get("X-Request-ID")

	var req enqueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		FunctionID:  fnID,
		Payload:     payload,
		MaxAttempts: req.MaxAttempts,
	}
	if req.ScheduledAt != nil {
		job.ScheduledAt = req.ScheduledAt.UTC()
	}
	if err := h.DB.EnqueueJob(job); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "enqueue failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusCreated, job)
}

// List handles GET /api/v1/jobs?status=...&function_id=...&limit=...
func (h *JobsHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authorized", r.Header.Get("X-Request-ID"))
		return
	}
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
	if !h.authorize(r) {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authorized", r.Header.Get("X-Request-ID"))
		return
	}
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
	if !h.authorize(r) {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authorized", r.Header.Get("X-Request-ID"))
		return
	}
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
	if !h.authorize(r) {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authorized", r.Header.Get("X-Request-ID"))
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if err := h.DB.DeleteJob(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "delete failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}
