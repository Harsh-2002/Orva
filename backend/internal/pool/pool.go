// Package pool manages warm nsjail+adapter worker processes, one pool per
// function_id. Workers are spawned on demand, idle workers are reused for
// subsequent invocations (no fork + VM boot per request), and stale or
// errored workers are reaped in the background.
//
// The isolation guarantee is structural: a worker spawned for function A
// can never serve function B because each functionPool closes over that
// function's ExecConfig via spawnFn. There is no cross-function pooling.
package pool

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/sandbox"
)

// ManagerConfig controls global pool behaviour. All values optional — sane
// defaults are applied in NewManager.
type ManagerConfig struct {
	// DefaultMin applies when a function has no pool_config row.
	DefaultMin int
	// DefaultMax applies when a function has no pool_config row.
	DefaultMax int
	// DefaultIdleTTL applies when a function has no pool_config row.
	DefaultIdleTTL time.Duration
	// DefaultMaxUses before a worker is retired (0 = unlimited).
	DefaultMaxUses int64
	// ReapInterval between background health sweeps per pool.
	ReapInterval time.Duration
	// EagerWarmup spawns min_warm workers for every active function at
	// startup (called via PrewarmAll). When false, workers are lazy.
	EagerWarmup bool
}

// SandboxTemplate holds config shared across all pools (nsjail binary path,
// rootfs dir, default seccomp policy, data dir). Per-function values are
// merged on top when spawning.
type SandboxTemplate struct {
	NsjailBin      string
	RootfsDir      string
	DataDir        string
	DefaultSeccomp string

	// SecretsLookup is consulted at every worker spawn to inject decrypted
	// secrets as env vars. nil means "function has no secrets layer wired
	// up." On secret upsert/delete, the secret handler triggers
	// RefreshForDeploy so the next spawn picks up the new values.
	SecretsLookup func(fnID string) map[string]string

	// InternalToken is a process-lifetime token injected into every worker
	// as ORVA_INTERNAL_TOKEN. The adapter sends it as the auth header when
	// calling the KV / F2F / jobs endpoints (Phases 3, 4, 5). Validated by
	// the corresponding handlers; not exposed to user code that doesn't
	// already have access to env vars.
	InternalToken string

	// APIBaseURL is the base URL the adapter uses when making outbound
	// calls to Orva's own internal endpoints (KV / F2F / jobs). Worker
	// sandboxes can always reach localhost on the host port via the
	// loopback network, even with network_mode=none.
	APIBaseURL string

	// Metrics, when non-nil, receives sandbox spawn-duration samples on
	// every successful Spawn so the /metrics histogram has data to draw.
	// Optional so unit tests that wire pools without a metrics instance
	// don't have to fake one.
	Metrics *metrics.Metrics
}

// Manager owns all function-scoped pools.
type Manager struct {
	cfg      ManagerConfig
	tmpl     SandboxTemplate
	db       *database.Database
	reg      *registry.Registry
	limiter  *sandbox.Limiter // host-wide ceiling
	pools    sync.Map         // fnID -> *functionPool
	closing  atomic.Bool
	wg       sync.WaitGroup // reaper goroutines
	shutdown chan struct{}

	// fnLocks: per-function mutex for serializing deploy + rollback against
	// each other. Acquired at the top of Queue.runJob and at the start of
	// the rollback handler so the two paths can never interleave on the
	// same function (which would race the symlink retarget).
	fnLocks sync.Map // fnID -> *sync.Mutex

	// Autoscaler — Knative-KPA-inspired per-function scaler. Drives
	// spawn/kill based on EWMA request rate + concurrency window.
	scaler  *scaler
	hostMem *hostMemTracker
}

// FunctionLock returns a per-function mutex, lazily creating it on first
// access. Both the build queue and the rollback handler call this to
// serialize deploy/rollback on the same function. Different functions
// never block each other.
func (m *Manager) FunctionLock(fnID string) *sync.Mutex {
	v, ok := m.fnLocks.Load(fnID)
	if ok {
		return v.(*sync.Mutex)
	}
	v, _ = m.fnLocks.LoadOrStore(fnID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// AcquireResult is returned from Acquire — caller must call Release on the
// returned Worker when done.
type AcquireResult struct {
	Worker    *sandbox.Worker
	ColdStart bool // true iff Acquire spawned a new worker
}

// PoolStats is a point-in-time snapshot for metrics.
type PoolStats struct {
	FunctionID      string
	Idle            int
	Busy            int64
	Spawned         int64
	Killed          int64
	ScaleUps        int64
	ScaleDowns      int64
	RateEWMA        float64 // req/s
	LatencyEWMAms   float64 // dispatch p-avg in ms
	DynamicMax      int64   // current memory+cpu-derived cap
	Target          int
	MemUsedAvgBytes int64   // EWMA of memory.current at release; 0 if cgroups disabled
	CPUFracAvg      float64 // EWMA of CPU fraction per invocation (0–1); 0 if cgroups disabled
}

// SetSecretsLookup wires the secrets fetcher into the pool template after
// construction. Useful when the secrets manager is created later than the
// pool manager (typical wiring order in server.New).
func (m *Manager) SetSecretsLookup(fn func(fnID string) map[string]string) {
	m.tmpl.SecretsLookup = fn
}

// HostMemStats returns the (total, available, reserved) bytes for the
// admission-control accounting. Zero-ed when the tracker is disabled.
func (m *Manager) HostMemStats() (total, avail, reserved int64) {
	if m.hostMem == nil {
		return 0, 0, 0
	}
	return m.hostMem.stats()
}

var (
	// ErrManagerClosed is returned from Acquire after Shutdown.
	ErrManagerClosed = errors.New("pool manager closed")
	// ErrNoFunction is returned when the requested function doesn't exist.
	ErrNoFunction = errors.New("function not found")
	// ErrPoolAtCapacity is returned when a function's pool is at its
	// dynamic max and no idle worker became free within the request's
	// context deadline. Distinct from a function-level timeout — the fn
	// never started; the operator should raise pool_config.max_warm or
	// reduce client concurrency.
	ErrPoolAtCapacity = errors.New("pool at capacity")
	// ErrMemoryExhausted is returned when host_mem reservation would
	// breach the 80% budget. Operator should deploy fewer concurrent
	// functions or increase host RAM.
	ErrMemoryExhausted = errors.New("host memory exhausted")
	// ErrFunctionBusy is returned when the function's per-fn concurrency
	// cap is reached AND its policy is "reject". With "queue" policy
	// callers wait until a slot frees; this error is "reject" only.
	ErrFunctionBusy = errors.New("function busy")
)

// NewManager creates a pool manager. limiter is the host-wide concurrency
// ceiling (may be nil to disable host-level capping).
func NewManager(cfg ManagerConfig, tmpl SandboxTemplate, db *database.Database, reg *registry.Registry, limiter *sandbox.Limiter) *Manager {
	if cfg.DefaultMin <= 0 {
		cfg.DefaultMin = 1
	}
	if cfg.DefaultMax <= 0 {
		cfg.DefaultMax = 5
	}
	if cfg.DefaultIdleTTL <= 0 {
		// 2 min — long enough that a function with steady traffic always
		// keeps its warm pool alive, short enough that workers from a
		// one-off burst don't loiter for ten minutes. The release-path
		// prune above handles the burst case directly; this catches
		// trickier scenarios like "warm pool grew, then traffic dropped
		// to a slow trickle that keeps the pool from being idle but
		// doesn't justify the scaled-up size."
		cfg.DefaultIdleTTL = 2 * time.Minute
	}
	if cfg.ReapInterval <= 0 {
		cfg.ReapInterval = 30 * time.Second
	}
	m := &Manager{
		cfg:      cfg,
		tmpl:     tmpl,
		db:       db,
		reg:      reg,
		limiter:  limiter,
		shutdown: make(chan struct{}),
	}
	// Host-memory tracker — reserves 80% of total RAM for worker pools,
	// leaves 20% for OS + Orva heap + SQLite page cache.
	if hm, err := newHostMemTracker(0.8); err == nil {
		m.hostMem = hm
		m.scaler = newScaler(m, hm)
		go m.scaler.run()
	} else {
		slog.Warn("host memory tracker unavailable; autoscaler disabled",
			"err", err)
	}
	return m
}

// Acquire returns a worker for the given function, spawning a new one if
// the pool is empty but under its max. Caller must Release the worker.
func (m *Manager) Acquire(ctx context.Context, fnID string) (*AcquireResult, error) {
	if m.closing.Load() {
		return nil, ErrManagerClosed
	}

	// Respect the host-wide concurrency ceiling first. This prevents the
	// sum of every pool's max_warm from overwhelming the box even if each
	// pool is within its own limit. TryAcquire returns ErrTooManyRequests
	// after a 250ms grace — long enough to ride out micro-spikes, short
	// enough to fail fast under sustained saturation.
	if m.limiter != nil {
		if err := m.limiter.TryAcquire(ctx, 250*time.Millisecond); err != nil {
			return nil, err
		}
	}

	p, err := m.getOrCreatePool(fnID)
	if err != nil {
		if m.limiter != nil {
			m.limiter.Release()
		}
		return nil, err
	}

	// Per-function concurrency gate. Runs *before* worker acquire so a
	// busy function doesn't pull workers from the pool only to error
	// out. Returns ErrFunctionBusy under "reject" policy or ctx.Err()
	// if the queue wait timed out.
	if err := p.acquireSlot(ctx); err != nil {
		if m.limiter != nil {
			m.limiter.Release()
		}
		return nil, err
	}

	res, err := p.acquire(ctx)
	if err != nil {
		p.releaseSlot()
		if m.limiter != nil {
			m.limiter.Release()
		}
		return nil, err
	}
	// Bump autoscaler signal so rate EWMA reflects real traffic.
	//
	// v0.4 C1 caveat (streaming): every Acquire bumps the rate counter
	// once, but a streaming request holds the worker for the full
	// response duration (potentially up to stream_max_seconds = 300s).
	// The autoscaler will see "1 req/burst" and a long latency EWMA, so
	// Little's-Law floor inflates apparent target concurrency. For
	// mixed streaming + non-streaming workloads this can over-provision.
	// We accept the tradeoff for v1; if it becomes a real problem we
	// could weight streaming acquires differently or sample inflight
	// concurrency separately. — TODO(autoscaler-streaming-weight)
	p.recordAcquire()
	return res, nil
}

// RecordLatency feeds a per-request dispatch latency sample into the
// function's EWMA. Called by the proxy after Dispatch returns (success or
// error). Non-blocking, safe from any goroutine.
func (m *Manager) RecordLatency(fnID string, d time.Duration) {
	if v, ok := m.pools.Load(fnID); ok {
		v.(*functionPool).recordLatency(d)
	}
}

// RefreshForDeploy drains any existing workers for a function and lets the
// next Acquire lazily respawn from the new code directory. Called by the
// build queue after a successful deploy. Idempotent — no-op if the pool
// hasn't been created yet.
func (m *Manager) RefreshForDeploy(fnID string) {
	v, ok := m.pools.Load(fnID)
	if !ok {
		return
	}
	p := v.(*functionPool)
	// Drain idle workers. Busy workers will be killed on Release via the
	// isUnusable check once we flip closing to false again — simplest to
	// just kill idles and let the next Acquire spawn a fresh one.
	for {
		select {
		case w := <-p.idle:
			_ = w.Quit(200 * time.Millisecond)
			p.killed.Add(1)
			if m.hostMem != nil && p.memoryBytes > 0 {
				m.hostMem.release(p.memoryBytes)
			}
		default:
			return
		}
	}
}

// DrainAndRemove kills all idle workers for a function and removes the pool
// entry entirely. Used when a function is deleted so that Stats() no longer
// includes it and memory reservations are freed immediately.
//
// Busy workers (mid-request) are handled lazily: once the pool entry is gone
// from the map, their next Release call finds !ok and kills them there.
func (m *Manager) DrainAndRemove(fnID string) {
	v, loaded := m.pools.LoadAndDelete(fnID)
	if !loaded {
		return
	}
	p := v.(*functionPool)
	for {
		select {
		case w := <-p.idle:
			_ = w.Quit(200 * time.Millisecond)
			p.killed.Add(1)
			if m.hostMem != nil && p.memoryBytes > 0 {
				m.hostMem.release(p.memoryBytes)
			}
		default:
			return
		}
	}
}

// Release returns a worker to the pool. If err is non-nil or the worker
// died mid-request, the worker is killed instead of re-queued.
func (m *Manager) Release(fnID string, w *sandbox.Worker, reqErr error) {
	defer func() {
		if m.limiter != nil {
			m.limiter.Release()
		}
	}()
	val, ok := m.pools.Load(fnID)
	if !ok {
		if w != nil {
			_ = w.Kill()
		}
		return
	}
	p := val.(*functionPool)
	// Always free the per-fn concurrency slot, regardless of whether the
	// worker exists (Acquire may have errored after taking the slot).
	defer p.releaseSlot()
	if w == nil {
		return
	}
	p.release(w, reqErr)
}

// Shutdown quits all workers, waits for reapers, and stops accepting new
// Acquire calls. Caller passes a context with a deadline — after that the
// shutdown escalates to SIGKILL.
func (m *Manager) Shutdown(ctx context.Context) error {
	if !m.closing.CompareAndSwap(false, true) {
		return nil
	}
	// Stop the scaler first so it doesn't race us spawning fresh workers.
	if m.scaler != nil {
		m.scaler.shutdown()
	}
	close(m.shutdown)

	grace := 200 * time.Millisecond
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl) / 4; d > grace {
			grace = d
		}
	}

	var wg sync.WaitGroup
	m.pools.Range(func(_, v any) bool {
		p := v.(*functionPool)
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.drain(grace)
		}()
		return true
	})
	wg.Wait()
	m.wg.Wait()
	if m.hostMem != nil {
		m.hostMem.close()
	}
	return nil
}

// Stats returns per-function pool stats.
func (m *Manager) Stats() []PoolStats {
	out := make([]PoolStats, 0)
	m.pools.Range(func(k, v any) bool {
		p := v.(*functionPool)
		rate, lat := p.snapshotSignals()
		memUsed, cpuFrac := p.snapshotResourceUsage()
		out = append(out, PoolStats{
			FunctionID:      k.(string),
			Idle:            len(p.idle),
			Busy:            p.busy.Load(),
			Spawned:         p.spawned.Load(),
			Killed:          p.killed.Load(),
			ScaleUps:        p.scaleUps.Load(),
			ScaleDowns:      p.scaleDowns.Load(),
			RateEWMA:        rate,
			LatencyEWMAms:   lat,
			DynamicMax:      p.dynamicMax.Load(),
			Target:          p.target,
			MemUsedAvgBytes: memUsed,
			CPUFracAvg:      cpuFrac,
		})
		return true
	})
	return out
}

// PrewarmAll spawns min_warm workers for every active function. Runs with
// bounded parallelism (NumCPU*2) so startup doesn't monopolize the box.
func (m *Manager) PrewarmAll(ctx context.Context) {
	if !m.cfg.EagerWarmup || m.reg == nil {
		return
	}
	fns := m.reg.ListActive()
	if len(fns) == 0 {
		return
	}
	slog.Info("pool prewarm starting", "functions", len(fns))

	sem := make(chan struct{}, runtime.NumCPU()*2)
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Add(1)
		go func(fnID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			p, err := m.getOrCreatePool(fnID)
			if err != nil {
				slog.Warn("prewarm: get pool failed", "fn", fnID, "err", err)
				return
			}
			p.mu.Lock()
			min := p.min
			p.mu.Unlock()
			for i := 0; i < min; i++ {
				w, err := p.spawnFn(ctx)
				if err != nil {
					slog.Warn("prewarm spawn failed", "fn", fnID, "err", err)
					return
				}
				p.spawned.Add(1)
				select {
				case p.idle <- w:
				default:
					_ = w.Kill()
					return
				}
			}
		}(fn.ID)
	}
	wg.Wait()
	slog.Info("pool prewarm complete")
}

// getOrCreatePool loads the pool for fnID, creating it if missing.
func (m *Manager) getOrCreatePool(fnID string) (*functionPool, error) {
	if existing, ok := m.pools.Load(fnID); ok {
		return existing.(*functionPool), nil
	}
	fn, err := m.reg.Get(fnID)
	if err != nil {
		return nil, ErrNoFunction
	}

	minWarm := m.cfg.DefaultMin
	maxWarm := m.cfg.DefaultMax
	idleTTL := m.cfg.DefaultIdleTTL
	targetConc := 10 // pool_config default; Knative-style target concurrency per worker
	scaleToZero := false
	if cfg, err := m.db.GetPoolConfig(fnID); err == nil && cfg != nil {
		if cfg.MinWarm > 0 {
			minWarm = cfg.MinWarm
		}
		if cfg.MaxWarm > 0 {
			maxWarm = cfg.MaxWarm
		}
		if cfg.IdleTTLS > 0 {
			idleTTL = time.Duration(cfg.IdleTTLS) * time.Second
		}
		if cfg.TargetConcurrency > 0 {
			targetConc = cfg.TargetConcurrency
		}
		scaleToZero = cfg.ScaleToZero
	}

	tmpl := m.tmpl
	dataDir := tmpl.DataDir
	// Round-G mini-git: spawn from /current symlink instead of the legacy
	// flat /code dir. The symlink points at versions/<active-hash> and is
	// retargeted atomically by builder.ActivateVersion on each successful
	// deploy or rollback. nsjail binds the symlink path itself (no deref)
	// so RefreshForDeploy draining workers + the closure resolving the
	// symlink fresh on next Spawn is enough to pick up the new target.
	codeDir := dataDir + "/functions/" + fn.ID + "/current"

	// Memory budget for admission control: per-worker cgroup memory.max is
	// 1.5 × declared memory_mb (split between high+max in buildArgs).
	memoryBytes := int64(fn.MemoryMB) * 3 / 2 * 1024 * 1024
	if memoryBytes < 16*1024*1024 {
		memoryBytes = 16 * 1024 * 1024 // 16MB floor so the budget math doesn't go wild on tiny fns
	}

	// Dynamic channel size: let the scaler grow up to a safe ceiling far
	// beyond the operator's cap. If the operator's max_warm is raised at
	// runtime (future feature), we won't need to rebuild the channel.
	channelCap := maxWarm
	if channelCap < 64 {
		channelCap = 64
	}

	// Per-function concurrency cap: if set, gate every Acquire on a
	// buffered channel of that size. 0 = unlimited (no sem).
	var concSem chan struct{}
	if fn.MaxConcurrency > 0 {
		concSem = make(chan struct{}, fn.MaxConcurrency)
	}
	concPolicy := fn.ConcurrencyPolicy
	if concPolicy == "" {
		concPolicy = "queue"
	}

	p := &functionPool{
		fnID:         fnID,
		min:          minWarm,
		max:          maxWarm,
		idleTTL:      idleTTL,
		maxUses:      m.cfg.DefaultMaxUses,
		target:       targetConc,
		memoryBytes:  memoryBytes,
		scaleToZero:  scaleToZero,
		hostMem:      m.hostMem,
		idle:         make(chan *sandbox.Worker, channelCap),
		concSem:      concSem,
		concPolicy:   concPolicy,
		spawnFn: func(ctx context.Context) (*sandbox.Worker, error) {
			// Merge env at spawn time: function config + decrypted secrets.
			// We read the lookup off m (not the local tmpl copy) so that
			// SetSecretsLookup is visible to existing pools too. Secrets
			// win on key collision so an operator can override a public
			// env var via a secret without redeploy.
			env := buildEnv(fn)
			if lookup := m.tmpl.SecretsLookup; lookup != nil {
				for k, v := range lookup(fn.ID) {
					env[k] = v
				}
			}
			// Internal SDK plumbing — adapter uses these to talk to the
			// KV / F2F / jobs endpoints. Empty when running outside the
			// server (tests) so user code can probe presence to decide
			// whether to fall back.
			if m.tmpl.InternalToken != "" {
				env["ORVA_INTERNAL_TOKEN"] = m.tmpl.InternalToken
				env["ORVA_API_BASE"] = m.tmpl.APIBaseURL
			}
			start := time.Now()
			w, err := sandbox.Spawn(ctx, sandbox.ExecConfig{
				Language:       sandbox.Language(fn.Runtime),
				CodeDir:        codeDir,
				MemoryMB:       int(fn.MemoryMB),
				MaxCPUs:        fn.CPUs,
				Env:            env,
				SeccompPolicy:  sandbox.BuildSeccompPolicy(tmpl.DefaultSeccomp, nil, nil),
				NetworkMode:    fn.NetworkMode,
				// Operator-managed DNS for egress sandboxes; written by
				// internal/firewall on every refresh tick. Bound at
				// /etc/resolv.conf and /etc/hosts when present.
				ResolvConfPath: dataDir + "/firewall/resolv.conf",
				HostsPath:      dataDir + "/firewall/hosts",
				NsjailBin:      tmpl.NsjailBin,
				RootfsDir:      tmpl.RootfsDir,
				Timeout:        time.Duration(fn.TimeoutMS) * time.Millisecond,
			})
			if err == nil && m.tmpl.Metrics != nil {
				m.tmpl.Metrics.RecordSpawnDuration(time.Since(start))
			}
			return w, err
		},
	}

	// Store — but if another goroutine raced us, discard our pool.
	actual, loaded := m.pools.LoadOrStore(fnID, p)
	if loaded {
		return actual.(*functionPool), nil
	}

	// Initialise the autoscaler signal ring for this pool.
	if m.scaler != nil {
		m.scaler.ensureSignals(p)
	}

	// Start the background reaper for this pool.
	m.wg.Add(1)
	go m.reap(p)
	return p, nil
}

// reap periodically drains the idle channel, kills expired workers, and
// pushes live ones back.
func (m *Manager) reap(p *functionPool) {
	defer m.wg.Done()
	ticker := time.NewTicker(m.cfg.ReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.sweep(m.cfg.DefaultMaxUses)
		case <-m.shutdown:
			return
		}
	}
}

// buildEnv merges function env_vars into the sandbox env map. Secrets are
// injected by the caller (invoke handler) because they require the secret
// manager; buildEnv only carries what the function config declared.
func buildEnv(fn *database.Function) map[string]string {
	env := map[string]string{}
	for k, v := range fn.EnvVars {
		env[k] = v
	}
	env["ORVA_FUNCTION_ID"] = fn.ID
	env["ORVA_MEMORY_MB"] = fmt.Sprintf("%d", fn.MemoryMB)
	// Tell the Node / Python adapter which file to load. The builder
	// rewrites this during a TypeScript build to "<outDir>/<stem>.js"
	// (e.g. "dist/handler.js") so the worker requires the compiled
	// artifact rather than the raw .ts source. For non-TS deploys this
	// is just the user-supplied entrypoint (handler.js / handler.py),
	// matching what the adapter would have defaulted to anyway.
	if fn.Entrypoint != "" {
		env["ORVA_ENTRYPOINT"] = fn.Entrypoint
	}
	return env
}
