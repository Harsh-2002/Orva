package pool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
)

type managedSnapshot struct{ generations map[string]*managedGeneration }
type managedState struct {
	authority *workloadiam.Authority
	current   atomic.Pointer[managedSnapshot]
}
type managedGeneration struct {
	factory *workloadspawn.Factory
	pool    atomic.Pointer[functionPool]
	active  atomic.Bool
	tuning  *database.PoolConfig  // poolMu
	pending *PendingConfiguration // IAM gate
}

// PendingConfiguration identifies an inactive slot's exact committed successor.
type PendingConfiguration struct {
	source     *workloadspawn.Factory
	generation workloadspawn.Generation
	paths      *artifactfs.PreparedPaths
}

func (p *PendingConfiguration) Binding() (*workloadspawn.Factory, workloadspawn.Generation) {
	if p == nil {
		return nil, workloadspawn.Generation{}
	}
	return p.source, p.generation
}

func newManagedState(a *workloadiam.Authority) *managedState {
	s := &managedState{authority: a}
	s.current.Store(&managedSnapshot{generations: map[string]*managedGeneration{}})
	return s
}

// InstallFactory transfers an exclusively owned Factory on success. It is not
// durable publication or authorization of any production mutation writer.
func (m *Manager) InstallFactory(ctx context.Context, factory *workloadspawn.Factory) error {
	cleanup, err := m.InstallFactoryDeferred(ctx, factory)
	if err == nil {
		cleanup()
	}
	return err
}

// InstallFactoryDeferred leaves old-generation cleanup to the caller after its
// owner locks are released. The new generation has no implicit IAM approval.
func (m *Manager) InstallFactoryDeferred(ctx context.Context, factory *workloadspawn.Factory) (func(), error) {
	return m.InstallFactoryChecked(ctx, factory, nil)
}

// InstallFactoryChecked runs check under the IAM gate immediately before swap.
// The caller owns the function mutation lock; check must not take pool locks.
func (m *Manager) InstallFactoryChecked(ctx context.Context, factory *workloadspawn.Factory, check func() error) (func(), error) {
	if m.managed == nil || ctx == nil || factory == nil || factory.Retired() || factory.Owner() == "" {
		return nil, errors.New("managed pool requires a live concrete Factory and Authority")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tuning, err := m.db.GetPoolConfig(factory.Owner())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read managed pool config: %w", err)
	}
	g := &managedGeneration{factory: factory, tuning: tuning}
	p := m.makeManagedPool(g, tuning)
	g.pool.Store(p)
	m.poolMu.Lock()
	if err := ctx.Err(); err != nil {
		m.poolMu.Unlock()
		return nil, err
	}
	if m.closing.Load() {
		m.poolMu.Unlock()
		return nil, ErrManagerClosed
	}
	before := m.managed.current.Load()
	old := before.generations[factory.Owner()]
	if len(before.generations) >= 256 && old == nil {
		m.poolMu.Unlock()
		return nil, errors.New("managed pool owner capacity")
	}
	if old != nil && old.factory.ArtifactRevision() == factory.ArtifactRevision() {
		m.poolMu.Unlock()
		return nil, errors.New("managed generation already installed")
	}
	next := &managedSnapshot{generations: make(map[string]*managedGeneration, len(before.generations)+1)}
	for id, current := range before.generations {
		next.generations[id] = current
	}
	next.generations[factory.Owner()] = g
	var oldPool *functionPool
	if old != nil {
		oldPool = old.pool.Load()
		oldPool.mu.Lock()
	}
	var checked error
	var pending *PendingConfiguration
	err = m.managed.authority.ChangeHostState(func() {
		if check != nil {
			checked = check()
			if checked != nil {
				return
			}
		}
		if old != nil {
			pending, old.pending = old.pending, nil
			old.active.Store(false)
			oldPool.markRetired()
		}
		g.active.Store(true)
		m.managed.current.Store(next)
		m.pools.Store(factory.Owner(), p)
	})
	if oldPool != nil {
		oldPool.mu.Unlock()
	}
	if err == nil && checked == nil {
		m.wg.Add(1)
		go m.reap(p)
	}
	m.poolMu.Unlock()
	if err != nil || checked != nil {
		return nil, errors.Join(err, checked)
	}
	return func() {
		pending.release()
		if old != nil {
			old.factory.Close()
			drainManagedIdle(oldPool)
		}
	}, nil
}

func (m *Manager) makeManagedPool(g *managedGeneration, tuning *database.PoolConfig) *functionPool {
	facts := g.factory.Resources()
	return m.makePool(&database.Function{ID: g.factory.Owner(), MemoryMB: facts.MemoryMB, CPUs: facts.CPUs,
		MaxConcurrency: facts.MaxConcurrency, ConcurrencyPolicy: facts.ConcurrencyPolicy}, g, tuning)
}

// RefreshManagedPool applies committed tuning while the caller holds the owner
// mutation lock. Run the returned worker cleanup after releasing that lock.
func (m *Manager) RefreshManagedPool(tuning *database.PoolConfig) func() {
	if m.managed != nil && tuning != nil {
		if g := m.managed.current.Load().generations[tuning.FunctionID]; g != nil {
			snapshot := *tuning
			if cleanup := m.rotateManagedPool(g, &snapshot); cleanup != nil {
				return cleanup
			}
		}
	}
	return func() {}
}

func (m *Manager) rotateManagedEgressPools() int {
	retired := 0
	for _, g := range m.managed.current.Load().generations {
		if g.active.Load() && g.factory.Resources().NetworkMode == "egress" {
			if cleanup := m.rotateManagedPool(g, nil); cleanup != nil {
				cleanup()
				retired++
			}
		}
	}
	return retired
}

func (m *Manager) rotateManagedPool(g *managedGeneration, tuning *database.PoolConfig) func() {
	owner := g.factory.Owner()
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	current := m.managed.current.Load()
	registered, _ := m.pools.Load(owner)
	old := g.pool.Load()
	if m.closing.Load() || current.generations[owner] != g || registered != old || !g.active.Load() {
		return nil
	}
	// Egress rotation must inherit the last published tuning, not a snapshot
	// captured before a concurrent update replaced this worker pool.
	if tuning == nil {
		tuning = g.tuning
	}
	next := m.makeManagedPool(g, tuning)
	next.concSem = old.concSem
	old.mu.Lock()
	swapped := false
	err := m.managed.authority.ChangeHostState(func() {
		if !g.active.Load() || old.closing.Load() || g.factory.Retired() {
			return
		}
		old.markRetired()
		g.tuning = tuning
		g.pool.Store(next)
		m.pools.Store(owner, next)
		swapped = true
	})
	old.mu.Unlock()
	if err != nil || !swapped {
		return nil
	}
	m.wg.Add(1)
	go m.reap(next)
	return func() {
		drainManagedIdle(old)
		if m.scaler != nil {
			m.scaler.nudge()
		}
	}
}

func (m *Manager) ManagedBy(a *workloadiam.Authority) bool {
	return m != nil && m.managed != nil && m.managed.authority == a
}

func drainManagedIdle(p *functionPool) {
	for {
		select {
		case w := <-p.idle:
			p.killWorker(w)
		default:
			return
		}
	}
}

func (m *Manager) removeManaged(owner string) {
	m.poolMu.Lock()
	before := m.managed.current.Load()
	g := before.generations[owner]
	if g == nil {
		m.poolMu.Unlock()
		return
	}
	next := &managedSnapshot{generations: make(map[string]*managedGeneration, len(before.generations))}
	for id, current := range before.generations {
		if id != owner {
			next.generations[id] = current
		}
	}
	p := g.pool.Load()
	p.mu.Lock()
	var pending *PendingConfiguration
	err := m.managed.authority.ChangeHostState(func() {
		pending, g.pending = g.pending, nil
		g.active.Store(false)
		p.markRetired()
		m.managed.current.Store(next)
		m.pools.Delete(owner)
	})
	p.mu.Unlock()
	m.poolMu.Unlock()
	if err == nil {
		pending.release()
		g.factory.Close()
		drainManagedIdle(p)
	}
}

// CurrentArtifact and TargetState are gate-held observers: atomics/immutable
// facts only, with no DB, path lookup, Factory preparation or worker mutex.
func (m *Manager) CurrentArtifact(owner string) string {
	if m == nil || m.managed == nil || m.closing.Load() {
		return ""
	}
	g := m.managed.current.Load().generations[owner]
	if g == nil || !g.active.Load() || g.factory.Retired() {
		return ""
	}
	return g.factory.ArtifactRevision()
}

func (m *Manager) IsCurrentArtifact(owner, artifact string) bool {
	return artifact != "" && m.CurrentArtifact(owner) == artifact
}

// CurrentFactory is a gate-held observation, not ownership transfer.
func (m *Manager) CurrentFactory(owner string) *workloadspawn.Factory {
	if m.CurrentArtifact(owner) == "" {
		return nil
	}
	return m.managed.current.Load().generations[owner].factory
}

// InvalidateOwnerFactoryUnderGate also discards superseded configuration pins.
func (m *Manager) InvalidateOwnerFactoryUnderGate(owner string) func() {
	g := m.managed.current.Load().generations[owner]
	if g == nil {
		return func() {}
	}
	pending := g.pending
	g.pending = nil
	p := g.pool.Load()
	active := g.active.Swap(false)
	return func() {
		pending.release()
		if !active {
			return
		}
		p.mu.Lock()
		p.markRetired()
		p.mu.Unlock()
		g.factory.Close()
		drainManagedIdle(p)
	}
}

// PublishConfigurationUnderGate transfers paths after successful COMMIT, with
// the same live Factory rechecked under this gate before COMMIT.
func (m *Manager) PublishConfigurationUnderGate(current *workloadspawn.Factory, generation workloadspawn.Generation, paths *artifactfs.PreparedPaths) func() {
	g := m.managed.current.Load().generations[current.Owner()]
	retire := m.InvalidateOwnerFactoryUnderGate(current.Owner())
	g.pending = &PendingConfiguration{source: current, generation: generation, paths: paths}
	return retire
}

func (m *Manager) PendingConfigurationUnderGate(owner string) *PendingConfiguration {
	if m.closing.Load() {
		return nil
	}
	if g := m.managed.current.Load().generations[owner]; g != nil && !g.active.Load() {
		return g.pending
	}
	return nil
}

func (p *PendingConfiguration) release() {
	if p != nil {
		p.paths.Release()
	}
}

func (a *AcquireResult) ManagedResources() (workloadspawn.ResourceFacts, bool) {
	if a == nil || a.pool == nil || a.pool.managed == nil {
		return workloadspawn.ResourceFacts{}, false
	}
	return a.pool.managed.factory.Resources(), true
}

// AcceptsManagedVersion prevents a queued HTTP request from using authorization
// based on a function version other than the Factory that will execute it.
func (m *Manager) AcceptsManagedVersion(a *AcquireResult, authorizedVersion int) bool {
	if m.managed == nil {
		return true
	}
	if a == nil || a.pool == nil || a.pool.managed == nil || authorizedVersion <= 0 || m.closing.Load() {
		return false
	}
	g := a.pool.managed
	return g.factory.FunctionVersion() == authorizedVersion && g.active.Load() && g.pool.Load() == a.pool &&
		!a.pool.closing.Load() &&
		m.managed.current.Load().generations[g.factory.Owner()] == g
}

func (m *Manager) TargetState(owner string, worker *sandbox.Worker) workloadiam.TargetState {
	if worker == nil || !worker.AcceptsNewDispatch() {
		return workloadiam.TargetState{}
	}
	s, ok := worker.WorkloadState()
	if !ok || !s.Alive || !s.Ready || s.Identity.FunctionID != owner ||
		!m.IsCurrentArtifact(owner, s.Identity.ArtifactRevision) {
		return workloadiam.TargetState{}
	}
	return workloadiam.TargetState{Active: true, Principal: workloadiam.Principal{
		FunctionID: owner, WorkerID: s.Identity.WorkerID, ArtifactRevision: s.Identity.ArtifactRevision}}
}

// egressPolicyForSpawn resolves the compiled policy an egress worker loads.
// Fail-closed: a pool with no policy source never spawns an egress worker.
func (m *Manager) egressPolicyForSpawn(networkMode string) (string, string, error) {
	if networkMode != "egress" {
		return "", "", nil
	}
	if m.tmpl.EgressPolicy == nil {
		return "", "", sandbox.ErrEgressPolicyMissing
	}
	return m.tmpl.EgressPolicy()
}

func (m *Manager) spawnManaged(ctx context.Context, p *functionPool) (*sandbox.Worker, error) {
	g := p.managed
	acceptsWork := func() bool { return !m.closing.Load() && g.active.Load() && !p.closing.Load() }
	if !acceptsWork() {
		return nil, errPoolRetired
	}
	if ready := m.tmpl.ManagedSpawnReady; ready != nil && !ready() {
		return nil, errors.New("managed worker startup is closed")
	}
	plan, err := g.factory.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer plan.Close()
	if !acceptsWork() {
		return nil, errPoolRetired
	}
	facts, tmpl := g.factory.Resources(), m.tmpl
	policy, revision, err := m.egressPolicyForSpawn(facts.NetworkMode)
	if err != nil {
		return nil, err
	}
	if facts.SDK && tmpl.SDKHandler == nil {
		return nil, errors.New("managed SDK host unavailable")
	}
	start := time.Now()
	worker, err := plan.Spawn(ctx, sandbox.ExecConfig{
		NsjailBin: tmpl.NsjailBin, MaxPids: maxPidsOr(tmpl.DefaultMaxPids, 32),
		Domain:     tmpl.ManagedDomain,
		SDKHandler: tmpl.SDKHandler, EgressPolicyPath: policy, EgressPolicyGen: revision,
		ResolvConfPath: tmpl.DataDir + "/firewall/resolv.conf", HostsPath: tmpl.DataDir + "/firewall/hosts",
		SeccompPolicy:           sandbox.BuildSeccompPolicy(tmpl.DefaultSeccomp, sandbox.SeccompAllowForNetworkMode(facts.NetworkMode), nil),
		WorkloadDispatchAllowed: acceptsWork,
		WorkloadTransitionHook: func(change func()) {
			// A configured Authority is immutable and live for this Manager's lifetime.
			if err := m.managed.authority.ChangeHostState(change); err != nil {
				panic(err)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	ready, cancel := context.WithTimeout(ctx, adapterReadyTimeout)
	err = worker.AwaitReady(ready)
	cancel()
	if err != nil || !acceptsWork() {
		_ = worker.Kill()
		if err != nil {
			return nil, err
		}
		return nil, errPoolRetired
	}
	if tmpl.Metrics != nil {
		tmpl.Metrics.RecordSpawnDuration(time.Since(start))
	}
	return worker, nil
}
