// Package mcp — inbound webhook trigger tools (v0.4 C2a).
//
// External services post to POST /webhook/<id> with a signed body to
// fire a function. These tools let an agent set up such triggers from
// MCP — list them, mint a new one with a fresh secret, or delete a
// stale one. The plaintext secret is returned ONCE on create; after
// that only the preview is surfaced.
package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// InboundWebhookView is the agent-facing projection. The plaintext
// Secret is intentionally omitted — only the create tool returns it.
type InboundWebhookView struct {
	ID              string `json:"id"`
	FunctionID      string `json:"function_id"`
	FunctionName    string `json:"function_name,omitempty"`
	Name            string `json:"name"`
	SecretPreview   string `json:"secret_preview"`
	SignatureHeader string `json:"signature_header"`
	SignatureFormat string `json:"signature_format"`
	Active          bool   `json:"active"`
	TriggerURL      string `json:"trigger_url"`
	CreatedAt       string `json:"created_at"`
}

func toInboundWebhookView(w *database.InboundWebhook) InboundWebhookView {
	return InboundWebhookView{
		ID:              w.ID,
		FunctionID:      w.FunctionID,
		FunctionName:    w.FunctionName,
		Name:            w.Name,
		SecretPreview:   w.SecretPreview,
		SignatureHeader: w.SignatureHeader,
		SignatureFormat: w.SignatureFormat,
		Active:          w.Active,
		TriggerURL:      "/webhook/" + w.ID,
		CreatedAt:       w.CreatedAt.UTC().Format(time.RFC3339),
	}
}

type ListInboundWebhooksInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name"`
}
type ListInboundWebhooksOutput struct {
	InboundWebhooks []InboundWebhookView `json:"inbound_webhooks"`
}

type CreateInboundWebhookInput struct {
	FunctionID      string `json:"function_id" jsonschema:"function id (UUID) or name to fire when the trigger arrives"`
	Name            string `json:"name" jsonschema:"operator-friendly label"`
	SignatureFormat string `json:"signature_format,omitempty" jsonschema:"hmac_sha256_hex (default), hmac_sha256_base64, github, stripe, slack"`
	SignatureHeader string `json:"signature_header,omitempty" jsonschema:"override the header carrying the signature; defaults are format-specific"`
}
type CreateInboundWebhookOutput struct {
	InboundWebhook InboundWebhookView `json:"inbound_webhook"`
	Secret         string             `json:"secret" jsonschema:"plaintext HMAC secret — shown ONCE. Configure on the upstream service to sign request bodies."`
}

type DeleteInboundWebhookInput struct {
	FunctionID string `json:"function_id"`
	ID         string `json:"id"`
	Confirm    bool   `json:"confirm"`
}

type InboundWebhookOpOutput struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
}

func registerInboundWebhookTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_inbound_webhooks",
				Title:        "List Inbound Webhooks",
				Description: "List inbound webhook triggers configured for a function. Each row exposes its trigger URL (/webhook/<id>) and signature format. Plaintext secrets are NEVER returned — use create_inbound_webhook to mint a new one if you've lost it.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListInboundWebhooksInput) (*mcpsdk.CallToolResult, ListInboundWebhooksOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, ListInboundWebhooksOutput{}, err
				}
				rows, err := deps.DB.ListInboundWebhooksForFunction(fn.ID)
				if err != nil {
					return nil, ListInboundWebhooksOutput{}, err
				}
				out := ListInboundWebhooksOutput{InboundWebhooks: make([]InboundWebhookView, 0, len(rows))}
				for _, r := range rows {
					r.FunctionName = fn.Name
					out.InboundWebhooks = append(out.InboundWebhooks, toInboundWebhookView(r))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "create_inbound_webhook",
				Title:        "Create Inbound Webhook",
				Description: "Create a new inbound webhook trigger for a function. Returns the trigger URL AND the plaintext HMAC secret — the secret is shown ONLY once; capture it now and configure your upstream service (GitHub, Stripe, Slack, your own backend) to sign request bodies with it.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in CreateInboundWebhookInput) (*mcpsdk.CallToolResult, CreateInboundWebhookOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, CreateInboundWebhookOutput{}, err
				}
				name := strings.TrimSpace(in.Name)
				if name == "" {
					return nil, CreateInboundWebhookOutput{}, errors.New("name is required")
				}
				format := strings.TrimSpace(in.SignatureFormat)
				if format == "" {
					format = "hmac_sha256_hex"
				}
				if _, ok := database.AllowedInboundFormats[format]; !ok {
					return nil, CreateInboundWebhookOutput{}, errors.New("unknown signature_format: must be hmac_sha256_hex, hmac_sha256_base64, github, stripe, or slack")
				}
				header := strings.TrimSpace(in.SignatureHeader)
				if header == "" {
					header = database.DefaultSignatureHeader(format)
				}
				secret := database.NewInboundWebhookSecret()
				row := &database.InboundWebhook{
					FunctionID:      fn.ID,
					Name:            name,
					Secret:          secret,
					SignatureHeader: header,
					SignatureFormat: format,
					Active:          true,
				}
				if err := deps.DB.InsertInboundWebhook(row); err != nil {
					return nil, CreateInboundWebhookOutput{}, err
				}
				row.SecretPreview = secret[:8] + "…"
				row.FunctionName = fn.Name
				return nil, CreateInboundWebhookOutput{
					InboundWebhook: toInboundWebhookView(row),
					Secret:         secret,
				}, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_inbound_webhook",
				Title:        "Delete Inbound Webhook",
				Description: "Remove an inbound webhook trigger. Pass confirm=true. The trigger URL stops accepting calls immediately; previously-signed requests will return 404.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteInboundWebhookInput) (*mcpsdk.CallToolResult, InboundWebhookOpOutput, error) {
				if !in.Confirm {
					return nil, InboundWebhookOpOutput{}, errors.New("delete refused: pass confirm=true")
				}
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, InboundWebhookOpOutput{}, err
				}
				row, err := deps.DB.GetInboundWebhook(in.ID)
				if err != nil || row.FunctionID != fn.ID {
					return nil, InboundWebhookOpOutput{}, errors.New("inbound webhook not found")
				}
				if err := deps.DB.DeleteInboundWebhook(in.ID); err != nil {
					return nil, InboundWebhookOpOutput{}, err
				}
				return nil, InboundWebhookOpOutput{ID: in.ID, Status: "deleted"}, nil
			},
		)
	})
}
