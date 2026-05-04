package mcp

import (
	"context"
	"fmt"
	"strings"

	authpkg "github.com/Harsh-2002/Orva/internal/auth"
	"github.com/Harsh-2002/Orva/internal/database"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerChannelTools registers exactly one MCP tool per function
// in the channel's bundle, all routed through the existing
// invokeFunction primitive. NO Orva-management tools are registered —
// the channel token has no Orva-management authority.
//
// Tool name: function name with dashes converted to underscores
// (`stripe-charge` → `stripe_charge`). Collision is prevented at
// channel-create/update time by the DAL's
// CheckChannelToolNameCollision; we trust that here.
//
// Tool description: per-channel override (junction-row description)
// if set; otherwise the function's own description; otherwise a
// generic fallback so agents see SOMETHING in tools/list.
//
// Tool input schema: open object — agents pass arbitrary JSON which
// flows through invoke as the request body. v2 will support operator-
// pasted JSON Schema per function.
func registerChannelTools(s *mcpsdk.Server, deps Deps, ref *authpkg.ChannelRef) {
	if ref == nil || len(ref.FunctionIDs) == 0 {
		return
	}

	// Pull per-channel descriptions in one shot so we don't query
	// once per function during registration.
	descByFnID := make(map[string]string, len(ref.FunctionIDs))
	if rows, err := deps.DB.ListChannelFunctionRecords(ref.ID); err == nil {
		for _, cf := range rows {
			if cf.Description != "" {
				descByFnID[cf.FunctionID] = cf.Description
			}
		}
	}

	for _, fnID := range ref.FunctionIDs {
		fn, err := deps.Registry.Get(fnID)
		if err != nil {
			// Function was deleted but the junction row hasn't been
			// cleaned up yet (CASCADE drops it on the next /channels
			// update). Skip this tool rather than register a broken
			// one.
			continue
		}

		toolName := strings.ReplaceAll(fn.Name, "-", "_")
		desc := descByFnID[fn.ID]
		if desc == "" {
			desc = fn.Description
		}
		if desc == "" {
			desc = "Invoke the " + fn.Name + " function on Orva."
		}

		// Bind fn into a closure variable so the handler captures it
		// correctly (the loop variable would otherwise alias).
		fnRef := fn
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        toolName,
				Description: desc,
				Annotations: &mcpsdk.ToolAnnotations{
					DestructiveHint: ptrFalse(),
					OpenWorldHint:   ptrTrue(), // function may call external APIs
				},
			},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in map[string]any) (*mcpsdk.CallToolResult, InvokeFunctionOutput, error) {
				// Hand off to the existing invoke primitive. The
				// channel boundary already gated the surface
				// (only fns in this bundle reached here); the proxy
				// + sandbox machinery is identical to operator-
				// invoked calls.
				return invokeFunction(ctx, deps, InvokeFunctionInput{
					FunctionID: fnRef.ID,
					Method:     "POST",
					Path:       "/",
					Body:       in,
				})
			},
		)
	}
}

// buildChannelInstructions generates the per-channel serverInstructions
// block. If the channel row carries a non-empty `instructions` value
// (operator-edited override, v2 UI), use that verbatim. Otherwise build
// a focused default that lists the available tools by name + description
// so the agent's planning prompt is grounded in what's actually
// available. No platform vocabulary leaks in.
func buildChannelInstructions(db *database.Database, ref *authpkg.ChannelRef) string {
	if ref == nil {
		return ""
	}
	if ref.Instructions != "" {
		return ref.Instructions
	}

	var b strings.Builder
	fmt.Fprintf(&b, "This channel exposes the **%s** toolset on Orva.", ref.Name)
	if ref.Description != "" {
		fmt.Fprintf(&b, " %s", ref.Description)
	}
	b.WriteString("\n\nTools available:\n")

	descByFnID := make(map[string]string, len(ref.FunctionIDs))
	if rows, err := db.ListChannelFunctionRecords(ref.ID); err == nil {
		for _, cf := range rows {
			if cf.Description != "" {
				descByFnID[cf.FunctionID] = cf.Description
			}
		}
	}

	for _, fnID := range ref.FunctionIDs {
		fn, err := db.GetFunction(fnID)
		if err != nil {
			continue
		}
		toolName := strings.ReplaceAll(fn.Name, "-", "_")
		desc := descByFnID[fn.ID]
		if desc == "" {
			desc = fn.Description
		}
		if desc == "" {
			fmt.Fprintf(&b, "  - `%s`\n", toolName)
		} else {
			fmt.Fprintf(&b, "  - `%s` — %s\n", toolName, desc)
		}
	}

	b.WriteString("\nEach tool invokes a deployed serverless function. " +
		"Pass arguments as JSON; the function's HTTP response body is returned. " +
		"No platform-management tools are available — this channel is invoke-only.")
	return b.String()
}
