package pool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// Egress-policy plumbing: a new policy generation must recycle exactly the
// network_mode=egress pools, and a spawn that cannot obtain a policy must fail
// rather than start an unfiltered worker (NSTUN allows every destination no
// rule matches, so "no policy" means "no filtering at all").

// errTestNoPolicy stands in for firewall.ErrPolicyUnavailable. The pool
// package must not import firewall (the dependency runs the other way), so the
// contract under test is "whatever the getter returns is propagated verbatim".
var errTestNoPolicy = errors.New("test: egress policy unavailable")

// egressTestManager builds a Manager by hand instead of via NewManager: that
// constructor starts the autoscaler, which would spawn and kill workers
// underneath these assertions.
func egressTestManager(t *testing.T) (*Manager, *registry.Registry) {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	m := &Manager{
		cfg: ManagerConfig{
			DefaultMin: 1, DefaultMax: 2,
			DefaultIdleTTL: time.Minute,
			ReapInterval:   time.Hour, // no background sweeps during assertions
		},
		db:       db,
		reg:      registry.New(db),
		shutdown: make(chan struct{}),
	}
	// Stops the reapers getOrCreatePool starts, before the DB cleanup below it
	// tears the connections down.
	t.Cleanup(func() {
		close(m.shutdown)
		m.wg.Wait()
	})
	return m, m.reg
}

// registerFn inserts a function so RetireEgressPools and getOrCreatePool can
// read its network mode back out of the registry.
func registerFn(t *testing.T, reg *registry.Registry, name, networkMode string) *database.Function {
	t.Helper()
	fn := &database.Function{
		Name: name, Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 1000, MemoryMB: 64, CPUs: 1,
		NetworkMode: networkMode, Status: "active",
	}
	if err := reg.Set(fn); err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return fn
}

// fakeSandboxTemplate produces a template whose nsjail is a shell script and
// whose rootfs is just enough for resolveRuntime. sandbox.Spawn still builds
// the real argv and process, so a spawn either happens or it does not —
// without needing namespaces or root.
func fakeSandboxTemplate(t *testing.T) SandboxTemplate {
	t.Helper()
	tmp := t.TempDir()
	rootfs := filepath.Join(tmp, "rootfs")
	nodeBin := filepath.Join(rootfs, "node", "usr", "local", "bin")
	adapterDir := filepath.Join(rootfs, "node", "opt", "orva")
	for _, dir := range []string{nodeBin, adapterDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nodeBin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapterDir, "adapter.js"), []byte("// test adapter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nsjail := filepath.Join(tmp, "fake-nsjail")
	if err := os.WriteFile(nsjail, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return SandboxTemplate{
		NsjailBin: nsjail, RootfsDir: rootfs,
		DataDir: tmp, DefaultMaxPids: 32,
	}
}

func poolOf(t *testing.T, m *Manager, fnID string) *functionPool {
	t.Helper()
	v, ok := m.pools.Load(fnID)
	if !ok {
		t.Fatalf("no pool generation registered for %s", fnID)
	}
	return v.(*functionPool)
}

func TestRetireEgressPoolsLeavesNonEgressGenerationsAlone(t *testing.T) {
	// A policy change only invalidates workers that carry a policy. Retiring
	// network_mode=none pools too would turn every operator rule edit into a
	// cold start for functions that have no network stack at all.
	m, reg := egressTestManager(t)
	const memoryBytes = int64(16 << 20)
	hm := &hostMemTracker{}
	m.hostMem = hm

	type generation struct {
		fnID   string
		pool   *functionPool
		worker *sandbox.Worker
	}
	modes := map[string]string{"egress": "egress", "none": "none", "unset": ""}
	gens := map[string]generation{}
	for label, mode := range modes {
		fn := registerFn(t, reg, "fn-retire-"+label, mode)
		p := testPool(fn.ID, hm, memoryBytes)
		w := spawnTestWorker(t)
		p.idle <- w
		hm.reserved.Add(memoryBytes)
		m.pools.Store(fn.ID, p)
		gens[label] = generation{fnID: fn.ID, pool: p, worker: w}
	}

	if n := m.RetireEgressPools(); n != 1 {
		t.Fatalf("retired %d generations, want 1 (only network_mode=egress)", n)
	}

	eg := gens["egress"]
	if !eg.pool.closing.Load() {
		t.Error("egress pool was not marked closing by the policy change")
	}
	if _, ok := m.pools.Load(eg.fnID); ok {
		t.Error("retired egress pool is still addressable by function ID")
	}
	if !eg.worker.IsDead() {
		t.Error("warm egress worker survived a policy change and keeps its old rules")
	}

	for _, label := range []string{"none", "unset"} {
		g := gens[label]
		if g.pool.closing.Load() {
			t.Errorf("network_mode=%q pool was retired by an egress policy change", modes[label])
		}
		if _, ok := m.pools.Load(g.fnID); !ok {
			t.Errorf("network_mode=%q pool was removed from the manager", modes[label])
		}
		if g.worker.IsDead() {
			t.Errorf("network_mode=%q worker was killed by an egress policy change", modes[label])
		}
		if got := len(g.pool.idle); got != 1 {
			t.Errorf("network_mode=%q idle workers: got %d, want 1", modes[label], got)
		}
	}

	// One generation's worth of reservation released, the other two intact.
	if got, want := hm.reserved.Load(), 2*memoryBytes; got != want {
		t.Fatalf("memory reserved after retiring one generation: got %d, want %d", got, want)
	}
}

func TestConcurrentEgressRetireAndReleaseCannotStrandWorker(t *testing.T) {
	// Same race as TestConcurrentRetireAndReleaseCannotStrandWorker, but the
	// retirement arrives from the firewall's policy callback rather than a
	// deploy. A stranded worker here would keep serving the old egress rules.
	const memoryBytes = int64(32 << 20)
	m, reg := egressTestManager(t)
	hm := &hostMemTracker{}
	hm.reserved.Store(memoryBytes)
	m.hostMem = hm

	fn := registerFn(t, reg, "fn-egress-release-race", "egress")
	p := testPool(fn.ID, hm, memoryBytes)
	p.busy.Store(1)
	p.concSem <- struct{}{}
	m.pools.Store(fn.ID, p)

	w := spawnTestWorker(t)
	acq := &AcquireResult{Worker: w, pool: p}

	// Hold the lifecycle lock until Release has reached its publication
	// boundary, then race it against retirement. Either ordering is valid:
	// Release parks first and retirement drains it, or retirement closes first
	// and Release kills the worker directly.
	p.mu.Lock()
	releaseDone := make(chan struct{})
	go func() {
		m.Release(acq, nil)
		close(releaseDone)
	}()
	deadline := time.Now().Add(time.Second)
	for p.busy.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.busy.Load() != 0 {
		p.mu.Unlock()
		t.Fatal("release did not reach the lifecycle publication boundary")
	}

	retired := make(chan int, 1)
	go func() { retired <- m.RetireEgressPools() }()
	p.mu.Unlock()

	select {
	case <-releaseDone:
	case <-time.After(2 * time.Second):
		t.Fatal("release deadlocked with egress policy retirement")
	}
	select {
	case n := <-retired:
		if n != 1 {
			t.Errorf("RetireEgressPools reported %d retired generations, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("egress policy retirement deadlocked with release")
	}

	if got := len(p.idle); got != 0 {
		t.Fatalf("retired generation retained a worker: len=%d", got)
	}
	if !w.IsDead() {
		t.Fatal("worker survived concurrent egress policy retirement")
	}
	if got := hm.reserved.Load(); got != 0 {
		t.Fatalf("worker memory reservation leaked: got %d", got)
	}
}

func TestEgressSpawnFailsClosedWhenPolicyUnavailable(t *testing.T) {
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t)
	fn := registerFn(t, reg, "fn-egress-spawn", "egress")

	var calls atomic.Int64
	m.SetEgressPolicy(func() (string, string, error) {
		calls.Add(1)
		return "", "", errTestNoPolicy
	})

	if _, err := m.Acquire(context.Background(), fn.ID); !errors.Is(err, errTestNoPolicy) {
		t.Fatalf("Acquire must surface the policy failure, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("policy getter calls: got %d, want 1", got)
	}
	p := poolOf(t, m, fn.ID)
	if got := p.spawned.Load(); got != 0 {
		t.Errorf("a worker was spawned without a policy: spawned=%d", got)
	}
	if got := len(p.idle); got != 0 {
		t.Errorf("unfiltered worker was parked as idle: len=%d", got)
	}
	if got := p.busy.Load(); got != 0 {
		t.Errorf("failed spawn leaked busy accounting: got %d", got)
	}

	// The negative above only means something if the same wiring spawns when a
	// policy IS available — otherwise it would pass for any unrelated reason.
	policyPath := filepath.Join(t.TempDir(), "egress-abc123def4567890.cfg")
	// The file must actually exist: buildArgs verifies the generation file is
	// still on disk, so that a policy whose file vanished fails closed with the
	// same error as no policy at all.
	if err := os.WriteFile(policyPath, []byte("mount_proc: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.SetEgressPolicy(func() (string, string, error) {
		calls.Add(1)
		return policyPath, "abc123def4567890", nil
	})
	res, err := m.Acquire(context.Background(), fn.ID)
	if err != nil {
		t.Fatalf("spawn with a published policy must succeed: %v", err)
	}
	if res.Worker == nil {
		t.Fatal("Acquire returned no worker")
	}
	if !res.ColdStart {
		t.Error("first acquisition after a policy failure must be a cold start")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("policy getter calls: got %d, want 2", got)
	}
	// Kill before releasing so the fake nsjail process cannot outlive the test.
	_ = res.Worker.Kill()
	m.Release(res, nil)
}

func TestEgressPolicyGetterIsReadOffTheManager(t *testing.T) {
	// server.New builds the pool manager before the firewall manager, so
	// SetEgressPolicy lands after pools may already exist. If the spawn closure
	// read a template copy captured at pool creation, those pools would keep
	// spawning workers with no policy at all — silently unfiltered.
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t) // deliberately no EgressPolicy yet
	fn := registerFn(t, reg, "fn-egress-late-bind", "egress")

	p, err := m.getOrCreatePool(fn.ID)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}

	m.SetEgressPolicy(func() (string, string, error) { return "", "", errTestNoPolicy })

	w, err := p.spawnFn(context.Background())
	if w != nil {
		_ = w.Kill() // only reachable if the getter was ignored
	}
	if !errors.Is(err, errTestNoPolicy) {
		t.Fatalf("existing pool ignored a late SetEgressPolicy: spawn error = %v", err)
	}
}
