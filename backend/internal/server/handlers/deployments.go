package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// DeploymentHandler surfaces async build state to clients. Deployments are
// created implicitly by the deploy endpoints (functions.go); this handler
// only exposes the read-side (get/list/logs/stream).
type DeploymentHandler struct {
	DB *database.Database
}

// Get — GET /api/v1/deployments/{id}
func (h *DeploymentHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reqID := r.Header.Get("X-Request-ID")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing deployment id", reqID)
		return
	}
	d, err := h.DB.GetDeployment(id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "deployment not found", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, d)
}

// ListForFunction — GET /api/v1/functions/{fn_id}/deployments
func (h *DeploymentHandler) ListForFunction(w http.ResponseWriter, r *http.Request) {
	fnID := r.PathValue("fn_id")
	reqID := r.Header.Get("X-Request-ID")
	if fnID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing function id", reqID)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	list, err := h.DB.ListDeploymentsForFunction(fnID, limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"deployments": list})
}

// GetLogs — GET /api/v1/deployments/{id}/logs?from=<seq>
// Returns a chunk of lines. Clients poll with `from=last_seq` for an
// incremental tail.
func (h *DeploymentHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reqID := r.Header.Get("X-Request-ID")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing deployment id", reqID)
		return
	}
	from := int64(0)
	if v := r.URL.Query().Get("from"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			from = n
		}
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	lines, err := h.DB.GetBuildLogs(id, from, limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"logs": lines})
}

// Stream — GET /api/v1/deployments/{id}/stream
// Server-Sent Events stream of log lines + terminal state. Polls the DB in
// a tight loop because SQLite doesn't notify us of inserts; good enough
// since the build log volume is low per deployment.
func (h *DeploymentHandler) Stream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	reqID := r.Header.Get("X-Request-ID")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing deployment id", reqID)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "streaming unsupported", reqID)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: disable buffering

	send := func(event string, payload any) {
		body, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body)
		flusher.Flush()
	}

	var last int64 = 0
	for {
		// Ship any new log lines.
		lines, err := h.DB.GetBuildLogs(id, last, 1000)
		if err != nil {
			send("error", map[string]string{"message": err.Error()})
			return
		}
		for _, l := range lines {
			send("log", l)
			if l.Seq > last {
				last = l.Seq
			}
		}

		// Check terminal state.
		d, err := h.DB.GetDeployment(id)
		if err != nil {
			send("error", map[string]string{"message": "deployment disappeared: " + err.Error()})
			return
		}
		if d.Status == "succeeded" || d.Status == "failed" {
			send(d.Status, d)
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}
