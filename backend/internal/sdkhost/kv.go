package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// Per-op argument ceilings mirror the body limits of the HTTP KV handlers
// (get/delete/list/incr 64 KiB, put/cas 1 MiB, batch 8 MiB) and are further
// clamped to the protocol frame size.
const (
	limitSmall = 1 << 16
	limitValue = 1 << 20
	limitBatch = 8 << 20

	// Reply-size bounds. A reply is capped by the 4 MiB frame, but building one
	// close to it for every request would let a function make the host hold
	// tens of megabytes per call. kv.list pages are cut here (with a cursor);
	// a kv.batch whose results exceed the bound is refused.
	maxListReplyBytes  = 2 << 20
	maxBatchReplyBytes = 3 << 20
	kvListSlice        = 100
)

func fail(status int, code, msg string) *sandbox.SDKError {
	return &sandbox.SDKError{Status: status, Code: code, Message: msg}
}

func valErr(err error) *sandbox.SDKError { return fail(400, "VALIDATION", err.Error()) }

// decodeArgs strictly decodes the "a" object: unknown fields, wrong types,
// trailing data, non-object values and oversized arguments are all rejected.
// An absent or null "a" is an empty object.
func decodeArgs(args json.RawMessage, limit int, dst any) *sandbox.SDKError {
	if limit > sandbox.MaxSDKFrame {
		limit = sandbox.MaxSDKFrame
	}
	if len(args) > limit {
		return fail(400, "INVALID_BODY", "failed to read body")
	}
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fail(400, "INVALID_JSON", "invalid request body")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fail(400, "INVALID_JSON", "invalid request body")
	}
	return nil
}

func fmtTime(e *database.KVEntry) *string {
	if e.ExpiresAt == nil {
		return nil
	}
	s := e.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
	return &s
}

// ---- wire shapes (identical JSON to the HTTP handlers' responses) ----

type getResult struct {
	Value     json.RawMessage `json:"value"`
	ExpiresAt *string         `json:"expires_at,omitempty"`
}

type listEntry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	ExpiresAt *string         `json:"expires_at,omitempty"`
}

type batchOpArgs struct {
	Op         string          `json:"op"`
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value,omitempty"`
	TTLSeconds *int            `json:"ttl_seconds,omitempty"`
}

type batchResult struct {
	Op        string          `json:"op"`
	Key       string          `json:"key"`
	Found     bool            `json:"found"`
	Value     json.RawMessage `json:"value,omitempty"`
	ExpiresAt *string         `json:"expires_at,omitempty"`
	Err       string          `json:"error,omitempty"`
}

// ---- args ----

type keyArgs struct {
	Key string `json:"key"`
}

type putArgs struct {
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
	TTLSeconds *int            `json:"ttl_seconds,omitempty"`
}

type listArgs struct {
	Prefix string          `json:"prefix"`
	Cursor string          `json:"cursor"`
	Limit  json.RawMessage `json:"limit"`
}

type batchArgs struct {
	Ops []batchOpArgs `json:"ops"`
}

type incrArgs struct {
	Key        string `json:"key"`
	Delta      int64  `json:"delta"`
	TTLSeconds *int   `json:"ttl_seconds,omitempty"`
}

type casArgs struct {
	Key        string           `json:"key"`
	Expected   *json.RawMessage `json:"expected"`
	New        json.RawMessage  `json:"new"`
	TTLSeconds *int             `json:"ttl_seconds,omitempty"`
}

// ---- ops ----

func (h *Host) kvGet(ctx context.Context, fn string, args json.RawMessage) (any, *sandbox.SDKError) {
	var a keyArgs
	if e := decodeArgs(args, limitSmall, &a); e != nil {
		return nil, e
	}
	if err := database.ValidateKVKey(a.Key); err != nil {
		return nil, valErr(err)
	}
	entry, err := h.DB.KVGetContext(ctx, fn, a.Key)
	if errors.Is(err, database.ErrKVNotFound) {
		return nil, fail(404, "NOT_FOUND", "key not found")
	}
	if err != nil {
		return nil, fail(500, "INTERNAL", "kv get failed: "+err.Error())
	}
	return getResult{Value: json.RawMessage(entry.Value), ExpiresAt: fmtTime(entry)}, nil
}

func (h *Host) kvPut(ctx context.Context, fn string, args json.RawMessage) (any, *sandbox.SDKError) {
	var a putArgs
	if e := decodeArgs(args, limitValue, &a); e != nil {
		return nil, e
	}
	if err := database.ValidateKVKey(a.Key); err != nil {
		return nil, valErr(err)
	}
	if err := database.ValidateKVValue(a.Value); err != nil {
		status := 400
		if len(a.Value) > database.KVMaxValueBytes {
			status = 413
		}
		return nil, fail(status, "VALIDATION", err.Error())
	}
	if err := database.ValidateKVTTL(a.TTLSeconds); err != nil {
		return nil, valErr(err)
	}
	if err := h.DB.KVPutContext(ctx, fn, a.Key, a.Value, a.TTLSeconds); err != nil {
		return nil, fail(500, "INTERNAL", "kv put failed: "+err.Error())
	}
	return map[string]any{"status": "ok", "key": a.Key, "ttl_seconds": a.TTLSeconds}, nil
}

func (h *Host) kvDelete(ctx context.Context, fn string, args json.RawMessage) (any, *sandbox.SDKError) {
	var a keyArgs
	if e := decodeArgs(args, limitSmall, &a); e != nil {
		return nil, e
	}
	if err := database.ValidateKVKey(a.Key); err != nil {
		return nil, valErr(err)
	}
	if err := h.DB.KVDeleteContext(ctx, fn, a.Key); err != nil {
		return nil, fail(500, "INTERNAL", "kv delete failed: "+err.Error())
	}
	return map[string]string{"status": "deleted", "key": a.Key}, nil
}

func (h *Host) kvList(ctx context.Context, fn string, args json.RawMessage) (any, *sandbox.SDKError) {
	var a listArgs
	if e := decodeArgs(args, limitSmall, &a); e != nil {
		return nil, e
	}
	// The HTTP handler ignores a limit that does not parse as an integer and
	// falls back to 100. SDKs send Number(limit), which may be fractional (or
	// null for NaN/Infinity), so the same leniency applies here.
	limit := 100
	if t := bytes.TrimSpace(a.Limit); len(t) > 0 && !bytes.Equal(t, []byte("null")) {
		switch t[0] {
		case '"':
			var s string
			if json.Unmarshal(t, &s) != nil {
				return nil, fail(400, "INVALID_JSON", "invalid request body")
			}
			if n, err := strconv.Atoi(s); err == nil {
				limit = n
			}
		case '{', '[', 't', 'f':
			return nil, fail(400, "INVALID_JSON", "invalid request body")
		default:
			if n, err := strconv.Atoi(string(t)); err == nil {
				limit = n
			}
		}
	}
	if limit <= 0 || limit > 1000 {
		limit = 100 // same normalisation the database applies
	}
	// A page can be limit x 64 KiB. Read it in slices and stop once the reply
	// would be large, handing back a cursor so the caller simply asks again:
	// the host never holds a multi-megabyte page for one small request.
	out := make([]listEntry, 0, min(limit, 100))
	size, cursor, next := 0, a.Cursor, ""
	for remaining := limit; remaining > 0; {
		page, err := h.DB.KVListWithCursorContext(ctx, fn, a.Prefix, cursor, min(remaining, kvListSlice))
		if err != nil {
			return nil, fail(500, "INTERNAL", "kv list failed: "+err.Error())
		}
		for _, e := range page.Entries {
			if len(out) > 0 && size+len(e.Key)+len(e.Value) > maxListReplyBytes {
				next = out[len(out)-1].Key
				return listReply(out, next), nil
			}
			size += len(e.Key) + len(e.Value) + 64
			out = append(out, listEntry{Key: e.Key, Value: json.RawMessage(e.Value), ExpiresAt: fmtTime(e)})
		}
		remaining -= len(page.Entries)
		if page.NextCursor == "" {
			next = ""
			break
		}
		cursor, next = page.NextCursor, page.NextCursor
		if len(page.Entries) == 0 {
			break
		}
	}
	return listReply(out, next), nil
}

func listReply(out []listEntry, next string) map[string]any {
	resp := map[string]any{"keys": out}
	if next != "" {
		resp["next_cursor"] = next
	}
	return resp
}

func (h *Host) kvBatch(ctx context.Context, fn string, args json.RawMessage) (any, *sandbox.SDKError) {
	var a batchArgs
	if e := decodeArgs(args, limitBatch, &a); e != nil {
		return nil, e
	}
	if len(a.Ops) == 0 {
		return map[string]any{"results": []any{}}, nil
	}
	if len(a.Ops) > 100 {
		return nil, fail(400, "VALIDATION", "batch capped at 100 ops")
	}
	dbOps := make([]database.KVBatchOp, len(a.Ops))
	for i, op := range a.Ops {
		if err := database.ValidateKVKey(op.Key); err != nil {
			return nil, valErr(err)
		}
		if op.Op != "get" && op.Op != "put" && op.Op != "delete" {
			return nil, fail(400, "VALIDATION", "unknown batch operation: "+op.Op)
		}
		if op.Op == "put" {
			if err := database.ValidateKVValue(op.Value); err != nil {
				return nil, valErr(err)
			}
			if err := database.ValidateKVTTL(op.TTLSeconds); err != nil {
				return nil, valErr(err)
			}
		}
		dbOps[i] = database.KVBatchOp{Op: op.Op, Key: op.Key, Value: []byte(op.Value), TTLSeconds: op.TTLSeconds}
	}
	results, err := h.DB.KVBatchContext(ctx, fn, dbOps)
	if err != nil {
		return nil, fail(503, "KV_UNAVAILABLE", "kv batch failed: "+err.Error())
	}
	wire := make([]batchResult, len(results))
	for i, r := range results {
		ent := batchResult{Op: r.Op, Key: r.Key, Found: r.Found, Err: r.Err}
		if r.Value != nil {
			ent.Value = json.RawMessage(r.Value)
		}
		if r.ExpiresAt != nil {
			s := r.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
			ent.ExpiresAt = &s
		}
		wire[i] = ent
	}
	total := 0
	for _, w := range wire {
		total += len(w.Value)
	}
	if total > maxBatchReplyBytes {
		return nil, fail(413, "RESULT_TOO_LARGE", "kv.batch results exceed the reply size limit; fetch fewer or smaller keys per batch")
	}
	return map[string]any{"results": wire}, nil
}

func (h *Host) kvIncr(ctx context.Context, fn string, args json.RawMessage) (any, *sandbox.SDKError) {
	a := incrArgs{Delta: 1}
	if e := decodeArgs(args, limitSmall, &a); e != nil {
		return nil, e
	}
	if err := database.ValidateKVKey(a.Key); err != nil {
		return nil, valErr(err)
	}
	if err := database.ValidateKVTTL(a.TTLSeconds); err != nil {
		return nil, valErr(err)
	}
	next, err := h.DB.KVIncrContext(ctx, fn, a.Key, a.Delta, a.TTLSeconds)
	if err != nil {
		if strings.Contains(err.Error(), "not an integer") {
			return nil, fail(409, "KV_INCR_FAILED", err.Error())
		}
		return nil, fail(500, "INTERNAL", err.Error())
	}
	return map[string]any{"value": next}, nil
}

func (h *Host) kvCAS(ctx context.Context, fn string, args json.RawMessage) (any, *sandbox.SDKError) {
	var a casArgs
	if e := decodeArgs(args, limitValue, &a); e != nil {
		return nil, e
	}
	if err := database.ValidateKVKey(a.Key); err != nil {
		return nil, valErr(err)
	}
	if len(a.New) == 0 {
		return nil, fail(400, "VALIDATION", "new value is required")
	}
	if err := database.ValidateKVValue(a.New); err != nil {
		return nil, valErr(err)
	}
	if err := database.ValidateKVTTL(a.TTLSeconds); err != nil {
		return nil, valErr(err)
	}
	var expected []byte
	if a.Expected != nil {
		expected = []byte(*a.Expected)
		if err := database.ValidateKVValue(expected); err != nil {
			return nil, fail(400, "VALIDATION", "expected value: "+err.Error())
		}
	}
	ok, current, err := h.DB.KVCASContext(ctx, fn, a.Key, expected, []byte(a.New), a.TTLSeconds)
	if err != nil {
		return nil, fail(500, "INTERNAL", "kv cas failed: "+err.Error())
	}
	// A failed precondition is a normal 200 {"ok":false,"current":...}, exactly
	// as the HTTP handler replies; it is not an error reply.
	resp := map[string]any{"ok": ok}
	if !ok && current != nil {
		resp["current"] = json.RawMessage(current)
	}
	return resp, nil
}
