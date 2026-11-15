package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

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

	body, err := readBoundedBody(r.Body, MaxEnqueueBody)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", err.Error(), reqID)
		return
	}
	var req EnqueueJobRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	job, serr := EnqueueJob(h.DB, callerFnID, callerTraceID, callerSpanID, &req)
	if serr != nil {
		serr.write(w, reqID)
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
