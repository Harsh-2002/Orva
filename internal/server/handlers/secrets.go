package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/secrets"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// SecretHandler manages per-function encrypted secrets.
type SecretHandler struct {
	Secrets  *secrets.Manager
	Registry *registry.Registry
	// PoolRefresh, when set, drains the function's warm pool so the next
	// invoke spawns a fresh worker with the updated secret env. Without
	// this, secrets only reach a function on its next worker recycle —
	// stale workers keep the old env until they expire.
	PoolRefresh func(fnID string)
}

// invalidatePool calls PoolRefresh if wired. Used by Upsert + Delete so
// secret changes take effect on the next invoke.
func (h *SecretHandler) invalidatePool(fnID string) {
	if h.PoolRefresh != nil {
		h.PoolRefresh(fnID)
	}
}

// List handles GET /api/v1/functions/{fn_id}/secrets.
// Returns secret NAMES only — values are never returned once stored.
func (h *SecretHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")

	if _, err := h.Registry.Get(fnID); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	keys, err := h.Secrets.List(fnID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list secrets", reqID)
		return
	}
	if keys == nil {
		keys = []string{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"secrets": keys})
}

// Upsert handles POST /api/v1/functions/{fn_id}/secrets.
// Body: {"key": "STRIPE_KEY", "value": "sk_live_..."}
func (h *SecretHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")

	if _, err := h.Registry.Get(fnID); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "key is required", reqID)
		return
	}

	if err := h.Secrets.Upsert(fnID, req.Key, req.Value); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to store secret: "+err.Error(), reqID)
		return
	}
	h.invalidatePool(fnID)
	respond.JSON(w, http.StatusOK, map[string]string{"status": "saved", "key": req.Key})
}

// Delete handles DELETE /api/v1/functions/{fn_id}/secrets/{key}.
func (h *SecretHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	if key == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing secret key", reqID)
		return
	}
	if err := h.Secrets.Delete(fnID, key); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to delete secret", reqID)
		return
	}
	h.invalidatePool(fnID)
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "key": key})
}
