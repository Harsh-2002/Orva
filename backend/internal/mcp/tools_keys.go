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
	Name          string   `json:"name"`
	Permissions   []string `json:"permissions,omitempty" jsonschema:"subset of [invoke, read, write, admin] — defaults to all four"`
	ExpiresInDays int      `json:"expires_in_days,omitempty"`
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

var defaultKeyPerms = []string{"invoke", "read", "write", "admin"}

func registerKeyTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_api_keys",
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
				Description: "Mint a new API key. The plaintext value is returned ONLY in this response; the server keeps a SHA256 hash and forgets the plaintext. Marked destructive because issuing a key with admin permissions is high-blast-radius — confirm with the user what permissions to grant before calling.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in CreateAPIKeyInput) (*mcpsdk.CallToolResult, CreateAPIKeyOutput, error) {
				if strings.TrimSpace(in.Name) == "" {
					return nil, CreateAPIKeyOutput{}, errors.New("name is required")
				}
				perms := in.Permissions
				if len(perms) == 0 {
					perms = defaultKeyPerms
				}
				rawKey := make([]byte, 32)
				if _, err := rand.Read(rawKey); err != nil {
					return nil, CreateAPIKeyOutput{}, err
				}
				plaintext := "orva_" + hex.EncodeToString(rawKey)
				hash := sha256.Sum256([]byte(plaintext))

				idBytes := make([]byte, 8)
				if _, err := rand.Read(idBytes); err != nil {
					return nil, CreateAPIKeyOutput{}, err
				}
				keyID := "key_" + hex.EncodeToString(idBytes)
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
