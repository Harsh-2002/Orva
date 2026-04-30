package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/scheduler"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// CronView is the agent-friendly projection of a database.CronSchedule.
// Times are RFC3339 strings, payload is a parsed object so the agent
// doesn't have to JSON.parse a string-of-JSON.
type CronView struct {
	ID           string `json:"id"`
	FunctionID   string `json:"function_id"`
	FunctionName string `json:"function_name,omitempty"`
	CronExpr     string `json:"cron_expr"`
	Enabled      bool   `json:"enabled"`
	LastRunAt    string `json:"last_run_at,omitempty"`
	NextRunAt    string `json:"next_run_at,omitempty"`
	LastStatus   string `json:"last_status,omitempty"`
	LastError    string `json:"last_error,omitempty"`
	Payload      any    `json:"payload"`
	CreatedAt    string `json:"created_at"`
}

func toCronView(s *database.CronSchedule, fnName string) CronView {
	v := CronView{
		ID:           s.ID,
		FunctionID:   s.FunctionID,
		FunctionName: fnName,
		CronExpr:     s.CronExpr,
		Enabled:      s.Enabled,
		LastStatus:   s.LastStatus,
		LastError:    s.LastError,
		CreatedAt:    s.CreatedAt.UTC().Format(time.RFC3339),
	}
	if s.LastRunAt != nil {
		v.LastRunAt = s.LastRunAt.UTC().Format(time.RFC3339)
	}
	if s.NextRunAt != nil {
		v.NextRunAt = s.NextRunAt.UTC().Format(time.RFC3339)
	}
	// Decode the JSON payload so the agent sees structure, not "{}".
	var decoded any
	if s.Payload != "" {
		if err := json.Unmarshal([]byte(s.Payload), &decoded); err == nil {
			v.Payload = decoded
		} else {
			v.Payload = s.Payload
		}
	} else {
		v.Payload = map[string]any{}
	}
	return v
}

type ListCronInput struct {
	FunctionID string `json:"function_id,omitempty" jsonschema:"optional — function id or name; if omitted lists all schedules across functions"`
}
type ListCronOutput struct {
	Schedules []CronView `json:"schedules"`
}

type CreateCronInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (fn_...) or friendly name"`
	CronExpr   string `json:"cron_expr"   jsonschema:"5-field cron expression. Supports @daily / @hourly / @weekly / @monthly / @yearly shorthands"`
	Enabled    *bool  `json:"enabled,omitempty"  jsonschema:"defaults to true"`
	Payload    any    `json:"payload,omitempty"  jsonschema:"JSON value delivered as the invoke body when the schedule fires; default {}"`
}

type UpdateCronInput struct {
	ID       string `json:"id"        jsonschema:"schedule id (cron_...)"`
	CronExpr string `json:"cron_expr,omitempty" jsonschema:"new cron expression; omit to keep"`
	Enabled  *bool  `json:"enabled,omitempty"   jsonschema:"new enabled flag; omit to keep"`
	Payload  any    `json:"payload,omitempty"   jsonschema:"new payload; omit to keep"`
}

type DeleteCronInput struct {
	ID      string `json:"id"`
	Confirm bool   `json:"confirm" jsonschema:"must be true to actually delete"`
}

type CronOpOutput struct {
	ID string `json:"id"`
}

func registerCronTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_cron_schedules",
				Description: "List cron schedules. Pass function_id to filter by function (id or name); omit to list every schedule. Times are RFC3339; last_status is 'ok' / 'failed' / empty.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListCronInput) (*mcpsdk.CallToolResult, ListCronOutput, error) {
				out := ListCronOutput{Schedules: []CronView{}}
				if strings.TrimSpace(in.FunctionID) != "" {
					fn, err := resolveFunction(deps, in.FunctionID)
					if err != nil {
						return nil, out, err
					}
					rows, err := deps.DB.ListCronSchedulesForFunction(fn.ID)
					if err != nil {
						return nil, out, err
					}
					for _, r := range rows {
						out.Schedules = append(out.Schedules, toCronView(r, fn.Name))
					}
					return nil, out, nil
				}
				rows, err := deps.DB.ListAllCronSchedulesWithFunction()
				if err != nil {
					return nil, out, err
				}
				for _, r := range rows {
					out.Schedules = append(out.Schedules, toCronView(r.CronSchedule, r.FunctionName))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "create_cron_schedule",
				Description: "Schedule a function to fire on a cron expression. Returns the new schedule with next_run_at filled in. The schedule is stored centrally; the orvad scheduler picks it up on its next 30s tick.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in CreateCronInput) (*mcpsdk.CallToolResult, CronView, error) {
				expr := strings.TrimSpace(in.CronExpr)
				if expr == "" {
					return nil, CronView{}, errors.New("cron_expr is required")
				}
				sched, err := scheduler.ParseCronExpr(expr)
				if err != nil {
					return nil, CronView{}, errors.New("invalid cron_expr: " + err.Error())
				}
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, CronView{}, err
				}
				enabled := true
				if in.Enabled != nil {
					enabled = *in.Enabled
				}
				payload := "{}"
				if in.Payload != nil {
					b, err := json.Marshal(in.Payload)
					if err != nil {
						return nil, CronView{}, errors.New("payload must be JSON-serializable")
					}
					payload = string(b)
				}
				row := &database.CronSchedule{
					FunctionID: fn.ID,
					CronExpr:   expr,
					Enabled:    enabled,
					Payload:    payload,
				}
				next := sched.Next(time.Now().UTC())
				row.NextRunAt = &next
				if err := deps.DB.InsertCronSchedule(row); err != nil {
					return nil, CronView{}, err
				}
				return nil, toCronView(row, fn.Name), nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "update_cron_schedule",
				Description: "Edit an existing cron schedule. Any of cron_expr / enabled / payload may be supplied; omitted fields keep their previous values. next_run_at is recomputed when the expression changes.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in UpdateCronInput) (*mcpsdk.CallToolResult, CronView, error) {
				row, err := deps.DB.GetCronSchedule(in.ID)
				if err != nil {
					return nil, CronView{}, errors.New("schedule not found")
				}
				exprChanged := false
				if expr := strings.TrimSpace(in.CronExpr); expr != "" && expr != row.CronExpr {
					if _, err := scheduler.ParseCronExpr(expr); err != nil {
						return nil, CronView{}, errors.New("invalid cron_expr: " + err.Error())
					}
					row.CronExpr = expr
					exprChanged = true
				}
				if in.Enabled != nil {
					row.Enabled = *in.Enabled
				}
				if in.Payload != nil {
					b, err := json.Marshal(in.Payload)
					if err != nil {
						return nil, CronView{}, errors.New("payload must be JSON-serializable")
					}
					row.Payload = string(b)
				}
				if exprChanged || row.Enabled {
					sched, _ := scheduler.ParseCronExpr(row.CronExpr)
					next := sched.Next(time.Now().UTC())
					row.NextRunAt = &next
				}
				if err := deps.DB.UpdateCronSchedule(row); err != nil {
					return nil, CronView{}, err
				}
				fnName := ""
				if fn, err := deps.DB.GetFunction(row.FunctionID); err == nil {
					fnName = fn.Name
				}
				return nil, toCronView(row, fnName), nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_cron_schedule",
				Description: "Remove a cron schedule. Pass confirm=true. The function itself is unchanged.",
				Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteCronInput) (*mcpsdk.CallToolResult, CronOpOutput, error) {
				if !in.Confirm {
					return nil, CronOpOutput{}, errors.New("delete refused: pass confirm=true")
				}
				if _, err := deps.DB.GetCronSchedule(in.ID); err != nil {
					return nil, CronOpOutput{}, errors.New("schedule not found")
				}
				if err := deps.DB.DeleteCronSchedule(in.ID); err != nil {
					return nil, CronOpOutput{}, err
				}
				return nil, CronOpOutput{ID: in.ID}, nil
			},
		)
	})
}
