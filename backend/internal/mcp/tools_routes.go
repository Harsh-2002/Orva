package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

type RouteView struct {
	Path       string    `json:"path"`
	FunctionID string    `json:"function_id"`
	Methods    string    `json:"methods"`
	CreatedAt  time.Time `json:"created_at"`
}

type ListRoutesOutput struct {
	Routes []RouteView `json:"routes"`
}

type SetRouteInput struct {
	Path       string `json:"path" jsonschema:"URL path. Must start with /. Add /* suffix for prefix matching (e.g. /shortener/*)"`
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary) to dispatch to"`
	Methods    string `json:"methods,omitempty" jsonschema:"* (default) or comma-separated list like GET,POST"`
}

type RouteOpOutput struct {
	Path       string `json:"path"`
	FunctionID string `json:"function_id"`
}

type DeleteRouteInput struct {
	Path    string `json:"path"`
	Confirm bool   `json:"confirm"`
}

func registerRouteTools(rc *regCtx) {
	deps := rc.deps
	rc.group = "routes"

	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:        "list_routes",
			Title:       "List Routes",
			Description: "List all custom routes (operator-defined URL → function mappings). Each entry has the path, target function_id, allowed methods, and creation time.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ListRoutesOutput, error) {
			rows, err := deps.DB.ListRoutes()
			if err != nil {
				return nil, ListRoutesOutput{}, err
			}
			out := ListRoutesOutput{Routes: make([]RouteView, 0, len(rows))}
			for _, r := range rows {
				out.Routes = append(out.Routes, RouteView{
					Path: r.Path, FunctionID: r.FunctionID, Methods: r.Methods, CreatedAt: r.CreatedAt,
				})
			}
			return nil, out, nil
		},
	)

	regAddTool(rc, permWrite,
		&mcpsdk.Tool{
			Name:        "set_route",
			Title:       "Set Route",
			Description: "Create or update a custom route. Use exact paths (/webhooks/stripe) for fixed URLs, or prefix paths (/shortener/*) when the function should see sub-paths. Reserved prefixes (/api/, /auth/, /web/, /_orva/, /fn/, /mcp/, /webhook/) are rejected.",
			Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in SetRouteInput) (*mcpsdk.CallToolResult, RouteOpOutput, error) {
			path := strings.TrimSpace(in.Path)
			if err := database.ValidateRoutePath(path); err != nil {
				return nil, RouteOpOutput{}, err
			}
			fn, err := resolveFunction(deps, in.FunctionID)
			if err != nil {
				return nil, RouteOpOutput{}, err
			}
			methods := in.Methods
			if methods == "" {
				methods = "*"
			}
			if err := deps.DB.UpsertRoute(path, fn.ID, methods); err != nil {
				return nil, RouteOpOutput{}, err
			}
			return nil, RouteOpOutput{Path: path, FunctionID: fn.ID}, nil
		},
	)

	regAddTool(rc, permWrite,
		&mcpsdk.Tool{
			Name:        "delete_route",
			Title:       "Delete Route",
			Description: "Remove a custom route by path. Pass confirm=true. Does not affect the function the route pointed at.",
			Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteRouteInput) (*mcpsdk.CallToolResult, RouteOpOutput, error) {
			if !in.Confirm {
				return nil, RouteOpOutput{}, errors.New("delete refused: pass confirm=true")
			}
			if err := deps.DB.DeleteRoute(in.Path); err != nil {
				return nil, RouteOpOutput{}, err
			}
			return nil, RouteOpOutput{Path: in.Path}, nil
		},
	)
}
