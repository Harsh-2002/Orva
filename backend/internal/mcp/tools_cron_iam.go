package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The AI registry calls JSON decoding directly, without MCP schema validation.
func (in *IAMCronCreate) UnmarshalJSON(raw []byte) error {
	type plain IAMCronCreate
	var out plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return err
	}
	*in = IAMCronCreate(out)
	return nil
}
func (in *IAMCronMutation) UnmarshalJSON(raw []byte) error {
	type plain IAMCronMutation
	var out plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return err
	}
	*in = IAMCronMutation(out)
	return nil
}

type IAMCronView struct {
	CronView
	Timezone string `json:"timezone"`
	IAMCronProvenance
	ConfigRevision string `json:"config_revision"`
}

// Explicit strings keep the inferred MCP schema aligned with exact wire revisions.
type IAMCronProvenance struct {
	AuthorityState          string `json:"authority_state"`
	AuthorityReason         string `json:"authority_reason"`
	OriginKind              string `json:"origin_kind"`
	CreatorID               string `json:"creator_id,omitempty"`
	OperatorKind            string `json:"operator_kind,omitempty"`
	OperatorID              string `json:"operator_id,omitempty"`
	CreationCallerRevision  string `json:"creation_caller_revision"`
	CreationTargetRevision  string `json:"creation_target_revision"`
	AuthorityGeneration     string `json:"authority_generation"`
	AdmissionCallerRevision string `json:"admission_caller_revision"`
	AdmissionTargetRevision string `json:"admission_target_revision"`
}

func iamCronView(c *database.AsyncCron) IAMCronView {
	return IAMCronView{CronView: toCronView(&c.CronSchedule, c.FunctionName), Timezone: c.Timezone,
		IAMCronProvenance: IAMCronProvenance{
			AuthorityState: c.AuthorityState, AuthorityReason: c.AuthorityReason, OriginKind: c.OriginKind,
			CreatorID: c.CreatorID, OperatorKind: c.OperatorKind, OperatorID: c.OperatorID,
			CreationCallerRevision:  strconv.FormatUint(c.CreationCallerRevision, 10),
			CreationTargetRevision:  strconv.FormatUint(c.CreationTargetRevision, 10),
			AuthorityGeneration:     strconv.FormatUint(c.AuthorityGeneration, 10),
			AdmissionCallerRevision: strconv.FormatUint(c.AdmissionCallerRevision, 10),
			AdmissionTargetRevision: strconv.FormatUint(c.AdmissionTargetRevision, 10),
		}, ConfigRevision: strconv.FormatUint(c.ConfigRevision, 10)}
}

type IAMCronList struct {
	Schedules []IAMCronView `json:"schedules"`
}
type IAMCronCreate struct {
	CreateCronInput
	Timezone string `json:"timezone,omitempty"`
}
type IAMCronMutation struct {
	ID                          string         `json:"id"`
	ExpectedConfigRevision      string         `json:"expected_config_revision"`
	ExpectedAuthorityGeneration string         `json:"expected_authority_generation"`
	CronExpr                    *string        `json:"cron_expr,omitempty"`
	Timezone                    *string        `json:"timezone,omitempty"`
	Enabled                     *bool          `json:"enabled,omitempty"`
	Payload                     map[string]any `json:"payload,omitempty"`
	Confirm                     bool           `json:"confirm,omitempty"`
}

func cronOperator(ctx context.Context, deps Deps) (database.OperatorCredentialReference, error) {
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok || deps.CronIAM == nil || deps.CronIAM.DB != deps.DB {
		return ref, database.ErrOperatorDenied
	}
	return ref, nil
}
func registerIAMCronTools(rc *regCtx) {
	deps := rc.deps
	regAddTool(rc, permRead, &mcpsdk.Tool{Name: "list_cron_schedules", Title: "List Cron Schedules", Description: "Inspect cron configuration, immutable source, current authority/reason and decimal CAS revisions. Reading does not grant management.",
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ListCronInput) (*mcpsdk.CallToolResult, IAMCronList, error) {
			if deps.CronIAM == nil || deps.CronIAM.DB != deps.DB {
				return nil, IAMCronList{}, database.ErrOperatorDenied
			}
			owner := ""
			if in.FunctionID != "" {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, IAMCronList{}, err
				}
				owner = fn.ID
			}
			rows, err := deps.DB.ListAsyncCrons(ctx, owner, nil)
			if err != nil {
				return nil, IAMCronList{}, err
			}
			out := IAMCronList{Schedules: []IAMCronView{}}
			for _, c := range rows {
				out.Schedules = append(out.Schedules, iamCronView(c))
			}
			return nil, out, nil
		})
	regAddTool(rc, permAdmin, &mcpsdk.Tool{Name: "create_cron_schedule", Title: "Create Cron Schedule", Description: "Create durable admin-owned work with live commit-time admin checks. Accepted work is independent of later submitter key revocation. Never supply origin or approval fields.",
		Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, OpenWorldHint: ptrFalse()}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in IAMCronCreate) (*mcpsdk.CallToolResult, IAMCronView, error) {
			ref, err := cronOperator(ctx, deps)
			if err != nil {
				return nil, IAMCronView{}, err
			}
			fn, err := resolveFunction(deps, in.FunctionID)
			if err != nil {
				return nil, IAMCronView{}, err
			}
			payload := []byte("{}")
			if in.Payload != nil {
				payload, err = json.Marshal(in.Payload)
				if err != nil {
					return nil, IAMCronView{}, err
				}
			}
			enabled := true
			if in.Enabled != nil {
				enabled = *in.Enabled
			}
			c := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: fn.ID, CronExpr: in.CronExpr, Timezone: in.Timezone, Enabled: enabled, Payload: string(payload)}}
			if err := deps.CronIAM.CreateCronOperator(ctx, ref, c); err != nil {
				return nil, IAMCronView{}, err
			}
			c.FunctionName = fn.Name
			return nil, iamCronView(c), nil
		})
	for _, action := range []string{"update", "resume", "delete"} {
		destructive := action == "delete"
		description := "Revalidate original authority; preserve provenance and require exact decimal CAS revisions. Update cannot resume suspended work; use resume explicitly. Resume schedules only a future firing, without catch-up. Quarantined work cannot resume."
		if action == "delete" {
			description = "Delete durable cron work with exact CAS revisions and confirm=true. Requires live admin; this is separate from the AI approval hint."
		}
		regAddTool(rc, permAdmin, &mcpsdk.Tool{Name: action + "_cron_schedule", Title: action + " Cron Schedule", Description: description,
			Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: false, DestructiveHint: &destructive, OpenWorldHint: ptrFalse()}},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in IAMCronMutation) (*mcpsdk.CallToolResult, IAMCronView, error) {
				ref, err := cronOperator(ctx, deps)
				if err != nil {
					return nil, IAMCronView{}, err
				}
				if action == "delete" && !in.Confirm {
					return nil, IAMCronView{}, errors.New("delete refused: pass confirm=true")
				}
				change := workloadasync.CronControl{ExpectedConfigRevision: in.ExpectedConfigRevision, ExpectedAuthorityGeneration: in.ExpectedAuthorityGeneration,
					CronExpr: in.CronExpr, Timezone: in.Timezone, Enabled: in.Enabled}
				if in.Payload != nil {
					change.Payload, err = json.Marshal(in.Payload)
					if err != nil {
						return nil, IAMCronView{}, err
					}
				}
				c, err := deps.CronIAM.ControlCron(ctx, ref, "", in.ID, action, change)
				if err != nil {
					return nil, IAMCronView{}, err
				}
				if c == nil {
					return nil, IAMCronView{CronView: CronView{ID: in.ID}}, nil
				}
				return nil, iamCronView(c), nil
			})
	}
}
