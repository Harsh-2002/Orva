package mcp

import (
	"context"
	"encoding/json"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type configurationArguments struct {
	ID         string
	Request    json.RawMessage
	Confirm    bool
	hasConfirm bool
}

func (*configurationArguments) UseStrictRawJSON() {}
func (a *configurationArguments) UnmarshalJSON(raw []byte) error {
	root, err := workloadpolicy.StrictObject(raw)
	if err != nil || len(root) < 2 || len(root) > 3 || root["id"] == nil || root["request"] == nil {
		return database.ErrWorkloadArtifactInvalid
	}
	for key := range root {
		if key != "id" && key != "request" && key != "confirm" {
			return database.ErrWorkloadArtifactInvalid
		}
	}
	if string(root["id"]) == "null" || json.Unmarshal(root["id"], &a.ID) != nil {
		return database.ErrWorkloadArtifactInvalid
	}
	if root["confirm"] != nil && (string(root["confirm"]) == "null" || json.Unmarshal(root["confirm"], &a.Confirm) != nil) {
		return database.ErrWorkloadArtifactInvalid
	}
	a.hasConfirm = root["confirm"] != nil
	if _, err := workloadpolicy.StrictObject(root["request"]); err != nil {
		return database.ErrWorkloadArtifactInvalid
	}
	a.Request = append(json.RawMessage(nil), root["request"]...)
	return nil
}

func registerConfigurationTools(rc *regCtx) {
	if rc.deps.Configuration == nil {
		return
	}
	rc.group = "configuration"
	destructiveRead := false
	regAddTool(rc, permRead, &mcpsdk.Tool{Name: "inspect_workload_configuration", Description: "READ-only: inspect exact configuration/generation/policy CAS and the actual current Factory digests. Returns counts, not environment or secret values. Inspection is not authority.",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "request"}, "properties": map[string]any{"id": map[string]any{"type": "string"}, "request": workloadconfig.RequestSchema("inspect")}},
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &destructiveRead}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in configurationArguments) (*mcpsdk.CallToolResult, any, error) {
			if in.hasConfirm {
				return nil, nil, database.ErrWorkloadArtifactInvalid
			}
			out, err := rc.deps.Configuration.Execute(ctx, "inspect", in.ID, in.Request)
			return nil, out, err
		})
	if !rc.perms.Has(permWrite) && !rc.perms.Has(permAdmin) {
		return
	}
	for _, item := range []struct {
		name, op, description string
		destructive           bool
	}{
		{"publish_workload_environment", "environment", "Publish exact-CAS environment changes as review-required DENY. Privileged owners require current ADMIN; empty owners allow WRITE. Does not approve or install a new generation.", false},
		{"publish_workload_settings", "settings", "Publish exact execution settings and environment as review-required DENY. No code/runtime changes or implicit grant approval.", false},
		{"publish_workload_secret", "secret_upsert", "Encrypt and publish a secret with generation and authority invalidation. Old injected secrets cannot be recalled. New Factory and explicit review are separate.", false},
		{"delete_workload_secret", "secret_delete", "Remove a secret with exact-CAS generation/authority invalidation. Requires confirm:true. Does not erase old workers' memory.", true},
		{"install_workload_configuration", "install", "ADMIN-only: install a fresh sealed Factory from the exact durable configuration/secret snapshot. No implicit approval; explicitly review its NEW current artifact afterward.", false},
	} {
		perm := permWrite
		if rc.perms.Has(permAdmin) || item.op == "install" {
			perm = permAdmin
		}
		destructive := item.destructive
		schema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "request"}, "properties": map[string]any{
			"id":      map[string]any{"type": "string", "description": "Exact function UUID."},
			"request": workloadconfig.RequestSchema(item.op),
			"confirm": map[string]any{"type": "boolean"},
		}}
		if destructive {
			schema["required"] = []string{"id", "request", "confirm"}
		}
		regAddTool(rc, perm, &mcpsdk.Tool{Name: item.name, Description: item.description, InputSchema: schema, Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: &destructive}},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in configurationArguments) (*mcpsdk.CallToolResult, any, error) {
				if destructive && !in.Confirm {
					return nil, nil, database.ErrOperatorDenied
				}
				out, err := rc.deps.Configuration.Execute(ctx, item.op, in.ID, in.Request)
				return nil, out, err
			})
	}
}
