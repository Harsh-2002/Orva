package handlers

import (
	"net/http"

	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// RuntimeHandler handles runtime listing endpoints.
type RuntimeHandler struct{}

// runtimeInfo describes a supported runtime.
type runtimeInfo struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Language       string   `json:"language"`
	DefaultHandler string   `json:"default_handler"`
	Extensions     []string `json:"extensions"`
}

var supportedRuntimes = []runtimeInfo{
	{
		ID:             "node22",
		Name:           "Node.js 22 (Active LTS)",
		Language:       "javascript",
		DefaultHandler: "handler.js",
		Extensions:     []string{".js", ".mjs", ".cjs"},
	},
	{
		ID:             "node24",
		Name:           "Node.js 24 (Current LTS)",
		Language:       "javascript",
		DefaultHandler: "handler.js",
		Extensions:     []string{".js", ".mjs", ".cjs"},
	},
	{
		ID:             "python313",
		Name:           "Python 3.13",
		Language:       "python",
		DefaultHandler: "handler.py",
		Extensions:     []string{".py"},
	},
	{
		ID:             "python314",
		Name:           "Python 3.14",
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
