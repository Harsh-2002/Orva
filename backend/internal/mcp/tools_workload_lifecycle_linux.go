//go:build linux

package mcp

import (
	"context"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type lifecycleArguments struct{ Input database.WorkloadPauseRequest }

func (*lifecycleArguments) UseStrictRawJSON() {}
func (a *lifecycleArguments) UnmarshalJSON(raw []byte) error {
	in, err := builder.DecodeLifecycleInput(raw, "", true)
	a.Input = in
	return err
}

func registerWorkloadLifecycleTools(rc *regCtx) {
	service := rc.deps.WorkloadLifecycle
	if service == nil {
		return
	}
	rc.group = "functions"
	for _, tool := range []struct {
		name, description string
		resume            bool
	}{
		{"pause_workload", "Record durable operator pause and invalidate the managed Factory. Requires live WRITE for unprivileged owners or ADMIN for privileged policy.", false},
		{"resume_workload", "ADMIN only: replace durable pause with an inactive review-required generation. Does not resume execution or standing work. A fresh Factory install and explicit source-aware review are separate.", true},
	} {
		perm := permWrite
		if tool.resume || rc.perms.Has(permAdmin) {
			perm = permAdmin
		}
		uuid := map[string]any{"type": "string", "format": "uuid"}
		counter := map[string]any{"type": "string", "pattern": "^[1-9][0-9]*$", "maxLength": 19}
		regAddTool(rc, perm, &mcpsdk.Tool{Name: tool.name,
			Description: tool.description + " Review generation, policy_revision and authority_epoch in inspect_workload_configuration first and explicitly pass confirm=true.",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false,
				"required": []string{"function_id", "generation", "policy_revision", "authority_epoch", "confirm"},
				"properties": map[string]any{"function_id": uuid, "generation": uuid,
					"policy_revision": counter, "authority_epoch": counter, "confirm": map[string]any{"type": "boolean", "const": true}}},
			Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()}},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, a lifecycleArguments) (*mcpsdk.CallToolResult, builder.LifecycleAcknowledgement, error) {
				out, err := service.ExecuteLifecycle(ctx, a.Input, tool.resume)
				return nil, out, err
			})
	}
}
