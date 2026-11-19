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
		Description: "Queue a single Node or Python source file with live submitter capture. For dependencies or a TypeScript project, use the managed REST archive upload. Successful publication is review-required DENY, not ready to invoke. A fresh host Factory installation and explicit source-aware review are separate.",
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

type publicationRollbackArguments struct{ Input builder.RollbackInput }

func (*publicationRollbackArguments) UseStrictRawJSON() {}
func (a *publicationRollbackArguments) UnmarshalJSON(raw []byte) error {
	in, err := builder.DecodeRollbackInput(raw, "", true)
	a.Input = in
	return err
}

func registerPublicationRollback(rc *regCtx) {
	type output struct {
		builder.RollbackPromotion
		DurationMS int64 `json:"duration_ms"`
	}
	regAddTool(rc, permWrite, &mcpsdk.Tool{Name: "rollback_function", Title: "Rollback Function",
		Description: "Promote an exact historical managed deployment UUID for an exact function UUID. Pass confirm=true. No hash or latest selector. Preserves current configuration and leaves inactive review-required DENY. Fresh host Factory installation and source-aware review are separate.",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false,
			"required": []string{"function_id", "deployment_id", "confirm"},
			"properties": map[string]any{"function_id": map[string]string{"type": "string", "format": "uuid"},
				"deployment_id": map[string]string{"type": "string", "format": "uuid"},
				"confirm":       map[string]any{"type": "boolean", "const": true}}},
		Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, args publicationRollbackArguments) (*mcpsdk.CallToolResult, output, error) {
			started := time.Now()
			out, err := rc.deps.DeployIAM.PromoteRollback(ctx, args.Input.FunctionID, args.Input.DeploymentID)
			return nil, output{RollbackPromotion: out, DurationMS: time.Since(started).Milliseconds()}, err
		})
}
