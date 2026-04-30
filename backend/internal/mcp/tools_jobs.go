package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// JobView is the agent-facing projection. payload is parsed back into
// an object so an agent can reason about it without re-decoding.
type JobView struct {
	ID           string `json:"id"`
	FunctionID   string `json:"function_id"`
	FunctionName string `json:"function_name,omitempty"`
	Status       string         `json:"status"`
	// See tools_cron.go for the rationale on map[string]any over `any`.
	Payload      map[string]any `json:"payload"`
	ScheduledAt  string `json:"scheduled_at"`
	StartedAt    string `json:"started_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
	Attempts     int    `json:"attempts"`
	MaxAttempts  int    `json:"max_attempts"`
	LastError    string `json:"last_error,omitempty"`
	CreatedAt    string `json:"created_at"`
}

func toJobView(j *database.Job) JobView {
	v := JobView{
		ID:           j.ID,
		FunctionID:   j.FunctionID,
		FunctionName: j.FunctionName,
		Status:       j.Status,
		ScheduledAt:  j.ScheduledAt.UTC().Format(time.RFC3339),
		Attempts:     j.Attempts,
		MaxAttempts:  j.MaxAttempts,
		LastError:    j.LastError,
		CreatedAt:    j.CreatedAt.UTC().Format(time.RFC3339),
	}
	if j.StartedAt != nil {
		v.StartedAt = j.StartedAt.UTC().Format(time.RFC3339)
	}
	if j.FinishedAt != nil {
		v.FinishedAt = j.FinishedAt.UTC().Format(time.RFC3339)
	}
	v.Payload = map[string]any{}
	if len(j.Payload) > 0 {
		var asObj map[string]any
		if err := json.Unmarshal(j.Payload, &asObj); err == nil {
			v.Payload = asObj
		} else {
			var asAny any
			if err := json.Unmarshal(j.Payload, &asAny); err == nil {
				v.Payload = map[string]any{"value": asAny}
			} else {
				v.Payload = map[string]any{"raw": string(j.Payload)}
			}
		}
	}
	return v
}

type EnqueueJobInput struct {
	FunctionID  string `json:"function_id" jsonschema:"function id (fn_...) or name to run when the job fires"`
	Payload     map[string]any `json:"payload,omitempty" jsonschema:"JSON object delivered as the invoke body; default {}"`
	ScheduledAt string `json:"scheduled_at,omitempty" jsonschema:"RFC3339 timestamp; omit to run as soon as the scheduler picks it up (~5s)"`
	MaxAttempts int    `json:"max_attempts,omitempty" jsonschema:"retry budget; default 3, exponential backoff between attempts"`
}

type ListJobsInput struct {
	Status     string `json:"status,omitempty"      jsonschema:"filter: pending | running | succeeded | failed"`
	FunctionID string `json:"function_id,omitempty" jsonschema:"function id (fn_...) or name to scope the list"`
	Limit      int    `json:"limit,omitempty"        jsonschema:"max rows; default 50, cap 500"`
}
type ListJobsOutput struct {
	Jobs []JobView `json:"jobs"`
}

type JobIDInput struct {
	ID string `json:"id" jsonschema:"job id (job_...)"`
}
type JobIDInputWithConfirm struct {
	ID      string `json:"id"`
	Confirm bool   `json:"confirm"`
}

type JobOpOutput struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
}

func registerJobTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "enqueue_job",
				Description: "Enqueue a background job. Returns immediately with the job id; the scheduler will dispatch the function within ~5s (or at scheduled_at if supplied). Failed runs retry with exponential backoff up to max_attempts.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in EnqueueJobInput) (*mcpsdk.CallToolResult, JobView, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, JobView{}, err
				}
				payload := []byte("{}")
				if in.Payload != nil {
					b, err := json.Marshal(in.Payload)
					if err != nil {
						return nil, JobView{}, errors.New("payload must be JSON-serializable")
					}
					payload = b
				}
				job := &database.Job{
					FunctionID:  fn.ID,
					Payload:     payload,
					MaxAttempts: in.MaxAttempts,
				}
				if in.ScheduledAt != "" {
					t, err := time.Parse(time.RFC3339, in.ScheduledAt)
					if err != nil {
						return nil, JobView{}, errors.New("scheduled_at must be RFC3339")
					}
					job.ScheduledAt = t.UTC()
				}
				if err := deps.DB.EnqueueJob(job); err != nil {
					return nil, JobView{}, err
				}
				job.FunctionName = fn.Name
				return nil, toJobView(job), nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_jobs",
				Description: "List background jobs, optionally filtered by status or function. Useful for inspecting the queue, surfacing failures, or auditing recent activity.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListJobsInput) (*mcpsdk.CallToolResult, ListJobsOutput, error) {
				fnID := ""
				if strings.TrimSpace(in.FunctionID) != "" {
					fn, err := resolveFunction(deps, in.FunctionID)
					if err != nil {
						return nil, ListJobsOutput{}, err
					}
					fnID = fn.ID
				}
				rows, err := deps.DB.ListJobs(in.Status, fnID, in.Limit)
				if err != nil {
					return nil, ListJobsOutput{}, err
				}
				out := ListJobsOutput{Jobs: make([]JobView, 0, len(rows))}
				for _, j := range rows {
					out.Jobs = append(out.Jobs, toJobView(j))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_job",
				Description: "Fetch a single job by id. Includes the original payload, full retry history, and the most recent error.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in JobIDInput) (*mcpsdk.CallToolResult, JobView, error) {
				j, err := deps.DB.GetJob(in.ID)
				if err != nil {
					return nil, JobView{}, errors.New("job not found")
				}
				if fn, err := deps.DB.GetFunction(j.FunctionID); err == nil {
					j.FunctionName = fn.Name
				}
				return nil, toJobView(j), nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "retry_job",
				Description: "Reset a terminal job (status=failed) back to pending so the scheduler picks it up on the next tick. attempts is reset to 0.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in JobIDInput) (*mcpsdk.CallToolResult, JobOpOutput, error) {
				if _, err := deps.DB.GetJob(in.ID); err != nil {
					return nil, JobOpOutput{}, errors.New("job not found")
				}
				if err := deps.DB.RetryJob(in.ID); err != nil {
					return nil, JobOpOutput{}, err
				}
				return nil, JobOpOutput{ID: in.ID, Status: "pending"}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_job",
				Description: "Remove a job row entirely. Pass confirm=true. Use this to clear stuck or duplicate enqueues; for a normal failed job prefer retry_job to keep the audit trail.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in JobIDInputWithConfirm) (*mcpsdk.CallToolResult, JobOpOutput, error) {
				if !in.Confirm {
					return nil, JobOpOutput{}, errors.New("delete refused: pass confirm=true")
				}
				if err := deps.DB.DeleteJob(in.ID); err != nil {
					return nil, JobOpOutput{}, err
				}
				return nil, JobOpOutput{ID: in.ID, Status: "deleted"}, nil
			},
		)
	})
}
