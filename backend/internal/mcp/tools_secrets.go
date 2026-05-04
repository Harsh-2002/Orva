package mcp

import (
	"context"
	"errors"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SECURITY POSTURE — read this before changing anything in this file.
//
// Secrets are AES-256-GCM encrypted at rest by the secrets.Manager.
// They are decrypted ONLY into the sandbox process at invocation time
// (via Manager.GetForFunction, called from the invoke path). They never
// cross any API boundary as plaintext after the initial set_secret call.
//
// There is intentionally NO get_secret tool here. There never will be.
// list_secrets returns names only; the values are unrecoverable through
// any HTTP, MCP, or UI path.

type ListSecretsInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
}

type ListSecretsOutput struct {
	FunctionID string   `json:"function_id"`
	Names      []string `json:"names"`
}

type SetSecretInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
	Key        string `json:"key" jsonschema:"environment variable name (typically SCREAMING_SNAKE_CASE)"`
	Value      string `json:"value" jsonschema:"the secret value — encrypted at rest, never returned"`
}

type SecretOpOutput struct {
	Key string `json:"key"`
}

type DeleteSecretInput struct {
	FunctionID string `json:"function_id"`
	Key        string `json:"key"`
	Confirm    bool   `json:"confirm"`
}

func registerSecretTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_secrets",
				Title:        "List Secrets",
				Description: "List the NAMES of secrets configured for a function. Values are write-only — they are encrypted at rest and decrypted only into the sandbox process at invocation time. There is no API path, MCP tool, or UI screen that can read a stored secret value.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListSecretsInput) (*mcpsdk.CallToolResult, ListSecretsOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, ListSecretsOutput{}, err
				}
				names, err := deps.Secrets.List(fn.ID)
				if err != nil {
					return nil, ListSecretsOutput{}, err
				}
				if names == nil {
					names = []string{}
				}
				return nil, ListSecretsOutput{FunctionID: fn.ID, Names: names}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "set_secret",
				Title:        "Set Secret",
				Description: "Store or update a secret for a function. Value is encrypted at rest (AES-256-GCM). Idempotent — re-setting the same key overwrites the prior value. After the call returns the value is unreadable through any API. The function's warm pool is drained so the next invoke spawns with the new value.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in SetSecretInput) (*mcpsdk.CallToolResult, SecretOpOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, SecretOpOutput{}, err
				}
				key := strings.TrimSpace(in.Key)
				if key == "" {
					return nil, SecretOpOutput{}, errors.New("key is required")
				}
				if err := deps.Secrets.Upsert(fn.ID, key, in.Value); err != nil {
					return nil, SecretOpOutput{}, err
				}
				if deps.PoolMgr != nil {
					deps.PoolMgr.RefreshForDeploy(fn.ID)
				}
				return nil, SecretOpOutput{Key: key}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_secret",
				Title:        "Delete Secret",
				Description: "Delete a secret from a function by name. Pass confirm=true. The function's warm pool is drained so the next invoke loses access immediately.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteSecretInput) (*mcpsdk.CallToolResult, SecretOpOutput, error) {
				if !in.Confirm {
					return nil, SecretOpOutput{}, errors.New("delete refused: pass confirm=true")
				}
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, SecretOpOutput{}, err
				}
				if err := deps.Secrets.Delete(fn.ID, in.Key); err != nil {
					return nil, SecretOpOutput{}, err
				}
				if deps.PoolMgr != nil {
					deps.PoolMgr.RefreshForDeploy(fn.ID)
				}
				return nil, SecretOpOutput{Key: in.Key}, nil
			},
		)
	})
}
