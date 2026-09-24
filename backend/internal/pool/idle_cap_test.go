package pool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// The idle channel used to be sized from DECLARED memory against a boot-time
// host snapshot, while dynamicMax is recomputed every tick from OBSERVED p95
// and climbs back toward max_warm. Once the two diverged, startSpawn admitted
// workers the channel could not hold and killed them on arrival, forever.
//
// This is a constructor property: p.max is never reassigned after
// getOrCreatePool, and a pool-config edit recreates the pool via
// RefreshForDeploy -> retirePool. So assert the invariant where it is
// established.
func TestIdleChannelHoldsOperatorMaxWhenDeclaredMemoryIsGenerous(t *testing.T) {
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t)
	hm := &hostMemTracker{totalBytes: 8 << 30, reservationPct: 0.8, cpuWorkers: 128}
	hm.availBytes.Store(8 << 30)
	m.hostMem = hm

	// 1 GiB declared -> 1.5 GiB budget -> only 4 fit in 80% of 8 GiB, so the
	// old memory branch sized the channel at 4 while max_warm stayed 32.
	fn := registerFn(t, reg, "declared-heavy", "none")
	fn.MemoryMB = 1024
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}
	if err := m.db.UpsertPoolConfig(&database.PoolConfig{
		FunctionID: fn.ID, MinWarm: 1, MaxWarm: 32, IdleTTLS: 600,
	}); err != nil {
		t.Fatal(err)
	}

	p, err := m.getOrCreatePool(fn.ID)
	if err != nil {
		t.Fatal(err)
	}

	if cap(p.idle) < p.max {
		t.Fatalf("cap(idle)=%d < p.max=%d: dynamicMax can exceed the channel, so the "+
			"controller admits workers it must kill on arrival", cap(p.idle), p.max)
	}

	// And the live ceiling must stay inside the channel once observed memory
	// falls far below the declared budget — the regime that drove the churn.
	p.sigMu.Lock()
	p.memSamples = []int64{48 << 20, 52 << 20, 56 << 20}
	p.sigMu.Unlock()
	s := newScaler(m, hm)
	if dyn, reason := s.dynamicMax(p, p.snapshotDemand(time.Now()).MemoryP95); dyn > cap(p.idle) {
		t.Fatalf("dynamicMax=%d (%s) exceeds cap(idle)=%d", dyn, reason, cap(p.idle))
	}
}

// The structural backstop: even if the two caps drift again, startSpawn must
// refuse to fork a worker that has nowhere to park.
func TestStartSpawnRefusesWorkersTheIdleChannelCannotHold(t *testing.T) {
	p, hm := controllerTestPool()
	// controllerTestPool builds cap(idle) == max, which IS the post-fix
	// invariant and so cannot express this regime. Narrow it deliberately.
	p.idle = make(chan *sandbox.Worker, 4)
	for i := 0; i < 4; i++ {
		p.idle <- nil
	}
	p.dynamicMax.Store(int64(p.max)) // 50, far above cap(idle) == 4

	var spawns atomic.Int64
	p.spawnFn = func(context.Context) (*sandbox.Worker, error) {
		spawns.Add(1)
		return &sandbox.Worker{}, nil
	}

	s := newScaler(&Manager{hostMem: hm}, hm)
	admitted := s.startSpawn(p, "test")
	s.spawnWG.Wait()

	if admitted {
		t.Error("startSpawn admitted a worker into a full idle channel")
	}
	if got := spawns.Load(); got != 0 {
		t.Errorf("spawned %d workers with nowhere to park them", got)
	}
	if got := p.killed.Load(); got != 0 {
		t.Errorf("spawn/kill churn: killed=%d", got)
	}
}

// The self-sustaining loop in its purest form: min_warm above the old
// channel cap, zero traffic. desired stays above current every tick, so the
// controller spawned and killed forever and the pool never settled.
func TestNoSpawnKillChurnAtRest(t *testing.T) {
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t)
	hm := &hostMemTracker{totalBytes: 8 << 30, reservationPct: 0.8, cpuWorkers: 128}
	hm.availBytes.Store(8 << 30)
	m.hostMem = hm

	fn := registerFn(t, reg, "at-rest", "none")
	fn.MemoryMB = 1024
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}
	if err := m.db.UpsertPoolConfig(&database.PoolConfig{
		FunctionID: fn.ID, MinWarm: 20, MaxWarm: 32, IdleTTLS: 600,
	}); err != nil {
		t.Fatal(err)
	}

	p, err := m.getOrCreatePool(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Fill the channel, as a settled pool would have.
	for len(p.idle) < cap(p.idle) {
		p.idle <- nil
	}
	// Observed RSS far below the 1 GiB declared budget is what lifts
	// dynamicMax above the old channel cap and starts the loop. Without this
	// the seeded dynamicMax still matches the old cap and nothing churns.
	p.sigMu.Lock()
	p.memSamples = []int64{48 << 20, 52 << 20, 56 << 20}
	p.sigMu.Unlock()

	var spawns atomic.Int64
	p.spawnFn = func(context.Context) (*sandbox.Worker, error) {
		spawns.Add(1)
		return &sandbox.Worker{}, nil
	}

	s := newScaler(m, hm)
	now := time.Now()
	s.evaluate(p, now)
	s.evaluate(p, now.Add(2*time.Second))
	s.spawnWG.Wait()

	if got := spawns.Load(); got != 0 {
		t.Errorf("settled pool at rest spawned %d workers", got)
	}
	if got := p.killed.Load(); got != 0 {
		t.Errorf("settled pool at rest killed %d workers", got)
	}
}

// Even an absurd configured max cannot allocate a larger idle channel than
// the host could ever fill under its CPU and minimum-memory reservations.
func TestMaxWarmClampedToResourceEnvelope(t *testing.T) {
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t)
	hm := &hostMemTracker{totalBytes: 64 << 30, reservationPct: 0.8, cpuWorkers: 2048}
	hm.availBytes.Store(64 << 30)
	m.hostMem = hm

	fn := registerFn(t, reg, "absurd-max", "none")
	if err := m.db.UpsertPoolConfig(&database.PoolConfig{
		FunctionID: fn.ID, MinWarm: 1, MaxWarm: 1_000_000, IdleTTLS: 600,
	}); err != nil {
		t.Fatal(err)
	}

	p, err := m.getOrCreatePool(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.max != 2048 || p.maxReason != "cpu_capacity" {
		t.Errorf("p.max = %d/%s, want 2048/cpu_capacity", p.max, p.maxReason)
	}
	if cap(p.idle) != 2048 {
		t.Errorf("cap(idle) = %d, want 2048", cap(p.idle))
	}
}

func TestAutoMaxTracksResourceEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		mem  int64
		cpu  int
		fn   int
		want int
		why  string
	}{
		{"cpu", 64 << 30, 2048, 0, 2048, "cpu_capacity"},
		{"memory", 1 << 30, 2048, 0, 51, "memory_capacity"},
		{"function", 64 << 30, 2048, 7, 7, "function_concurrency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hm := &hostMemTracker{totalBytes: tc.mem, reservationPct: 0.8, cpuWorkers: tc.cpu}
			got, reason := poolResourceCeiling(hm, 1000, tc.fn)
			if got != tc.want || reason != tc.why {
				t.Fatalf("ceiling=%d/%s, want %d/%s", got, reason, tc.want, tc.why)
			}
		})
	}
}

func TestPoolWithoutOverrideUsesResourceCeiling(t *testing.T) {
	m, reg := egressTestManager(t)
	m.cfg.DefaultMax = 0
	m.tmpl = fakeSandboxTemplate(t)
	hm := &hostMemTracker{totalBytes: 64 << 30, reservationPct: 0.8, cpuWorkers: 256}
	hm.availBytes.Store(64 << 30)
	m.hostMem = hm
	fn := registerFn(t, reg, "auto-max", "none")
	p, err := m.getOrCreatePool(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.max != 256 || p.maxReason != "cpu_capacity" || cap(p.idle) != 256 {
		t.Fatalf("automatic pool ceiling=%d/%s idle=%d, want 256/cpu_capacity/256",
			p.max, p.maxReason, cap(p.idle))
	}
}
