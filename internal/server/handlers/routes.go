package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// RouteHandler manages user-defined custom route → function mappings. These
// give functions pretty URLs like /webhooks/stripe instead of /api/v1/invoke/fn_xxx.
type RouteHandler struct {
	DB       *database.Database
	Registry *registry.Registry
}

type routeDTO struct {
	Path       string `json:"path"`
	FunctionID string `json:"function_id"`
	Methods    string `json:"methods,omitempty"`
}

func (h *RouteHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rows, err := h.DB.ListRoutes()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list routes", reqID)
		return
	}
	if rows == nil {
		rows = []*database.Route{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"routes": rows})
}

func (h *RouteHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	var req routeDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	req.Path = strings.TrimSpace(req.Path)
	req.FunctionID = strings.TrimSpace(req.FunctionID)

	if !strings.HasPrefix(req.Path, "/") {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "path must start with /", reqID)
		return
	}
	// Reserve internal Orva namespaces so users can't hijack the control plane.
	for _, reserved := range []string{"/api/", "/auth/", "/ui/", "/_orva/"} {
		if strings.HasPrefix(req.Path, reserved) {
			respond.Error(w, http.StatusBadRequest, "VALIDATION",
				"path conflicts with reserved prefix "+reserved, reqID)
			return
		}
	}
	// Prefix routes end in "/*" (e.g. "/shortener/*" matches any sub-path).
	// Validate that the `/*` sits at the end and nowhere else.
	if strings.Contains(req.Path, "*") && !strings.HasSuffix(req.Path, "/*") {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			"wildcard must be the final segment ('/prefix/*')", reqID)
		return
	}
	if req.FunctionID == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "function_id required", reqID)
		return
	}
	if _, err := h.Registry.Get(req.FunctionID); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	if err := h.DB.UpsertRoute(req.Path, req.FunctionID, req.Methods); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to save route", reqID)
		return
	}
	respond.JSON(w, http.StatusCreated, map[string]string{
		"status": "saved", "path": req.Path, "function_id": req.FunctionID,
	})
}

func (h *RouteHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	path := r.URL.Query().Get("path")
	if path == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "path query param required", reqID)
		return
	}
	if err := h.DB.DeleteRoute(path); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to delete route", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "path": path})
}
