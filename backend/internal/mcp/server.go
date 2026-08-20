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
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	authpkg "github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/proxy"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/server/events"
	"github.com/Harsh-2002/Orva/backend/internal/urlhint"

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

	// NsjailBin is the sandbox binary path, so system_health can report
	// whether this host can actually run functions rather than assuming it.
	NsjailBin string

	// BaseURL is the fallback canonical scheme://host used by callers outside
	// the HTTP path (notably the in-process AI agent). HTTP requests carry their
	// own base URL in context so cached MCP servers never capture one tenant's
	// host and reuse it for another.
	BaseURL string

	// AllowedOrigins is ORVA_CORS_ORIGINS. Empty, or ["*"], means any origin —
	// the default. Narrowing it also narrows /mcp.
	AllowedOrigins []string
}

// operatorServerCache holds one immutable server per permission surface. Orva
// has four permissions, so the cache is bounded at 16 entries. The MCP SDK
// Server is concurrency-safe; request-varying identity and origin live in the
// method context rather than in these cached registrations.
type operatorServerCache struct {
	mu      sync.Mutex
	servers map[uint8]*mcpsdk.Server
}

func (c *operatorServerCache) get(perms permSet, build func() *mcpsdk.Server) *mcpsdk.Server {
	key := permissionCacheKey(perms)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.servers == nil {
		c.servers = make(map[uint8]*mcpsdk.Server, 16)
	}
	if s := c.servers[key]; s != nil {
		return s
	}
	s := build()
	c.servers[key] = s
	return s
}

func permissionCacheKey(perms permSet) uint8 {
	var key uint8
	if perms.Has(permRead) {
		key |= 1 << 0
	}
	if perms.Has(permInvoke) {
		key |= 1 << 1
	}
	if perms.Has(permWrite) {
		key |= 1 << 2
	}
	if perms.Has(permAdmin) {
		key |= 1 << 3
	}
	return key
}

func buildOperatorServer(deps Deps, perms permSet) *mcpsdk.Server {
	s := mcpsdk.NewServer(
		&mcpsdk.Implementation{
			Name:    "orva",
			Version: deps.Version,
			Title:   "Orva — serverless platform",
		},
		&mcpsdk.ServerOptions{
			Instructions: serverInstructions,
			SchemaCache:  schemaCache,
		},
	)

	// Activity attribution comes from the request context, so sharing this
	// server between API keys with the same permissions cannot cross-label rows.
	s.AddReceivingMiddleware(activityMiddleware(deps), catalogEncodingMiddleware(), cacheScopeMiddleware())

	// Every operator-tool family is registered through a regCtx so the exact
	// same definitions feed the external MCP server and in-process AI registry.
	rc := serverRegCtx(s, deps, perms)
	registerSystemTools(rc)
	registerFunctionTools(rc)
	registerDeployTools(rc)
	registerInvokeTools(rc)
	registerSecretTools(rc)
	registerRouteTools(rc)
	registerKeyTools(rc)
	registerFirewallTools(rc)
	registerPoolTools(rc)
	registerCronTools(rc)
	registerKVTools(rc)
	registerJobTools(rc)
	registerWebhookTools(rc)
	registerInboundWebhookTools(rc)
	registerFixtureTools(rc)
	registerTraceTools(rc)
	registerDocsTools(rc)
	registerResources(s, deps, perms)
	return s
}

// NewHandler returns an http.Handler that speaks MCP Streamable HTTP
// at the path it's mounted under. The handler:
//   - extracts the bearer token / X-Orva-API-Key on every request
//   - resolves it against the same API-key store the REST API uses
//   - reuses one operator *Server per permission set while keeping channel
//     servers request-scoped
//   - rejects unauthenticated calls with 401 before any MCP work
//
// The result is that an agent's tool catalog is always scoped to
// what its key can actually do, which keeps planning context lean
// and removes "tool exists but errors" surprises.
func NewHandler(deps Deps) http.Handler {
	var operatorServers operatorServerCache

	getServer := func(r *http.Request) *mcpsdk.Server {
		// Channel servers remain request-scoped because their tool set and
		// instructions contain channel-specific function metadata.
		reqDeps := deps
		reqDeps.BaseURL = baseURLForContext(r.Context(), deps.BaseURL)

		// Reuse the principal the auth gate already resolved for this
		// request rather than resolving a second time. Resolving is not
		// free and not side-effect free: it hashes the token, hits the
		// DB, and queues an api_keys.last_used_at write. Under the old
		// stateful transport getServer ran once per SESSION, so the
		// duplicate cost was amortised; stateless mode runs it on every
		// single request, which would otherwise double the DB work and the
		// last-used writes on the MCP hot path.
		//
		// The fallback still resolves, so this stays correct if the SDK
		// ever calls getServer with a request that did not come through
		// the gate below. If the header is missing or invalid we still
		// return a server instance so the SDK can produce a clean 401
		// via that gate — refusing to construct one here would surface a
		// less useful "internal error".
		principal, _ := principalForRequest(reqDeps.DB, r)

		// Channel mode: register exactly the bundled functions as
		// MCP tools. Skip every operator-management register call —
		// the channel token explicitly does NOT carry Orva-mgmt
		// authority. The system prompt is per-channel so the agent
		// sees the right tool catalog framing.
		if principal != nil && principal.Kind == authpkg.KindChannel {
			instr := buildChannelInstructions(reqDeps.DB, principal.Channel)
			s := mcpsdk.NewServer(
				&mcpsdk.Implementation{
					Name:    "orva",
					Version: deps.Version,
					Title:   "Orva — " + principal.Channel.Name,
				},
				&mcpsdk.ServerOptions{Instructions: instr, SchemaCache: schemaCache},
			)
			s.AddReceivingMiddleware(activityMiddleware(reqDeps), cacheScopeMiddleware())
			registerChannelTools(s, reqDeps, principal.Channel)
			return s
		}

		// Operator path. permSet derives from the principal (api_key
		// or oauth); empty set when auth missed (the gate below
		// rejects anyway, but defending against a SDK quirk).
		var perms permSet
		if principal != nil {
			perms = principal.Perms
		} else {
			perms = permSet{}
		}

		return operatorServers.get(perms, func() *mcpsdk.Server {
			return buildOperatorServer(deps, perms)
		})
	}

	mcpHandler := mcpsdk.NewStreamableHTTPHandler(getServer, &mcpsdk.StreamableHTTPOptions{
		// Serve the 2026-07-28 protocol. The SDK refuses that version on any
		// non-stateless server (streamable.go), so with the default options
		// Orva answered every 2026-07-28 client with HTTP 400 -- "protocol
		// version is only supported on stateless HTTP servers" -- while still
		// speaking the older version fine. This one field is what makes the
		// new protocol reachable at all.
		//
		// Orva keeps no session state of its own to give up: there is no
		// session map here, and the bearer token is re-resolved to a Principal
		// on every request by the auth gate below. Cached servers contain only
		// static registrations; origin and actor identity are request context.
		//
		// It also closes a real gap. In stateful mode getServer ran once per
		// session, so the registered tool set froze at the permissions the key
		// held when the session opened; a permission downgrade mid-session was
		// not re-gated. Selecting the cache entry from freshly-resolved
		// permissions means the catalog always matches the caller's current perms.
		//
		// Legacy clients are unaffected: a stateless server still accepts the
		// old initialize handshake and negotiates the older version, it simply
		// issues no Mcp-Session-Id. GET and DELETE on /mcp become 405.
		Stateless: true,
	})

	// Compiled once, outside the handler. Mirrors middleware.go's corsMiddleware
	// so /mcp and the REST API agree on what an allowed origin is;
	// config.go already trims each element.
	allowAnyOrigin := len(deps.AllowedOrigins) == 0 || deps.AllowedOrigins[0] == "*"
	allowedOrigins := make(map[string]bool, len(deps.AllowedOrigins))
	for _, o := range deps.AllowedOrigins {
		allowedOrigins[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Origin validation — the spec mandates it for the Streamable HTTP
		// transport. A request with NO Origin is always allowed: that is every
		// non-browser MCP client, which is nearly all of them. Deliberately not
		// compared against r.Host — under DNS rebinding the attacker controls
		// both. The net effect is 401 -> 403 for a hostile browser origin,
		// since /mcp has no ambient credential to steal in the first place.
		if origin := r.Header.Get("Origin"); origin != "" && !allowAnyOrigin && !allowedOrigins[origin] {
			slog.Warn("mcp: origin rejected", "origin", origin, "allowed", deps.AllowedOrigins)
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}

		// Auth gate. We do this BEFORE handing off to the SDK so that
		// unauthenticated requests get a clean 401 with a JSON error
		// envelope matching the rest of the REST API.
		principal, ok := authenticateRequest(deps.DB, r)
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"missing or invalid bearer token")
			return
		}

		// Hand request-varying identity and origin to the cached server through
		// context. Neither value may be captured by a shared registration.
		ctx := context.WithValue(r.Context(), principalCtxKey{}, principal)
		ctx = context.WithValue(ctx, baseURLCtxKey{}, urlhint.BaseURL(r))
		mcpHandler.ServeHTTP(w, r.WithContext(ctx))
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
key/value state, subscribe to system-event webhooks, accept signed
inbound webhook POSTs, save reusable request fixtures, and inspect
system health.

A typical workflow to ship a new function:
  1. list_runtimes  — see which Python/Node/TypeScript versions are available
     (Python 3.14, Node.js 24, and TypeScript on the Node runtime in v0.4).
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
    Humans can also browse / edit this state from the dashboard at
    /web/functions/<name>/kv (or the REST mirror /api/v1/functions/<id>/kv);
    don't reach for these MCP tools when the operator is happy clicking around.
  - create_webhook / list_webhooks / test_webhook — subscribe to system events
    (deployment.failed, job.failed, cron.failed, etc.) with HMAC-signed POSTs.

v0.4 capabilities:
  - Inbound webhook triggers — list_inbound_webhooks / create_inbound_webhook /
    delete_inbound_webhook expose signed external POST endpoints that fan into
    a function. Five signature formats are supported out of the box: GitHub
    (X-Hub-Signature-256), Stripe (Stripe-Signature t=…,v1=…), Slack (v0=…),
    generic HMAC-SHA256, and unsigned (token-in-header).
  - Saved request fixtures — list_fixtures / save_fixture / delete_fixture
    plus test_function_with_fixture, a Postman-style invoke variant that
    replays a stored body+headers preset against a function.
  - Streaming responses — invoke_function transparently streams when the
    handler emits chunked output; SSE is preserved end-to-end.
  - Backup / restore — full system backup (DB + functions tree) and one-shot
    restore endpoints, exposed via the REST API and the dashboard's Settings
    page; not yet wrapped as MCP tools.
  - Settings storage + vacuum — GET /api/v1/system/storage breaks down DB,
    WAL, and functions-tree sizes; POST /api/v1/system/vacuum runs
    PRAGMA wal_checkpoint(TRUNCATE) followed by VACUUM to reclaim space.
  - TypeScript runtime — first-class TS support on the Node 24 runtime; pick
    runtime="node" and a .ts handler source.
  - /metrics histograms — orva_invocation_duration_ms_bucket{le=…} +
    orva_sandbox_spawn_duration_ms_bucket{le=…} are emitted alongside the
    legacy quantile lines, so Grafana can render heatmaps and recompute
    percentiles via histogram_quantile().

Operators can browse saved fixtures and configured inbound webhook
triggers from the dashboard (/web/functions/<name>/fixtures and
/web/functions/<name>/triggers); prefer pointing humans there over
driving these tools when the workflow is interactive.

Destructive tools (delete_*, rollback_*) require an explicit
"confirm: true" argument so a runaway loop can't accidentally delete
production state.

Function URLs:
  - Every list_functions / get_function result includes an "invoke_url"
    field — the fully-qualified canonical URL. Use it verbatim for
    HTTP invocations. Never construct URLs by concatenating "/fn/" +
    id; the MCP server already knows its public host and renders the
    correct URL per response.
  - If a function has custom routes registered, they appear in the
    "routes" array as fully-qualified URLs. Prefer a route URL when
    the human asked for the function by route path
    (e.g. "POST to /api/payments").
  - The "id" field is a UUIDv7 — pass it back to MCP tools verbatim.
    There is no separate "short id" form anymore.

Agent channels:
  Operators can bundle a subset of deployed functions under a name
  plus a static bearer token (orva_chn_<32 hex>) and hand that token
  to a third-party agent. When that token is presented at /mcp, the
  agent sees ONE invoke-only MCP tool per bundled function and
  nothing else — no list_functions, no deploy, no system_*. This is
  the right surface to expose curated function toolboxes to other
  agentic workflows. The operator manages channels at
  /web/channels in the dashboard or /api/v1/channels via REST. The
  current MCP session you're connected to is full operator power
  (you're on the API-key / OAuth path); channel-mode is what the
  external agents you spin up will see.`

// PRMHandler returns the RFC 9728 OAuth Protected Resource Metadata
// document for /mcp. Mounted at /.well-known/oauth-protected-resource
// (and the /mcp-suffixed variant some SDKs probe).
//
// The `resource` field MUST match the URL the MCP client called when
// it received a 401 (i.e. /mcp on this host) — otherwise some clients
// reject the metadata as referring to a different resource. We build
// the URL from the request's Host + scheme rather than hard-coding,
// so the same binary works on localhost, behind reverse proxies, and
// on custom domains without code changes.
//
// `authorization_servers` lists this same host: Orva is its own
// authorization server. claude.ai/ChatGPT use this to find the AS
// metadata at /.well-known/oauth-authorization-server and bootstrap
// the OAuth 2.1 flow.
func PRMHandler(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	issuer := scheme + "://" + host
	resource := issuer + "/mcp"
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{
  "resource": "` + resource + `",
  "authorization_servers": ["` + issuer + `"],
  "bearer_methods_supported": ["header"],
  "scopes_supported": ["read", "invoke", "write", "admin"],
  "resource_documentation": "https://github.com/Harsh-2002/Orva"
}`))
}

// activityMiddleware emits an activity_log row for every MCP method
// call (primarily tools/call). It runs INSIDE the JSON-RPC dispatcher,
// so the live Activity feed sees per-tool granularity even though the
// outer HTTP transport is one streaming POST to /mcp.
//
// The principal comes from request context and may be nil if auth could not
// resolve the bearer (the outer handler already returns 401 in that case).
// Otherwise its Kind / ID / Label flow into ActorType / ActorID / ActorLabel
// — which is how channel calls show up as `actor_type=channel`
// instead of being misattributed to api_key like the older synth-
// APIKey hack used to do.
// principalCtxKey carries the request's already-resolved principal from the
// auth gate to getServer. Unexported empty-struct key so nothing outside this
// package can set or spoof it.
type principalCtxKey struct{}
type baseURLCtxKey struct{}

// principalForRequest returns the principal the auth gate resolved for this
// request, falling back to a fresh resolution if it is absent.
func principalForRequest(db *database.Database, r *http.Request) (*authpkg.Principal, bool) {
	if p, ok := r.Context().Value(principalCtxKey{}).(*authpkg.Principal); ok && p != nil {
		return p, true
	}
	return authenticateRequest(db, r)
}

// baseURLForContext returns the inbound origin carried by NewHandler, falling
// back to Deps.BaseURL for non-HTTP callers such as the in-process AI registry.
func baseURLForContext(ctx context.Context, fallback string) string {
	if baseURL, ok := ctx.Value(baseURLCtxKey{}).(string); ok {
		if baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/"); baseURL != "" {
			return baseURL
		}
	}
	return strings.TrimRight(strings.TrimSpace(fallback), "/")
}

// schemaCache is process-wide on purpose.
//
// It memoises the JSON schema derived by reflection from each tool's input
// type, keyed by reflect.Type. Those types are compile-time constants, so the
// derived schema is identical for every request, every principal and every
// channel -- there is nothing per-caller in it to leak. Sharing it is what
// keeps the bounded permission variants (and request-scoped channel servers)
// from re-reflecting tool signatures during their first construction.
var schemaCache = mcpsdk.NewSchemaCache()

func activityMiddleware(deps Deps) mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			// We only attribute the protocol calls that an operator
			// would consider "actions" — tools/call and resources/read.
			// Skip catalog probes, pings, and capability negotiation: they
			// would flood the feed without telling the operator anything new.
			if !shouldRecordMCPMethod(method) {
				return next(ctx, method, req)
			}

			started := time.Now()
			result, err := next(ctx, method, req)
			elapsed := time.Since(started).Milliseconds()

			actorType, actorID, actorLabel := "", "", ""
			principal, _ := ctx.Value(principalCtxKey{}).(*authpkg.Principal)
			if principal != nil {
				actorType = principal.Kind
				actorID = principal.ID
				actorLabel = principal.Label
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
				ActorType:  actorType,
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
