// Package mcp implements an MCP (Model Context Protocol) server that
// exposes Orva's full management surface to AI agents — list/create/
// deploy/invoke/inspect functions, manage secrets/routes/keys, watch
// firewall config, and so on.
//
// The server speaks Streamable HTTP (the canonical 2026 transport;
// the old separate /sse + /message transport is deprecated and not
// implemented). Auth is static bearer over the existing API-key model
// — agents send `Authorization: Bearer <orva_xxx>` and the server
// resolves the key the same way the REST API does, then registers
// only the tools that key's permissions allow.
//
// All tool handlers call directly into Orva's existing services
// (database, registry, secrets manager, builder, etc.). MCP is a
// thin protocol adapter, not a re-implementation.
package mcp

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/firewall"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/proxy"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/secrets"
	"github.com/Harsh-2002/Orva/internal/server/events"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deps wires Orva's existing services into the MCP layer. Tool
// handlers reach into these directly — no HTTP marshaling.
type Deps struct {
	DB         *database.Database
	Registry   *registry.Registry
	Builder    *builder.Builder
	BuildQueue *builder.Queue
	PoolMgr    *pool.Manager
	Secrets    *secrets.Manager
	Proxy      *proxy.Proxy
	Firewall   *firewall.Manager
	Metrics    *metrics.Metrics
	EventHub   *events.Hub
	DataDir    string
	Version    string // Orva version string, surfaced via initialize
}

// NewHandler returns an http.Handler that speaks MCP Streamable HTTP
// at the path it's mounted under. The handler:
//   - extracts the bearer token / X-Orva-API-Key on every request
//   - resolves it against the same API-key store the REST API uses
//   - builds a per-request *Server registering only the tools the
//     key's permissions allow
//   - rejects unauthenticated calls with 401 before any MCP work
//
// The result is that an agent's tool catalog is always scoped to
// what its key can actually do, which keeps planning context lean
// and removes "tool exists but errors" surprises.
func NewHandler(deps Deps) http.Handler {
	getServer := func(r *http.Request) *mcpsdk.Server {
		// Resolve the caller's permissions from the request. If the
		// header is missing or invalid, we still return a server
		// instance so the SDK can produce a clean 401 via the auth
		// gate — refusing to construct one here would cause the SDK
		// to surface a less useful "internal error".
		perms := resolvePermissions(deps.DB, r)

		// Re-resolve the API key so we can attribute tool calls in the
		// activity log. We discard the bool — middleware on the server
		// is best-effort observability; if auth somehow degrades, we
		// log an anonymous mcp call instead of crashing.
		actorKey, _ := authenticateRequest(deps.DB, r)

		s := mcpsdk.NewServer(
			&mcpsdk.Implementation{
				Name:    "orva",
				Version: deps.Version,
				Title:   "Orva — serverless platform",
			},
			&mcpsdk.ServerOptions{
				Instructions: serverInstructions,
			},
		)

		// Activity middleware: each tools/call goes through here, so we
		// see every MCP tool invocation as a distinct row in the live
		// feed even though the underlying transport is one streaming
		// POST to /mcp. The HTTP-level loggerMiddleware would otherwise
		// only show the streaming request itself.
		s.AddReceivingMiddleware(activityMiddleware(deps, actorKey))

		registerSystemTools(s, deps, perms)
		registerFunctionTools(s, deps, perms)
		registerDeployTools(s, deps, perms)
		registerInvokeTools(s, deps, perms)
		registerSecretTools(s, deps, perms)
		registerRouteTools(s, deps, perms)
		registerKeyTools(s, deps, perms)
		registerFirewallTools(s, deps, perms)
		registerPoolTools(s, deps, perms)
		// v0.2 + v0.3: cron schedules, KV store, background jobs, and
		// system-event webhooks. Each respects the same permission gates.
		registerCronTools(s, deps, perms)
		registerKVTools(s, deps, perms)
		registerJobTools(s, deps, perms)
		registerWebhookTools(s, deps, perms)

		registerResources(s, deps, perms)

		return s
	}

	mcpHandler := mcpsdk.NewStreamableHTTPHandler(getServer, &mcpsdk.StreamableHTTPOptions{})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Origin validation — spec mandates this for the Streamable
		// HTTP transport. Our existing CORS middleware also adds the
		// Access-Control-* headers; here we just refuse browsers
		// pointing at us from the wrong origin if Origin is set.
		if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}

		// Auth gate. We do this BEFORE handing off to the SDK so that
		// unauthenticated requests get a clean 401 with a JSON error
		// envelope matching the rest of the REST API.
		if _, ok := authenticateRequest(deps.DB, r); !ok {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"missing or invalid bearer token")
			return
		}

		mcpHandler.ServeHTTP(w, r)
	})
}

// serverInstructions is sent to clients on initialize. Short, points
// the agent at the most useful tools first, and explains the "deploy
// then wait then invoke" workflow.
const serverInstructions = `Orva is a self-hosted serverless platform.

You can use these tools to do everything a developer or operator can do
from the web UI: list and create functions, deploy code, watch builds,
invoke functions, read execution logs, manage secrets, configure
custom routes, schedule cron jobs, enqueue background work, store
key/value state, subscribe to system-event webhooks, and inspect
system health.

A typical workflow to ship a new function:
  1. list_runtimes  — see which Python/Node versions are available.
  2. create_function — give it a name, runtime, and resource limits.
  3. (optional) set_secret — store API keys the function will read at runtime.
  4. (optional) update_function with network_mode="egress" if it needs to call external HTTPS.
  5. deploy_function_inline with wait=true — pass the handler source as a string.
  6. invoke_function — call it and inspect the response.
  7. get_execution_logs — read stderr if invocation failed.

v0.2 / v0.3 capabilities:
  - create_cron_schedule / list_cron_schedules — fire a function on a cron expression.
  - enqueue_job / list_jobs / retry_job — background queue with retries + exp backoff.
  - kv_get / kv_put / kv_delete / kv_list — per-function KV store with optional TTL.
  - create_webhook / list_webhooks / test_webhook — subscribe to system events
    (deployment.failed, job.failed, cron.failed, etc.) with HMAC-signed POSTs.

Destructive tools (delete_*, rollback_*) require an explicit
"confirm: true" argument so a runaway loop can't accidentally delete
production state.`

// OAuthASNotFoundHandler returns a JSON-shaped 404 for OAuth
// Authorization Server discovery endpoints (RFC 8414, OpenID Connect
// Discovery). MCP SDKs follow these endpoints AFTER our PRM as part
// of their auth-bootstrap cascade. The default Go mux returns
// `text/plain` "404 page not found" which the typescript-sdk OAuth
// handler tries to JSON.parse and crashes on, surfacing the
// confusing "Invalid OAuth error response: SyntaxError" dialog.
//
// Returning a parseable JSON envelope here lets the SDK accept the
// 404 cleanly, fall back to the static-bearer path advertised in our
// PRM (authorization_servers: []), and use whatever Authorization
// header the operator has configured for this server.
func OAuthASNotFoundHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{
  "error": "not_found",
  "error_description": "Orva uses static bearer auth — no OAuth Authorization Server. See /.well-known/oauth-protected-resource."
}`))
}

// OAuthEndpointNotSupportedHandler returns an RFC 6749-shaped error
// response for OAuth endpoints we deliberately don't implement
// (/register for Dynamic Client Registration per RFC 7591, /oauth/token,
// /oauth/authorize). The MCP TypeScript SDK calls registerClient() at
// /register when it can't find OAuth-AS metadata, then JSON-parses any
// non-2xx body via parseErrorResponse. Without this handler the
// default mux returns `text/plain` "404 page not found", JSON.parse
// throws SyntaxError, and the user sees the cryptic "HTTP 404: Invalid
// OAuth error response: SyntaxError: JSON Parse error" dialog.
//
// The OAuth error code "invalid_client" is the closest standard match
// for "this server doesn't do OAuth at all". The accompanying
// description points operators at the right configuration: add an
// Authorization header to their MCP client config rather than rely on
// dynamic client registration.
func OAuthEndpointNotSupportedHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{
  "error": "invalid_client",
  "error_description": "Orva does not support OAuth dynamic client registration or token exchange — it uses static API-key bearer auth. Configure your MCP client with: \"headers\": {\"Authorization\": \"Bearer <orva-api-key>\"}"
}`))
}

// PRMHandler returns a minimal RFC 9728 OAuth Protected Resource
// Metadata response describing the static-bearer auth mechanism.
// Mounted at /.well-known/oauth-protected-resource.
//
// The `resource` field MUST match the URL the MCP client called when
// it received a 401 (i.e. /mcp on this host) — otherwise some clients
// reject the metadata as referring to a different resource. We build
// the URL from the request's Host + scheme rather than hard-coding,
// so the same binary works on localhost, behind reverse proxies, and
// on custom domains without code changes.
func PRMHandler(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	resource := scheme + "://" + host + "/mcp"
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{
  "resource": "` + resource + `",
  "authorization_servers": [],
  "bearer_methods_supported": ["header"],
  "resource_documentation": "https://github.com/Harsh-2002/Orva"
}`))
}

// originAllowed mirrors the simple permissive policy of our CORS
// middleware. We let any non-browser origin through (typical for
// agents) and any same-origin browser request. Tighten later if
// hosted Orva needs CSRF-style protection on the MCP path.
func originAllowed(_ string) bool { return true }

// activityMiddleware emits an activity_log row for every MCP method
// call (primarily tools/call). It runs INSIDE the JSON-RPC dispatcher,
// so the live Activity feed sees per-tool granularity even though the
// outer HTTP transport is one streaming POST to /mcp.
//
// actorKey may be nil if auth couldn't resolve the bearer (the outer
// http.Handler would already have returned 401 in that case, but we
// defend by still emitting an anonymous activity row).
func activityMiddleware(deps Deps, actorKey *database.APIKey) mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			// We only attribute the protocol calls that an operator
			// would consider "actions" — tools/call, tools/list (since
			// agents probe the surface), and resources/read. Skip the
			// chatty pings and capability negotiation — they'd flood
			// the feed without telling the operator anything new.
			if !shouldRecordMCPMethod(method) {
				return next(ctx, method, req)
			}

			started := time.Now()
			result, err := next(ctx, method, req)
			elapsed := time.Since(started).Milliseconds()

			actorID, actorLabel := "", ""
			if actorKey != nil {
				actorID = actorKey.ID
				actorLabel = actorKey.Name
			}
			toolName := extractToolName(method, req)
			summary := summariseMCPCall(method, toolName)
			status := 200
			if err != nil {
				status = 500
			}

			row := database.ActivityRow{
				TS:         time.Now().UnixMilli(),
				Source:     "mcp",
				ActorType:  "api_key",
				ActorID:    actorID,
				ActorLabel: actorLabel,
				Method:     "tool",
				Path:       toolName,
				Status:     status,
				DurationMS: elapsed,
				Summary:    summary,
			}
			if deps.DB != nil {
				deps.DB.InsertActivity(row)
			}
			if deps.EventHub != nil {
				deps.EventHub.Publish(events.TypeActivity, row)
			}
			return result, err
		}
	}
}

// shouldRecordMCPMethod is the allowlist for activity emission. Keep
// it tight — list/initialize/ping spam would drown out the genuinely
// useful "tool was called" rows.
func shouldRecordMCPMethod(method string) bool {
	switch method {
	case "tools/call", "resources/read":
		return true
	}
	return false
}

// extractToolName pulls the tool name out of a tools/call request.
// For other recorded methods (resources/read), returns the method
// itself so the operator at least sees what kind of MCP call hit.
func extractToolName(method string, req mcpsdk.Request) string {
	if method != "tools/call" {
		return method
	}
	if p, ok := req.GetParams().(*mcpsdk.CallToolParamsRaw); ok {
		return p.Name
	}
	if p, ok := req.GetParams().(*mcpsdk.CallToolParams); ok {
		return p.Name
	}
	return method
}

// summariseMCPCall produces the one-line summary rendered in the
// Activity feed. Special-cased for the most common ops the operator
// will recognise; everything else falls back to the tool name.
func summariseMCPCall(method, tool string) string {
	if method == "resources/read" {
		return "mcp resource read"
	}
	return "mcp tool: " + tool
}
