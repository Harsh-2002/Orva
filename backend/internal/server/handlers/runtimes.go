package handlers

import (
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// RuntimeHandler handles runtime listing endpoints.
type RuntimeHandler struct{}

// runtimeInfo describes a supported runtime. The ID is the generic, stable
// identifier callers use (`node` / `python`); Name + Version are display-only
// labels that reveal the concrete latest-stable version the runtime currently
// tracks (clients should not parse them).
type runtimeInfo struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Language       string   `json:"language"`
	DefaultHandler string   `json:"default_handler"`
	Extensions     []string `json:"extensions"`
}

// supportedRuntimes is Orva's runtime catalog: exactly two, latest-stable only.
// The generic IDs never change; the Name/Version labels move as we bump the
// underlying major.
var supportedRuntimes = []runtimeInfo{
	{
		ID:             "node",
		Name:           "Node.js 24 (current)",
		Version:        "24",
		Language:       "javascript",
		DefaultHandler: "handler.js",
		Extensions:     []string{".js", ".mjs", ".cjs", ".ts"},
	},
	{
		ID:             "python",
		Name:           "Python 3.14 (current)",
		Version:        "3.14",
		Language:       "python",
		DefaultHandler: "handler.py",
		Extensions:     []string{".py"},
	},
}

// List handles GET /api/v1/runtimes.
func (h *RuntimeHandler) List(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]any{
		"runtimes": supportedRuntimes,
	})
}
