package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func asyncOperator(ctx context.Context, deps Deps) (database.OperatorCredentialReference, error) {
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok || deps.AsyncIAM == nil || deps.AsyncIAM.DB != deps.DB {
		return ref, errors.New("async operation denied")
	}
	return ref, nil
}

type AsyncJobView struct {
	JobView
	AuthorityState  string `json:"authority_state"`
	AuthorityReason string `json:"authority_reason"`
	OriginKind      string `json:"origin_kind"`
	CreatorID       string `json:"creator_id,omitempty"`
}
type AsyncListJobsOutput struct {
	Jobs []AsyncJobView `json:"jobs"`
}

func toAsyncJobView(j *database.AsyncJob) AsyncJobView {
	return AsyncJobView{JobView: toJobView(&j.Job), AuthorityState: j.AuthorityState,
		AuthorityReason: j.AuthorityReason, OriginKind: j.OriginKind, CreatorID: j.CreatorID}
}
func registerAsyncJobTools(rc *regCtx) {
	deps := rc.deps
	regAddTool(rc, permAdmin, &mcpsdk.Tool{Name: "enqueue_job", Title: "Enqueue Job",
		Description: "Create durable admin-owned work. Current admin authority is checked at commit. Function-origin work must use the SDK; never pass origin metadata. Later key expiry does not revoke accepted operator work.",
		Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in EnqueueJobInput) (*mcpsdk.CallToolResult, AsyncJobView, error) {
			ref, err := asyncOperator(ctx, deps)
			if err != nil {
				return nil, AsyncJobView{}, err
			}
			fn, err := resolveFunction(deps, in.FunctionID)
			if err != nil {
				return nil, AsyncJobView{}, err
			}
			payload := []byte("{}")
			if in.Payload != nil {
				payload, err = json.Marshal(in.Payload)
				if err != nil {
					return nil, AsyncJobView{}, err
				}
			}
			j := &database.AsyncJob{Job: database.Job{FunctionID: fn.ID, Payload: payload, MaxAttempts: in.MaxAttempts,
				IdempotencyKey: in.IdempotencyKey, IdempotencyWindowSeconds: in.IdempotencyWindowSeconds}}
			if in.ScheduledAt != "" {
				at, err := time.Parse(time.RFC3339, in.ScheduledAt)
				if err != nil {
					return nil, AsyncJobView{}, errors.New("scheduled_at must be RFC3339")
				}
				j.ScheduledAt = at.UTC()
			}
			if err := deps.AsyncIAM.EnqueueOperator(ctx, ref, j); err != nil {
				return nil, AsyncJobView{}, err
			}
			j.FunctionName = fn.Name
			return nil, toAsyncJobView(j), nil
		})
	regAddTool(rc, permRead, &mcpsdk.Tool{Name: "list_jobs", Title: "List Jobs", Description: "Inspect stored job status, authority state, source and denial reason. Read access never authorizes resume.",
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ListJobsInput) (*mcpsdk.CallToolResult, AsyncListJobsOutput, error) {
			fnID := ""
			if strings.TrimSpace(in.FunctionID) != "" {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, AsyncListJobsOutput{}, err
				}
				fnID = fn.ID
			}
			rows, err := deps.DB.ListAsyncJobs(ctx, in.Status, fnID, in.Limit, nil)
			if err != nil {
				return nil, AsyncListJobsOutput{}, err
			}
			out := AsyncListJobsOutput{Jobs: make([]AsyncJobView, 0, len(rows))}
			for _, j := range rows {
				out.Jobs = append(out.Jobs, toAsyncJobView(j))
			}
			return nil, out, nil
		})
	regAddTool(rc, permRead, &mcpsdk.Tool{Name: "get_job", Title: "Get Job", Description: "Inspect one job and its original source and current authority state.",
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in JobIDInput) (*mcpsdk.CallToolResult, AsyncJobView, error) {
			j, err := deps.DB.GetAsyncJob(ctx, in.ID)
			if err != nil {
				return nil, AsyncJobView{}, err
			}
			return nil, toAsyncJobView(j), nil
		})
	regAddTool(rc, permAdmin, &mcpsdk.Tool{Name: "retry_job", Title: "Retry Job",
		Description: "Explicitly retry a failed active job after rechecking its original authority. Preserves origin and idempotency scope; never adopts function work as operator work.",
		Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in JobIDInput) (*mcpsdk.CallToolResult, JobOpOutput, error) {
			ref, err := asyncOperator(ctx, deps)
			if err != nil {
				return nil, JobOpOutput{}, err
			}
			if err := deps.AsyncIAM.MutateJob(ctx, ref, in.ID, "retry"); err != nil {
				return nil, JobOpOutput{}, err
			}
			return nil, JobOpOutput{ID: in.ID, Status: "pending"}, nil
		})
	regAddTool(rc, permAdmin, &mcpsdk.Tool{Name: "delete_job", Title: "Delete Job",
		Description: "Delete a durable job. Admin only; requires confirm=true. Deletion is the operational revocation path for accepted operator work.",
		Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in JobIDInputWithConfirm) (*mcpsdk.CallToolResult, JobOpOutput, error) {
			if !in.Confirm {
				return nil, JobOpOutput{}, errors.New("delete refused: pass confirm=true")
			}
			ref, err := asyncOperator(ctx, deps)
			if err != nil {
				return nil, JobOpOutput{}, err
			}
			if err := deps.AsyncIAM.MutateJob(ctx, ref, in.ID, "delete"); err != nil {
				return nil, JobOpOutput{}, err
			}
			return nil, JobOpOutput{ID: in.ID, Status: "deleted"}, nil
		})
	regAddTool(rc, permAdmin, &mcpsdk.Tool{Name: "resume_job", Title: "Resume Job",
		Description: "Explicitly resume one suspended pending job after revalidating its original creator and current target rights. Admin only. Never reopens terminal or quarantined jobs; regrant alone does not resume work.",
		Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in JobIDInput) (*mcpsdk.CallToolResult, JobOpOutput, error) {
			ref, err := asyncOperator(ctx, rc.deps)
			if err != nil {
				return nil, JobOpOutput{}, err
			}
			if err := rc.deps.AsyncIAM.MutateJob(ctx, ref, in.ID, "resume"); err != nil {
				return nil, JobOpOutput{}, err
			}
			return nil, JobOpOutput{ID: in.ID, Status: "pending"}, nil
		})
}
