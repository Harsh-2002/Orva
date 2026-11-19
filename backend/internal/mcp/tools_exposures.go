package mcp

import (
	"context"
	"encoding/json"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/backend/internal/workloadtrigger"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type exposureArguments struct{ raw json.RawMessage }

func (*exposureArguments) UseStrictRawJSON() {}
func (a *exposureArguments) UnmarshalJSON(raw []byte) error {
	if _, err := workloadpolicy.StrictObject(raw); err != nil {
		return err
	}
	a.raw = append(json.RawMessage(nil), raw...)
	return nil
}

func registerExposureTools(rc *regCtx, routes bool) {
	items := []struct {
		name, op    string
		destructive bool
	}{}
	if routes {
		items = append(items, struct {
			name, op    string
			destructive bool
		}{"inspect_exposure", "inspect", false},
			struct {
				name, op    string
				destructive bool
			}{"set_route", "route_set", false},
			struct {
				name, op    string
				destructive bool
			}{"delete_route", "route_delete", true})
	} else {
		items = append(items, struct {
			name, op    string
			destructive bool
		}{"create_inbound_webhook", "inbound_create", false},
			struct {
				name, op    string
				destructive bool
			}{"update_inbound_webhook", "inbound_update", false},
			struct {
				name, op    string
				destructive bool
			}{"delete_inbound_webhook", "inbound_delete", true})
	}
	for _, item := range items {
		perm := permRead
		if item.op != "inspect" {
			if !rc.perms.Has(permWrite) && !rc.perms.Has(permAdmin) {
				continue
			}
			perm = permWrite
			if rc.perms.Has(permAdmin) {
				perm = permAdmin
			}
		}
		destructive := item.destructive
		var opts []regOption
		if item.op == "inbound_create" {
			opts = append(opts, withOneTimeResult("secret"))
		}
		regAddTool(rc, perm, &mcpsdk.Tool{Name: item.name,
			Description: "Opt-in exact-CAS public exposure management. Inspect first. Live ADMIN required for either protected owner, WRITE for truly empty review-free owners. Does not change SDK authority, artifact approval or secrets. Delete requires confirm:true; webhook create returns its new signature secret once.",
			InputSchema: workloadtrigger.RequestSchema(item.op), Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: item.op == "inspect", DestructiveHint: &destructive}},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in exposureArguments) (*mcpsdk.CallToolResult, any, error) {
				out, err := rc.deps.Triggers.Execute(ctx, item.op, in.raw)
				if err != nil {
					return nil, nil, err
				}
				return nil, exposureToolProjection(rc.deps, item.op, out), nil
			}, opts...)
	}
}

func exposureToolProjection(deps Deps, operation string, out map[string]any) any {
	switch operation {
	case "inbound_create", "inbound_update":
		row := *out["inbound_webhook"].(*database.InboundWebhook)
		// Display metadata follows successful authorization; it never gates the ACK.
		if fn, err := deps.DB.GetFunction(row.FunctionID); err == nil && fn.ID == row.FunctionID {
			row.FunctionName = fn.Name
		}
		view := toInboundWebhookView(&row)
		if operation == "inbound_create" {
			return CreateInboundWebhookOutput{InboundWebhook: view, Secret: out["secret"].(string)}
		}
		return map[string]any{"inbound_webhook": view}
	case "inbound_delete":
		return InboundWebhookOpOutput{ID: out["id"].(string), Status: out["status"].(string)}
	case "route_set":
		return RouteOpOutput{Path: out["path"].(string), FunctionID: out["function_id"].(string)}
	case "route_delete":
		return RouteOpOutput{Path: out["path"].(string)}
	default:
		return out
	}
}
