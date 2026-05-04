package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// allowedWebhookEvents is the catalog the agent can subscribe to.
// Mirrors handlers/webhooks.go — keep in sync if the system event
// surface grows. "*" matches any event.
var allowedWebhookEvents = map[string]struct{}{
	"*":                    {},
	"deployment.succeeded": {},
	"deployment.failed":    {},
	"function.created":     {},
	"function.updated":     {},
	"function.deleted":     {},
	"execution.error":      {},
	"cron.failed":          {},
	"job.succeeded":        {},
	"job.failed":           {},
}

// SubscriptionView is the agent-facing projection. The plaintext
// secret is only ever returned ONCE on create; afterward only the
// preview is surfaced.
type SubscriptionView struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	SecretPreview  string   `json:"secret_preview"`
	Events         []string `json:"events"`
	Enabled        bool     `json:"enabled"`
	LastDeliveryAt string   `json:"last_delivery_at,omitempty"`
	LastStatus     string   `json:"last_status,omitempty"`
	LastError      string   `json:"last_error,omitempty"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

func toSubscriptionView(s *database.EventSubscription) SubscriptionView {
	v := SubscriptionView{
		ID:            s.ID,
		Name:          s.Name,
		URL:           s.URL,
		SecretPreview: s.SecretPreview,
		Events:        s.Events,
		Enabled:       s.Enabled,
		LastStatus:    s.LastStatus,
		LastError:     s.LastError,
		CreatedAt:     s.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     s.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if s.LastDeliveryAt != nil {
		v.LastDeliveryAt = s.LastDeliveryAt.UTC().Format(time.RFC3339)
	}
	return v
}

type DeliveryView struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscription_id"`
	EventName      string `json:"event_name"`
	Status         string `json:"status"`
	Attempts       int    `json:"attempts"`
	MaxAttempts    int    `json:"max_attempts"`
	ResponseStatus int    `json:"response_status,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	ScheduledAt    string `json:"scheduled_at"`
	StartedAt      string `json:"started_at,omitempty"`
	FinishedAt     string `json:"finished_at,omitempty"`
	CreatedAt      string `json:"created_at"`
}

func toDeliveryView(d *database.WebhookDelivery) DeliveryView {
	v := DeliveryView{
		ID:             d.ID,
		SubscriptionID: d.SubscriptionID,
		EventName:      d.EventName,
		Status:         d.Status,
		Attempts:       d.Attempts,
		MaxAttempts:    d.MaxAttempts,
		ResponseStatus: d.ResponseStatus,
		LastError:      d.LastError,
		ScheduledAt:    d.ScheduledAt.UTC().Format(time.RFC3339),
		CreatedAt:      d.CreatedAt.UTC().Format(time.RFC3339),
	}
	if d.StartedAt != nil {
		v.StartedAt = d.StartedAt.UTC().Format(time.RFC3339)
	}
	if d.FinishedAt != nil {
		v.FinishedAt = d.FinishedAt.UTC().Format(time.RFC3339)
	}
	return v
}

type ListWebhooksOutput struct {
	Subscriptions []SubscriptionView `json:"subscriptions"`
}

type CreateWebhookInput struct {
	Name    string   `json:"name"`
	URL     string   `json:"url" jsonschema:"http(s) URL the receiver listens on"`
	Events  []string `json:"events,omitempty" jsonschema:"event names from the catalog (deployment.succeeded, deployment.failed, function.created/updated/deleted, execution.error, cron.failed, job.succeeded/failed) or [\"*\"] for all"`
	Enabled *bool    `json:"enabled,omitempty"`
}
type CreateWebhookOutput struct {
	Subscription SubscriptionView `json:"subscription"`
	Secret       string           `json:"secret" jsonschema:"plaintext HMAC secret — shown once. Configure on the receiver to verify X-Orva-Signature."`
}

type UpdateWebhookInput struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	URL     string   `json:"url,omitempty"`
	Events  []string `json:"events,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

type WebhookIDInput struct {
	ID string `json:"id"`
}
type WebhookDeleteInput struct {
	ID      string `json:"id"`
	Confirm bool   `json:"confirm"`
}

type WebhookOpOutput struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
}

type ListDeliveriesInput struct {
	ID    string `json:"id"   jsonschema:"subscription id (sub_...)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max rows; default 100"`
}
type ListDeliveriesOutput struct {
	Deliveries []DeliveryView `json:"deliveries"`
}

func registerWebhookTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_webhooks",
				Title:        "List Webhooks",
				Description: "List every operator-configured webhook subscription. The plaintext secret is never returned (only the preview); use create_webhook to mint a new one if you've lost it.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ListWebhooksOutput, error) {
				rows, err := deps.DB.ListEventSubscriptions()
				if err != nil {
					return nil, ListWebhooksOutput{}, err
				}
				out := ListWebhooksOutput{Subscriptions: make([]SubscriptionView, 0, len(rows))}
				for _, r := range rows {
					out.Subscriptions = append(out.Subscriptions, toSubscriptionView(r))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "create_webhook",
				Title:        "Create Webhook",
				Description: "Create a new webhook subscription. Returns the subscription record AND the plaintext HMAC secret — the secret is shown ONLY once; capture it now and configure your receiver to verify X-Orva-Signature.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in CreateWebhookInput) (*mcpsdk.CallToolResult, CreateWebhookOutput, error) {
				name := strings.TrimSpace(in.Name)
				if name == "" {
					return nil, CreateWebhookOutput{}, errors.New("name is required")
				}
				if err := validateURL(in.URL); err != nil {
					return nil, CreateWebhookOutput{}, err
				}
				events := normalizeAgentEvents(in.Events)
				if err := validateAgentEvents(events); err != nil {
					return nil, CreateWebhookOutput{}, err
				}
				enabled := true
				if in.Enabled != nil {
					enabled = *in.Enabled
				}
				secret := database.NewWebhookSecret()
				sub := &database.EventSubscription{
					Name:    name,
					URL:     strings.TrimSpace(in.URL),
					Secret:  secret,
					Events:  events,
					Enabled: enabled,
				}
				if err := deps.DB.InsertEventSubscription(sub); err != nil {
					return nil, CreateWebhookOutput{}, err
				}
				sub.SecretPreview = secret[:8] + "…"
				return nil, CreateWebhookOutput{Subscription: toSubscriptionView(sub), Secret: secret}, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "update_webhook",
				Title:        "Update Webhook",
				Description: "Edit an existing subscription. Any of name / url / events / enabled may be supplied; omitted fields keep their previous values. Secret cannot be rotated through this path — delete and re-create to rotate.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in UpdateWebhookInput) (*mcpsdk.CallToolResult, SubscriptionView, error) {
				sub, err := deps.DB.GetEventSubscription(in.ID)
				if err != nil {
					return nil, SubscriptionView{}, errors.New("webhook not found")
				}
				if name := strings.TrimSpace(in.Name); name != "" {
					sub.Name = name
				}
				if u := strings.TrimSpace(in.URL); u != "" {
					if err := validateURL(u); err != nil {
						return nil, SubscriptionView{}, err
					}
					sub.URL = u
				}
				if in.Events != nil {
					events := normalizeAgentEvents(in.Events)
					if err := validateAgentEvents(events); err != nil {
						return nil, SubscriptionView{}, err
					}
					sub.Events = events
				}
				if in.Enabled != nil {
					sub.Enabled = *in.Enabled
				}
				if err := deps.DB.UpdateEventSubscription(sub); err != nil {
					return nil, SubscriptionView{}, err
				}
				return nil, toSubscriptionView(sub), nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_webhook",
				Title:        "Delete Webhook",
				Description: "Remove a webhook subscription. Pass confirm=true. All in-flight deliveries are removed via FK cascade.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in WebhookDeleteInput) (*mcpsdk.CallToolResult, WebhookOpOutput, error) {
				if !in.Confirm {
					return nil, WebhookOpOutput{}, errors.New("delete refused: pass confirm=true")
				}
				if _, err := deps.DB.GetEventSubscription(in.ID); err != nil {
					return nil, WebhookOpOutput{}, errors.New("webhook not found")
				}
				if err := deps.DB.DeleteEventSubscription(in.ID); err != nil {
					return nil, WebhookOpOutput{}, err
				}
				return nil, WebhookOpOutput{ID: in.ID, Status: "deleted"}, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "test_webhook",
				Title:        "Test Webhook",
				Description: "Queue a synthetic 'webhook.test' delivery against a subscription's URL so you can validate signature handling without waiting for a real event. Picked up on the next 5s scheduler tick.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in WebhookIDInput) (*mcpsdk.CallToolResult, WebhookOpOutput, error) {
				sub, err := deps.DB.GetEventSubscription(in.ID)
				if err != nil {
					return nil, WebhookOpOutput{}, errors.New("webhook not found")
				}
				envelope := map[string]any{
					"id":       "evt_test_" + database.NewSubscriptionID()[4:],
					"type":     "webhook.test",
					"fired_at": time.Now().UTC().Format(time.RFC3339Nano),
					"data": map[string]any{
						"subscription_id": sub.ID,
						"name":            sub.Name,
						"message":         "synthetic test event from MCP test_webhook",
					},
				}
				body, _ := json.Marshal(envelope)
				d := &database.WebhookDelivery{
					SubscriptionID: sub.ID,
					EventName:      "webhook.test",
					Payload:        body,
					Status:         "pending",
					MaxAttempts:    1,
				}
				if err := deps.DB.InsertDelivery(d); err != nil {
					return nil, WebhookOpOutput{}, err
				}
				return nil, WebhookOpOutput{ID: d.ID, Status: "queued"}, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_webhook_deliveries",
				Title:        "List Webhook Deliveries",
				Description: "List recent deliveries for a webhook subscription. Useful for diagnosing stuck retries or confirming a fire happened.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListDeliveriesInput) (*mcpsdk.CallToolResult, ListDeliveriesOutput, error) {
				rows, err := deps.DB.ListDeliveriesForSubscription(in.ID, in.Limit)
				if err != nil {
					return nil, ListDeliveriesOutput{}, err
				}
				out := ListDeliveriesOutput{Deliveries: make([]DeliveryView, 0, len(rows))}
				for _, d := range rows {
					out.Deliveries = append(out.Deliveries, toDeliveryView(d))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "retry_webhook_delivery",
				Title:        "Retry Webhook Delivery",
				Description: "Reset a terminal (failed) delivery back to pending so the scheduler will re-attempt it. attempts is reset to 0.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in WebhookIDInput) (*mcpsdk.CallToolResult, WebhookOpOutput, error) {
				if _, err := deps.DB.GetDelivery(in.ID); err != nil {
					return nil, WebhookOpOutput{}, errors.New("delivery not found")
				}
				if err := deps.DB.RetryDelivery(in.ID); err != nil {
					return nil, WebhookOpOutput{}, err
				}
				return nil, WebhookOpOutput{ID: in.ID, Status: "pending"}, nil
			},
		)
	})
}

// ── helpers ────────────────────────────────────────────────────────

func normalizeAgentEvents(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	if len(out) == 0 {
		out = []string{"*"}
	}
	return out
}

func validateAgentEvents(events []string) error {
	for _, e := range events {
		if _, ok := allowedWebhookEvents[e]; !ok {
			return errors.New("unknown event name: " + e)
		}
	}
	return nil
}

func validateURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(s)
	if err != nil {
		return errors.New("invalid url: " + err.Error())
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must be http:// or https://")
	}
	if u.Host == "" {
		return errors.New("url must include a host")
	}
	return nil
}
