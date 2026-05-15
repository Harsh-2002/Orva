package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
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
// token. Returns false (and writes a 401) when invalid. Also opportunistically
// records the SDK version for operator visibility.
func (h *KVHandler) authorize(w http.ResponseWriter, r *http.Request) bool {
	got := r.Header.Get("X-Orva-Internal-Token")
	if h.InternalToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(h.InternalToken)) != 1 {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid internal token", r.Header.Get("X-Request-ID"))
		return false
	}
	observeSDKVersion(r)
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

// List handles GET /api/v1/_kv/{fn_id}?prefix=foo&limit=100&cursor=k. The
// cursor is the last key from the previous page (exclusive); response
// includes next_cursor when more rows remain.
func (h *KVHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	q := r.URL.Query()
	prefix := q.Get("prefix")
	cursor := q.Get("cursor")
	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	page, err := h.DB.KVListWithCursor(fnID, prefix, cursor, limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv list failed: "+err.Error(), reqID)
		return
	}
	type wireEntry struct {
		Key       string          `json:"key"`
		Value     json.RawMessage `json:"value"`
		ExpiresAt *string         `json:"expires_at,omitempty"`
	}
	out := make([]wireEntry, 0, len(page.Entries))
	for _, e := range page.Entries {
		var exp *string
		if e.ExpiresAt != nil {
			s := e.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
			exp = &s
		}
		out = append(out, wireEntry{Key: e.Key, Value: json.RawMessage(e.Value), ExpiresAt: exp})
	}
	resp := map[string]any{"keys": out}
	if page.NextCursor != "" {
		resp["next_cursor"] = page.NextCursor
	}
	respond.JSON(w, http.StatusOK, resp)
}

// kvBatchRequest carries up to N operations executed in a single SQLite
// write transaction. Order is preserved in the response.
type kvBatchRequest struct {
	Ops []struct {
		Op         string          `json:"op"`
		Key        string          `json:"key"`
		Value      json.RawMessage `json:"value,omitempty"`
		TTLSeconds int             `json:"ttl_seconds,omitempty"`
	} `json:"ops"`
}

// Batch handles POST /api/v1/_kv/{fn_id}/batch. A single batch is capped
// at 100 ops to keep the SQLite write transaction bounded.
func (h *KVHandler) Batch(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20)) // 8 MB cap per batch
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req kvBatchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if len(req.Ops) == 0 {
		respond.JSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	if len(req.Ops) > 100 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "batch capped at 100 ops", reqID)
		return
	}
	dbOps := make([]database.KVBatchOp, len(req.Ops))
	for i, op := range req.Ops {
		dbOps[i] = database.KVBatchOp{
			Op:         op.Op,
			Key:        op.Key,
			Value:      []byte(op.Value),
			TTLSeconds: op.TTLSeconds,
		}
	}
	results, err := h.DB.KVBatch(fnID, dbOps)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv batch failed: "+err.Error(), reqID)
		return
	}
	type wireResult struct {
		Op        string          `json:"op"`
		Key       string          `json:"key"`
		Found     bool            `json:"found"`
		Value     json.RawMessage `json:"value,omitempty"`
		ExpiresAt *string         `json:"expires_at,omitempty"`
		Err       string          `json:"error,omitempty"`
	}
	wire := make([]wireResult, len(results))
	for i, r := range results {
		ent := wireResult{Op: r.Op, Key: r.Key, Found: r.Found, Err: r.Err}
		if r.Value != nil {
			ent.Value = json.RawMessage(r.Value)
		}
		if r.ExpiresAt != nil {
			s := r.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
			ent.ExpiresAt = &s
		}
		wire[i] = ent
	}
	respond.JSON(w, http.StatusOK, map[string]any{"results": wire})
}

// kvIncrRequest carries the delta and optional TTL refresh.
type kvIncrRequest struct {
	Delta      int64 `json:"delta"`
	TTLSeconds int   `json:"ttl_seconds,omitempty"`
}

// Incr handles POST /api/v1/_kv/{fn_id}/{key}/incr. Atomically updates
// an integer value and returns the new value.
func (h *KVHandler) Incr(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	req := kvIncrRequest{Delta: 1}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
			return
		}
	}
	next, err := h.DB.KVIncr(fnID, key, req.Delta, req.TTLSeconds)
	if err != nil {
		respond.Error(w, http.StatusConflict, "KV_INCR_FAILED", err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"value": next})
}

// kvCASRequest expresses "swap from Expected to New, only if Expected
// matches the current value". A null Expected means "the key must not
// currently exist" (insert-if-absent).
type kvCASRequest struct {
	Expected   *json.RawMessage `json:"expected"`
	New        json.RawMessage  `json:"new"`
	TTLSeconds int              `json:"ttl_seconds,omitempty"`
}

// CAS handles POST /api/v1/_kv/{fn_id}/{key}/cas.
func (h *KVHandler) CAS(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req kvCASRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if len(req.New) == 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "new value is required", reqID)
		return
	}
	var expectedBytes []byte
	if req.Expected != nil {
		expectedBytes = []byte(*req.Expected)
	}
	ok, current, err := h.DB.KVCAS(fnID, key, expectedBytes, []byte(req.New), req.TTLSeconds)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv cas failed: "+err.Error(), reqID)
		return
	}
	resp := map[string]any{"ok": ok}
	if !ok && current != nil {
		resp["current"] = json.RawMessage(current)
	}
	respond.JSON(w, http.StatusOK, resp)
}
