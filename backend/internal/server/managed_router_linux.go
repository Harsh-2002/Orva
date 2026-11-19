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
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/urlhint"
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
	return managedRoutesWithAsync(db, reg, authority, manager, configuration, publication, dataDir, invoke, nil, nil)
}

func managedRoutesWithAsync(db *database.Database, reg *registry.Registry, authority *workloadiam.Authority, manager *pool.Manager,
	configuration *workloadconfig.Service, publication *builder.PublicationQueue, dataDir string, invoke *handlers.InvokeHandler,
	async *workloadasync.Service, chat func(*http.ServeMux, mcp.Deps),
) (http.Handler, error) {
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
	authenticate := managedOperator(db)
	(&workloadpolicy.ManagedHTTPAdapter{Service: policy, Authenticate: authenticate}).Register(mux)
	if invoke != nil {
		if invoke.Registry != reg || invoke.DB != db || invoke.Proxy == nil || invoke.Proxy.Pool != manager || invoke.Proxy.DB != db {
			return nil, errManagedServerUnavailable
		}
		mux.HandleFunc("/fn/{id}", invoke.ServeManagedHTTP)
		mux.HandleFunc("/fn/{id}/{rest...}", invoke.ServeManagedHTTP)
	}
	var dispatch http.Handler = mux
	if publication != nil {
		if invoke != nil {
			triggers, err := workloadtrigger.New(db, reg, authority)
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
		mux.HandleFunc("GET /api/v1/iam/capabilities", requireManagedOperator(db, false, func(w http.ResponseWriter, _ *http.Request) {
			respond.JSON(w, 200, map[string]bool{"managed": true})
		}))
		(&workloadconfig.HTTPAdapter{Service: configuration, Authenticate: authenticate}).Register(mux)
		(&handlers.WorkloadLifecycleHandler{Publication: publication, Authenticate: authenticate}).Register(mux)
		deploy := &handlers.FunctionHandler{Registry: reg, DB: db, DataDir: dataDir, DeployIAM: publication, RequireDeployIAM: true}
		deployments := &handlers.DeploymentHandler{DB: db}
		mux.HandleFunc("GET /api/v1/functions", requireManagedOperator(db, false, deploy.List))
		mux.HandleFunc("GET /api/v1/functions/{fn_id}", requireManagedOperator(db, false, deploy.Get))
		mux.HandleFunc("GET /api/v1/functions/{fn_id}/source", requireManagedOperator(db, false, readManagedSource(db, publication)))
		mux.HandleFunc("POST /api/v1/functions", requireManagedOperator(db, false, createManagedFunction(publication)))
		mux.HandleFunc("POST /api/v1/iam/functions", requireManagedOperator(db, false, createManagedFunction(publication)))
		mux.HandleFunc("POST /api/v1/functions/{fn_id}/deploy", requireManagedOperator(db, false, deploy.Deploy))
		mux.HandleFunc("POST /api/v1/functions/{fn_id}/deploy-inline", requireManagedOperator(db, false, deploy.DeployInline))
		mux.HandleFunc("POST /api/v1/functions/{fn_id}/rollback", requireManagedOperator(db, false, deploy.Rollback))
		history := &workloadhistory.Service{DB: db, Authority: authority, Registry: reg, Managed: policy,
			CurrentArtifact: manager.CurrentArtifact, InvalidateFactoryUnderGate: manager.InvalidateOwnerFactoryUnderGate}
		reg.DeletionHook = history
		(&workloadhistory.HTTPAdapter{Service: history, Authenticate: authenticate}).Register(mux)
		mux.HandleFunc("DELETE /api/v1/functions/{fn_id}", requireManagedOperator(db, true, deleteManagedFunction(reg, manager)))
		mux.HandleFunc("GET /api/v1/deployments/{id}", requireManagedOperator(db, false, deployments.Get))
		mux.HandleFunc("GET /api/v1/deployments/{id}/logs", requireManagedOperator(db, false, deployments.GetLogs))
		mux.HandleFunc("GET /api/v1/deployments/{id}/stream", requireManagedOperator(db, false, deployments.Stream))
		mux.HandleFunc("GET /api/v1/functions/{fn_id}/deployments", requireManagedOperator(db, false, deployments.ListForFunction))
		mux.HandleFunc("POST /api/v1/deployments/{id}/install", requireManagedOperator(db, true, func(w http.ResponseWriter, r *http.Request) {
			artifact, err := publication.Install(r.Context(), r.PathValue("id"))
			if err != nil {
				respond.Error(w, http.StatusConflict, "CONFLICT", "Managed deployment cannot be installed; inspect current state", "")
				return
			}
			respond.JSON(w, http.StatusOK, map[string]any{"artifact": artifact, "review_required": true})
		}))
		tools := mcp.Deps{ManagedServer: true, DB: db, Registry: reg, IAM: policy, Configuration: configuration,
			DeployIAM: publication, RequireDeployIAM: true, WorkloadLifecycle: publication,
			AsyncEnabled: async != nil, AsyncIAM: async, CronEnabled: async != nil, CronIAM: async, History: history}
		mux.Handle("/mcp", mcp.NewHandler(tools))
		if chat != nil {
			chat(mux, tools)
		}
		if async != nil {
			jobs := &handlers.JobsHandler{DB: db, Registry: reg, AsyncEnabled: true, AsyncIAM: async}
			mux.HandleFunc("POST /api/v1/jobs", requireManagedOperator(db, true, jobs.Enqueue))
			mux.HandleFunc("GET /api/v1/jobs", requireManagedOperator(db, false, jobs.List))
			mux.HandleFunc("GET /api/v1/jobs/{id}", requireManagedOperator(db, false, jobs.Get))
			mux.HandleFunc("POST /api/v1/jobs/{id}/retry", requireManagedOperator(db, true, jobs.Retry))
			mux.HandleFunc("POST /api/v1/jobs/{id}/resume", requireManagedOperator(db, true, jobs.Resume))
			mux.HandleFunc("DELETE /api/v1/jobs/{id}", requireManagedOperator(db, true, jobs.Delete))
			cron := &handlers.CronHandler{DB: db, Registry: reg, CronEnabled: true, CronIAM: async}
			mux.HandleFunc("GET /api/v1/cron", requireManagedOperator(db, false, cron.ListAll))
			mux.HandleFunc("GET /api/v1/functions/{fn_id}/cron", requireManagedOperator(db, false, cron.List))
			mux.HandleFunc("POST /api/v1/functions/{fn_id}/cron", requireManagedOperator(db, true, cron.Create))
			mux.HandleFunc("PUT /api/v1/functions/{fn_id}/cron/{id}", requireManagedOperator(db, true, cron.Update))
			mux.HandleFunc("POST /api/v1/functions/{fn_id}/cron/{id}/resume", requireManagedOperator(db, true, cron.Resume))
			mux.HandleFunc("DELETE /api/v1/functions/{fn_id}/cron/{id}", requireManagedOperator(db, true, cron.Delete))
		}
	}
	return requestIDMiddleware(bodySizeMiddleware(managedMaxBodyBytes, dispatch)), nil
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

func managedSessionOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	return err == nil && origin == urlhint.BaseURL(r) && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func requireManagedOperator(db *database.Database, mutation bool, next http.HandlerFunc) http.HandlerFunc {
	authenticate := managedOperator(db)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		principal, err := authenticate(r)
		if err != nil {
			respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Operator authentication required", "")
			return
		}
		ctx := auth.WithOperator(WithActor(r.Context(), &Actor{
			ID: principal.ID, Type: string(principal.Kind), Label: principal.Label,
		}), principal)
		ref, ok := auth.OperatorFromContext(ctx)
		if !ok || db.CheckIAMOperator(ctx, ref, mutation) != nil {
			respond.Error(w, http.StatusForbidden, "FORBIDDEN", "Current operator authority required", "")
			return
		}
		next(w, r.WithContext(ctx))
	}
}
