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
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
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

	// DefaultMaxPids is the per-sandbox process-count cap passed to nsjail
	// (--cgroup_pids_max). Must be > 0 — a 0 value disables the cap, which is
	// how warm workers used to launch (the field was simply never set here).
	DefaultMaxPids int

	// SecretsLookup is consulted at every worker spawn to inject decrypted
	// secrets as env vars. nil means "function has no secrets layer wired
	// up." On secret upsert/delete, the secret handler triggers
	// RefreshForDeploy so the next spawn picks up the new values.
	SecretsLookup func(fnID string) map[string]string

	// SDKToken mints a process-scoped credential bound to the worker's
	// function ID. User code can read it, but cannot alter the signed caller.
	SDKToken func(functionID string) string

	// APIBaseURL is the base URL the adapter uses when making outbound
	// calls to Orva's own internal endpoints (KV / F2F / jobs).
	//
	// This is deliberately NOT a loopback address: from inside the jail
	// 127.0.0.1 is the sandbox's own loopback, so it cannot reach orvad.
	// detectInternalAPIBase probes for a routable address instead, which is
	// normally RFC1918 — hence the control-plane carve-out the egress policy
	// compiles ahead of the operator's block rules. Functions with
	// network_mode=none have no network stack at all and cannot use the SDK.
	APIBaseURL string

	// EgressPolicy is consulted at every network_mode=egress spawn and returns
	// the compiled NSTUN policy to run the worker under. Returning an error
	// aborts the spawn: NSTUN allows anything no rule matches, so a worker
	// started without a policy would be entirely unfiltered.
	//
	// nil means no policy layer is wired (unit tests), in which case egress
	// spawns proceed without a policy exactly as they did before this existed.
	EgressPolicy func() (path, gen string, err error)

	// Metrics, when non-nil, receives sandbox spawn-duration samples on
	// every successful Spawn so the /metrics histogram has data to draw.
	// Optional so unit tests that wire pools without a metrics instance
	// don't have to fake one.
	Metrics *metrics.Metrics
}

// maxPidsOr returns v when it is positive, else the fallback. Guards against a
// zero DefaultMaxPids reaching nsjail as `--cgroup_pids_max 0` (no cap).
func maxPidsOr(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func workerCPUUnits(cpus float64) int64 {
	if cpus <= 0 {
		return 1000
	}
	units := int64(math.Ceil(cpus * 1000))
	if units < 1 {
		return 1
	}
	return units
}

// Manager owns all function-scoped pools.
type Manager struct {
	cfg      ManagerConfig
	tmpl     SandboxTemplate
	db       *database.Database
	reg      *registry.Registry
	limiter  *sandbox.Limiter // host-wide ceiling
	pools    sync.Map         // fnID -> *functionPool
	poolMu   sync.Mutex       // serializes pool generation create/retire
	closing  atomic.Bool
	wg       sync.WaitGroup // reaper goroutines
	shutdown chan struct{}

	// fnLocks: per-function mutex for serializing deploy + rollback against
	// each other. Acquired at the top of Queue.runJob and at the start of
	// the rollback handler so the two paths can never interleave on the
	// same function (which would race the symlink retarget).
	fnLocks sync.Map // fnID -> *sync.Mutex

	// Pool Controller v2 — demand formulas plus global round-robin admission.
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

	// pool is the exact pool generation that handed out Worker. A function's
	// pool can be retired and recreated while this request is still running;
	// release must never look the pool up by fnID again or an old worker can be
	// inserted into the replacement generation (an ABA race).
	pool *functionPool

	released atomic.Bool
}

// PoolStats is a point-in-time snapshot for metrics.
type PoolStats struct {
	FunctionID       string
	Idle             int
	Busy             int64
	Queued           int64
	Spawning         int64
	Arrivals         int64
	Spawned          int64
	Killed           int64
	Desired          int64
	EffectiveMax     int64
	StableRate       float64
	BurstRate        float64
	QueueWaitP95MS   float64
	ServiceP95MS     float64
	ColdStartP95MS   float64
	LimitingReason   string
	Rejections       int64
	CapacityTimeouts int64
}

// SetSecretsLookup wires the secrets fetcher into the pool template after
// construction. Useful when the secrets manager is created later than the
// pool manager (typical wiring order in server.New).
func (m *Manager) SetSecretsLookup(fn func(fnID string) map[string]string) {
	m.tmpl.SecretsLookup = fn
}

// SetEgressPolicy wires the compiled-policy accessor into the pool template
// after construction, mirroring SetSecretsLookup: the firewall manager is
// built later than the pool manager in server.New.
func (m *Manager) SetEgressPolicy(fn func() (string, string, error)) {
	m.tmpl.EgressPolicy = fn
}

// RetireEgressPools retires every network_mode=egress pool generation, leaving
// network_mode=none pools untouched. Called when a new egress policy
// generation is published.
//
// NSTUN loads its rules once at worker start, so an already-warm worker keeps
// the policy it was spawned with. That makes a policy change exactly the class
// of change retirePool exists for — the same one a network_mode flip uses —
// so this reuses that single primitive rather than adding a second
// worker-lifecycle mechanism with its own lock ordering.
func (m *Manager) RetireEgressPools() int {
	var fnIDs []string
	m.pools.Range(func(k, _ any) bool {
		id, ok := k.(string)
		if !ok {
			return true
		}
		fn, err := m.reg.Get(id)
		if err != nil || fn == nil || fn.NetworkMode != "egress" {
			return true // "" and "none" are unaffected by egress policy
		}
		fnIDs = append(fnIDs, id)
		return true
	})
	// Retire outside Range: retirePool takes poolMu and mutates the same map.
	for _, id := range fnIDs {
		m.retirePool(id)
	}
	return len(fnIDs)
}

// HostMemStats returns the (total, available, reserved) bytes for the
// admission-control accounting. Zero-ed when the tracker is disabled.
func (m *Manager) HostMemStats() (total, avail, reserved int64) {
	if m.hostMem == nil {
		return 0, 0, 0
	}
	return m.hostMem.stats()
}

func (m *Manager) EffectiveCPUCapacity() int {
	if m.hostMem == nil {
		return 1
	}
	return m.hostMem.effectiveCPUWorkers()
}

// EffectiveMemoryCapacity returns the bytes still available for worker
// admission after cgroup headroom and all in-flight/live reservations.
func (m *Manager) EffectiveMemoryCapacity() int64 {
	if m.hostMem == nil {
		return 0
	}
	return m.hostMem.availableForWorkers()
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
	// errPoolRetired is internal control flow: a deploy retired the pool
	// generation while Acquire was taking a slot or spawning a worker. The
	// manager retries against the replacement generation without surfacing a
	// transient failure to the caller.
	errPoolRetired = errors.New("pool generation retired")
)

// NewManager creates a pool manager. limiter is the host-wide concurrency
// ceiling (may be nil to disable host-level capping).
func NewManager(cfg ManagerConfig, tmpl SandboxTemplate, db *database.Database, reg *registry.Registry, limiter *sandbox.Limiter) *Manager {
	if cfg.DefaultMin <= 0 {
		cfg.DefaultMin = 1
	}
	if cfg.DefaultMax <= 0 {
		cfg.DefaultMax = 50
	}
	if cfg.DefaultIdleTTL <= 0 {
		// Public/default contract: ten minutes before an opted-in
		// scale-to-zero pool may discard its final warm worker.
		cfg.DefaultIdleTTL = 10 * time.Minute
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

	p, err := m.getOrCreatePool(fnID)
	if err != nil {
		return nil, err
	}
	arrivedAt := time.Now()
	p.recordArrival(arrivedAt)
	p.queued.Add(1)
	finishQueue := func(pool *functionPool, rejected bool) {
		pool.queued.Add(-1)
		pool.recordQueueWait(time.Since(arrivedAt))
		if rejected {
			pool.rejections.Add(1)
		}
	}

	// Respect the host-wide concurrency ceiling after recording demand.
	// sum of every pool's max_warm from overwhelming the box even if each
	// pool is within its own limit. TryAcquire returns ErrTooManyRequests
	// after a 250ms grace — long enough to ride out micro-spikes, short
	// enough to fail fast under sustained saturation.
	if m.limiter != nil {
		if err := m.limiter.TryAcquire(ctx, 250*time.Millisecond); err != nil {
			p.capacityTimeouts.Add(1)
			finishQueue(p, true)
			return nil, err
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			finishQueue(p, true)
			if m.limiter != nil {
				m.limiter.Release()
			}
			return nil, err
		}

		// Per-function concurrency gate. Runs *before* worker acquire so a
		// busy function doesn't pull workers from the pool only to error
		// out. A generation retired while waiting is retried transparently.
		if err := p.acquireSlot(ctx); err != nil {
			if errors.Is(err, errPoolRetired) {
				p.queued.Add(-1)
				p, err = m.getOrCreatePool(fnID)
				if err != nil {
					if m.limiter != nil {
						m.limiter.Release()
					}
					return nil, err
				}
				p.queued.Add(1)
				continue
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				p.capacityTimeouts.Add(1)
			}
			finishQueue(p, true)
			if m.limiter != nil {
				m.limiter.Release()
			}
			return nil, err
		}

		res, err := p.acquire(ctx)
		if err != nil {
			p.releaseSlot()
			if errors.Is(err, errPoolRetired) {
				p.queued.Add(-1)
				p, err = m.getOrCreatePool(fnID)
				if err != nil {
					if m.limiter != nil {
						m.limiter.Release()
					}
					return nil, err
				}
				p.queued.Add(1)
				continue
			}
			finishQueue(p, true)
			if m.limiter != nil {
				m.limiter.Release()
			}
			return nil, err
		}
		res.pool = p
		finishQueue(p, false)
		return res, nil
	}
}

// RecordLatency feeds a per-request service-time sample into the
// function's rolling p95. Every dispatch path calls it after user code returns
// or fails. Non-blocking and safe from any goroutine.
func (m *Manager) RecordLatency(acq *AcquireResult, d time.Duration) {
	if acq != nil && acq.pool != nil {
		acq.pool.recordLatency(d)
	}
}

// RefreshForDeploy retires the current pool generation so the next Acquire
// rebuilds all spawn and pool-level configuration from the registry/DB. Idle
// workers die immediately; busy workers finish their in-flight request and
// are killed when their generation-bound AcquireResult is released.
//
// Removing the whole generation is necessary for more than code refreshes:
// concurrency semaphores, memory admission budgets, network namespaces and
// secret/env snapshots all live on the functionPool or its workers.
func (m *Manager) RefreshForDeploy(fnID string) {
	m.retirePool(fnID)
}

// Drain performs a hard reset of a function's pool: every idle worker is
// killed synchronously, the pool entry is removed from the manager map,
// and any busy worker mid-request is killed on its next Release (the
// generation-bound Release path observes closing and kills rather than
// reparking it).
//
// Use this when the spawn config has changed in a way that makes a
// surviving worker incorrect — most importantly, network_mode flips.
// A worker spawned under network_mode="none" lives in an isolated net
// namespace with no bridge connectivity, so it cannot satisfy a
// follow-up invocation that has been promised "egress" semantics.
//
// The next Acquire lazily recreates the pool via getOrCreatePool, which
// reads the function record fresh and spawns a new worker with the
// updated config.
func (m *Manager) Drain(fnID string) {
	m.DrainAndRemove(fnID)
}

// DrainAndRemove kills all idle workers for a function and removes the pool
// entry entirely. Used when a function is deleted so that Stats() no longer
// includes it and memory reservations are freed immediately.
//
// Busy workers (mid-request) are handled lazily: their AcquireResult retains
// this generation, and its next Release sees closing and kills them there.
func (m *Manager) DrainAndRemove(fnID string) {
	m.retirePool(fnID)
}

// retirePool atomically removes and closes one pool generation. poolMu makes
// retirement linearizable with getOrCreatePool, so a racing creator cannot
// publish an unretired stale generation after this method returns. Releases
// remain safe because AcquireResult retains a pointer to the exact generation.
func (m *Manager) retirePool(fnID string) {
	m.poolMu.Lock()
	v, loaded := m.pools.LoadAndDelete(fnID)
	if !loaded {
		m.poolMu.Unlock()
		return
	}
	p := v.(*functionPool)
	p.mu.Lock()
	p.markRetired()
	p.mu.Unlock()
	m.poolMu.Unlock()
	for {
		select {
		case w := <-p.idle:
			p.killWorker(w)
		default:
			return
		}
	}
}

// Release returns an acquired worker to the exact pool generation that handed
// it out. If that generation was retired, functionPool.release sees closing
// and kills the worker while balancing that generation's semaphore and memory
// reservation. Releasing the same acquisition twice is a no-op.
func (m *Manager) Release(acq *AcquireResult, reqErr error) {
	if acq == nil || !acq.released.CompareAndSwap(false, true) {
		return
	}
	defer func() {
		if m.limiter != nil {
			m.limiter.Release()
		}
	}()
	p := acq.pool
	if p == nil {
		if acq.Worker != nil {
			_ = acq.Worker.Kill()
		}
		return
	}
	// Always free the per-fn concurrency slot, regardless of whether the
	// worker exists (Acquire may have errored after taking the slot).
	defer p.releaseSlot()
	if acq.Worker == nil {
		return
	}
	p.release(acq.Worker, reqErr)
}

// Shutdown quits all workers, waits for reapers, and stops accepting new
// Acquire calls. Caller passes a context with a deadline — after that the
// shutdown escalates to SIGKILL.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.poolMu.Lock()
	if !m.closing.CompareAndSwap(false, true) {
		m.poolMu.Unlock()
		return nil
	}
	m.poolMu.Unlock()
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
		demand := p.snapshotDemand(time.Now())
		p.sigMu.Lock()
		reason := p.limitingReason
		p.sigMu.Unlock()
		out = append(out, PoolStats{
			FunctionID:       k.(string),
			Idle:             len(p.idle),
			Busy:             p.busy.Load(),
			Queued:           p.queued.Load(),
			Spawning:         p.spawning.Load(),
			Arrivals:         p.arrivalsTotal.Load(),
			Spawned:          p.spawned.Load(),
			Killed:           p.killed.Load(),
			Desired:          p.desired.Load(),
			EffectiveMax:     p.dynamicMax.Load(),
			StableRate:       demand.StableRate,
			BurstRate:        demand.BurstRate,
			QueueWaitP95MS:   float64(demand.QueueWaitP95.Microseconds()) / 1000,
			ServiceP95MS:     float64(demand.ServiceP95.Microseconds()) / 1000,
			ColdStartP95MS:   float64(demand.SpawnP95.Microseconds()) / 1000,
			LimitingReason:   reason,
			Rejections:       p.rejections.Load(),
			CapacityTimeouts: p.capacityTimeouts.Load(),
		})
		return true
	})
	return out
}

// PrewarmAll registers every active pool, then lets the global coordinator
// fill configured minimums in round-robin order. It returns when each pool is
// warm or its effective host cap proves the minimum cannot currently fit.
func (m *Manager) PrewarmAll(ctx context.Context) {
	if !m.cfg.EagerWarmup || m.reg == nil {
		return
	}
	fns := m.reg.ListActive()
	if len(fns) == 0 {
		return
	}
	slog.Info("pool prewarm starting", "functions", len(fns))

	pools := make([]*functionPool, 0, len(fns))
	for _, fn := range fns {
		p, err := m.getOrCreatePool(fn.ID)
		if err != nil {
			slog.Warn("prewarm: get pool failed", "fn", fn.ID, "err", err)
			continue
		}
		pools = append(pools, p)
	}
	if m.scaler == nil {
		slog.Warn("pool prewarm skipped: host capacity coordinator unavailable")
		return
	}
	m.scaler.nudge()
	waitLimit := time.NewTimer(30 * time.Second)
	defer waitLimit.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		settled := true
		for _, p := range pools {
			current := int(p.busy.Load()+p.spawning.Load()) + len(p.idle)
			effective := int(p.dynamicMax.Load())
			if current < p.min && (effective >= p.min || p.spawning.Load() > 0) {
				settled = false
				break
			}
		}
		if settled {
			break
		}
		select {
		case <-ctx.Done():
			slog.Warn("pool prewarm stopped", "err", ctx.Err())
			return
		case <-waitLimit.C:
			slog.Warn("pool prewarm timed out; coordinator will continue in background")
			return
		case <-ticker.C:
		}
	}
	slog.Info("pool prewarm complete")
}

// getOrCreatePool loads the pool for fnID, creating it if missing.
func (m *Manager) getOrCreatePool(fnID string) (*functionPool, error) {
	if existing, ok := m.pools.Load(fnID); ok {
		p := existing.(*functionPool)
		if !p.closing.Load() {
			return p, nil
		}
	}

	// Serialize the missing-pool path with retirement. Without this lock,
	// RefreshForDeploy could observe no entry and return just before this
	// goroutine publishes a pool built from the pre-refresh registry snapshot.
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	if m.closing.Load() {
		return nil, ErrManagerClosed
	}
	if existing, ok := m.pools.Load(fnID); ok {
		p := existing.(*functionPool)
		if !p.closing.Load() {
			return p, nil
		}
		// A closed generation should normally already have been removed by
		// retirePool. Cleaning it here also keeps hand-built test managers and
		// interrupted retirements from spinning forever.
		m.pools.Delete(fnID)
	}
	fn, err := m.reg.Get(fnID)
	if err != nil {
		return nil, ErrNoFunction
	}

	minWarm := m.cfg.DefaultMin
	maxWarm := m.cfg.DefaultMax
	idleTTL := m.cfg.DefaultIdleTTL
	scaleToZero := false
	if cfg, err := m.db.GetPoolConfig(fnID); err == nil && cfg != nil {
		minWarm = cfg.MinWarm
		if cfg.MaxWarm > 0 {
			maxWarm = cfg.MaxWarm
		}
		idleTTL = time.Duration(cfg.IdleTTLS) * time.Second
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
	cpuUnits := workerCPUUnits(fn.CPUs)

	// The idle channel is storage, not a budget. Its capacity is frozen for
	// the life of the pool, so sizing it from DECLARED memory pinned the pool
	// below the controller's dynamicMax — which is recomputed every tick from
	// OBSERVED p95 and climbs back toward max_warm once real RSS proves lower.
	// startSpawn gates on dynamicMax, so it kept admitting workers the channel
	// could not hold and killing them on arrival, indefinitely.
	//
	// Invariant relied on by startSpawn and the release path:
	//     cap(p.idle) >= p.max >= dynamicMax
	//
	// Memory safety never rested on this allocation and does not now:
	// hostMem.reserve() is a fail-closed, live, global gate on every spawn
	// path, backed by real MemAvailable rather than a boot-time estimate.
	if maxWarm > MaxWarmLimit {
		slog.Warn("max_warm clamped to the idle-channel ceiling",
			"function", fn.ID, "requested", maxWarm, "clamped", MaxWarmLimit)
		maxWarm = MaxWarmLimit
	}
	if maxWarm < 1 {
		maxWarm = 1
	}
	if minWarm > maxWarm {
		minWarm = maxWarm
	}

	// Seed the controller's published ceiling with the conservative
	// host-derived estimate: PrewarmAll and effective_max in
	// /api/v1/system/pool read dynamicMax before the scaler's first tick and
	// must not claim capacity the host cannot back. Overwritten within one
	// tick from observed p95.
	initialMax := maxWarm
	if m.hostMem != nil {
		if cpuCap := int(int64(m.hostMem.effectiveCPUWorkers()) * 1000 / cpuUnits); cpuCap < initialMax {
			initialMax = cpuCap
		}
		if memoryBytes > 0 {
			total, _, _ := m.hostMem.stats()
			if memCap := int(float64(total)*0.8) / int(memoryBytes); memCap < initialMax {
				initialMax = memCap
			}
		}
	}
	if initialMax < 1 {
		initialMax = 1
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
		fnID:        fnID,
		min:         minWarm,
		max:         maxWarm,
		idleTTL:     idleTTL,
		maxUses:     m.cfg.DefaultMaxUses,
		memoryBytes: memoryBytes,
		cpuUnits:    cpuUnits,
		scaleToZero: scaleToZero,
		hostMem:     m.hostMem,
		idle:        make(chan *sandbox.Worker, maxWarm),
		spawnSlots:  make(chan struct{}, maxConcurrentSpawnsPerPool),
		retired:     make(chan struct{}),
		concSem:     concSem,
		concPolicy:  concPolicy,
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
			if m.tmpl.SDKToken != nil {
				env["ORVA_INTERNAL_TOKEN"] = m.tmpl.SDKToken(fn.ID)
				env["ORVA_API_BASE"] = m.tmpl.APIBaseURL
			}
			// Resolve the egress policy for this spawn. Read off m (not the
			// local tmpl copy) so a late SetEgressPolicy is visible to pools
			// that already exist — same reason as SecretsLookup above.
			//
			// The path is a concrete generation file, never a moving symlink,
			// so a policy change mid-spawn cannot alter what this worker runs
			// under. Pools are retired separately to roll workers forward.
			var policyPath, policyGen string
			if fn.NetworkMode == "egress" {
				if getPolicy := m.tmpl.EgressPolicy; getPolicy != nil {
					p, g, perr := getPolicy()
					if perr != nil {
						return nil, perr // fail closed: no policy, no worker
					}
					policyPath, policyGen = p, g
				}
			}

			start := time.Now()
			w, err := sandbox.Spawn(ctx, sandbox.ExecConfig{
				Language: sandbox.Language(fn.Runtime),
				CodeDir:  codeDir,
				MemoryMB: int(fn.MemoryMB),
				MaxCPUs:  fn.CPUs,
				// Without this the warm-pool spawn passed MaxPids=0 →
				// `--cgroup_pids_max 0` (fork-bomb cap disabled). Fall back to
				// 32 (the historical sandbox.Execute default) if unset.
				MaxPids: maxPidsOr(tmpl.DefaultMaxPids, 32),
				Env:     env,
				SeccompPolicy: sandbox.BuildSeccompPolicy(tmpl.DefaultSeccomp,
					// Outbound network access needs the socket syscalls the base
					// policies withhold; what it may reach is decided by the
					// compiled egress policy, not by seccomp.
					sandbox.SeccompAllowForNetworkMode(fn.NetworkMode), nil),
				NetworkMode: fn.NetworkMode,
				// Operator-managed DNS for egress sandboxes; written by
				// internal/firewall on every refresh tick. Bound at
				// /etc/resolv.conf and /etc/hosts when present.
				ResolvConfPath: dataDir + "/firewall/resolv.conf",
				HostsPath:      dataDir + "/firewall/hosts",
				// Compiled NSTUN egress policy (empty for network_mode=none).
				EgressPolicyPath: policyPath,
				EgressPolicyGen:  policyGen,
				NsjailBin:        tmpl.NsjailBin,
				RootfsDir:        tmpl.RootfsDir,
				Timeout:          time.Duration(fn.TimeoutMS) * time.Millisecond,
			})
			if err == nil && m.tmpl.Metrics != nil {
				m.tmpl.Metrics.RecordSpawnDuration(time.Since(start))
			}
			return w, err
		},
	}
	p.dynamicMax.Store(int64(initialMax))
	if m.scaler != nil {
		p.reclaimFn = func() bool { return m.scaler.reclaimBorrowedIdle(p) }
		p.requestSpawn = m.scaler.nudge
	}

	// Store — but if another goroutine raced us, discard our pool.
	actual, loaded := m.pools.LoadOrStore(fnID, p)
	if loaded {
		return actual.(*functionPool), nil
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
		case <-p.retired:
			return
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
	// Both adapters expose this to user code as ctx.functionName. Nothing
	// injected it before, so that field was permanently the empty string.
	env["ORVA_FUNCTION_NAME"] = fn.Name
	env["ORVA_MEMORY_MB"] = fmt.Sprintf("%d", fn.MemoryMB)
	// Backs getRemainingTimeInMillis() / ctx.timeoutMs in both adapters.
	// This is a per-function property, so it belongs on the worker at spawn;
	// the proxy cannot inject it per request because warm workers outlive the
	// request that created them. Mirrors the <=0 fallback in InvokeHandler.
	timeoutMS := fn.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 30000
	}
	env["ORVA_TIMEOUT_MS"] = fmt.Sprintf("%d", timeoutMS)
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
