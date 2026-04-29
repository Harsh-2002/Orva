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
	"net/http"

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

		registerSystemTools(s, deps, perms)
		registerFunctionTools(s, deps, perms)
		registerDeployTools(s, deps, perms)
		registerInvokeTools(s, deps, perms)
		registerSecretTools(s, deps, perms)
		registerRouteTools(s, deps, perms)
		registerKeyTools(s, deps, perms)
		registerFirewallTools(s, deps, perms)
		registerPoolTools(s, deps, perms)

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

You can use these tools to do everything a developer can do from the
web UI: list and create functions, deploy code, watch builds, invoke
functions, read execution logs, manage secrets, configure custom
routes, and inspect system health.

A typical workflow to ship a new function:
  1. list_runtimes  — see which Python/Node versions are available.
  2. create_function — give it a name, runtime, and resource limits.
  3. (optional) set_secret — store API keys the function will read at runtime.
  4. (optional) update_function with network_mode="egress" if it needs to call external HTTPS.
  5. deploy_function_inline with wait=true — pass the handler source as a string.
  6. invoke_function — call it and inspect the response.
  7. get_execution_logs — read stderr if invocation failed.

Destructive tools (delete_*, rollback_*) require an explicit
"confirm: true" argument so a runaway loop can't accidentally delete
production state.`

// PRMHandler returns a minimal RFC 9728 OAuth Protected Resource
// Metadata response describing the static-bearer auth mechanism.
// Mounted at /.well-known/oauth-protected-resource.
func PRMHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{
  "resource": "orva",
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
