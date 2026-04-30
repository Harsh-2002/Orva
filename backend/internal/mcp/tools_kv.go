package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"

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
	FunctionID string `json:"function_id" jsonschema:"function id (fn_...) or name owning this key"`
	Key        string `json:"key"`
}
type KVGetOutput struct {
	Found bool   `json:"found"`
	Entry KVView `json:"entry,omitempty"`
}

// KVPutInput uses the same KVValue envelope as the output. Agents pick
// exactly one of the typed fields based on the JSON kind they want to
// store. Slightly more verbose than `value: any` but Zod-strict
// validators accept the schema; bare `any` would emit JSON-Schema
// `true` and crash MCP clients on tools/list parsing.
type KVPutInput struct {
	FunctionID string  `json:"function_id" jsonschema:"function id (fn_...) or name owning this key"`
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
}
type KVListOutput struct {
	Entries []KVView `json:"entries"`
}

func registerKVTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "kv_get",
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
				return nil, KVGetOutput{Found: true, Entry: toKVView(entry)}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "kv_put",
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
				Description: "List a function's KV keys, optionally filtered by prefix. Useful for inspecting what state a function has accumulated. Expired keys are excluded.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in KVListInput) (*mcpsdk.CallToolResult, KVListOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, KVListOutput{}, err
				}
				rows, err := deps.DB.KVList(fn.ID, in.Prefix, in.Limit)
				if err != nil {
					return nil, KVListOutput{}, err
				}
				out := KVListOutput{Entries: make([]KVView, 0, len(rows))}
				for _, e := range rows {
					out.Entries = append(out.Entries, toKVView(e))
				}
				return nil, out, nil
			},
		)
	})
}
