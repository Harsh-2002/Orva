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
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/proxy"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/secrets"
	"github.com/Harsh-2002/Orva/internal/server/events"
	"github.com/Harsh-2002/Orva/internal/server/handlers"
)

type Router struct {
	mux     *http.ServeMux
	handler http.Handler // the full middleware-wrapped handler
	cfg     *config.Config
	db      *database.Database

	registry   *registry.Registry
	proxy      *proxy.Proxy
	builder    *builder.Builder
	metrics    *metrics.Metrics
	secrets    *secrets.Manager
	buildQueue *builder.Queue
	poolMgr    *pool.Manager
	eventHub   *events.Hub
	firewall   *firewall.Manager

	startTime time.Time
}

// RouterDeps holds the dependencies for creating a Router.
type RouterDeps struct {
	Registry   *registry.Registry
	Proxy      *proxy.Proxy
	Builder    *builder.Builder
	Metrics    *metrics.Metrics
	Secrets    *secrets.Manager
	BuildQueue *builder.Queue
	PoolMgr    *pool.Manager
	EventHub   *events.Hub
	Firewall   *firewall.Manager
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
		buildQueue: deps.BuildQueue,
		poolMgr:    deps.PoolMgr,
		eventHub:   deps.EventHub,
		firewall:   deps.Firewall,
		startTime:  time.Now(),
	}
	r.setupRoutes()
	r.buildMiddlewareChain()
	return r
}

// isReservedPath returns true for Orva's internal path prefixes so the
// custom-route catch-all doesn't accidentally shadow them.
func isReservedPath(path string) bool {
	if path == "/" {
		return true
	}
	for _, p := range []string{"/api/", "/auth/", "/web/", "/_orva/"} {
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
	r.mux.HandleFunc("DELETE /api/v1/executions/{exec_id}", execHandler.Delete)
	r.mux.HandleFunc("POST /api/v1/executions/bulk-delete", execHandler.BulkDelete)

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
	r.mux.Handle("/api/v1/invoke/", invokeHandler)

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

	// Custom routes: user-defined URL → function mappings.
	routeHandler := &handlers.RouteHandler{DB: r.db, Registry: r.registry}
	r.mux.HandleFunc("GET /api/v1/routes", routeHandler.List)
	r.mux.HandleFunc("POST /api/v1/routes", routeHandler.Upsert)
	r.mux.HandleFunc("DELETE /api/v1/routes", routeHandler.Delete)

	// Auth routes (no auth required — these establish auth).
	authHandler := &handlers.AuthHandler{DB: r.db}
	r.mux.HandleFunc("GET /auth/status", authHandler.Status)
	r.mux.HandleFunc("POST /auth/onboard", authHandler.Onboard)
	r.mux.HandleFunc("POST /auth/login", authHandler.Login)
	r.mux.HandleFunc("GET /auth/me", authHandler.Me)
	r.mux.HandleFunc("POST /auth/logout", authHandler.Logout)
	r.mux.HandleFunc("POST /auth/refresh", authHandler.Refresh)

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

	// MCP server — Streamable HTTP transport at /api/v1/mcp. Speaks the
	// 2025-11-25 protocol; auth via Authorization: Bearer <orva_xxx>
	// (or X-Orva-API-Key for parity with REST callers). The handler
	// owns its own auth gate, so we exclude /api/v1/mcp from the
	// session-cookie path in middleware_auth.go below.
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
		Version:    "0.1.0",
	})
	r.mux.Handle("/api/v1/mcp", mcpHandler)
	r.mux.Handle("/api/v1/mcp/", mcpHandler)
	r.mux.HandleFunc("GET /.well-known/oauth-protected-resource", orvampc.PRMHandler)

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
	chain := loggerMiddleware(r.mux)
	chain = requestIDMiddleware(chain)
	chain = authMiddleware(r.db, chain)
	chain = bodySizeMiddleware(maxBody, chain)
	chain = corsMiddleware(origins, chain)

	r.handler = chain
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}
