package mcp

import (
	"context"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type publicationArguments struct{ Input builder.PublicationInline }

func (*publicationArguments) UseStrictRawJSON() {}
func (a *publicationArguments) UnmarshalJSON(raw []byte) error {
	in, err := builder.DecodePublicationInline(raw, true)
	a.Input = in
	return err
}

func registerPublicationDeploy(rc *regCtx) {
	regAddTool(rc, permWrite, &mcpsdk.Tool{Name: "deploy_function_inline", Title: "Deploy Function Inline",
		Description: "Queue plain no-dependency Node/Python code with live submitter capture. Successful publication is review-required DENY, not ready to invoke. A fresh host Factory installation and explicit source-aware review are separate.",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"function_id", "code", "wait"},
			"properties": map[string]any{"function_id": map[string]string{"type": "string"}, "code": map[string]string{"type": "string"},
				"filename": map[string]string{"type": "string"}, "dependencies": map[string]string{"type": "string"},
				"wait": map[string]string{"type": "boolean"}}},
		Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrFalse(), OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, args publicationArguments) (*mcpsdk.CallToolResult, DeployInlineOutput, error) {
			if rc.deps.DeployIAM == nil {
				return nil, DeployInlineOutput{}, builder.ErrPublicationUnavailable
			}
			fn, err := resolveFunction(rc.deps, args.Input.FunctionID)
			if err != nil {
				return nil, DeployInlineOutput{}, err
			}
			id, err := rc.deps.DeployIAM.Inline(ctx, fn.ID, args.Input)
			if err != nil {
				return nil, DeployInlineOutput{DeploymentID: id}, err
			}
			out := DeployInlineOutput{DeploymentID: id, FunctionID: fn.ID, Status: "queued"}
			if args.Input.Wait {
				final := waitDeployment(ctx, rc.deps, id, 120*time.Second)
				out.Status, out.Phase, out.CodeHash, out.Error = final.Status, final.Phase, final.CodeHash, final.Error
			}
			return nil, out, nil
		})
}
