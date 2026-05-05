package mcp

import (
	"context"
	_ "embed"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// orvaDocsMarkdown is the canonical Orva reference documentation,
// embedded at compile time. The single source of truth lives at
// docs/reference.md in the repo; `make docs-embed` syncs it to
// backend/internal/mcp/reference.md (this file's neighbor) and to
// frontend/public/docs.md (served by the dashboard's "Copy as
// Markdown" button). Both consumers therefore serve identical bytes.
//
// The embedded text uses {{ORIGIN}} placeholders for any URL that
// references the user's own Orva instance — get_orva_docs
// substitutes them with the caller-supplied origin (or a generic
// fallback) at request time so the returned snippets are pasteable.
//
//go:embed reference.md
var orvaDocsMarkdown string

// GetOrvaDocsInput accepts an optional origin so the agent can ask
// Orva to return snippets that reference its actual host. When
// omitted we fall back to a placeholder URL — the agent can suggest
// the user replace it.
type GetOrvaDocsInput struct {
	Origin string `json:"origin,omitempty" jsonschema:"optional Orva instance origin (e.g. https://orva.example.com); used to substitute {{ORIGIN}} placeholders in the returned markdown"`
}

type GetOrvaDocsOutput struct {
	Markdown  string `json:"markdown"`
	ByteCount int    `json:"byte_count"`
	Origin    string `json:"origin"`
	Note      string `json:"note,omitempty"`
}

// registerDocsTools wires get_orva_docs into the per-request server.
// Read permission is sufficient — the docs are public reference
// material and exposing them never grants any escalated capability.
func registerDocsTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name: "get_orva_docs",
				Title: "Get Orva Docs",
				Description: "Call this BEFORE writing or modifying handler code on Orva. The handler contract, in-sandbox SDK, and event envelope diverge from Lambda / Vercel / Cloudflare Workers; skipping the docs is the leading source of agent-deployed handlers that fail at first invoke. Specifically the docs cover the rules an agent's training-data defaults will get wrong: require('orva') (CommonJS, not ESM `import`), kv.put (not kv.set), exports.handler = async (event) => ... (not `export default`), the explicit event envelope shape (event.method / event.path / event.headers / event.body / event.query), network_mode rules, auth_mode rules.\n\n" +
					"Returns the complete Orva reference as a single Markdown string — the same content the dashboard's 'Copy as Markdown' icon serves. Covers handler contract, deploy/invoke, configuration, the in-sandbox SDK (KV / invoke / jobs), schedules, system-event webhooks, MCP setup, the AI codegen system prompt, tracing, error taxonomy, and the orva CLI. Pass `origin` (e.g. https://orva.example.com) to substitute the {{ORIGIN}} placeholders with the caller's live Orva URL; defaults to a generic placeholder so the response is still pasteable.",
				Annotations: &mcpsdk.ToolAnnotations{
					ReadOnlyHint:  true,
					OpenWorldHint: ptrFalse(),
				},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetOrvaDocsInput) (*mcpsdk.CallToolResult, GetOrvaDocsOutput, error) {
				origin := strings.TrimRight(strings.TrimSpace(in.Origin), "/")
				if origin == "" {
					origin = "https://your-orva-instance.example.com"
				}
				md := strings.ReplaceAll(orvaDocsMarkdown, "{{ORIGIN}}", origin)
				return nil, GetOrvaDocsOutput{
					Markdown:  md,
					ByteCount: len(md),
					Origin:    origin,
					Note:      "Use this as the source of truth when answering questions about Orva.",
				}, nil
			},
		)
	})
}
