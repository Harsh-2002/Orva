package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SECURITY POSTURE — read this before changing anything.
//
// API keys are SHA256-hashed at rest. The plaintext is shown ONCE in the
// response of create_api_key and never again. There is no read tool for
// existing keys — list_api_keys returns only metadata (id, prefix, name,
// permissions, timestamps).

type APIKeyView struct {
	ID          string     `json:"id"`
	Prefix      string     `json:"prefix"`
	Name        string     `json:"name"`
	Permissions []string   `json:"permissions"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

func toAPIKeyView(k *database.APIKey) APIKeyView {
	return APIKeyView{
		ID: k.ID, Prefix: k.Prefix, Name: k.Name,
		Permissions: k.PermissionsList(),
		CreatedAt:   k.CreatedAt, LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt,
	}
}

type ListAPIKeysOutput struct {
	Keys []APIKeyView `json:"keys"`
}

type CreateAPIKeyInput struct {
	Name          string   `json:"name" jsonschema:"human-readable label for this key (e.g. 'ci-deploys', 'mobile-app-prod'). Surfaces in list_api_keys and the dashboard's API Keys page."`
	Permissions   []string `json:"permissions" jsonschema:"REQUIRED — subset of [invoke, read, write, admin]. 'invoke' = call functions; 'read' = list/get resources; 'write' = create/update/delete functions+secrets+routes etc.; 'admin' = manage API keys + system settings. Pick the smallest set the consumer actually needs (least-privilege); 'admin' should be rare. Empty / silently-defaulted to 'all four' was the previous behaviour and produced over-privileged keys."`
	ExpiresInDays int      `json:"expires_in_days" jsonschema:"REQUIRED — days until the key auto-expires. Pick from intent: short-lived CI runs ~1-7, dev/staging ~30, production rotations ~90, long-lived service accounts up to 365. Pass 0 ONLY if the user explicitly asked for a never-expiring key (rare; usually wrong)."`
}

type CreateAPIKeyOutput struct {
	ID          string     `json:"id"`
	Key         string     `json:"key" jsonschema:"plaintext token — shown ONLY in this response. Store it now; the server cannot reveal it again."`
	Prefix      string     `json:"prefix"`
	Name        string     `json:"name"`
	Permissions []string   `json:"permissions"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type DeleteAPIKeyInput struct {
	KeyID   string `json:"key_id"`
	Confirm bool   `json:"confirm"`
}

func registerKeyTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_api_keys",
				Title:        "List API Keys",
				Description: "List all API keys with their metadata (id, prefix, name, permissions, last-used, expiry). Plaintext key values are NEVER returned — they are SHA256-hashed at rest, and the only opportunity to see one is in the response of create_api_key.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ListAPIKeysOutput, error) {
				rows, err := deps.DB.ListAPIKeys()
				if err != nil {
					return nil, ListAPIKeysOutput{}, err
				}
				out := ListAPIKeysOutput{Keys: make([]APIKeyView, 0, len(rows))}
				for _, k := range rows {
					out.Keys = append(out.Keys, toAPIKeyView(k))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "create_api_key",
				Title:        "Create API Key",
				Description: "Mint a new API key. The plaintext value is returned ONLY in this response; the server keeps a SHA256 hash and forgets the plaintext. `permissions` and `expires_in_days` are REQUIRED — least-privilege scope and finite lifetime are the cheapest defenses against a leaked key. Marked destructive because issuing a key with admin permissions is high-blast-radius — confirm with the user what permissions to grant and how long it should live before calling.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in CreateAPIKeyInput) (*mcpsdk.CallToolResult, CreateAPIKeyOutput, error) {
				if strings.TrimSpace(in.Name) == "" {
					return nil, CreateAPIKeyOutput{}, errors.New("name is required (human-readable label, e.g. 'ci-deploys')")
				}
				if len(in.Permissions) == 0 {
					return nil, CreateAPIKeyOutput{}, errors.New(
						"permissions is required: pick a subset of [invoke, " +
							"read, write, admin] matching what the consumer " +
							"actually needs. Granting all four was the silent " +
							"default and produced over-privileged keys; least-" +
							"privilege is the right shape.",
					)
				}
				if in.ExpiresInDays < 0 {
					return nil, CreateAPIKeyOutput{}, errors.New("expires_in_days must be >= 0 (0 = never expires; >0 = days until expiry)")
				}
				perms := in.Permissions
				rawKey := make([]byte, 32)
				if _, err := rand.Read(rawKey); err != nil {
					return nil, CreateAPIKeyOutput{}, err
				}
				plaintext := "orva_" + hex.EncodeToString(rawKey)
				hash := sha256.Sum256([]byte(plaintext))

				keyID := ids.New()
				prefix := plaintext[:12]

				var expiresAt *time.Time
				if in.ExpiresInDays > 0 {
					t := time.Now().UTC().Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour)
					expiresAt = &t
				}

				permsJSON, _ := json.Marshal(perms)
				row := &database.APIKey{
					ID: keyID, KeyHash: hex.EncodeToString(hash[:]),
					Prefix: prefix, Name: in.Name,
					Permissions: string(permsJSON), ExpiresAt: expiresAt,
				}
				if err := deps.DB.InsertAPIKey(row); err != nil {
					return nil, CreateAPIKeyOutput{}, err
				}

				return nil, CreateAPIKeyOutput{
					ID: keyID, Key: plaintext, Prefix: prefix,
					Name: in.Name, Permissions: perms,
					ExpiresAt: expiresAt, CreatedAt: time.Now().UTC(),
				}, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_api_key",
				Title:        "Delete API Key",
				Description: "Revoke an API key by id. Pass confirm=true. Active sessions using that key fail their next request with 401.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteAPIKeyInput) (*mcpsdk.CallToolResult, DeletedOutput, error) {
				if !in.Confirm {
					return nil, DeletedOutput{}, errors.New("delete refused: pass confirm=true")
				}
				if err := deps.DB.DeleteAPIKey(in.KeyID); err != nil {
					return nil, DeletedOutput{}, err
				}
				return nil, DeletedOutput{DeletedID: in.KeyID}, nil
			},
		)
	})
}
