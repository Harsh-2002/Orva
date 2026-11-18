package pool

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

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
	pool    *functionPool
	active  atomic.Bool
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
	if m.managed == nil || ctx == nil || factory == nil || factory.Retired() || factory.Owner() == "" {
		return nil, errors.New("managed pool requires a live concrete Factory and Authority")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	facts := factory.Resources()
	g := &managedGeneration{factory: factory}
	g.pool = m.makePool(&database.Function{ID: factory.Owner(), MemoryMB: facts.MemoryMB, CPUs: facts.CPUs,
		MaxConcurrency: facts.MaxConcurrency, ConcurrencyPolicy: facts.ConcurrencyPolicy}, g)
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
	if old != nil {
		old.pool.mu.Lock()
	}
	err := m.managed.authority.ChangeHostState(func() {
		if old != nil {
			old.active.Store(false)
			old.pool.markRetired()
		}
		g.active.Store(true)
		m.managed.current.Store(next)
		m.pools.Store(factory.Owner(), g.pool)
	})
	if old != nil {
		old.pool.mu.Unlock()
	}
	if err == nil {
		m.wg.Add(1)
		go m.reap(g.pool)
	}
	m.poolMu.Unlock()
	if err != nil {
		return nil, err
	}
	return func() {
		if old != nil {
			old.factory.Close()
			drainManagedIdle(old.pool)
		}
	}, nil
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
	g.pool.mu.Lock()
	err := m.managed.authority.ChangeHostState(func() {
		g.active.Store(false)
		g.pool.markRetired()
		m.managed.current.Store(next)
		m.pools.Delete(owner)
	})
	g.pool.mu.Unlock()
	m.poolMu.Unlock()
	if err == nil {
		g.factory.Close()
		drainManagedIdle(g.pool)
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

// InvalidateFactoryUnderGate requires the shared IAM gate and the exact current
// Factory. Only the returned cleanup may take pool locks or release leases.
func (m *Manager) InvalidateFactoryUnderGate(factory *workloadspawn.Factory) func() {
	g := m.managed.current.Load().generations[factory.Owner()]
	if g == nil || g.factory != factory {
		return func() {}
	}
	g.active.Store(false)
	return func() {
		g.pool.mu.Lock()
		g.pool.markRetired()
		g.pool.mu.Unlock()
		g.factory.Close()
		drainManagedIdle(g.pool)
	}
}

func (a *AcquireResult) ManagedResources() (workloadspawn.ResourceFacts, bool) {
	if a == nil || a.pool == nil || a.pool.managed == nil {
		return workloadspawn.ResourceFacts{}, false
	}
	return a.pool.managed.factory.Resources(), true
}

func (m *Manager) TargetState(owner string, worker *sandbox.Worker) workloadiam.TargetState {
	if worker == nil {
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

func (m *Manager) spawnManaged(ctx context.Context, g *managedGeneration) (*sandbox.Worker, error) {
	if m.closing.Load() || !g.active.Load() {
		return nil, errPoolRetired
	}
	plan, err := g.factory.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer plan.Close()
	if m.closing.Load() || !g.active.Load() {
		return nil, errPoolRetired
	}
	facts, tmpl := g.factory.Resources(), m.tmpl
	var policy, revision string
	if facts.NetworkMode == "egress" {
		if tmpl.EgressPolicy == nil {
			return nil, sandbox.ErrEgressPolicyMissing
		}
		policy, revision, err = tmpl.EgressPolicy()
		if err != nil {
			return nil, err
		}
	}
	if facts.SDK && tmpl.SDKHandler == nil {
		return nil, errors.New("managed SDK host unavailable")
	}
	start := time.Now()
	worker, err := plan.Spawn(ctx, sandbox.ExecConfig{
		NsjailBin: tmpl.NsjailBin, MaxPids: maxPidsOr(tmpl.DefaultMaxPids, 32),
		SDKHandler: tmpl.SDKHandler, EgressPolicyPath: policy, EgressPolicyGen: revision,
		ResolvConfPath: tmpl.DataDir + "/firewall/resolv.conf", HostsPath: tmpl.DataDir + "/firewall/hosts",
		SeccompPolicy: sandbox.BuildSeccompPolicy(tmpl.DefaultSeccomp, sandbox.SeccompAllowForNetworkMode(facts.NetworkMode), nil),
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
	if err != nil || m.closing.Load() || !g.active.Load() {
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
