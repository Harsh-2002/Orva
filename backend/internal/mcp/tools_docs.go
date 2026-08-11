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
// This is the ONE document the agent reads — get_orva_docs always
// returns it in full. There is deliberately no per-section slicing or
// alternate doc set to maintain; a single, always-current reference is
// the contract.
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
// omitted we fall back to the instance's own base URL (if known) and
// finally a generic placeholder.
type GetOrvaDocsInput struct {
	Origin string `json:"origin,omitempty" jsonschema:"optional Orva instance origin (e.g. https://orva.example.com); used to substitute {{ORIGIN}} placeholders in the returned markdown"`
}

type GetOrvaDocsOutput struct {
	Markdown  string `json:"markdown"`
	ByteCount int    `json:"byte_count"`
	Origin    string `json:"origin"`
	Note      string `json:"note,omitempty"`
}

// docsPlaceholderOrigin is shown when no real instance origin is known.
const docsPlaceholderOrigin = "https://your-orva-instance.example.com"

// resolveDocsOrigin trims a caller-supplied origin and falls back to the generic
// placeholder when empty. Single source for the origin normalization used by
// both ReferenceMarkdown and the get_orva_docs handler so they can't drift.
func resolveDocsOrigin(origin string) string {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" {
		origin = docsPlaceholderOrigin
	}
	return origin
}

// ReferenceMarkdown returns the full embedded Orva reference with {{ORIGIN}}
// substituted by origin (or a generic placeholder when origin is empty). It is
// the same single document get_orva_docs serves, exposed for callers that need
// to inline it directly — e.g. grounding the in-product AI agent on a
// conversation's first turn. There is still only ONE document; this is just
// another way to read it.
func ReferenceMarkdown(origin string) string {
	return strings.ReplaceAll(orvaDocsMarkdown, "{{ORIGIN}}", resolveDocsOrigin(origin))
}

// registerDocsTools wires get_orva_docs into the operator server.
// Read permission is sufficient — the docs are public reference
// material and exposing them never grants any escalated capability.
func registerDocsTools(rc *regCtx) {
	rc.group = "docs"
	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:  "get_orva_docs",
			Title: "Get Orva Docs",
			Description: "The authoritative, complete Orva reference — the single source of truth. Consult it instead of guessing: Orva's handler contract, in-sandbox SDK, and event envelope diverge from Lambda / Vercel / Cloudflare Workers, so guessing wastes a deploy/invoke cycle. Call this BEFORE writing or modifying handler code, choosing a runtime/config value, or using the SDK. It covers the rules an agent's training-data defaults get wrong: require('orva') (CommonJS, not ESM import), kv.put (not kv.set), exports.handler = async (event) => ... (not export default), the event envelope (event.method / event.path / event.headers / event.body / event.query), network_mode and auth_mode rules.\n\n" +
				"Returns the entire reference as one Markdown string — the same content the dashboard's 'Copy as Markdown' icon serves: handler contract, deploy/invoke, configuration, the in-sandbox SDK (KV / invoke / jobs), schedules, system-event webhooks, MCP setup, tracing, error taxonomy, and the orva CLI. Read the whole thing; it is the current spec for this build. Pass `origin` (e.g. https://orva.example.com) to substitute the {{ORIGIN}} placeholders with the live Orva URL (defaults to this instance's URL when known, else a generic placeholder) so returned snippets are pasteable as-is.",
			Annotations: &mcpsdk.ToolAnnotations{
				ReadOnlyHint:  true,
				OpenWorldHint: ptrFalse(),
			},
		},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in GetOrvaDocsInput) (*mcpsdk.CallToolResult, GetOrvaDocsOutput, error) {
			// Caller-supplied origin wins; else the per-request base URL; else the
			// generic placeholder (applied by resolveDocsOrigin via ReferenceMarkdown).
			origin := strings.TrimRight(strings.TrimSpace(in.Origin), "/")
			if origin == "" {
				origin = baseURLForContext(ctx, rc.deps.BaseURL)
			}
			origin = resolveDocsOrigin(origin)
			md := ReferenceMarkdown(origin)
			return nil, GetOrvaDocsOutput{
				Markdown:  md,
				ByteCount: len(md),
				Origin:    origin,
				Note:      "The complete, current Orva reference — the single source of truth. Use it verbatim when answering or building.",
			}, nil
		},
	)
}
