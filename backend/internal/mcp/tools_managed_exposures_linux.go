//go:build linux

package mcp

import (
	"context"
	"strconv"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ManagedRouteRow is one inspected custom-route mapping. Published means the
// mapping is dispatchable; a raw or restored row is listed with published=false.
type ManagedRouteRow struct {
	Path       string    `json:"path"`
	FunctionID string    `json:"function_id"`
	Methods    string    `json:"methods"`
	CreatedAt  time.Time `json:"created_at"`
	Version    string    `json:"version" jsonschema:"exposure CAS version as a decimal string"`
	Published  bool      `json:"published"`
}

type ManagedListRoutesOutput struct {
	Routes    []ManagedRouteRow `json:"routes"`
	Truncated bool              `json:"truncated"`
}

type ManagedListInboundInput struct {
	FunctionID string `json:"function_id" jsonschema:"exact lowercase function UUID; names are never resolved"`
}

// ManagedInboundRow never carries the signing secret.
type ManagedInboundRow struct {
	ID              string `json:"id"`
	FunctionID      string `json:"function_id"`
	Name            string `json:"name"`
	SecretPreview   string `json:"secret_preview"`
	SignatureHeader string `json:"signature_header"`
	SignatureFormat string `json:"signature_format"`
	Active          bool   `json:"active"`
	TriggerURL      string `json:"trigger_url"`
	CreatedAt       string `json:"created_at"`
	Version         string `json:"version" jsonschema:"exposure CAS version as a decimal string"`
	Published       bool   `json:"published"`
}

type ManagedListInboundOutput struct {
	InboundWebhooks []ManagedInboundRow          `json:"inbound_webhooks"`
	Expected        map[string]map[string]string `json:"expected" jsonschema:"owner UUID to decimal-string policy_revision and authority_epoch"`
	Truncated       bool                         `json:"truncated"`
}

// registerManagedExposureTools adds custom-route and inbound-webhook tools to the
// managed catalog only. Every operation is the shared workloadtrigger.Service;
// the legacy database helpers are never reached.
func registerManagedExposureTools(rc *regCtx) {
	deps := rc.deps
	rc.group = "routes"
	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:  "list_routes",
			Title: "List Routes",
			Description: "List custom-route mappings (bounded). published=false rows are not dispatchable. Versions are decimal strings for the exact-CAS " +
				"set_route/delete_route tools; use inspect_exposure for a route's owner policy_revision/authority_epoch expectations.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ManagedListRoutesOutput, error) {
			rows, truncated, err := deps.Triggers.ListRoutes(ctx)
			if err != nil {
				return nil, ManagedListRoutesOutput{}, err
			}
			out := ManagedListRoutesOutput{Routes: make([]ManagedRouteRow, 0, len(rows)), Truncated: truncated}
			for _, r := range rows {
				out.Routes = append(out.Routes, ManagedRouteRow{Path: r.Path, FunctionID: r.FunctionID, Methods: r.Methods,
					CreatedAt: r.CreatedAt, Version: strconv.FormatUint(r.Version, 10), Published: r.Published})
			}
			return nil, out, nil
		})

	rc.group = "inbound_webhooks"
	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:  "list_inbound_webhooks",
			Title: "List Inbound Webhooks",
			Description: "List one function's inbound webhook triggers by exact function UUID, with the owner's current policy_revision/authority_epoch " +
				"expectations. The signing secret is never returned; it is shown once by create_inbound_webhook. published=false rows are not dispatchable.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ManagedListInboundInput) (*mcpsdk.CallToolResult, ManagedListInboundOutput, error) {
			rows, expected, truncated, err := deps.Triggers.ListInbound(ctx, in.FunctionID)
			if err != nil {
				return nil, ManagedListInboundOutput{}, err
			}
			out := ManagedListInboundOutput{InboundWebhooks: make([]ManagedInboundRow, 0, len(rows)),
				Expected: map[string]map[string]string{}, Truncated: truncated}
			for id, e := range expected {
				out.Expected[id] = map[string]string{
					"policy_revision": strconv.FormatUint(e.PolicyRevision, 10),
					"authority_epoch": strconv.FormatUint(e.AuthorityEpoch, 10),
				}
			}
			for i := range rows {
				v := toInboundWebhookView(&rows[i].InboundWebhook)
				out.InboundWebhooks = append(out.InboundWebhooks, ManagedInboundRow{ID: v.ID, FunctionID: v.FunctionID, Name: v.Name,
					SecretPreview: v.SecretPreview, SignatureHeader: v.SignatureHeader, SignatureFormat: v.SignatureFormat,
					Active: v.Active, TriggerURL: v.TriggerURL, CreatedAt: v.CreatedAt,
					Version: strconv.FormatUint(rows[i].Version, 10), Published: rows[i].Published})
			}
			return nil, out, nil
		})

	// inspect_exposure (get), set_route, delete_route and the inbound mutations
	// reuse the shared strict adapter; WRITE or ADMIN is advertised, the service
	// enforces live ADMIN where policy or review state requires it.
	rc.group = "routes"
	registerExposureTools(rc, true)
	rc.group = "inbound_webhooks"
	registerExposureTools(rc, false)
}
