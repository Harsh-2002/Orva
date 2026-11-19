//go:build linux

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerManagedOperationTools adds the operator surfaces that exist in every
// serving mode to the managed catalog, at the permission tiers the legacy
// tools use. Nothing here invokes function code except replay, which dispatches
// through the managed pool's Factory admission. OAuth applications and browser
// sessions are cookie-bound REST management with no MCP counterpart.
func registerManagedOperationTools(rc *regCtx) {
	registerExecutionTools(rc)
	registerManagedReplayTool(rc)
	registerTraceTools(rc)
	registerSystemOpsTools(rc)
	registerKeyTools(rc)
	registerKVTools(rc)
	registerManagedActivityTool(rc)
	registerManagedChannelTools(rc)
}

type ReplayExecutionInput struct {
	ExecutionID string `json:"execution_id" jsonschema:"id of a captured execution to re-run against the function's current managed deployment"`
}

type ReplayExecutionOutput struct {
	ExecutionID string `json:"execution_id" jsonschema:"the new execution created by the replay"`
	ReplayOf    string `json:"replay_of"`
	StatusCode  int    `json:"status_code" jsonschema:"the replayed function's own HTTP status"`
	Body        string `json:"body"`
}

func registerManagedReplayTool(rc *regCtx) {
	replay := rc.deps.ReplayExecution
	if replay == nil {
		return
	}
	rc.group = "invoke"
	regAddTool(rc, permWrite,
		&mcpsdk.Tool{
			Name:  "replay_execution",
			Title: "Replay Execution",
			Description: "Re-run a captured request against the function's current managed deployment. The call is admitted by the " +
				"managed pool exactly like an HTTP invocation and records a new execution whose replay_of points at the original. " +
				"Refused if the capture was truncated or purged, or if the function is paused, inactive or changed during admission.",
			Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrFalse(), OpenWorldHint: ptrTrue()},
		},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ReplayExecutionInput) (*mcpsdk.CallToolResult, ReplayExecutionOutput, error) {
			if in.ExecutionID == "" || strings.Contains(in.ExecutionID, "/") {
				return nil, ReplayExecutionOutput{}, errors.New("execution_id is required")
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/executions/"+in.ExecutionID+"/replay", nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			replay(rec, req)
			// The handler sets X-Orva-Replay-Of only once the function ran and its
			// execution was recorded, and then forwards the function's own status
			// (a 201 or 204 is a successful replay, a 500 is the function's answer).
			// Admission, capture and dispatch failures never set it: those are errors.
			if rec.Header().Get("X-Orva-Replay-Of") == "" {
				var failure struct {
					Error struct {
						Code, Message string
					}
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &failure)
				if failure.Error.Message == "" {
					failure.Error.Message = http.StatusText(rec.Code)
				}
				return nil, ReplayExecutionOutput{}, errors.New("replay refused: " + failure.Error.Message)
			}
			return nil, ReplayExecutionOutput{
				ExecutionID: rec.Header().Get("X-Orva-Execution-ID"), ReplayOf: rec.Header().Get("X-Orva-Replay-Of"),
				StatusCode: rec.Code, Body: rec.Body.String(),
			}, nil
		},
	)
}

type ListActivityInput struct {
	Source    string `json:"source,omitempty" jsonschema:"filter by source: web, api, mcp, sdk, cron or internal"`
	Search    string `json:"search,omitempty" jsonschema:"case-insensitive match on path, summary or actor label"`
	StatusMin int    `json:"status_min,omitempty" jsonschema:"only rows with status >= this (use 400 for errors)"`
	Limit     int    `json:"limit,omitempty" jsonschema:"max rows, default 50, capped at 200"`
}

type ListActivityOutput struct {
	Activity []database.ActivityRow `json:"activity"`
}

func registerManagedActivityTool(rc *regCtx) {
	deps := rc.deps
	rc.group = "system"
	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:        "list_activity",
			Title:       "List Activity",
			Description: "Newest-first operator activity feed: who did what through the dashboard, REST API, MCP and webhooks, with status and trace ids.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListActivityInput) (*mcpsdk.CallToolResult, ListActivityOutput, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			if limit > 200 {
				limit = 200
			}
			rows, _, _, err := deps.DB.ListActivity(database.ActivityFilter{
				Source: in.Source, Search: in.Search, StatusMin: in.StatusMin, Limit: limit,
			})
			if err != nil {
				return nil, ListActivityOutput{}, err
			}
			return nil, ListActivityOutput{Activity: rows}, nil
		},
	)
}

type GetChannelInput struct {
	ID string `json:"id" jsonschema:"channel id"`
}

type ListChannelsOutput struct {
	Channels []*database.Channel `json:"channels"`
}

type GetChannelOutput struct {
	Channel   *database.Channel           `json:"channel"`
	Functions []*database.ChannelFunction `json:"functions"`
}

// Channel tokens are credentials: only inspection is offered here, and never
// the token or its hash. Minting, rotation and deletion stay on the admin REST
// surface and the dashboard, as in the legacy catalog.
func registerManagedChannelTools(rc *regCtx) {
	deps := rc.deps
	rc.group = "channels"
	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name: "list_channels", Title: "List Channels",
			Description: "List agent channels (named bundles of functions behind a bearer token) with token prefix, expiry and last use. Tokens are never returned.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ListChannelsOutput, error) {
			rows, err := deps.DB.ListChannels()
			if err != nil {
				return nil, ListChannelsOutput{}, err
			}
			return nil, ListChannelsOutput{Channels: rows}, nil
		},
	)
	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name: "get_channel", Title: "Get Channel",
			Description: "Fetch one agent channel with its bundled functions. The token is never returned.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetChannelInput) (*mcpsdk.CallToolResult, GetChannelOutput, error) {
			c, err := deps.DB.GetChannelByID(in.ID)
			if err != nil {
				return nil, GetChannelOutput{}, errors.New("channel not found")
			}
			functions, _ := deps.DB.ListChannelFunctionRecords(c.ID)
			return nil, GetChannelOutput{Channel: c, Functions: functions}, nil
		},
	)
}
