package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

// Resume has no normal-serving route until coordinated IAM cut-over.
func (h *JobsHandler) Resume(w http.ResponseWriter, r *http.Request) {
	if !h.AsyncEnabled {
		respond.Error(w, 404, "NOT_FOUND", "async controls unavailable", "")
		return
	}
	h.asyncMutate(w, r, "resume")
}

func asyncJobError(w http.ResponseWriter, err error) {
	status, code, msg := http.StatusServiceUnavailable, "INTERNAL", "async operation unavailable"
	if errors.Is(err, workloadiam.ErrDenied) || errors.Is(err, database.ErrOperatorDenied) {
		status, code, msg = 403, "IAM_DENIED", "async operation denied"
	}
	if errors.Is(err, database.ErrAsyncConflict) {
		status, code, msg = 409, "CONFLICT", "async work changed or is not eligible"
	}
	respond.Error(w, status, code, msg, "")
}
func (h *JobsHandler) asyncReady(w http.ResponseWriter) bool {
	if h.AsyncIAM == nil || h.AsyncIAM.DB != h.DB {
		asyncJobError(w, workloadiam.ErrDenied)
		return false
	}
	return true
}
func (h *JobsHandler) asyncEnqueue(w http.ResponseWriter, r *http.Request) {
	if !h.asyncReady(w) {
		return
	}
	ref, ok := auth.OperatorFromContext(r.Context())
	if !ok {
		asyncJobError(w, workloadiam.ErrDenied)
		return
	}
	body, err := readBoundedBody(r.Body, MaxEnqueueBody)
	if err != nil {
		respond.Error(w, 400, "INVALID_BODY", "request body exceeds limit", "")
		return
	}
	var req EnqueueJobRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if !json.Valid(body) || dec.Decode(&req) != nil {
		respond.Error(w, 400, "INVALID_JSON", "invalid request body", "")
		return
	}
	job, serr := PrepareEnqueueJob(h.DB, "", "", "", &req)
	if serr != nil {
		serr.write(w, "")
		return
	}
	j := &database.AsyncJob{Job: *job}
	if err := h.AsyncIAM.EnqueueOperator(r.Context(), ref, j); err != nil {
		asyncJobError(w, err)
		return
	}
	if j.Replayed {
		w.Header().Set("X-Idempotency-Replayed", "true")
	}
	respond.JSON(w, http.StatusCreated, j)
}
func (h *JobsHandler) asyncList(w http.ResponseWriter, r *http.Request) {
	if !h.asyncReady(w) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := h.DB.ListAsyncJobs(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("function_id"), limit, nil)
	if err != nil {
		asyncJobError(w, err)
		return
	}
	ref, ok := auth.OperatorFromContext(r.Context())
	respond.JSON(w, 200, map[string]any{"jobs": jobs, "async_controls": true, "can_manage": ok && h.DB.CanManageAsync(r.Context(), ref)})
}
func (h *JobsHandler) asyncGet(w http.ResponseWriter, r *http.Request) {
	if !h.asyncReady(w) {
		return
	}
	j, err := h.DB.GetAsyncJob(r.Context(), r.PathValue("id"))
	if err != nil {
		respond.Error(w, 404, "NOT_FOUND", "job not found", "")
		return
	}
	respond.JSON(w, 200, j)
}
func (h *JobsHandler) asyncMutate(w http.ResponseWriter, r *http.Request, action string) {
	if !h.asyncReady(w) {
		return
	}
	ref, ok := auth.OperatorFromContext(r.Context())
	if !ok {
		asyncJobError(w, workloadiam.ErrDenied)
		return
	}
	id := r.PathValue("id")
	if err := h.AsyncIAM.MutateJob(r.Context(), ref, id, action); err != nil {
		asyncJobError(w, err)
		return
	}
	respond.JSON(w, 200, map[string]string{"id": id, "action": action})
}
