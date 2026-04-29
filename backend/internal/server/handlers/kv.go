package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// KVHandler exposes the per-function key/value store to worker SDKs over
// HTTP loopback. Authentication is the per-process internal token —
// callers (the worker adapters) prove they're inside a sandbox by sending
// it back. Function ID is in the URL; values are arbitrary bytes wrapped
// in JSON for transport.
type KVHandler struct {
	DB            *database.Database
	InternalToken string
}

// authorize confirms the request bears the matching X-Orva-Internal-Token
// header. Constant-time comparison so timing leaks can't help guess the
// token. Returns false (and writes a 401) when invalid.
func (h *KVHandler) authorize(w http.ResponseWriter, r *http.Request) bool {
	got := r.Header.Get("X-Orva-Internal-Token")
	if h.InternalToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.InternalToken)) != 1 {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid internal token", r.Header.Get("X-Request-ID"))
		return false
	}
	return true
}

// kvPutRequest carries the value + optional TTL.
type kvPutRequest struct {
	Value      json.RawMessage `json:"value"`
	TTLSeconds int             `json:"ttl_seconds,omitempty"`
}

// kvGetResponse mirrors the put shape so adapters can round-trip values.
type kvGetResponse struct {
	Value      json.RawMessage `json:"value"`
	ExpiresAt  *string         `json:"expires_at,omitempty"`
}

// Put handles PUT /api/v1/_kv/{fn_id}/{key}.
func (h *KVHandler) Put(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	if fnID == "" || key == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "fn_id and key are required", reqID)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB cap per value
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req kvPutRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if len(req.Value) == 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "value is required", reqID)
		return
	}
	if err := h.DB.KVPut(fnID, key, req.Value, req.TTLSeconds); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv put failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"status": "ok", "key": key, "ttl_seconds": req.TTLSeconds})
}

// Get handles GET /api/v1/_kv/{fn_id}/{key}.
func (h *KVHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	entry, err := h.DB.KVGet(fnID, key)
	if errors.Is(err, database.ErrKVNotFound) {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "key not found", reqID)
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv get failed: "+err.Error(), reqID)
		return
	}
	resp := kvGetResponse{Value: json.RawMessage(entry.Value)}
	if entry.ExpiresAt != nil {
		s := entry.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
		resp.ExpiresAt = &s
	}
	respond.JSON(w, http.StatusOK, resp)
}

// Delete handles DELETE /api/v1/_kv/{fn_id}/{key}.
func (h *KVHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	if err := h.DB.KVDelete(fnID, key); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv delete failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "key": key})
}

// List handles GET /api/v1/_kv/{fn_id}?prefix=foo&limit=100. Projects
// each row's []byte value into json.RawMessage so the wire payload is
// the original JSON the user PUT, not a base64 wrapper of it.
func (h *KVHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	prefix := r.URL.Query().Get("prefix")
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	entries, err := h.DB.KVList(fnID, prefix, limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv list failed: "+err.Error(), reqID)
		return
	}
	type wireEntry struct {
		Key       string          `json:"key"`
		Value     json.RawMessage `json:"value"`
		ExpiresAt *string         `json:"expires_at,omitempty"`
	}
	out := make([]wireEntry, 0, len(entries))
	for _, e := range entries {
		var exp *string
		if e.ExpiresAt != nil {
			s := e.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
			exp = &s
		}
		out = append(out, wireEntry{Key: e.Key, Value: json.RawMessage(e.Value), ExpiresAt: exp})
	}
	respond.JSON(w, http.StatusOK, map[string]any{"keys": out})
}
