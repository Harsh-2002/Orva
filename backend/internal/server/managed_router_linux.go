//go:build linux

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/config"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/events"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/urlhint"
	"github.com/Harsh-2002/Orva/backend/internal/version"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
	"github.com/Harsh-2002/Orva/backend/internal/workloadhistory"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/backend/internal/workloadtrigger"
	"github.com/google/uuid"
)

var errManagedServerUnavailable = errors.New("managed server dependencies unavailable")

const managedMaxBodyBytes = 6 << 20

func managedRoutes(db *database.Database, reg *registry.Registry, authority *workloadiam.Authority, manager *pool.Manager,
	configuration *workloadconfig.Service, publication *builder.PublicationQueue, dataDir string, invoke *handlers.InvokeHandler) (http.Handler, error) {
	return managedRoutesWithAsync(db, reg, authority, manager, configuration, publication, dataDir, invoke, nil, nil, nil, nil)
}

// managedRouteDeps carries the process-level services the operator surfaces
// share with the legacy router. Every field is optional; nil/zero disables the
// matching feature rather than falling back to a legacy equivalent.
type managedRouteDeps struct {
	Hub       *events.Hub
	Metrics   *metrics.Metrics
	StartTime time.Time
	NsjailBin string
	// InvalidateKey evicts an API key hash from any authentication cache. The
	// managed authenticator reads keys live, so the default is a no-op, but
	// the REST and MCP key surfaces must share this one closure.
	InvalidateKey func(keyHash string)
	// InvalidateSession evicts a revoked browser-session token from any cache.
	// Managed authentication rereads sessions live; nil is a no-op.
	InvalidateSession func(token string)

	// recordRoute, when set (tests), is told every tier-gated route as it is
	// registered, so a table test can compare the whole surface to the legacy tiers.
	recordRoute func(pattern string, tier managedTier)
}

func (d *managedRouteDeps) invalidateKey() func(string) {
	if d == nil || d.InvalidateKey == nil {
		return func(string) {}
	}
	return d.InvalidateKey
}

func managedRoutesWithAsync(db *database.Database, reg *registry.Registry, authority *workloadiam.Authority, manager *pool.Manager,
	configuration *workloadconfig.Service, publication *builder.PublicationQueue, dataDir string, invoke *handlers.InvokeHandler,
	async *workloadasync.Service, chat func(*http.ServeMux, mcp.Deps), downloadBackup http.HandlerFunc, ops *managedRouteDeps,
) (http.Handler, error) {
	if ops == nil {
		ops = &managedRouteDeps{}
	}
	if db == nil || reg == nil || authority == nil || !manager.ManagedBy(authority) {
		return nil, errManagedServerUnavailable
	}
	if (configuration == nil) != (publication == nil) || (publication != nil && dataDir == "") {
		return nil, errManagedServerUnavailable
	}
	if async != nil && (async.DB != db || async.Authority != authority || async.CurrentArtifact == nil) {
		return nil, errManagedServerUnavailable
	}
	buildPolicy := workloadpolicy.NewManaged
	if publication != nil {
		buildPolicy = workloadpolicy.NewManagedServing
	}
	policy, err := buildPolicy(db, reg, authority, func(owner string) workloadpolicy.ArtifactState {
		artifact := manager.CurrentArtifact(owner)
		return workloadpolicy.ArtifactState{OwnerID: owner, Artifact: artifact, Known: artifact != ""}
	})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	if downloadBackup != nil {
		gateRoute(mux, db, ops, "GET /api/v1/backup", managedAdmin, downloadBackup)
	}
	authenticate := managedActivityOperator(db)
	(&workloadpolicy.ManagedHTTPAdapter{Service: policy, Authenticate: authenticate}).Register(mux)
	if invoke != nil {
		if invoke.Registry != reg || invoke.DB != db || invoke.Proxy == nil || invoke.Proxy.Pool != manager || invoke.Proxy.DB != db {
			return nil, errManagedServerUnavailable
		}
		mux.HandleFunc("/fn/{id}", invoke.ServeManagedHTTP)
		mux.HandleFunc("/fn/{id}/{rest...}", invoke.ServeManagedHTTP)
	}
	mountManagedExecutions(mux, db, reg, manager, invoke, ops)
	mountManagedObservability(mux, db, reg, manager, dataDir, ops)
	mountManagedAccess(mux, db, ops)
	var dispatch http.Handler = mux
	var triggers *workloadtrigger.Service
	if publication != nil {
		if invoke != nil {
			var err error
			triggers, err = workloadtrigger.New(db, reg, authority)
			if err != nil {
				return nil, err
			}
			dispatch = mountManagedCustomRoutes(mux, triggers, authenticate, invoke)
			// Published, attested inbound triggers only; never the legacy row lookup.
			mux.Handle("/webhook/", &handlers.InboundTriggerHandler{DB: db, Registry: reg, Pool: manager,
				Metrics: invoke.Metrics, SDKAuth: invoke.Proxy.SDKAuth, Managed: true})
		}
		webAuth := &handlers.AuthHandler{DB: db}
		mux.HandleFunc("GET /api/v1/auth/status", webAuth.Status)
		mux.HandleFunc("POST /api/v1/auth/onboard", webAuth.Onboard)
		mux.HandleFunc("POST /api/v1/auth/login", webAuth.Login)
		mux.HandleFunc("POST /api/v1/auth/logout", webAuth.Logout)
		mux.HandleFunc("GET /api/v1/auth/me", webAuth.Me)
		mux.HandleFunc("POST /api/v1/auth/refresh", webAuth.Refresh)
		mux.Handle("GET /web/", uiHandler())
		gateRoute(mux, db, ops, "GET /api/v1/iam/capabilities", managedRead, func(w http.ResponseWriter, _ *http.Request) {
			respond.JSON(w, 200, map[string]bool{"managed": true})
		})
		(&workloadconfig.HTTPAdapter{Service: configuration, Authenticate: authenticate}).Register(mux)
		(&handlers.WorkloadLifecycleHandler{Publication: publication, Authenticate: authenticate}).Register(mux)
		deploy := &handlers.FunctionHandler{Registry: reg, DB: db, DataDir: dataDir, DeployIAM: publication, RequireDeployIAM: true}
		deployments := &handlers.DeploymentHandler{DB: db}
		gateRoute(mux, db, ops, "GET /api/v1/functions", managedRead, deploy.List)
		gateRoute(mux, db, ops, "GET /api/v1/functions/{fn_id}", managedRead, deploy.Get)
		gateRoute(mux, db, ops, "GET /api/v1/functions/{fn_id}/source", managedRead, readManagedSource(db, publication))
		gateRoute(mux, db, ops, "POST /api/v1/functions", managedWrite, createManagedFunction(publication))
		gateRoute(mux, db, ops, "POST /api/v1/iam/functions", managedWrite, createManagedFunction(publication))
		gateRoute(mux, db, ops, "POST /api/v1/functions/{fn_id}/deploy", managedWrite, deploy.Deploy)
		gateRoute(mux, db, ops, "POST /api/v1/functions/{fn_id}/deploy-inline", managedWrite, deploy.DeployInline)
		gateRoute(mux, db, ops, "POST /api/v1/functions/{fn_id}/rollback", managedWrite, deploy.Rollback)
		history := &workloadhistory.Service{DB: db, Authority: authority, Registry: reg, Managed: policy,
			CurrentArtifact: manager.CurrentArtifact, InvalidateFactoryUnderGate: manager.InvalidateOwnerFactoryUnderGate}
		reg.DeletionHook = history
		(&workloadhistory.HTTPAdapter{Service: history, Authenticate: authenticate}).Register(mux)
		gateRoute(mux, db, ops, "DELETE /api/v1/functions/{fn_id}", managedWrite, deleteManagedFunction(reg, manager))
		gateRoute(mux, db, ops, "GET /api/v1/deployments/{id}", managedRead, deployments.Get)
		gateRoute(mux, db, ops, "GET /api/v1/deployments/{id}/logs", managedRead, deployments.GetLogs)
		gateRoute(mux, db, ops, "GET /api/v1/deployments/{id}/stream", managedRead, deployments.Stream)
		gateRoute(mux, db, ops, "GET /api/v1/functions/{fn_id}/deployments", managedRead, deployments.ListForFunction)
		gateRoute(mux, db, ops, "POST /api/v1/deployments/{id}/install", managedAdmin, func(w http.ResponseWriter, r *http.Request) {
			artifact, err := publication.Install(r.Context(), r.PathValue("id"))
			if err != nil {
				respond.Error(w, http.StatusConflict, "CONFLICT", "Managed deployment cannot be installed; inspect current state", "")
				return
			}
			respond.JSON(w, http.StatusOK, map[string]any{"artifact": artifact, "review_required": true})
		})
		tools := mcp.Deps{ManagedServer: true, DB: db, Registry: reg, IAM: policy, DataDir: dataDir, Configuration: configuration,
			DeployIAM: publication, RequireDeployIAM: true, WorkloadLifecycle: publication,
			AsyncEnabled: async != nil, AsyncIAM: async, CronEnabled: async != nil, CronIAM: async, History: history, Triggers: triggers,
			InvalidateKey: ops.invalidateKey()}
		tools.PoolMgr, tools.NsjailBin, tools.Version = manager, ops.NsjailBin, version.Version
		if invoke != nil {
			// Channel tokens invoke only through this managed proxy and pool.
			tools.Proxy, tools.Metrics = invoke.Proxy, invoke.Metrics
			tools.ReplayExecution = managedReplayHandler(db, reg, manager, invoke, ops).Replay
		}
		mux.Handle("/mcp", mcp.NewHandler(tools))
		if chat != nil {
			chat(mux, tools)
		}
		if async != nil {
			jobs := &handlers.JobsHandler{DB: db, Registry: reg, AsyncEnabled: true, AsyncIAM: async}
			gateRoute(mux, db, ops, "POST /api/v1/jobs", managedWrite, jobs.Enqueue)
			gateRoute(mux, db, ops, "GET /api/v1/jobs", managedRead, jobs.List)
			gateRoute(mux, db, ops, "GET /api/v1/jobs/{id}", managedRead, jobs.Get)
			gateRoute(mux, db, ops, "POST /api/v1/jobs/{id}/retry", managedWrite, jobs.Retry)
			gateRoute(mux, db, ops, "POST /api/v1/jobs/{id}/resume", managedWrite, jobs.Resume)
			gateRoute(mux, db, ops, "DELETE /api/v1/jobs/{id}", managedWrite, jobs.Delete)
			cron := &handlers.CronHandler{DB: db, Registry: reg, CronEnabled: true, CronIAM: async}
			gateRoute(mux, db, ops, "GET /api/v1/cron", managedRead, cron.ListAll)
			gateRoute(mux, db, ops, "GET /api/v1/functions/{fn_id}/cron", managedRead, cron.List)
			gateRoute(mux, db, ops, "POST /api/v1/functions/{fn_id}/cron", managedWrite, cron.Create)
			gateRoute(mux, db, ops, "PUT /api/v1/functions/{fn_id}/cron/{id}", managedWrite, cron.Update)
			gateRoute(mux, db, ops, "POST /api/v1/functions/{fn_id}/cron/{id}/resume", managedWrite, cron.Resume)
			gateRoute(mux, db, ops, "DELETE /api/v1/functions/{fn_id}/cron/{id}", managedWrite, cron.Delete)
		}
	}
	return requestIDMiddleware(bodySizeMiddleware(managedMaxBodyBytes, managedActivityMiddleware(db, ops.Hub, dispatch))), nil
}

// mountManagedExecutions mounts execution history, captured requests and
// replay. Reads need read; deletion and replay need admin. Replay dispatches
// only through the managed pool and re-checks the current Factory version.
func mountManagedExecutions(mux *http.ServeMux, db *database.Database, reg *registry.Registry, manager *pool.Manager,
	invoke *handlers.InvokeHandler, ops *managedRouteDeps,
) {
	exec := &handlers.ExecutionHandler{DB: db}
	gateRoute(mux, db, ops, "GET /api/v1/executions", managedRead, exec.List)
	gateRoute(mux, db, ops, "GET /api/v1/executions/{exec_id}", managedRead, exec.Get)
	gateRoute(mux, db, ops, "GET /api/v1/executions/{exec_id}/logs", managedRead, exec.GetLogs)
	gateRoute(mux, db, ops, "GET /api/v1/executions/{exec_id}/request", managedRead, exec.GetRequest)
	gateRoute(mux, db, ops, "DELETE /api/v1/executions/{exec_id}", managedWrite, exec.Delete)
	gateRoute(mux, db, ops, "POST /api/v1/executions/bulk-delete", managedWrite, exec.BulkDelete)
	if invoke == nil {
		return
	}
	replay := managedReplayHandler(db, reg, manager, invoke, ops)
	gateRoute(mux, db, ops, "POST /api/v1/executions/{exec_id}/replay", managedWrite, replay.Replay)
}

// mountManagedObservability mounts traces, the activity feed, the SSE event
// stream, the system endpoints and the static runtime/syscall inventory with
// the legacy permission tiers: read for views, admin for storage and vacuum.
// Health and the Prometheus /metrics alias stay unauthenticated, as on the
// legacy router, for probes and scrapers.
func mountManagedObservability(mux *http.ServeMux, db *database.Database, reg *registry.Registry, manager *pool.Manager,
	dataDir string, ops *managedRouteDeps,
) {
	met := ops.Metrics
	if met == nil {
		met = metrics.New()
	}
	traces := &handlers.TracesHandler{DB: db, Registry: reg, Metrics: met}
	gateRoute(mux, db, ops, "GET /api/v1/traces", managedRead, traces.ListTraces)
	gateRoute(mux, db, ops, "GET /api/v1/traces/{trace_id}", managedRead, traces.GetTrace)
	gateRoute(mux, db, ops, "GET /api/v1/functions/{id}/baseline", managedRead, traces.GetFunctionBaseline)
	activity := &handlers.ActivityHandler{DB: db}
	gateRoute(mux, db, ops, "GET /api/v1/activity", managedRead, activity.List)
	if ops.Hub != nil {
		gateRoute(mux, db, ops, "GET /api/v1/events", managedRead, ops.Hub.Handler())
	}
	started := ops.StartTime
	if started.IsZero() {
		started = time.Now()
	}
	system := &handlers.SystemHandler{Metrics: met, DB: db, PoolMgr: manager, Registry: reg,
		StartTime: started, NsjailBin: ops.NsjailBin}
	mux.HandleFunc("GET /api/v1/system/health", system.Health)
	mux.HandleFunc("GET /metrics", system.GetMetrics)
	gateRoute(mux, db, ops, "GET /api/v1/system/metrics", managedRead, system.GetMetrics)
	gateRoute(mux, db, ops, "GET /api/v1/system/metrics.json", managedRead, system.GetMetricsJSON)
	if dataDir != "" {
		storage := &handlers.SystemStorageHandler{DB: db, Cfg: &config.Config{Data: config.DataConfig{Dir: dataDir}}}
		gateRoute(mux, db, ops, "GET /api/v1/system/storage", managedAdmin, storage.GetStorage)
		gateRoute(mux, db, ops, "POST /api/v1/system/vacuum", managedAdmin, storage.Vacuum)
	}
	// Static inventory only: no owner, artifact or dispatch. `orva login --test`
	// probes /api/v1/runtimes before saving a key and shell completion lists
	// runtimes, so both must answer here at the legacy read tier.
	runtime := &handlers.RuntimeHandler{}
	gateRoute(mux, db, ops, "GET /api/v1/runtimes", managedRead, runtime.List)
	syscalls := &handlers.SyscallHandler{}
	gateRoute(mux, db, ops, "GET /api/v1/syscalls", managedRead, syscalls.List)
}

// mountManagedAccess mounts API keys, OAuth connected applications, browser
// sessions and agent channels. Keys and channels need admin, as on the legacy
// router; revoking a grant or a session needs write. The OAuth authorization
// server itself is not mounted: only existing grants can be listed and revoked.
func mountManagedAccess(mux *http.ServeMux, db *database.Database, ops *managedRouteDeps) {
	keys := &handlers.KeyHandler{DB: db, InvalidateKey: ops.invalidateKey()}
	gateRoute(mux, db, ops, "POST /api/v1/keys", managedAdmin, keys.Create)
	gateRoute(mux, db, ops, "GET /api/v1/keys", managedAdmin, keys.List)
	gateRoute(mux, db, ops, "DELETE /api/v1/keys/{key_id}", managedAdmin, keys.Delete)

	apps := &handlers.OAuthAppsHandler{DB: db}
	gateSessionRoute(mux, db, ops, "GET /api/v1/oauth/connected-apps", managedRead, apps.List)
	gateSessionRoute(mux, db, ops, "DELETE /api/v1/oauth/connected-apps/{id}", managedWrite, apps.Revoke)
	gateSessionRoute(mux, db, ops, "DELETE /api/v1/oauth/clients/{client_id}", managedWrite, apps.RemoveApplication)

	sessions := &handlers.AuthHandler{DB: db}
	if ops.InvalidateSession != nil {
		sessions.InvalidateSession = ops.InvalidateSession
	}
	gateSessionRoute(mux, db, ops, "GET /api/v1/auth/sessions", managedRead, sessions.Sessions)
	gateSessionRoute(mux, db, ops, "DELETE /api/v1/auth/sessions/{prefix}", managedWrite, sessions.RevokeSession)

	channels := &handlers.ChannelHandler{DB: db}
	gateRoute(mux, db, ops, "GET /api/v1/channels", managedAdmin, channels.List)
	gateRoute(mux, db, ops, "POST /api/v1/channels", managedAdmin, channels.Create)
	gateRoute(mux, db, ops, "GET /api/v1/channels/{id}", managedAdmin, channels.Get)
	gateRoute(mux, db, ops, "PATCH /api/v1/channels/{id}", managedAdmin, channels.Update)
	gateRoute(mux, db, ops, "PUT /api/v1/channels/{id}/functions", managedAdmin, channels.SetFunctions)
	gateRoute(mux, db, ops, "POST /api/v1/channels/{id}/rotate", managedAdmin, channels.Rotate)
	gateRoute(mux, db, ops, "DELETE /api/v1/channels/{id}", managedAdmin, channels.Delete)
}

func managedReplayHandler(db *database.Database, reg *registry.Registry, manager *pool.Manager,
	invoke *handlers.InvokeHandler, ops *managedRouteDeps,
) *handlers.ReplayHandler {
	replay := &handlers.ReplayHandler{DB: db, Registry: reg, Pool: manager, Metrics: invoke.Metrics,
		SDKAuth: invoke.Proxy.SDKAuth, Managed: true}
	if ops.Hub != nil {
		replay.PublishEvent = ops.Hub.Publish
	}
	return replay
}

func mountManagedCustomRoutes(mux *http.ServeMux, triggers *workloadtrigger.Service,
	authenticate func(*http.Request) (*auth.Principal, error), invoke *handlers.InvokeHandler,
) http.Handler {
	triggers.RegisterManagedRoutes(mux, authenticate)
	// Reserve whole namespaces before method matching, not via a mux catch-all.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if database.ManagedCustomRoutePath(r.URL.Path) && !strings.Contains(strings.ToLower(r.URL.RawPath), "%2f") {
			invoke.ServeManagedHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func readManagedSource(db *database.Database, publication *builder.PublicationQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("fn_id")
		if _, err := uuid.Parse(id); err != nil {
			fn, err := db.GetFunctionByName(id)
			if err != nil {
				respond.Error(w, 404, "NOT_FOUND", "Managed function unavailable", "")
				return
			}
			id = fn.ID
		}
		code, deps, err := publication.ReadSource(r.Context(), id)
		if errors.Is(err, database.ErrIAMFunctionNotFound) {
			respond.Error(w, 404, "NOT_FOUND", "Managed function unavailable", "")
			return
		}
		if err != nil {
			respond.Error(w, 503, "UNAVAILABLE", "Sealed managed source unavailable", "")
			return
		}
		respond.JSON(w, 200, map[string]string{"code": code, "dependencies": deps})
	}
}

func deleteManagedFunction(reg *registry.Registry, manager *pool.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("fn_id")
		u, err := uuid.Parse(id)
		if err != nil || u.String() != id || u.Variant() != uuid.RFC4122 || u.Version() != 7 {
			respond.Error(w, 400, "VALIDATION", "Exact managed function UUIDv7 required", "")
			return
		}
		if _, err := reg.Get(id); err != nil {
			respond.Error(w, 404, "NOT_FOUND", "Managed function unavailable", "")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		lock := manager.FunctionLock(id)
		lock.Lock()
		defer lock.Unlock()
		err = reg.DeleteWithDrain(ctx, id, func() { manager.DrainAndRemove(id) })
		switch {
		case errors.Is(err, database.ErrOperatorDenied):
			respond.Error(w, 403, "FORBIDDEN", "Current operator authority required", "")
		case errors.Is(err, database.ErrIAMConflict), errors.Is(err, database.ErrHistoryCapacity):
			respond.Error(w, 409, "CONFLICT", "Managed deletion could not reserve history; inspect current state", "")
		case err != nil:
			respond.Error(w, 503, "UNAVAILABLE", "Managed deletion refused or incomplete; inspect current state", "")
		default:
			respond.JSON(w, 200, map[string]string{"status": "deleted", "id": id})
		}
	}
}

func createManagedFunction(publication *builder.PublicationQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		create, err := decodeManagedCreation(dec)
		if err != nil {
			respond.Error(w, 400, "VALIDATION", "Invalid managed function request", "")
			return
		}
		fn, err := publication.CreateWorkload(r.Context(), create)
		switch {
		case errors.Is(err, database.ErrOperatorDenied):
			respond.Error(w, 403, "FORBIDDEN", "Current operator authority required", "")
		case errors.Is(err, database.ErrWorkloadCreateInvalid):
			respond.Error(w, 400, "VALIDATION", "Invalid managed function name, runtime or entrypoint", "")
		case errors.Is(err, database.ErrWorkloadNameConflict):
			respond.Error(w, 409, "CONFLICT", "Function name already exists", "")
		case err != nil:
			respond.Error(w, 503, "UNAVAILABLE", "Managed function creation unavailable", "")
		default:
			respond.JSON(w, 201, map[string]string{"id": fn.ID, "name": fn.Name, "runtime": fn.Runtime, "status": fn.Status})
		}
	}
}

func decodeManagedCreation(dec *json.Decoder) (database.WorkloadCreateRequest, error) {
	var in database.WorkloadCreateRequest
	invalid := database.ErrWorkloadCreateInvalid
	open, err := dec.Token()
	if err != nil || open != json.Delim('{') {
		return in, invalid
	}
	fields := map[string]*string{"name": &in.Name, "runtime": &in.Runtime, "entrypoint": &in.Entrypoint}
	for dec.More() {
		key, err := dec.Token()
		name, ok := key.(string)
		value := fields[name]
		if err != nil || !ok || value == nil {
			return in, invalid
		}
		raw, err := dec.Token()
		text, ok := raw.(string)
		if err != nil || !ok || strings.ContainsRune(text, '\uFFFD') {
			return in, invalid
		}
		*value = text
		delete(fields, name)
	}
	if close, err := dec.Token(); err != nil || close != json.Delim('}') {
		return in, invalid
	}
	if _, err := dec.Token(); err != io.EOF || fields["name"] != nil || fields["runtime"] != nil {
		return in, invalid
	}
	return in, nil
}

// Managed management rechecks API keys and UUID-backed browser sessions.
func managedOperator(db *database.Database) func(*http.Request) (*auth.Principal, error) {
	return func(r *http.Request) (*auth.Principal, error) {
		key := r.Header.Get("X-Orva-API-Key")
		if key == "" {
			bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if ok {
				key = strings.TrimSpace(bearer)
			}
		}
		if key == "" {
			return managedSessionOperator(db)(r)
		}
		if !strings.HasPrefix(key, "orva_") || strings.HasPrefix(key, "orva_chn_") ||
			strings.HasPrefix(key, "orva_oat_") || strings.HasPrefix(key, "orva_ort_") || len(key) > 128 {
			return nil, database.ErrOperatorDenied
		}
		hash := sha256.Sum256([]byte(key))
		row, err := db.GetAPIKeyByHash(hex.EncodeToString(hash[:]))
		if err != nil || (row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now())) {
			return nil, database.ErrOperatorDenied
		}
		perms := auth.PermSet{}
		for _, perm := range row.PermissionsList() {
			perms[perm] = true
		}
		return &auth.Principal{Kind: auth.KindAPIKey, ID: row.ID, Label: row.Name, Perms: perms}, nil
	}
}

// managedSessionOperator authenticates only the session cookie, with the
// same-origin proof for mutations. Routes whose handlers authorise by cookie
// (sessions, connected applications) must authenticate and attribute the same
// credential: a bearer header is never consulted, as legacy never lets one
// stand in for the cookie.
func managedSessionOperator(db *database.Database) func(*http.Request) (*auth.Principal, error) {
	return func(r *http.Request) (*auth.Principal, error) {
		cookie, err := r.Cookie("session_token")
		if err != nil || !managedSessionOrigin(r) {
			return nil, database.ErrOperatorDenied
		}
		session, err := db.ResolveSessionIdentityByToken(r.Context(), cookie.Value)
		if err != nil {
			return nil, database.ErrOperatorDenied
		}
		return &auth.Principal{Kind: auth.KindSession, ID: session.ID, Label: session.Username,
			Perms: auth.PermSet{"read": true, "write": true, "invoke": true, "admin": true}}, nil
	}
}

func managedActor(principal *auth.Principal) *Actor {
	source := "api"
	if principal.Kind == auth.KindSession {
		source = "web"
	}
	return &Actor{Source: source, ID: principal.ID, Type: string(principal.Kind), Label: principal.Label}
}

// managedActivityOperator is managedOperator for adapters that authenticate
// themselves: it also attributes the request in the activity log.
func managedActivityOperator(db *database.Database) func(*http.Request) (*auth.Principal, error) {
	inner := managedOperator(db)
	return func(r *http.Request) (*auth.Principal, error) {
		principal, err := inner(r)
		if err == nil {
			if slot, ok := r.Context().Value(actorSlotKey{}).(*actorSlot); ok {
				slot.set(managedActor(principal))
			}
		}
		return principal, err
	}
}

func managedSessionOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	return err == nil && origin == urlhint.BaseURL(r) && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// managedTier is the permission a route requires, exactly as the legacy
// router's requiredPermission defines it: GET needs read, other routes write,
// and keys, channels, the AI assistant, backup, storage and vacuum admin.
// Services that enforce stricter authority internally (deploy, deletion, jobs,
// cron, IAM, history) keep doing so at commit; the tier is only the front gate.
type managedTier string

const (
	managedRead  managedTier = "read"
	managedWrite managedTier = "write"
	managedAdmin managedTier = "admin"
)

// gateRoute registers a route behind requireManagedOperator.
func gateRoute(mux *http.ServeMux, db *database.Database, ops *managedRouteDeps, pattern string, tier managedTier, next http.HandlerFunc) {
	if ops != nil && ops.recordRoute != nil {
		ops.recordRoute(pattern, tier)
	}
	mux.HandleFunc(pattern, requireManagedOperator(db, tier, next))
}

func requireManagedOperator(db *database.Database, tier managedTier, next http.HandlerFunc) http.HandlerFunc {
	return requireManagedPrincipal(db, managedOperator(db), tier, next)
}

// gateSessionRoute registers a cookie-authorised route; see managedSessionOperator.
func gateSessionRoute(mux *http.ServeMux, db *database.Database, ops *managedRouteDeps, pattern string, tier managedTier, next http.HandlerFunc) {
	if ops != nil && ops.recordRoute != nil {
		ops.recordRoute(pattern, tier)
	}
	mux.HandleFunc(pattern, requireManagedPrincipal(db, managedSessionOperator(db), tier, next))
}

func requireManagedPrincipal(db *database.Database, authenticate func(*http.Request) (*auth.Principal, error), tier managedTier, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// A GET pattern also serves HEAD. The legacy router assigns every
		// non-GET method on an ordinary route `write`, so HEAD there needs write.
		effective := tier
		if r.Method == http.MethodHead && tier == managedRead {
			effective = managedWrite
		}
		w.Header().Set("Cache-Control", "no-store")
		principal, err := authenticate(r)
		if err != nil {
			respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Operator authentication required", "")
			return
		}
		ctx := auth.WithOperator(WithActor(r.Context(), managedActor(principal)), principal)
		ref, ok := auth.OperatorFromContext(ctx)
		if !ok || db.CheckOperatorPermission(ctx, ref, string(effective)) != nil {
			respond.Error(w, http.StatusForbidden, "FORBIDDEN", "Current operator authority required", "")
			return
		}
		next(w, r.WithContext(ctx))
	}
}
