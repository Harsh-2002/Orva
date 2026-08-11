package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// KVHandler exposes the per-function key/value store to worker SDKs over
// HTTP loopback. Authentication uses a process-signed credential bound to one
// function ID. The route namespace must match that signed claim.
type KVHandler struct {
	DB      *database.Database
	SDKAuth *sdkauth.Authenticator
}

// authorize verifies the credential signature and route scope. It returns a
// standard 401 for invalid/stale credentials and 403 for cross-namespace use.
func (h *KVHandler) authorize(w http.ResponseWriter, r *http.Request, functionID string) bool {
	caller, err := h.SDKAuth.Verify(r.Header.Get("X-Orva-Internal-Token"))
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"missing or invalid SDK credential", r.Header.Get("X-Request-ID"))
		return false
	}
	if caller != functionID {
		respond.Error(w, http.StatusForbidden, "SDK_SCOPE_VIOLATION",
			"SDK credential cannot access another function's KV namespace", r.Header.Get("X-Request-ID"))
		return false
	}
	observeSDKVersion(r, caller)
	return true
}

// kvPutRequest carries the value + optional TTL.
type kvPutRequest struct {
	Value      json.RawMessage `json:"value"`
	TTLSeconds *int            `json:"ttl_seconds,omitempty"`
}

// kvGetResponse mirrors the put shape so adapters can round-trip values.
type kvGetResponse struct {
	Value     json.RawMessage `json:"value"`
	ExpiresAt *string         `json:"expires_at,omitempty"`
}

// Put handles PUT /api/v1/_kv/{fn_id}/{key}.
func (h *KVHandler) Put(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.PathValue("fn_id")) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	if err := database.ValidateKVKey(key); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	body, err := readBoundedBody(r.Body, 1<<20)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req kvPutRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if err := database.ValidateKVValue(req.Value); err != nil {
		status := http.StatusBadRequest
		if len(req.Value) > database.KVMaxValueBytes {
			status = http.StatusRequestEntityTooLarge
		}
		respond.Error(w, status, "VALIDATION", err.Error(), reqID)
		return
	}
	if err := database.ValidateKVTTL(req.TTLSeconds); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	if err := h.DB.KVPutContext(r.Context(), fnID, key, req.Value, req.TTLSeconds); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv put failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"status": "ok", "key": key, "ttl_seconds": req.TTLSeconds})
}

// Get handles GET /api/v1/_kv/{fn_id}/{key}.
func (h *KVHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.PathValue("fn_id")) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	if err := database.ValidateKVKey(key); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	entry, err := h.DB.KVGetContext(r.Context(), fnID, key)
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
	if !h.authorize(w, r, r.PathValue("fn_id")) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	if err := database.ValidateKVKey(key); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	if err := h.DB.KVDeleteContext(r.Context(), fnID, key); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv delete failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "key": key})
}

// List handles GET /api/v1/_kv/{fn_id}?prefix=foo&limit=100&cursor=k. The
// cursor is the last key from the previous page (exclusive); response
// includes next_cursor when more rows remain.
func (h *KVHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.PathValue("fn_id")) {
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
	page, err := h.DB.KVListWithCursorContext(r.Context(), fnID, prefix, cursor, limit)
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
		TTLSeconds *int            `json:"ttl_seconds,omitempty"`
	} `json:"ops"`
}

// Batch handles POST /api/v1/_kv/{fn_id}/batch. A single batch is capped
// at 100 ops to keep the SQLite write transaction bounded.
func (h *KVHandler) Batch(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.PathValue("fn_id")) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	body, err := readBoundedBody(r.Body, 8<<20)
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
		if err := database.ValidateKVKey(op.Key); err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
			return
		}
		if op.Op != "get" && op.Op != "put" && op.Op != "delete" {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "unknown batch operation: "+op.Op, reqID)
			return
		}
		if op.Op == "put" {
			if err := database.ValidateKVValue(op.Value); err != nil {
				respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
				return
			}
			if err := database.ValidateKVTTL(op.TTLSeconds); err != nil {
				respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
				return
			}
		}
		dbOps[i] = database.KVBatchOp{
			Op:         op.Op,
			Key:        op.Key,
			Value:      []byte(op.Value),
			TTLSeconds: op.TTLSeconds,
		}
	}
	results, err := h.DB.KVBatchContext(r.Context(), fnID, dbOps)
	if err != nil {
		respond.Error(w, http.StatusServiceUnavailable, "KV_UNAVAILABLE", "kv batch failed: "+err.Error(), reqID)
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
	TTLSeconds *int  `json:"ttl_seconds,omitempty"`
}

// Incr handles POST /api/v1/_kv/{fn_id}/{key}/incr. Atomically updates
// an integer value and returns the new value.
func (h *KVHandler) Incr(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.PathValue("fn_id")) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	if err := database.ValidateKVKey(key); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	body, err := readBoundedBody(r.Body, 1<<16)
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
	if err := database.ValidateKVTTL(req.TTLSeconds); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	next, err := h.DB.KVIncrContext(r.Context(), fnID, key, req.Delta, req.TTLSeconds)
	if err != nil {
		status := http.StatusInternalServerError
		code := "INTERNAL"
		if strings.Contains(err.Error(), "not an integer") {
			status = http.StatusConflict
			code = "KV_INCR_FAILED"
		}
		respond.Error(w, status, code, err.Error(), reqID)
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
	TTLSeconds *int             `json:"ttl_seconds,omitempty"`
}

// CAS handles POST /api/v1/_kv/{fn_id}/{key}/cas.
func (h *KVHandler) CAS(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, r.PathValue("fn_id")) {
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.PathValue("fn_id")
	key := r.PathValue("key")
	if err := database.ValidateKVKey(key); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	body, err := readBoundedBody(r.Body, 1<<20)
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
	if err := database.ValidateKVValue(req.New); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	if err := database.ValidateKVTTL(req.TTLSeconds); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	var expectedBytes []byte
	if req.Expected != nil {
		expectedBytes = []byte(*req.Expected)
		if err := database.ValidateKVValue(expectedBytes); err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "expected value: "+err.Error(), reqID)
			return
		}
	}
	ok, current, err := h.DB.KVCASContext(r.Context(), fnID, key, expectedBytes, []byte(req.New), req.TTLSeconds)
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

func readBoundedBody(body io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("request body exceeds limit")
	}
	return b, nil
}
