//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/ai"
	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/backup"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/proxy"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/scheduler"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/server/events"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadrecovery"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
)

type managedRuntime struct {
	pool      *pool.Manager
	invoke    *handlers.InvokeHandler
	scheduler *scheduler.Scheduler
	async     *workloadasync.Service
	hub       *events.Hub
	metrics   *metrics.Metrics
	startTime time.Time
}

func newManagedRuntime(db *database.Database, reg *registry.Registry, authority *workloadiam.Authority, sm *secrets.Manager, tmpl pool.SandboxTemplate, limiter *sandbox.Limiter) *managedRuntime {
	m := metrics.New()
	sdkAuth := sdkauth.New()
	var manager *pool.Manager
	async := &workloadasync.Service{DB: db, Authority: authority, CurrentArtifact: func(owner string) string {
		return manager.CurrentArtifact(owner)
	}}
	host := sdkhost.New(db, sdkhost.WithIAM(authority),
		sdkhost.WithIAMCurrentArtifact(func(owner, artifact string) bool { return manager.IsCurrentArtifact(owner, artifact) }),
		sdkhost.WithIAMTargetState(func(owner string, worker *sandbox.Worker) workloadiam.TargetState {
			return manager.TargetState(owner, worker)
		}),
		sdkhost.WithAsyncIAM(async), sdkhost.WithCronIAM(async))

	// No SDK state is observable before the pool is constructed and a Factory installed.
	tmpl.SDKHandler, tmpl.SecretsLookup, tmpl.Metrics = host.Handle, nil, m
	manager = pool.NewManager(pool.ManagerConfig{ManagedAuthority: authority}, tmpl, db, reg, limiter)
	host.SetInvoker(&handlers.InternalInvokeHandler{DB: db, Registry: reg, Pool: manager, Metrics: m, SDKAuth: sdkAuth})
	p := &proxy.Proxy{Pool: manager, DB: db, SDKAuth: sdkAuth, Config: proxy.ProxyConfig{MaxBodyBytes: managedMaxBodyBytes}}
	hub := events.NewHub()
	reg.PublishEvent = hub.Publish
	invoke := &handlers.InvokeHandler{Registry: reg, Proxy: p, DB: db, Metrics: m, DataDir: tmpl.DataDir, Secrets: sm, PublishEvent: hub.Publish}
	s := scheduler.New(db, manager, tmpl.DataDir, nil, scheduler.WithAsyncIAM(async), scheduler.WithCronIAM(async))
	s.SetMetrics(m)
	s.SetSDKAuth(sdkAuth)
	return &managedRuntime{pool: manager, invoke: invoke, scheduler: s, async: async, hub: hub, metrics: m, startTime: time.Now()}
}

func (r *managedRuntime) routeDeps(nsjail string) *managedRouteDeps {
	return &managedRouteDeps{Hub: r.hub, Metrics: r.metrics, StartTime: r.startTime, NsjailBin: nsjail}
}

// managedServerInput is supplied only after exclusive, admission-closed
// recovery. The caller retains the DB and artifact Owner until Shutdown succeeds.
type managedServerInput struct {
	artifacts          *artifactfs.Owner
	output             builder.OutputConfig
	runtimeGenerations map[sandbox.Language]string
	dataDir, nsjail    string
	chatGuard          firewall.Guard
}

type managedServer struct {
	handler           http.Handler
	runtime           *managedRuntime
	publication       *builder.PublicationQueue
	output            *builder.OutputOwner
	stopping          atomic.Bool
	shutdownMu        sync.Mutex
	cancel            context.CancelFunc
	done              chan error
	spawnReady        *atomic.Bool
	chat              *ai.Manager
	firewall          *firewall.Manager
	buildServicesDone chan struct{}
}

// newManagedServer composes the admission-closed, opted-in serving instance.
func newManagedServer(ctx context.Context, db *database.Database, in managedServerInput) (_ *managedServer, err error) {
	if ctx == nil || db == nil || in.artifacts == nil || !filepath.IsAbs(in.dataDir) || !filepath.IsAbs(in.nsjail) ||
		in.runtimeGenerations == nil || len(in.runtimeGenerations) > 2 {
		return nil, errManagedServerUnavailable
	}
	assets, err := in.artifacts.Bindings(ctx)
	if err != nil {
		return nil, fmt.Errorf("managed artifact ownership: %w", err)
	}
	if assets.Domain == nil {
		return nil, errManagedServerUnavailable
	}
	sm := secrets.New(db, in.dataDir)
	evaluators, paused, factories, err := reconstructedFactories(ctx, db, in.artifacts, sm)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, factory := range factories {
			factory.Close()
		}
	}()
	authority, err := newReconstructedAuthority(evaluators, paused)
	if err != nil {
		return nil, err
	}
	reg := registry.New(db)
	// Operators replay captured requests; without this the capture toggle is never read.
	proxy.LoadCaptureConfig(db)
	spawnReady := &atomic.Bool{}
	runtime := newManagedRuntime(db, reg, authority, sm, pool.SandboxTemplate{
		DataDir: in.dataDir, NsjailBin: in.nsjail, DefaultSeccomp: "default", ManagedDomain: assets.Domain,
		ManagedSpawnReady: spawnReady.Load,
	}, sandbox.NewLimiter(8))
	defer func() {
		if err != nil {
			err = errors.Join(err, runtime.pool.Shutdown(context.Background()))
		}
	}()
	for owner, factory := range factories {
		if err := runtime.pool.InstallFactory(ctx, factory); err != nil {
			return nil, err
		}
		delete(factories, owner)
	}
	configuration, err := workloadconfig.NewOwnerBound(ctx, db, reg, authority, runtime.pool, sm, in.artifacts)
	if err != nil {
		return nil, err
	}
	generations := make(map[sandbox.Language]string, len(in.runtimeGenerations))
	for lang, generation := range in.runtimeGenerations {
		if lang != sandbox.Node && lang != sandbox.Python {
			return nil, errManagedServerUnavailable
		}
		lease, err := assets.Runtime.Acquire(generation)
		if err != nil {
			return nil, err
		}
		id := lease.Identity()
		lease.Release()
		if id.Kind != artifactfs.KindRuntime || id.Owner != string(lang) {
			return nil, errManagedServerUnavailable
		}
		generations[lang] = generation
	}
	in.output.CodeStore, in.output.ParentDomain = assets.Code, assets.Domain
	output, err := builder.NewOutputOwner(in.output)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, output.Close())
		}
	}()
	b := builder.New(builder.WithManagedOutput(output), builder.WithRuntimeBinding(func(_ context.Context, req builder.RuntimeRequest) (builder.RuntimeBinding, error) {
		generation := generations[req.Language]
		if generation == "" {
			return builder.RuntimeBinding{}, errManagedServerUnavailable
		}
		return builder.RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: assets.Runtime, Generation: generation}}, nil
	}))
	b.NsjailBin, b.DataDir = in.nsjail, in.dataDir
	b.DB = db
	fw := firewall.NewManager(db, in.dataDir)
	b.SetEgressPolicy(fw.EgressPolicy)
	// Every managed egress spawn resolves its policy from this same manager.
	// Without this source the pool refuses each egress spawn with
	// ErrEgressPolicyMissing, so the operator's rules would never be in force.
	runtime.pool.SetEgressPolicy(fw.EgressPolicy)
	// NSTUN loads its rules once at worker start, so a new generation only
	// reaches warm managed workers when their egress pools are retired.
	fw.SetOnPolicyChange(func(gen string) {
		retired := runtime.pool.RetireEgressPools()
		slog.Info("egress policy applied", "generation", gen, "retired_pools", retired)
		runtime.hub.Publish("firewall.policy_applied", map[string]any{
			"generation":    gen,
			"retired_pools": retired,
		})
	})
	publication, err := builder.NewPublicationQueue(b, db, reg, authority, runtime.pool, sm, assets.Code, assets.Runtime)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	var chat *ai.Manager
	defer func() {
		if err != nil {
			cancel()
			if chat != nil {
				chat.Close()
			}
			err = errors.Join(err, fw.Stop(context.Background()))
		}
	}()
	mountChat := func(mux *http.ServeMux, tools mcp.Deps) {
		chat = ai.New(db, sm, tools)
		chat.SetRequiredEgressGuard(firewall.GuardFunc(func(addr netip.Addr, port uint16) bool {
			p, err := fw.CurrentPolicy()
			return err != nil || p.Blocks(addr, port) || (in.chatGuard != nil && in.chatGuard.Blocks(addr, port))
		}), func() error {
			_, err := fw.CurrentPolicy()
			return err
		})
		registerManagedChat(mux, &AIHandler{Manager: chat, DB: db})
	}
	handler, err := managedRoutesWithAsync(db, reg, authority, runtime.pool, configuration, publication, in.dataDir,
		runtime.invoke, runtime.async, mountChat, func(w http.ResponseWriter, r *http.Request) {
			handlers.DownloadManagedBackup(w, r, db, in.dataDir, func(ctx context.Context, dst io.Writer, snapshot string) error {
				stage, err := os.MkdirTemp(in.dataDir, "snapshot-sources-")
				if err != nil {
					return err
				}
				defer os.RemoveAll(stage)
				return backup.ArchiveManagedTo(ctx, dst, in.artifacts, in.dataDir, snapshot, stage)
			})
		}, runtime.routeDeps(in.nsjail))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	buildServicesDone := make(chan struct{})
	ready := make(chan struct{})
	spawnReady.Store(true)
	go func() { done <- runtime.scheduler.RunManagedReady(runCtx, ready); close(done) }()
	select {
	case err := <-done:
		spawnReady.Store(false)
		if err == nil {
			err = errManagedServerUnavailable
		}
		return nil, err
	case <-ready:
		fw.Start(runCtx)
		gc := builder.NewBuildCacheGC(in.dataDir, db)
		gc.FnLock = runtime.pool.FunctionLock
		go func() {
			defer close(buildServicesDone)
			gc.Run(runCtx)
		}()
		publication.Start()
	}
	return &managedServer{handler: handler, runtime: runtime, publication: publication, output: output,
		cancel: cancel, done: done, spawnReady: spawnReady, chat: chat, firewall: fw, buildServicesDone: buildServicesDone}, nil
}

func (s *managedServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.stopping.Load() {
		http.Error(w, "Managed server is stopping", http.StatusServiceUnavailable)
		return
	}
	s.handler.ServeHTTP(w, r)
}

// Shutdown is called by the owner after stopping/draining its HTTP listener.
func (s *managedServer) Shutdown(ctx context.Context) error {
	s.shutdownMu.Lock()
	defer s.shutdownMu.Unlock()
	s.stopping.Store(true)
	s.spawnReady.Store(false)
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-s.buildServicesDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.publication.Shutdown(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	s.runtime.hub.Close()
	if s.chat != nil {
		s.chat.Close()
	}
	if err := s.firewall.Stop(ctx); err != nil {
		return err
	}
	if err := s.runtime.pool.Shutdown(ctx); err != nil {
		return err
	}
	if err := s.output.RetryCleanup(ctx); err != nil {
		return err
	}
	return s.output.Close()
}

func newReconstructedAuthority(evaluators []*workloadiam.Evaluator, paused []workloadiam.Metadata) (*workloadiam.Authority, error) {
	authority, err := workloadiam.NewAuthority(256, evaluators)
	if err != nil {
		return nil, err
	}
	for _, meta := range paused {
		// Recovery already established durable pause. Restore only its in-memory gate.
		cancel, err := authority.DeactivateOwnerDeferred(meta.OwnerID, meta.Revision, func() error { return nil })
		if err != nil {
			return nil, err
		}
		cancel()
	}
	return authority, nil
}

func reconstructedFactories(ctx context.Context, db *database.Database, owner *artifactfs.Owner, sm *secrets.Manager) (
	[]*workloadiam.Evaluator, []workloadiam.Metadata, map[string]*workloadspawn.Factory, error,
) {
	rows, err := db.ListFunctions(database.ListFunctionsParams{Limit: 256})
	if err != nil || rows.Total > 256 {
		return nil, nil, nil, errors.Join(errManagedServerUnavailable, err)
	}
	// Unlike a disabled mode, a staged managed server must enforce durable pause.
	var pauseMode string
	if err := db.ReadDB().QueryRowContext(ctx, `SELECT value FROM system_config WHERE key='workload_operator_pause_mode'`).Scan(&pauseMode); err != nil || pauseMode != "1" {
		return nil, nil, nil, errManagedServerUnavailable
	}
	evaluators := make([]*workloadiam.Evaluator, 0, len(rows.Functions))
	var pausedOwners []workloadiam.Metadata
	factories := make(map[string]*workloadspawn.Factory)
	ok := false
	defer func() {
		if !ok {
			for _, factory := range factories {
				factory.Close()
			}
		}
	}()
	for _, fn := range rows.Functions {
		paused, err := db.WorkloadOperatorPaused(ctx, fn.ID)
		if err != nil || (paused && fn.Status != "inactive") {
			return nil, nil, nil, errors.Join(errManagedServerUnavailable, err)
		}
		if !paused && fn.Status == "active" {
			factory, evaluator, err := workloadrecovery.PrepareCurrentDeployment(ctx, db, sm, owner, fn.ActiveDeploymentID,
				workloadspawn.FactoryLimits{MaxPreparedPlans: 16, MaxPreparedBytes: 64 << 20, MaxSnapshotBytes: 64 << 20})
			if err != nil {
				return nil, nil, nil, fmt.Errorf("managed Factory reconstruction: %w", err)
			}
			if factory.Owner() != fn.ID || !factory.Resources().SDK {
				factory.Close()
				return nil, nil, nil, artifactfs.ErrIntegrity
			}
			evaluators = append(evaluators, evaluator)
			factories[fn.ID] = factory
			continue
		}
		source, err := db.GetIAMSources(ctx, fn.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		evaluator, err := workloadiam.CompileDeny(source.Record.Metadata)
		if err != nil {
			return nil, nil, nil, err
		}
		evaluators = append(evaluators, evaluator)
		if paused {
			pausedOwners = append(pausedOwners, evaluator.Metadata())
		}
	}
	ok = true
	return evaluators, pausedOwners, factories, nil
}
