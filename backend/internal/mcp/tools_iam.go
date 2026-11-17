package mcp

import (
	"context"
	"encoding/json"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type iamArguments struct {
	ID      string
	Request json.RawMessage
	hasID   bool
}

func (*iamArguments) UseStrictRawJSON() {}
func (a *iamArguments) UnmarshalJSON(raw []byte) error {
	m, err := workloadpolicy.StrictObject(raw)
	if err != nil || m["request"] == nil || len(m) > 2 {
		return database.ErrIAMInvalidPolicy
	}
	for k := range m {
		if k != "id" && k != "request" {
			return database.ErrIAMInvalidPolicy
		}
	}
	if m["id"] != nil {
		a.hasID = true
		if string(m["id"]) == "null" || json.Unmarshal(m["id"], &a.ID) != nil {
			return database.ErrIAMInvalidPolicy
		}
	}
	if _, err := workloadpolicy.StrictObject(m["request"]); err != nil {
		return err
	}
	a.Request = append(json.RawMessage(nil), m["request"]...)
	return nil
}

func registerIAMTools(rc *regCtx) {
	if rc.deps.IAM == nil || !rc.perms.Has(permRead) {
		return
	}
	rc.group = "iam"
	for _, item := range []struct {
		name, op, description string
		read, destructive     bool
	}{
		{"list_iam_policies", "list", "List named policies, including terminally revoked IDs. request: optional decimal-string offset.", true, false},
		{"get_iam_policy", "get", "Inspect a named policy, pinned attachments and optional history/version. Versions never auto-propagate.", true, false},
		{"create_iam_policy", "create", "Create a named immutable policy version. request: name and strict policy. This grants nothing until explicitly attached.", false, false},
		{"update_iam_policy", "update", "Create a NEW immutable version using definition_revision and policy. Does not change existing attachment pins.", false, false},
		{"get_iam_policy_impact", "impact", "Preview exact-owner attach/detach/revoke aggregate changes. Preview is not approval; mutation rechecks CAS/artifacts.", false, false},
		{"attach_iam_policy", "attach", "Attach or explicitly roll out a pinned version to selected exact functions; supply membership_revision, version and owners with expected artifact/revision.", false, false},
		{"detach_iam_policy", "detach", "Remove only this policy contribution from selected owners. Overlapping inline or other managed grants remain. Requires confirm:true.", false, true},
		{"revoke_iam_policy", "revoke", "TERMINALLY revoke this ID across its complete bounded owner set. Preserves other sources. Requires both revisions, owners and confirm:true.", false, true},
		{"delete_iam_policy", "delete", "Delete an unattached named policy. Refuses attached policies, including disabled references; never cascades as revoke. Requires both revisions and confirm:true.", false, true},
		{"get_function_permissions", "get_function", "Inspect separate inline source, attached pins, read-only effective union, host approval/quarantine and history.", true, false},
		{"set_function_permissions", "inline", "Replace ONLY inline policy using expected revision/current_artifact/approved_artifact. Managed sources stay separate.", false, false},
		{"review_function_permissions", "review", "Explicitly review the exact aggregate against expected revision and reviewed current artifact; never submits a replacement policy.", false, false},
	} {
		item := item
		perm := permAdmin
		if item.read {
			perm = permRead
		}
		destructive := item.destructive
		schema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"id":      map[string]any{"type": "string", "description": "Canonical policy UUID; function UUID for permission tools. Omit only for list/create."},
			"request": workloadpolicy.ManagedRequestSchema(item.op),
		}, "required": []string{"request"}}
		if item.op != "list" && item.op != "create" {
			schema["required"] = []string{"id", "request"}
		}
		regAddTool(rc, perm, &mcpsdk.Tool{Name: item.name, Description: item.description, InputSchema: schema, Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: item.read, DestructiveHint: &destructive}},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in iamArguments) (*mcpsdk.CallToolResult, any, error) {
				if (item.op == "list" || item.op == "create") && in.hasID || (item.op != "list" && item.op != "create" && !in.hasID) {
					return nil, nil, database.ErrIAMInvalidPolicy
				}
				out, err := rc.deps.IAM.ExecuteManaged(ctx, item.op, in.ID, in.Request)
				return nil, out, err
			})
	}
}
