package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/config"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/firewall"
	orvampc "github.com/Harsh-2002/Orva/internal/mcp"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/oauth"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/proxy"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/secrets"
	"github.com/Harsh-2002/Orva/internal/server/events"
	"github.com/Harsh-2002/Orva/internal/server/handlers"
	"github.com/Harsh-2002/Orva/internal/version"
)

type Router struct {
	mux     *http.ServeMux
	handler http.Handler // the full middleware-wrapped handler
	cfg     *config.Config
	db      *database.Database

	registry      *registry.Registry
	proxy         *proxy.Proxy
	builder       *builder.Builder
	metrics       *metrics.Metrics
	secrets       *secrets.Manager
	buildQueue    *builder.Queue
	poolMgr       *pool.Manager
	eventHub      *events.Hub
	firewall      *firewall.Manager
	internalToken string

	startTime time.Time
}

// RouterDeps holds the dependencies for creating a Router.
type RouterDeps struct {
	Registry      *registry.Registry
	Proxy         *proxy.Proxy
	Builder       *builder.Builder
	Metrics       *metrics.Metrics
	Secrets       *secrets.Manager
	BuildQueue    *builder.Queue
	PoolMgr       *pool.Manager
	EventHub      *events.Hub
	Firewall      *firewall.Manager
	InternalToken string // Per-process token for kv/jobs/F2F SDK auth (Phase 3+).
}

func NewRouter(cfg *config.Config, db *database.Database, deps RouterDeps) *Router {
	r := &Router{
		mux:        http.NewServeMux(),
		cfg:        cfg,
		db:         db,
		registry:   deps.Registry,
		proxy:      deps.Proxy,
		builder:    deps.Builder,
		metrics:    deps.Metrics,
		secrets:    deps.Secrets,
		buildQueue:    deps.BuildQueue,
		poolMgr:       deps.PoolMgr,
		eventHub:      deps.EventHub,
		firewall:      deps.Firewall,
		internalToken: deps.InternalToken,
		startTime:     time.Now(),
	}
	r.setupRoutes()
	r.buildMiddlewareChain()
	return r
}

// isReservedPath returns true for Orva's internal path prefixes so the
// custom-route catch-all doesn't accidentally shadow them.
func isReservedPath(path string) bool {
	if path == "/" || path == "/mcp" {
		return true
	}
	for _, p := range []string{"/api/", "/fn/", "/mcp/", "/web/", "/_orva/", "/webhook/"} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func (r *Router) setupRoutes() {
	// System routes.
	sysHandler := &handlers.SystemHandler{
		Metrics:    r.metrics,
		DB:         r.db,
		Sandbox:    r.proxy.Sandbox,
		PoolMgr:    r.proxy.Pool,
		BuildQueue: r.buildQueue,
		Registry:   r.registry,
		StartTime:  r.startTime,
	}
	r.mux.HandleFunc("GET /api/v1/system/health", sysHandler.Health)
	r.mux.HandleFunc("GET /api/v1/system/metrics", sysHandler.GetMetrics)
	r.mux.HandleFunc("GET /api/v1/system/metrics.json", sysHandler.GetMetricsJSON)
	// Prometheus-convention root alias. authMiddleware skips any path
	// outside /api/, so this is reachable without an API key. The catch-all
	// "/" handler is keyed by exact path here so the routes table never
	// shadows it.
	r.mux.HandleFunc("GET /metrics", sysHandler.GetMetrics)

	// Round-G PR4: unified SSE stream — one connection per browser tab,
	// fans out metrics + execution + deployment events. Client filters
	// by `event:` field. See internal/server/events/ for the broker.
	if r.eventHub != nil {
		r.mux.HandleFunc("GET /api/v1/events", r.eventHub.Handler())
	}

	// Function CRUD routes.
	fnHandler := &handlers.FunctionHandler{
		Registry:   r.registry,
		Builder:    r.builder,
		DB:         r.db,
		Metrics:    r.metrics,
		DataDir:    r.cfg.Data.Dir,
		BuildQueue: r.buildQueue,
	}
	if r.poolMgr != nil {
		fnHandler.PoolRefresh = r.poolMgr.RefreshForDeploy
		fnHandler.PoolDrain = r.poolMgr.DrainAndRemove
		fnHandler.FnLock = r.poolMgr.FunctionLock
	}
	r.mux.HandleFunc("POST /api/v1/functions", fnHandler.Create)
	r.mux.HandleFunc("GET /api/v1/functions", fnHandler.List)
	r.mux.HandleFunc("GET /api/v1/functions/{fn_id}", fnHandler.Get)
	r.mux.HandleFunc("PUT /api/v1/functions/{fn_id}", fnHandler.Update)
	r.mux.HandleFunc("DELETE /api/v1/functions/{fn_id}", fnHandler.Delete)
	r.mux.HandleFunc("POST /api/v1/functions/{fn_id}/deploy", fnHandler.Deploy)
	r.mux.HandleFunc("POST /api/v1/functions/{fn_id}/deploy-inline", fnHandler.DeployInline)
	r.mux.HandleFunc("POST /api/v1/functions/{fn_id}/rollback", fnHandler.Rollback)
	r.mux.HandleFunc("GET /api/v1/functions/{fn_id}/source", fnHandler.GetSource)

	// Deployments — async build state + log streaming.
	depHandler := &handlers.DeploymentHandler{DB: r.db}
	r.mux.HandleFunc("GET /api/v1/deployments/{id}", depHandler.Get)
	r.mux.HandleFunc("GET /api/v1/deployments/{id}/logs", depHandler.GetLogs)
	r.mux.HandleFunc("GET /api/v1/deployments/{id}/stream", depHandler.Stream)
	r.mux.HandleFunc("GET /api/v1/functions/{fn_id}/deployments", depHandler.ListForFunction)

	// Execution routes.
	execHandler := &handlers.ExecutionHandler{
		DB: r.db,
	}
	r.mux.HandleFunc("GET /api/v1/executions", execHandler.List)
	r.mux.HandleFunc("GET /api/v1/executions/{exec_id}", execHandler.Get)
	r.mux.HandleFunc("GET /api/v1/executions/{exec_id}/logs", execHandler.GetLogs)
	r.mux.HandleFunc("GET /api/v1/executions/{exec_id}/request", execHandler.GetRequest)
	r.mux.HandleFunc("DELETE /api/v1/executions/{exec_id}", execHandler.Delete)
	r.mux.HandleFunc("POST /api/v1/executions/bulk-delete", execHandler.BulkDelete)

	// Replay (v0.4 A3): re-runs a captured request against the function's
	// current code. Records a fresh execution row with replay_of pointing
	// at the original; chains of replays are allowed.
	replayHandler := &handlers.ReplayHandler{
		DB:       r.db,
		Registry: r.registry,
		Pool:     r.poolMgr,
		Metrics:  r.metrics,
	}
	if r.eventHub != nil {
		replayHandler.PublishEvent = r.eventHub.Publish
	}
	r.mux.HandleFunc("POST /api/v1/executions/{exec_id}/replay", replayHandler.Replay)

	// Tracing surface — every execution row is also a span; these
	// endpoints stitch them into causal trees on demand.
	tracesHandler := &handlers.TracesHandler{DB: r.db, Registry: r.registry, Metrics: r.metrics}
	r.mux.HandleFunc("GET /api/v1/traces", tracesHandler.ListTraces)
	r.mux.HandleFunc("GET /api/v1/traces/{trace_id}", tracesHandler.GetTrace)
	r.mux.HandleFunc("GET /api/v1/functions/{id}/baseline", tracesHandler.GetFunctionBaseline)

	// Live Activity feed — historical companion to the SSE event stream.
	activityHandler := &handlers.ActivityHandler{DB: r.db}
	r.mux.HandleFunc("GET /api/v1/activity", activityHandler.List)

	// Invoke route (catch-all for invoke paths AND custom user routes).
	invokeHandler := &handlers.InvokeHandler{
		Registry:       r.registry,
		Proxy:          r.proxy,
		DB:             r.db,
		Metrics:        r.metrics,
		Secrets:        r.secrets,
		DataDir:        r.cfg.Data.Dir,
		DefaultSeccomp: r.cfg.Sandbox.SeccompPolicy,
	}
	if r.eventHub != nil {
		invokeHandler.PublishEvent = r.eventHub.Publish
	}
	r.mux.Handle("/fn/", invokeHandler)

	// Secret management (per-function, encrypted at rest).
	secretHandler := &handlers.SecretHandler{
		Secrets:  r.secrets,
		Registry: r.registry,
	}
	if r.poolMgr != nil {
		secretHandler.PoolRefresh = r.poolMgr.RefreshForDeploy
	}
	r.mux.HandleFunc("GET /api/v1/functions/{fn_id}/secrets", secretHandler.List)
	r.mux.HandleFunc("POST /api/v1/functions/{fn_id}/secrets", secretHandler.Upsert)
	r.mux.HandleFunc("DELETE /api/v1/functions/{fn_id}/secrets/{key}", secretHandler.Delete)

	// Operator-facing KV inspector (the dashboard's KV page). The
	// internal-token /api/v1/_kv/* surface stays separate for SDK calls
	// from inside sandboxes.
	kvOperatorHandler := &handlers.KVOperatorHandler{DB: r.db, Registry: r.registry}
	r.mux.HandleFunc("GET    /api/v1/functions/{fn_id}/kv", kvOperatorHandler.List)
	r.mux.HandleFunc("GET    /api/v1/functions/{fn_id}/kv/{key}", kvOperatorHandler.Get)
	r.mux.HandleFunc("PUT    /api/v1/functions/{fn_id}/kv/{key}", kvOperatorHandler.Put)
	r.mux.HandleFunc("DELETE /api/v1/functions/{fn_id}/kv/{key}", kvOperatorHandler.Delete)

	// Saved request fixtures (v0.4 B3) — Postman-style presets reused by
	// the editor's Test pane and the test_function_with_fixture MCP tool.
	// Per-(function, name) UNIQUE; PUT acts as an upsert on the {name}
	// path segment so callers don't need a separate insert/update split.
	fixtureHandler := &handlers.FixtureHandler{DB: r.db, Registry: r.registry}
	r.mux.HandleFunc("GET    /api/v1/functions/{fn_id}/fixtures",        fixtureHandler.List)
	r.mux.HandleFunc("POST   /api/v1/functions/{fn_id}/fixtures",        fixtureHandler.Create)
	r.mux.HandleFunc("GET    /api/v1/functions/{fn_id}/fixtures/{name}", fixtureHandler.Get)
	r.mux.HandleFunc("PUT    /api/v1/functions/{fn_id}/fixtures/{name}", fixtureHandler.Upsert)
	r.mux.HandleFunc("DELETE /api/v1/functions/{fn_id}/fixtures/{name}", fixtureHandler.Delete)

	// Cron schedules (per-function, fired by internal/scheduler).
	cronHandler := &handlers.CronHandler{DB: r.db, Registry: r.registry}
	r.mux.HandleFunc("GET    /api/v1/functions/{fn_id}/cron",         cronHandler.List)
	r.mux.HandleFunc("POST   /api/v1/functions/{fn_id}/cron",         cronHandler.Create)
	r.mux.HandleFunc("PUT    /api/v1/functions/{fn_id}/cron/{id}",    cronHandler.Update)
	r.mux.HandleFunc("DELETE /api/v1/functions/{fn_id}/cron/{id}",    cronHandler.Delete)
	// Dashboard "Schedules" page lists schedules across all functions.
	r.mux.HandleFunc("GET /api/v1/cron", cronHandler.ListAll)

	// Internal token used by the worker SDK (kv, jobs, F2F invoke).
	internalToken := r.internalToken

	// Per-function key/value store (Phase 3). Internal-only — auth is the
	// per-process internal token, not API keys.
	kvHandler := &handlers.KVHandler{DB: r.db, InternalToken: internalToken}
	r.mux.HandleFunc("PUT    /api/v1/_kv/{fn_id}/{key}", kvHandler.Put)
	r.mux.HandleFunc("GET    /api/v1/_kv/{fn_id}/{key}", kvHandler.Get)
	r.mux.HandleFunc("DELETE /api/v1/_kv/{fn_id}/{key}", kvHandler.Delete)
	r.mux.HandleFunc("GET    /api/v1/_kv/{fn_id}",       kvHandler.List)

	// Function-to-function calls (Phase 4). Path uses the friendly name.
	f2fHandler := &handlers.InternalInvokeHandler{
		DB: r.db, Registry: r.registry, Pool: r.poolMgr, Metrics: r.metrics, InternalToken: internalToken,
	}
	r.mux.HandleFunc("POST /api/v1/_internal/invoke/{name}", f2fHandler.Invoke)

	// Background job queue (Phase 5). Public + internal token both work.
	jobsHandler := &handlers.JobsHandler{
		DB: r.db, Registry: r.registry, InternalToken: internalToken,
	}
	r.mux.HandleFunc("POST   /api/v1/jobs",            jobsHandler.Enqueue)
	r.mux.HandleFunc("GET    /api/v1/jobs",            jobsHandler.List)
	r.mux.HandleFunc("GET    /api/v1/jobs/{id}",       jobsHandler.Get)
	r.mux.HandleFunc("POST   /api/v1/jobs/{id}/retry", jobsHandler.Retry)
	r.mux.HandleFunc("DELETE /api/v1/jobs/{id}",       jobsHandler.Delete)

	// Inbound webhook triggers (v0.4 C2a). CRUD lives under the
	// authenticated /api/v1/functions/{fn_id}/inbound-webhooks tree;
	// the public POST /webhook/{id} that external services hit is
	// registered separately below so the auth middleware skips it.
	inboundHandler := &handlers.InboundWebhookHandler{DB: r.db, Registry: r.registry}
	r.mux.HandleFunc("GET    /api/v1/functions/{fn_id}/inbound-webhooks",      inboundHandler.List)
	r.mux.HandleFunc("POST   /api/v1/functions/{fn_id}/inbound-webhooks",      inboundHandler.Create)
	r.mux.HandleFunc("GET    /api/v1/functions/{fn_id}/inbound-webhooks/{id}", inboundHandler.Get)
	r.mux.HandleFunc("PUT    /api/v1/functions/{fn_id}/inbound-webhooks/{id}", inboundHandler.Update)
	r.mux.HandleFunc("DELETE /api/v1/functions/{fn_id}/inbound-webhooks/{id}", inboundHandler.Delete)

	// Public trigger endpoint. Path is at the root (NOT under /api/v1)
	// so external callers — GitHub, Stripe, Slack, your own services —
	// don't need an API key. Authentication is the HMAC signature on
	// the request body itself.
	inboundTrigger := &handlers.InboundTriggerHandler{
		DB: r.db, Registry: r.registry, Pool: r.poolMgr, Metrics: r.metrics,
	}
	if r.eventHub != nil {
		inboundTrigger.PublishEvent = r.eventHub.Publish
	}
	r.mux.Handle("/webhook/", inboundTrigger)

	// Webhook subscriptions (Phase v0.3). Operator-managed; system
	// events fan out to subscribers via internal/scheduler's webhook
	// delivery loop.
	webhooksHandler := &handlers.WebhooksHandler{DB: r.db}
	r.mux.HandleFunc("GET    /api/v1/webhooks",                       webhooksHandler.List)
	r.mux.HandleFunc("POST   /api/v1/webhooks",                       webhooksHandler.Create)
	r.mux.HandleFunc("GET    /api/v1/webhooks/{id}",                  webhooksHandler.Get)
	r.mux.HandleFunc("PUT    /api/v1/webhooks/{id}",                  webhooksHandler.Update)
	r.mux.HandleFunc("DELETE /api/v1/webhooks/{id}",                  webhooksHandler.Delete)
	r.mux.HandleFunc("POST   /api/v1/webhooks/{id}/test",             webhooksHandler.Test)
	r.mux.HandleFunc("GET    /api/v1/webhooks/{id}/deliveries",       webhooksHandler.ListDeliveries)
	r.mux.HandleFunc("POST   /api/v1/webhooks/deliveries/{id}/retry", webhooksHandler.RetryDelivery)

	// Custom routes: user-defined URL → function mappings.
	routeHandler := &handlers.RouteHandler{DB: r.db, Registry: r.registry}
	r.mux.HandleFunc("GET /api/v1/routes", routeHandler.List)
	r.mux.HandleFunc("POST /api/v1/routes", routeHandler.Upsert)
	r.mux.HandleFunc("DELETE /api/v1/routes", routeHandler.Delete)

	// Auth routes (no auth required — these establish auth).
	authHandler := &handlers.AuthHandler{
		DB:                r.db,
		SecureCookies:     r.cfg.Security.SecureCookies,
		SessionMaxAgeSecs: r.cfg.Security.SessionDays * 24 * 60 * 60,
	}
	r.mux.HandleFunc("GET /api/v1/auth/status", authHandler.Status)
	r.mux.HandleFunc("POST /api/v1/auth/onboard", authHandler.Onboard)
	r.mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	r.mux.HandleFunc("GET /api/v1/auth/me", authHandler.Me)
	r.mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)
	r.mux.HandleFunc("POST /api/v1/auth/refresh", authHandler.Refresh)
	r.mux.HandleFunc("POST /api/v1/auth/change-password", authHandler.ChangePassword)
	r.mux.HandleFunc("GET /api/v1/auth/sessions", authHandler.Sessions)
	r.mux.HandleFunc("DELETE /api/v1/auth/sessions/{prefix}", authHandler.RevokeSession)

	// Settings → Connected applications: list + revoke OAuth grants
	// (claude.ai web, ChatGPT web, etc.) issued through /oauth/authorize.
	oauthAppsHandler := &handlers.OAuthAppsHandler{DB: r.db}
	r.mux.HandleFunc("GET /api/v1/oauth/connected-apps", oauthAppsHandler.List)
	r.mux.HandleFunc("DELETE /api/v1/oauth/connected-apps/{id}", oauthAppsHandler.Revoke)

	// Agent channels: a named bundle of N functions + a static bearer
	// token. Presenting the token at /mcp exposes ONLY those functions
	// as MCP tools (invoke-only) and nothing else. Operator-managed
	// from the dashboard's Channels page.
	channelHandler := &handlers.ChannelHandler{DB: r.db}
	r.mux.HandleFunc("GET /api/v1/channels", channelHandler.List)
	r.mux.HandleFunc("POST /api/v1/channels", channelHandler.Create)
	r.mux.HandleFunc("GET /api/v1/channels/{id}", channelHandler.Get)
	r.mux.HandleFunc("PATCH /api/v1/channels/{id}", channelHandler.Update)
	r.mux.HandleFunc("PUT /api/v1/channels/{id}/functions", channelHandler.SetFunctions)
	r.mux.HandleFunc("POST /api/v1/channels/{id}/rotate", channelHandler.Rotate)
	r.mux.HandleFunc("DELETE /api/v1/channels/{id}", channelHandler.Delete)

	// Runtime routes.
	runtimeHandler := &handlers.RuntimeHandler{}
	r.mux.HandleFunc("GET /api/v1/runtimes", runtimeHandler.List)

	// Syscall reference.
	syscallHandler := &handlers.SyscallHandler{}
	r.mux.HandleFunc("GET /api/v1/syscalls", syscallHandler.List)

	// API Key management routes.
	keyHandler := &handlers.KeyHandler{
		DB: r.db,
	}
	r.mux.HandleFunc("POST /api/v1/keys", keyHandler.Create)
	r.mux.HandleFunc("GET /api/v1/keys", keyHandler.List)
	r.mux.HandleFunc("DELETE /api/v1/keys/{key_id}", keyHandler.Delete)

	// Backup / restore — operator-only snapshot of the data dir.
	// admin permission gated in middleware_auth.go::requiredPermission.
	backupHandler := &handlers.BackupHandler{DB: r.db, Cfg: r.cfg}
	r.mux.HandleFunc("GET /api/v1/backup", backupHandler.Download)
	r.mux.HandleFunc("POST /api/v1/restore", backupHandler.Restore)

	// Storage breakdown + VACUUM. Same admin gate as backup; the VACUUM
	// path also serializes itself behind a sync.Mutex inside the
	// handler so concurrent button-mashes 409 instead of queue.
	storageHandler := &handlers.SystemStorageHandler{DB: r.db, Cfg: r.cfg}
	r.mux.HandleFunc("GET /api/v1/system/storage", storageHandler.GetStorage)
	r.mux.HandleFunc("POST /api/v1/system/vacuum", storageHandler.Vacuum)

	// Per-function autoscaler tuning. PUT/POST require admin (enforced by
	// middleware_auth.go). Errmap.go advertises this as the recovery path
	// for POOL_AT_CAPACITY, so the route must exist.
	poolCfgHandler := &handlers.PoolConfigHandler{
		DB:       r.db,
		Registry: r.registry,
	}
	if r.poolMgr != nil {
		poolCfgHandler.PoolRefresh = r.poolMgr.RefreshForDeploy
	}
	r.mux.HandleFunc("GET /api/v1/pool/config", poolCfgHandler.Get)
	r.mux.HandleFunc("PUT /api/v1/pool/config", poolCfgHandler.Upsert)
	r.mux.HandleFunc("POST /api/v1/pool/config", poolCfgHandler.Upsert)

	// Egress firewall — UI-driven blocklist. Every mutation calls
	// Manager.ForceRefresh so the operator sees changes apply live.
	fwHandler := &handlers.FirewallHandler{DB: r.db, Manager: r.firewall}
	r.mux.HandleFunc("GET /api/v1/firewall/rules", fwHandler.List)
	r.mux.HandleFunc("POST /api/v1/firewall/rules", fwHandler.Create)
	r.mux.HandleFunc("PUT /api/v1/firewall/rules/{rule_id}", fwHandler.Update)
	r.mux.HandleFunc("DELETE /api/v1/firewall/rules/{rule_id}", fwHandler.Delete)
	r.mux.HandleFunc("POST /api/v1/firewall/resolve", fwHandler.Resolve)
	r.mux.HandleFunc("GET /api/v1/firewall/dns", fwHandler.GetDNS)
	r.mux.HandleFunc("PUT /api/v1/firewall/dns", fwHandler.PutDNS)

	// MCP server — Streamable HTTP transport at /mcp. Speaks the
	// 2025-11-25 protocol; auth via Authorization: Bearer <orva_xxx>
	// (or X-Orva-API-Key for parity with REST callers). The handler
	// owns its own auth gate; /mcp does not start with /api/ so it
	// naturally bypasses middleware_auth.go.
	mcpHandler := orvampc.NewHandler(orvampc.Deps{
		DB:         r.db,
		Registry:   r.registry,
		Builder:    r.builder,
		BuildQueue: r.buildQueue,
		PoolMgr:    r.poolMgr,
		Secrets:    r.secrets,
		Proxy:      r.proxy,
		Firewall:   r.firewall,
		Metrics:    r.metrics,
		EventHub:   r.eventHub,
		DataDir:    r.cfg.Data.Dir,
		Version:    version.Version,
	})
	r.mux.Handle("/mcp", mcpHandler)
	r.mux.Handle("/mcp/", mcpHandler)
	// RFC 9728 §3.1 — clients MAY look up resource metadata at a path
	// derived from the protected resource's URL. Serve the same
	// document at both the bare and the /mcp-suffixed location so MCP
	// SDKs that try the path-aware variant first don't get a 404.
	r.mux.HandleFunc("GET /.well-known/oauth-protected-resource", orvampc.PRMHandler)
	r.mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", orvampc.PRMHandler)
	// RFC 8414 Authorization Server Metadata + OIDC discovery alias.
	// Both URLs return the same RFC 8414 document; the OIDC variant
	// adds subject_types_supported / id_token_signing_alg_values_supported
	// so ChatGPT's discovery probe validates without a fallback round-trip.
	r.mux.HandleFunc("GET /.well-known/oauth-authorization-server", handlers.OAuthAuthServerMetadataHandler)
	r.mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", handlers.OAuthAuthServerMetadataHandler)
	r.mux.HandleFunc("GET /.well-known/openid-configuration", handlers.OpenIDConfigurationHandler)
	r.mux.HandleFunc("GET /.well-known/openid-configuration/mcp", handlers.OpenIDConfigurationHandler)
	// OAuth 2.1 endpoints — DCR (RFC 7591), authorize (consent SSR),
	// token (authorization_code + refresh_token grants), revoke (RFC 7009).
	// Lets claude.ai / ChatGPT add /mcp as a custom channel via OAuth
	// without the operator pasting a bearer token.
	oauthHandler := &oauth.Handler{
		DB:            r.db,
		SecureCookies: r.cfg.Security.SecureCookies,
	}
	r.mux.HandleFunc("POST /register", oauthHandler.Register)
	r.mux.HandleFunc("GET /oauth/authorize", oauthHandler.AuthorizeGET)
	r.mux.HandleFunc("POST /oauth/authorize", oauthHandler.AuthorizePOST)
	r.mux.HandleFunc("POST /oauth/token", oauthHandler.Token)
	r.mux.HandleFunc("POST /oauth/revoke", oauthHandler.Revoke)

	// UI routes — serve the Vue SPA at /web/. No credentials are injected;
	// the UI uses /auth/onboard + /auth/login to establish a session.
	r.mux.Handle("/web/", uiHandler())
	r.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/web/", http.StatusFound)
	})

	// Catch-all for user-defined custom routes. Go's ServeMux sends any
	// path that doesn't match a more specific pattern to "/", so we use
	// this handler to look up /webhooks/stripe-style routes (exact or
	// /prefix/*) in the `routes` table and dispatch to the invoke handler.
	r.mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if isReservedPath(req.URL.Path) {
			http.NotFound(w, req)
			return
		}
		route, _, err := r.db.MatchRoute(req.URL.Path)
		if err != nil || route == nil {
			http.NotFound(w, req)
			return
		}
		invokeHandler.ServeHTTP(w, req)
	})
}

// buildMiddlewareChain builds the full middleware chain:
// CORS -> BodySize -> Auth -> RequestID -> Logger -> Handler
func (r *Router) buildMiddlewareChain() {
	maxBody := r.cfg.Server.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 6 * 1024 * 1024 // 6MB default
	}

	origins := r.cfg.Security.CORSOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}

	// Build chain from inside out: Handler -> Logger -> RequestID -> Auth -> BodySize -> CORS
	chain := loggerMiddleware(r.db, r.eventHub, r.mux)
	chain = requestIDMiddleware(chain)
	chain = authMiddleware(r.db, chain)
	chain = bodySizeMiddleware(maxBody, chain)
	chain = corsMiddleware(origins, chain)

	r.handler = chain
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}
