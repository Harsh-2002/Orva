package mcp

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	authpkg "github.com/Harsh-2002/Orva/internal/auth"
	"github.com/Harsh-2002/Orva/internal/database"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ChannelInvokeInput is the typed envelope for channel-mode tool calls.
// Replaces the older `map[string]any` input so the schema is
// JSON-Schema-portable (no `any`, no implicit "anything goes" — every
// field is typed and described). Same shape as InvokeFunctionInput
// minus the function_id, which is bound at registration time.
type ChannelInvokeInput struct {
	Method    string            `json:"method" jsonschema:"REQUIRED — HTTP method (uppercase). GET for read endpoints, POST for write/create, PUT/PATCH for updates, DELETE for removals."`
	Path      string            `json:"path,omitempty" jsonschema:"sub-path passed to the handler as event.path; defaults to '/'."`
	Headers   map[string]string `json:"headers,omitempty" jsonschema:"request headers (lowercased on the way in)."`
	Body      *InvokeBody       `json:"body,omitempty" jsonschema:"typed request-body envelope. Omit entirely for GET/DELETE/HEAD; otherwise pass {type:'json',json:{...}} or {type:'string',string:'...'} or {type:'empty'}."`
	TimeoutMS int64             `json:"timeout_ms,omitempty" jsonschema:"override the function's configured timeout for this one call."`
}

// reservedToolPrefixes lists name prefixes channel-mode must refuse —
// either because they collide with built-in client conventions
// (`mcp__<server>__<tool>` flatten in Claude Code) or because they
// suggest privileged surfaces the channel cannot expose.
var reservedToolPrefixes = []string{
	"mcp_", "mcp__", "claude_", "chatgpt_", "cursor_",
	"system_", "default_", "_",
}

// sanitiseChannelToolName produces a portable MCP tool name from a
// function name. Rules (mirroring docs/CHECKLIST.md):
//
//   - lowercase, replace anything outside [a-z0-9_] with '_',
//     collapse runs of '_'.
//   - reject empty result.
//   - prefix with 'fn_' if the result starts with a digit.
//   - reject reserved prefixes (mcp_, mcp__, claude_, chatgpt_, …).
//   - truncate at 63 chars; on truncation append '_<8-char hash of
//     original name>' so two long names that share a prefix don't
//     collide.
//
// Returns ("", err) for inputs that can't be made portable. Callers
// should surface the error to the operator at channel-create/update
// time, not at MCP registration time.
func sanitiseChannelToolName(fnName string) (string, error) {
	original := fnName
	fnName = strings.ToLower(strings.TrimSpace(fnName))
	if fnName == "" {
		return "", fmt.Errorf("function name is empty")
	}
	var b strings.Builder
	prevUnderscore := false
	for _, r := range fnName {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevUnderscore = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		case r == '_':
			if !prevUnderscore {
				b.WriteRune('_')
			}
			prevUnderscore = true
		default:
			if !prevUnderscore {
				b.WriteRune('_')
			}
			prevUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "", fmt.Errorf("function name %q has no valid characters after sanitisation", original)
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "fn_" + out
	}
	for _, p := range reservedToolPrefixes {
		if strings.HasPrefix(out, p) {
			return "", fmt.Errorf("function name %q maps to reserved prefix %q — rename the function", original, p)
		}
	}
	if len(out) > 63 {
		// Keep the first 54 chars, then '_' + 8-char hash of the
		// original name → 63 total, deterministic, collision-resistant.
		sum := sha1.Sum([]byte(original))
		out = out[:54] + "_" + hex.EncodeToString(sum[:])[:8]
	}
	return out, nil
}

// channelToolDescription builds the self-introducing description text
// every channel-mode tool ships with. Channel agents see this with no
// surrounding context about Orva, so the description has to introduce
// the platform, the function, the input shape, and the output shape.
//
// The user-supplied summary (per-channel description override → fn
// description → fallback) is interpolated verbatim into the middle.
func channelToolDescription(fn *database.Function, channelName, summary string) string {
	if summary == "" {
		summary = fmt.Sprintf(
			"Runtime: %s. Entrypoint: %s. (No description was set on the function record — ask the operator who owns this channel for context on what the handler does.)",
			fn.Runtime, fn.Entrypoint,
		)
	}
	return fmt.Sprintf(
		"Invokes the Orva function %q on channel %q. %s\n\n"+
			"INPUT: pass `method` (REQUIRED HTTP verb — GET/POST/PUT/PATCH/DELETE), "+
			"optional `path` (default '/'), optional `headers` (map[string]string), "+
			"and optional `body` envelope (shape: {type:'json',json:{...}} | "+
			"{type:'string',string:'...'} | {type:'empty'}; omit the field entirely "+
			"for GET/DELETE/HEAD).\n\n"+
			"OUTPUT: status_code (HTTP status), headers (response headers as flat "+
			"map), body (response body as a string — caller parses JSON if needed), "+
			"execution_id (for follow-up log lookup), optional stderr (handler's "+
			"stderr if non-empty), optional orva_hint (platform diagnostic when the "+
			"handler crashed in a known shape, e.g. network sandbox blocking the SDK).",
		fn.Name, channelName, summary,
	)
}

// registerChannelTools registers exactly one MCP tool per function
// in the channel's bundle, all routed through the existing
// invokeFunction primitive. NO Orva-management tools are registered —
// the channel token has no Orva-management authority.
//
// Tool name comes from sanitiseChannelToolName (snake_case, ≤63 chars,
// reserved-prefix-safe, collision-resistant via hash suffix). Any
// function whose name can't be made portable is skipped at registration
// (the error is surfaced earlier at channel-create/update via the DAL's
// CheckChannelToolNameCollision check).
//
// Tool description self-introduces (channelToolDescription) so the
// downstream agent has full context: what platform this is, what the
// function does, what the input/output shapes look like.
//
// Tool input: typed ChannelInvokeInput envelope (method/path/headers/
// body/timeout_ms). Body uses the same InvokeBody discriminator as
// operator-mode invoke_function. v2 will support operator-pasted JSON
// Schema per function for an even tighter input shape.
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

	registered := make(map[string]struct{}, len(ref.FunctionIDs))
	for _, fnID := range ref.FunctionIDs {
		fn, err := deps.Registry.Get(fnID)
		if err != nil {
			// Function was deleted but the junction row hasn't been
			// cleaned up yet (CASCADE drops it on the next /channels
			// update). Skip rather than register a broken tool.
			continue
		}

		toolName, err := sanitiseChannelToolName(fn.Name)
		if err != nil {
			// Name can't be made portable. Skip this tool — operator
			// will see the error path at channel-create/update time.
			continue
		}
		if _, dup := registered[toolName]; dup {
			// Two functions sanitised to the same name. The DAL's
			// collision check should prevent this, but the registration
			// path has to be safe too — skip silently rather than
			// silently shadow the first registration.
			continue
		}
		registered[toolName] = struct{}{}

		summary := descByFnID[fn.ID]
		if summary == "" {
			summary = fn.Description
		}

		// Bind fn into a closure variable so the handler captures it
		// correctly (the loop variable would otherwise alias).
		fnRef := fn
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        toolName,
				Title:       fn.Name,
				Description: channelToolDescription(fn, ref.Name, summary),
				Annotations: &mcpsdk.ToolAnnotations{
					DestructiveHint: ptrFalse(),
					OpenWorldHint:   ptrTrue(), // function may call external APIs
				},
			},
			func(ctx context.Context, _ *mcpsdk.CallToolRequest, in ChannelInvokeInput) (*mcpsdk.CallToolResult, InvokeFunctionOutput, error) {
				return invokeFunction(ctx, deps, InvokeFunctionInput{
					FunctionID: fnRef.ID,
					Method:     in.Method,
					Path:       in.Path,
					Headers:    in.Headers,
					Body:       in.Body,
					TimeoutMS:  in.TimeoutMS,
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
		toolName, err := sanitiseChannelToolName(fn.Name)
		if err != nil {
			continue
		}
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
		"Pass arguments via the typed envelope (method/path/headers/body); " +
		"the function's HTTP response body is returned as a string. " +
		"No platform-management tools are available — this channel is invoke-only.")
	return b.String()
}
