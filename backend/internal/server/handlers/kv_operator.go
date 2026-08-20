// Operator-facing REST handlers for the per-function KV store. Mirrors
// the MCP tools in tools_kv.go (same shapes, same database calls) so
// the dashboard can browse / inspect / edit / delete keys without
// having to reach for the MCP transport.
//
// The internal-token handlers in kv.go stay separate and untouched —
// those serve worker SDK calls from inside sandboxes; these serve
// session/api-key authenticated callers from the dashboard. Sharing
// a file would muddy two distinct auth models.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// Per-value cap for KV writes from the dashboard. Mirrors the cap the
// SDK adapter enforces from inside sandboxes — keeps the operator path
// honest. The SDK side checks this in a different spot, so we duplicate
// the constant rather than couple the two packages.
const kvOperatorMaxValueBytes = database.KVMaxValueBytes

// kvOperatorBodyCap is the request-body read limit. Slightly larger
// than the value cap so the {"value": ..., "ttl_seconds": ...} envelope
// always fits when the value is right at 64 KB.
const kvOperatorBodyCap = 1 << 20 // 1 MB

// KVOperatorHandler exposes /api/v1/functions/{fn_id}/kv[/{key}].
type KVOperatorHandler struct {
	DB       *database.Database
	Registry *registry.Registry
}

// resolveFnID accepts either a UUID id or a friendly name and returns
// the canonical function ID. Mirrors the MCP tool helper so
// /functions/<name>/kv works the same as /functions/<uuid>/kv.
func (h *KVOperatorHandler) resolveFnID(idOrName string) (string, bool) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return "", false
	}
	if ids.IsUUID(idOrName) {
		if _, err := h.Registry.Get(idOrName); err == nil {
			return idOrName, true
		}
	}
	if fn, err := h.DB.GetFunctionByName(idOrName); err == nil && fn != nil {
		return fn.ID, true
	}
	return "", false
}

// kvWireEntry is the public per-row shape for the dashboard. The size
// field is computed at serialise time — operators want to see "what's
// eating my 64 KB cap" at a glance without a separate request.
type kvWireEntry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	ExpiresAt *string         `json:"expires_at,omitempty"`
	UpdatedAt string          `json:"updated_at"`
	SizeBytes int             `json:"size_bytes"`
}

func toWireEntry(e *database.KVEntry) kvWireEntry {
	w := kvWireEntry{
		Key:       e.Key,
		Value:     json.RawMessage(e.Value),
		UpdatedAt: e.UpdatedAt.UTC().Format(time.RFC3339),
		SizeBytes: len(e.Value),
	}
	if e.ExpiresAt != nil {
		s := e.ExpiresAt.UTC().Format(time.RFC3339)
		w.ExpiresAt = &s
	}
	return w
}

// List handles GET /api/v1/functions/{fn_id}/kv.
//
// Query params: prefix, limit (default 200, max 1000).
// Response: { entries: [...], total: N, truncated: bool }
//
// `total` is the COUNT(*) of live keys (including ones outside the
// returned page); the dashboard surfaces it in the "X keys" badge.
// `truncated` is true when the entries slice was capped by `limit`,
// which is the operator's hint that they should narrow the prefix.
func (h *KVOperatorHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	prefix := r.URL.Query().Get("prefix")
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	// Use the cursor-capable list, as the SDK-facing twin already does. This
	// endpoint had no cursor at all, and the dashboard renders "Showing first
	// 200. Narrow the prefix to see more." -- so 5000 flat keys were simply
	// not browsable.
	page, err := h.DB.KVListWithCursorContext(r.Context(), fnID, prefix, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv list failed: "+err.Error(), reqID)
		return
	}
	entries := page.Entries
	total, err := h.DB.KVCountContext(r.Context(), fnID)
	if err != nil {
		// Soft-fail on the count — the list itself succeeded, surface 0
		// rather than 500ing the whole response.
		total = len(entries)
	}

	out := make([]kvWireEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, toWireEntry(e))
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"entries":     out,
		"total":       total,
		"next_cursor": page.NextCursor,
		"truncated":   page.NextCursor != "",
	})
}

// Get handles GET /api/v1/functions/{fn_id}/kv/{key}.
func (h *KVOperatorHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	key := r.PathValue("key")
	if err := database.ValidateKVKey(key); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}

	entry, err := h.DB.KVGetContext(r.Context(), fnID, key)
	if errors.Is(err, database.ErrKVNotFound) {
		respond.JSON(w, http.StatusNotFound, map[string]any{"found": false, "key": key})
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv get failed: "+err.Error(), reqID)
		return
	}
	wire := toWireEntry(entry)
	respond.JSON(w, http.StatusOK, map[string]any{
		"found":      true,
		"key":        wire.Key,
		"value":      wire.Value,
		"expires_at": wire.ExpiresAt,
		"updated_at": wire.UpdatedAt,
		"size_bytes": wire.SizeBytes,
	})
}

// kvOperatorPutRequest is the shape of PUT bodies. Value is required
// (any JSON), ttl_seconds is optional (0 = no expiry).
type kvOperatorPutRequest struct {
	Value      json.RawMessage `json:"value"`
	TTLSeconds *int            `json:"ttl_seconds,omitempty"`
}

// Put handles PUT /api/v1/functions/{fn_id}/kv/{key}. Upserts.
func (h *KVOperatorHandler) Put(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	key := r.PathValue("key")
	if err := database.ValidateKVKey(key); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}

	body, err := readBoundedBody(r.Body, kvOperatorBodyCap)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req kvOperatorPutRequest
	if err := json.Unmarshal(body, &req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if len(req.Value) == 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "value is required", reqID)
		return
	}
	if len(req.Value) > kvOperatorMaxValueBytes {
		respond.Error(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
			"value exceeds 64 KB cap", reqID)
		return
	}
	// Reject malformed JSON values — the field is `json.RawMessage` so
	// the outer Unmarshal accepts any byte sequence; we re-parse to make
	// sure the dashboard didn't send garbage that would round-trip
	// unreadable on the next GET.
	var probe any
	if err := json.Unmarshal(req.Value, &probe); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "value must be valid JSON", reqID)
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
	respond.JSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"key":         key,
		"ttl_seconds": req.TTLSeconds,
	})
}

// Delete handles DELETE /api/v1/functions/{fn_id}/kv/{key}. Idempotent —
// 200 whether or not the row existed (matches MCP / SDK semantics).
func (h *KVOperatorHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
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

// kvOperatorIncrRequest carries the delta and optional TTL refresh. Mirrors
// the internal kvIncrRequest (kv.go) — same shape, operator auth.
type kvOperatorIncrRequest struct {
	Delta      int64 `json:"delta"`
	TTLSeconds *int  `json:"ttl_seconds,omitempty"`
}

// Incr handles POST /api/v1/functions/{fn_id}/kv/{key}/incr. Atomically adds
// delta (default 1) to an integer value and returns the new value. The
// API-key-authed twin of the internal-token KVHandler.Incr.
func (h *KVOperatorHandler) Incr(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
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
	req := kvOperatorIncrRequest{Delta: 1}
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
		respond.Error(w, http.StatusConflict, "KV_INCR_FAILED", err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"value": next})
}

// kvOperatorCASRequest expresses "swap from Expected to New, only if Expected
// matches the current value". A null Expected means "the key must not currently
// exist" (insert-if-absent). Mirrors the internal kvCASRequest (kv.go).
type kvOperatorCASRequest struct {
	Expected   *json.RawMessage `json:"expected"`
	New        json.RawMessage  `json:"new"`
	TTLSeconds *int             `json:"ttl_seconds,omitempty"`
}

// CAS handles POST /api/v1/functions/{fn_id}/kv/{key}/cas. A failed precondition
// is a successful HTTP 200 with {"ok": false, "current": <value>} so callers can
// retry; only a genuine store error is a 500.
func (h *KVOperatorHandler) CAS(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	key := r.PathValue("key")
	if err := database.ValidateKVKey(key); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	body, err := readBoundedBody(r.Body, kvOperatorBodyCap)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_BODY", "failed to read body", reqID)
		return
	}
	var req kvOperatorCASRequest
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
		if err := database.ValidateKVValue(expectedBytes); err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "expected value: "+err.Error(), reqID)
			return
		}
	}
	if err := database.ValidateKVValue(req.New); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	if err := database.ValidateKVTTL(req.TTLSeconds); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	swapped, current, err := h.DB.KVCASContext(r.Context(), fnID, key, expectedBytes, []byte(req.New), req.TTLSeconds)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "kv cas failed: "+err.Error(), reqID)
		return
	}
	resp := map[string]any{"ok": swapped}
	if !swapped && current != nil {
		resp["current"] = json.RawMessage(current)
	}
	respond.JSON(w, http.StatusOK, resp)
}
