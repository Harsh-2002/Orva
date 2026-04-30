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

// KVView surfaces a single KV entry with the value parsed back into an
// object. Per-function namespacing means agents must always pass the
// function id (or name) — there is no global keyspace.
type KVView struct {
	FunctionID string `json:"function_id"`
	Key        string `json:"key"`
	Value      any    `json:"value"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

func toKVView(e *database.KVEntry) KVView {
	v := KVView{
		FunctionID: e.FunctionID,
		Key:        e.Key,
		UpdatedAt:  e.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if e.ExpiresAt != nil {
		v.ExpiresAt = e.ExpiresAt.UTC().Format(time.RFC3339)
	}
	var parsed any
	if err := json.Unmarshal(e.Value, &parsed); err == nil {
		v.Value = parsed
	} else {
		v.Value = string(e.Value)
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

type KVPutInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (fn_...) or name owning this key"`
	Key        string `json:"key"`
	Value      any    `json:"value" jsonschema:"any JSON value; arrays/objects/strings/numbers all work"`
	TTLSeconds int    `json:"ttl_seconds,omitempty" jsonschema:"0 (default) = no expiry; positive = expire after that many seconds"`
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
				if in.Value == nil {
					return nil, KVPutOutput{}, errors.New("value is required")
				}
				body, err := json.Marshal(in.Value)
				if err != nil {
					return nil, KVPutOutput{}, errors.New("value must be JSON-serializable")
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
