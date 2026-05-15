package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// KVView surfaces a single KV entry. Value is wrapped in a tiny
// envelope ({json: <decoded>, raw: "<string>"}) rather than a bare
// `any` — the latter compiles to JSON-Schema `true` in the Go MCP
// SDK, and Zod v4 strict validators in MCP clients reject that as
// "not a valid object schema". The wrapper keeps both decoded and
// raw forms available so agents can reason structurally OR
// reconstruct the original bytes for re-hashing/HMAC use cases.
type KVView struct {
	FunctionID string  `json:"function_id"`
	Key        string  `json:"key"`
	Value      KVValue `json:"value"`
	ExpiresAt  string  `json:"expires_at,omitempty"`
	UpdatedAt  string  `json:"updated_at"`
}

// KVValue is the wire envelope for a stored value. Exactly one of the
// typed fields is populated based on the JSON kind.
type KVValue struct {
	Type   string         `json:"type"             jsonschema:"one of: object, array, string, number, boolean, null"`
	Object map[string]any `json:"object,omitempty"`
	Array  []any          `json:"array,omitempty"`
	String string         `json:"string,omitempty"`
	Number float64        `json:"number,omitempty"`
	Bool   bool           `json:"bool,omitempty"`
}

// encodeKVValue serializes a KVValue envelope back into the canonical
// JSON for storage. Used on the kv_put path. Returns an error if the
// envelope's `type` doesn't match a populated field.
func encodeKVValue(v KVValue) ([]byte, error) {
	switch v.Type {
	case "object":
		if v.Object == nil {
			v.Object = map[string]any{}
		}
		return json.Marshal(v.Object)
	case "array":
		if v.Array == nil {
			v.Array = []any{}
		}
		return json.Marshal(v.Array)
	case "string":
		return json.Marshal(v.String)
	case "number":
		return json.Marshal(v.Number)
	case "boolean":
		return json.Marshal(v.Bool)
	case "null":
		return []byte("null"), nil
	case "":
		return nil, errors.New("value.type is required (object | array | string | number | boolean | null)")
	}
	return nil, errors.New("unknown value.type: " + v.Type)
}

func decodeKVValue(raw []byte) KVValue {
	if len(raw) == 0 {
		return KVValue{Type: "null"}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return KVValue{Type: "string", String: string(raw)}
	}
	switch x := v.(type) {
	case map[string]any:
		return KVValue{Type: "object", Object: x}
	case []any:
		return KVValue{Type: "array", Array: x}
	case string:
		return KVValue{Type: "string", String: x}
	case float64:
		return KVValue{Type: "number", Number: x}
	case bool:
		return KVValue{Type: "boolean", Bool: x}
	case nil:
		return KVValue{Type: "null"}
	}
	return KVValue{Type: "string", String: string(raw)}
}

func toKVView(e *database.KVEntry) KVView {
	v := KVView{
		FunctionID: e.FunctionID,
		Key:        e.Key,
		UpdatedAt:  e.UpdatedAt.UTC().Format(time.RFC3339),
		Value:      decodeKVValue(e.Value),
	}
	if e.ExpiresAt != nil {
		v.ExpiresAt = e.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return v
}

type KVGetInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name owning this key"`
	Key        string `json:"key"`
}
type KVGetOutput struct {
	Found bool    `json:"found"`
	Entry *KVView `json:"entry,omitempty"`
}

// KVPutInput uses the same KVValue envelope as the output. Agents pick
// exactly one of the typed fields based on the JSON kind they want to
// store. Slightly more verbose than `value: any` but Zod-strict
// validators accept the schema; bare `any` would emit JSON-Schema
// `true` and crash MCP clients on tools/list parsing.
type KVPutInput struct {
	FunctionID string  `json:"function_id" jsonschema:"function id (UUID) or name owning this key"`
	Key        string  `json:"key"`
	Value      KVValue `json:"value" jsonschema:"the value to store; populate one typed field matching the type field"`
	TTLSeconds int     `json:"ttl_seconds,omitempty" jsonschema:"0 (default) = no expiry; positive = expire after that many seconds"`
}
type KVPutOutput struct {
	Key        string `json:"key"`
	TTLSeconds int    `json:"ttl_seconds"`
}

type KVDeleteInput struct {
	FunctionID string `json:"function_id"`
	Key        string `json:"key"`
}
type KVDeleteOutput struct {
	Status string `json:"status"`
	Key    string `json:"key"`
}

type KVListInput struct {
	FunctionID string `json:"function_id"`
	Prefix     string `json:"prefix,omitempty" jsonschema:"only return keys starting with this prefix"`
	Limit      int    `json:"limit,omitempty"  jsonschema:"max keys to return; defaults to 100, capped at 1000"`
	Cursor     string `json:"cursor,omitempty" jsonschema:"resume after this key (last key from the previous page's next_cursor); empty starts a new walk"`
}
type KVListOutput struct {
	Entries    []KVView `json:"entries"`
	NextCursor string   `json:"next_cursor,omitempty" jsonschema:"pass back as cursor in the next call when more rows remain; empty when the walk is complete"`
}

// KVIncrInput / KVIncrOutput drive the atomic counter primitive.
type KVIncrInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name owning this key"`
	Key        string `json:"key"`
	Delta      int64  `json:"delta,omitempty" jsonschema:"increment amount; default 1; negative decrements"`
	TTLSeconds int    `json:"ttl_seconds,omitempty" jsonschema:"0 = preserve existing TTL; positive = refresh expiry"`
}
type KVIncrOutput struct {
	Value int64 `json:"value"`
}

// KVCASInput / KVCASOutput express atomic compare-and-swap. Use a null
// Expected to assert "key must not currently exist" (insert-if-absent).
type KVCASInput struct {
	FunctionID string  `json:"function_id"`
	Key        string  `json:"key"`
	Expected   KVValue `json:"expected" jsonschema:"current value to match before swapping; populate exactly one typed field"`
	New        KVValue `json:"new" jsonschema:"value to install when Expected matches"`
	TTLSeconds int     `json:"ttl_seconds,omitempty"`
}
type KVCASOutput struct {
	OK      bool      `json:"ok"`
	Current *KVValue  `json:"current,omitempty" jsonschema:"on !ok, the value currently stored so callers can retry with a fresh expectation"`
}

func registerKVTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "kv_get",
				Title:        "KV Get",
				Description: "Read a value from a function's per-namespace KV store. Returns found=false if the key is missing or has expired (TTL elapsed).",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in KVGetInput) (*mcpsdk.CallToolResult, KVGetOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, KVGetOutput{}, err
				}
				key := strings.TrimSpace(in.Key)
				if key == "" {
					return nil, KVGetOutput{}, errors.New("key is required")
				}
				entry, err := deps.DB.KVGet(fn.ID, key)
				if errors.Is(err, database.ErrKVNotFound) {
					return nil, KVGetOutput{Found: false}, nil
				}
				if err != nil {
					return nil, KVGetOutput{}, err
				}
				view := toKVView(entry)
				return nil, KVGetOutput{Found: true, Entry: &view}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "kv_put",
				Title:        "KV Put",
				Description: "Write a value to a function's KV store. value can be any JSON-serializable type; it's stored as JSON and returned by kv_get with the same shape. Optional ttl_seconds expires the key automatically.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in KVPutInput) (*mcpsdk.CallToolResult, KVPutOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, KVPutOutput{}, err
				}
				key := strings.TrimSpace(in.Key)
				if key == "" {
					return nil, KVPutOutput{}, errors.New("key is required")
				}
				body, err := encodeKVValue(in.Value)
				if err != nil {
					return nil, KVPutOutput{}, err
				}
				if err := deps.DB.KVPut(fn.ID, key, body, in.TTLSeconds); err != nil {
					return nil, KVPutOutput{}, err
				}
				return nil, KVPutOutput{Key: key, TTLSeconds: in.TTLSeconds}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "kv_delete",
				Title:        "KV Delete",
				Description: "Remove a single key from a function's KV store. Idempotent — returns ok even if the key never existed.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in KVDeleteInput) (*mcpsdk.CallToolResult, KVDeleteOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, KVDeleteOutput{}, err
				}
				if err := deps.DB.KVDelete(fn.ID, in.Key); err != nil {
					return nil, KVDeleteOutput{}, err
				}
				return nil, KVDeleteOutput{Status: "deleted", Key: in.Key}, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "kv_list",
				Title:        "KV List",
				Description: "List a function's KV keys, optionally filtered by prefix. Useful for inspecting what state a function has accumulated. Expired keys are excluded.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in KVListInput) (*mcpsdk.CallToolResult, KVListOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, KVListOutput{}, err
				}
				page, err := deps.DB.KVListWithCursor(fn.ID, in.Prefix, in.Cursor, in.Limit)
				if err != nil {
					return nil, KVListOutput{}, err
				}
				out := KVListOutput{
					Entries:    make([]KVView, 0, len(page.Entries)),
					NextCursor: page.NextCursor,
				}
				for _, e := range page.Entries {
					out.Entries = append(out.Entries, toKVView(e))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "kv_incr",
				Title:       "KV Increment",
				Description: "Atomically increment an integer counter. Missing keys are treated as 0; pass a negative delta to decrement. Use this instead of read+put when multiple writers can update the same counter concurrently.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in KVIncrInput) (*mcpsdk.CallToolResult, KVIncrOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, KVIncrOutput{}, err
				}
				key := strings.TrimSpace(in.Key)
				if key == "" {
					return nil, KVIncrOutput{}, errors.New("key is required")
				}
				delta := in.Delta
				if delta == 0 {
					delta = 1
				}
				next, err := deps.DB.KVIncr(fn.ID, key, delta, in.TTLSeconds)
				if err != nil {
					return nil, KVIncrOutput{}, err
				}
				return nil, KVIncrOutput{Value: next}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "kv_cas",
				Title:       "KV Compare-and-swap",
				Description: "Atomically swap a key's value only if the current value matches Expected. Useful for safe read-modify-write loops where multiple writers could otherwise overwrite each other. Returns ok=false plus the current value when the precondition fails.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in KVCASInput) (*mcpsdk.CallToolResult, KVCASOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, KVCASOutput{}, err
				}
				key := strings.TrimSpace(in.Key)
				if key == "" {
					return nil, KVCASOutput{}, errors.New("key is required")
				}
				expected, err := encodeKVValue(in.Expected)
				if err != nil {
					return nil, KVCASOutput{}, err
				}
				next, err := encodeKVValue(in.New)
				if err != nil {
					return nil, KVCASOutput{}, err
				}
				ok, current, err := deps.DB.KVCAS(fn.ID, key, expected, next, in.TTLSeconds)
				if err != nil {
					return nil, KVCASOutput{}, err
				}
				out := KVCASOutput{OK: ok}
				if !ok && current != nil {
					// Surface the raw bytes back through KVValue so the
					// schema stays self-describing for MCP clients.
					decoded := decodeKVValue(current)
					out.Current = &decoded
				}
				return nil, out, nil
			},
		)
	})
}
