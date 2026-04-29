package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// ExecutionHandler handles execution-related endpoints.
type ExecutionHandler struct {
	DB *database.Database
}

// List handles GET /api/v1/executions.
func (h *ExecutionHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	params := database.ListExecutionsParams{
		FunctionID: r.URL.Query().Get("function_id"),
		Status:     r.URL.Query().Get("status"),
		Since:      r.URL.Query().Get("since"),
		Until:      r.URL.Query().Get("until"),
		Search:     r.URL.Query().Get("q"),
	}

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			params.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			params.Offset = n
		}
	}

	result, err := h.DB.ListExecutions(params)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list executions", reqID)
		return
	}

	respond.JSON(w, http.StatusOK, result)
}

// Get handles GET /api/v1/executions/{exec_id}.
func (h *ExecutionHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	execID := extractExecID(r.URL.Path)
	if execID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing execution ID", reqID)
		return
	}

	exec, err := h.DB.GetExecution(execID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "execution not found", reqID)
		return
	}

	respond.JSON(w, http.StatusOK, exec)
}

// GetLogs handles GET /api/v1/executions/{exec_id}/logs.
func (h *ExecutionHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	execID := extractExecLogsID(r.URL.Path)
	if execID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing execution ID", reqID)
		return
	}

	logs, err := h.DB.GetExecutionLog(execID)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "execution logs not found", reqID)
		return
	}

	respond.JSON(w, http.StatusOK, logs)
}

// Delete handles DELETE /api/v1/executions/{exec_id}. Removes the row
// + its logs (FK CASCADE in schema).
func (h *ExecutionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	execID := extractExecID(r.URL.Path)
	if execID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing execution ID", reqID)
		return
	}
	if err := h.DB.DeleteExecution(execID); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": execID})
}

// BulkDelete handles POST /api/v1/executions/bulk-delete with body
// {"ids":["exec_a","exec_b",...]}. Returns counts of deleted/failed.
// Path-based DELETE on individual execs still works for one-off use.
func (h *ExecutionHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if len(body.IDs) == 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "ids must be a non-empty array", reqID)
		return
	}
	if len(body.IDs) > 1000 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "max 1000 ids per request", reqID)
		return
	}
	deleted, failed := 0, 0
	for _, id := range body.IDs {
		if err := h.DB.DeleteExecution(id); err != nil {
			failed++
			continue
		}
		deleted++
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"deleted": deleted,
		"failed":  failed,
	})
}

// extractExecID extracts the execution ID from path /api/v1/executions/{exec_id}.
func extractExecID(path string) string {
	const prefix = "/api/v1/executions/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(remainder, "/"); idx >= 0 {
		return remainder[:idx]
	}
	return remainder
}

// extractExecLogsID extracts the execution ID from path /api/v1/executions/{exec_id}/logs.
func extractExecLogsID(path string) string {
	const prefix = "/api/v1/executions/"
	const suffix = "/logs"
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
