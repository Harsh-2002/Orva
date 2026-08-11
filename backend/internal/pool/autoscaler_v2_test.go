package pool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

func controllerTestPool() (*functionPool, *hostMemTracker) {
	hm := &hostMemTracker{totalBytes: 64 << 30, reservationPct: 0.8, cpuWorkers: 128}
	hm.availBytes.Store(64 << 30)
	p := &functionPool{
		fnID: "fn-test", min: 1, max: 50, idleTTL: 10 * time.Minute,
		memoryBytes: 64 << 20, cpuUnits: 1000, hostMem: hm,
		idle: make(chan *sandbox.Worker, 50), spawnSlots: make(chan struct{}, 4),
		retired: make(chan struct{}),
	}
	p.dynamicMax.Store(50)
	return p, hm
}

func TestControllerV2CPUCapacityIsGlobalAndFunctionWeighted(t *testing.T) {
	hm := &hostMemTracker{totalBytes: 64 << 30, reservationPct: 0.8, cpuWorkers: 4}
	hm.availBytes.Store(64 << 30)
	if !hm.reserve(64<<20, 3000) {
		t.Fatal("initial three-CPU reservation failed")
	}
	p, _ := controllerTestPool()
	p.hostMem = hm
	p.cpuUnits = 500
	p.max = 50
	s := newScaler(&Manager{hostMem: hm}, hm)
	if got, reason := s.dynamicMax(p, 0); got != 2 || reason != "cpu_capacity" {
		t.Fatalf("weighted global CPU cap=%d/%s, want 2/cpu_capacity", got, reason)
	}
}

func TestControllerV2RespectsFunctionConcurrency(t *testing.T) {
	p, hm := controllerTestPool()
	p.concSem = make(chan struct{}, 3)
	s := newScaler(&Manager{hostMem: hm}, hm)
	if got, reason := s.dynamicMax(p, 0); got != 3 || reason != "function_concurrency" {
		t.Fatalf("function concurrency cap=%d/%s, want 3/function_concurrency", got, reason)
	}
}

func TestControllerV2DemandFormula(t *testing.T) {
	p, hm := controllerTestPool()
	m := &Manager{hostMem: hm}
	s := newScaler(m, hm)
	now := time.Now()
	for i := 0; i < 60; i++ {
		p.recordArrival(now.Add(-time.Duration(i) * time.Second))
	}
	p.recordLatency(time.Second)
	desired, reason := s.computeDesiredAt(p, now)
	if desired != 2 || reason != "stable_rate" {
		t.Fatalf("stable formula: got desired=%d reason=%q, want 2/stable_rate", desired, reason)
	}

	p.recordSpawn(time.Second)
	for i := 0; i < 6; i++ {
		p.recordArrival(now.Add(-time.Duration(i) * 100 * time.Millisecond))
	}
	desired, reason = s.computeDesiredAt(p, now)
	if desired < 3 || reason != "burst_rate" {
		t.Fatalf("burst formula: got desired=%d reason=%q, want at least 3/burst_rate", desired, reason)
	}

	p.busy.Store(7)
	desired, reason = s.computeDesiredAt(p, now)
	if desired != 10 || reason != "immediate_pressure" {
		t.Fatalf("pressure formula: got desired=%d reason=%q, want 10/immediate_pressure", desired, reason)
	}
}

func TestControllerV2ScaleToZeroHonorsIdleTTL(t *testing.T) {
	p, hm := controllerTestPool()
	p.min = 0
	p.scaleToZero = true
	p.idleTTL = 10 * time.Minute
	s := newScaler(&Manager{hostMem: hm}, hm)
	now := time.Now()
	p.recordArrival(now.Add(-9 * time.Minute))
	if desired, reason := s.computeDesiredAt(p, now); desired != 1 || reason != "idle_ttl" {
		t.Fatalf("inside idle TTL: got %d/%s", desired, reason)
	}
	if desired, reason := s.computeDesiredAt(p, now.Add(2*time.Minute)); desired != 0 || reason != "scale_to_zero" {
		t.Fatalf("after idle TTL: got %d/%s", desired, reason)
	}
}

func TestAdmissionUsesDeclaredLimitUntilMemoryP95Exists(t *testing.T) {
	p, _ := controllerTestPool()
	if got := p.admissionBytes(); got != p.memoryBytes {
		t.Fatalf("unobserved admission=%d, want declared limit %d", got, p.memoryBytes)
	}
	p.sigMu.Lock()
	p.memSamples = []int64{24 << 20, 32 << 20, 40 << 20}
	p.sigMu.Unlock()
	if got := p.admissionBytes(); got != 40<<20 {
		t.Fatalf("observed admission=%d, want p95 40 MiB", got)
	}
}

func TestControllerV2NeverOverlapsMoreThanFourSpawns(t *testing.T) {
	p, hm := controllerTestPool()
	m := &Manager{hostMem: hm}
	m.pools.Store(p.fnID, p)
	s := newScaler(m, hm)
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	var active atomic.Int64
	var peak atomic.Int64
	p.spawnFn = func(context.Context) (*sandbox.Worker, error) {
		current := active.Add(1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return &sandbox.Worker{}, nil
	}
	s.scaleUp(p, 20, "test")
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("expected four concurrent starts")
		}
	}
	if got := p.spawning.Load(); got != 4 {
		t.Fatalf("spawning=%d, want 4", got)
	}
	s.scaleUp(p, 20, "repeat")
	if got := p.spawning.Load(); got != 4 {
		t.Fatalf("repeat evaluation duplicated starts: spawning=%d", got)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for p.spawning.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if peak.Load() != 4 {
		t.Fatalf("peak starts=%d, want 4", peak.Load())
	}
}

func TestControllerV2ScaleDownNeedsThirtySecondsAndCapsTwentyPercent(t *testing.T) {
	p, hm := controllerTestPool()
	for i := 0; i < 10; i++ {
		p.idle <- nil
	}
	s := newScaler(&Manager{hostMem: hm}, hm)
	now := time.Now()
	s.evaluate(p, now)
	if len(p.idle) != 10 {
		t.Fatal("scaled down before hysteresis")
	}
	s.evaluate(p, now.Add(29*time.Second))
	if len(p.idle) != 10 {
		t.Fatal("scaled down before 30 seconds")
	}
	s.evaluate(p, now.Add(30*time.Second))
	if len(p.idle) != 8 {
		t.Fatalf("idle=%d, want 8 after one 20%% evaluation", len(p.idle))
	}
}

func TestControllerV2ScaleUpBreaksBelowTargetContinuity(t *testing.T) {
	p, hm := controllerTestPool()
	p.belowTargetSince = time.Now().Add(-time.Minute)
	p.queued.Store(20)
	p.spawnFn = func(context.Context) (*sandbox.Worker, error) {
		return nil, context.Canceled
	}
	s := newScaler(&Manager{hostMem: hm}, hm)
	s.evaluate(p, time.Now())
	p.sigMu.Lock()
	below := p.belowTargetSince
	p.sigMu.Unlock()
	if !below.IsZero() {
		t.Fatal("scale-up demand did not reset below-target hysteresis")
	}
	s.spawnWG.Wait()
}

func TestControllerV2ReclaimCountsBusyWorkersTowardMinimum(t *testing.T) {
	donor, hm := controllerTestPool()
	donor.fnID = "donor"
	donor.min = 2
	donor.busy.Store(2)
	donor.idle <- nil
	requester, _ := controllerTestPool()
	requester.fnID = "requester"
	m := &Manager{hostMem: hm}
	m.pools.Store(donor.fnID, donor)
	m.pools.Store(requester.fnID, requester)
	s := newScaler(m, hm)
	if !s.reclaimBorrowedIdle(requester) {
		t.Fatal("idle worker above a busy-satisfied minimum was not reclaimed")
	}
	if len(donor.idle) != 0 {
		t.Fatal("reclaimed worker remained idle")
	}
}

func TestControllerV2ReclaimsBorrowedIdleCapacityForQueuedFunction(t *testing.T) {
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t)
	hm := &hostMemTracker{totalBytes: 120 << 20, reservationPct: 0.8, cpuWorkers: 8, stop: make(chan struct{})}
	hm.availBytes.Store(120 << 20)
	m.hostMem = hm
	m.scaler = newScaler(m, hm)
	go m.scaler.run()
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = m.Shutdown(shutdownCtx)
	})

	first := registerFn(t, reg, "fair-first", "none")
	second := registerFn(t, reg, "fair-second", "none")
	for _, fn := range []*database.Function{first, second} {
		if err := m.db.UpsertPoolConfig(&database.PoolConfig{
			FunctionID: fn.ID, MinWarm: 0, MaxWarm: 4, IdleTTLS: 600, ScaleToZero: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	firstAcquired := make(chan *AcquireResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		acq, err := m.Acquire(ctx, first.ID)
		if err == nil {
			firstAcquired <- acq
		} else {
			firstErr <- err
		}
	}()
	var acq1 *AcquireResult
	select {
	case acq1 = <-firstAcquired:
	case err := <-firstErr:
		t.Fatalf("first acquire: %v", err)
	case <-ctx.Done():
		t.Fatal("first function never received the only host slot")
	}
	m.Release(acq1, nil)

	acq2, err := m.Acquire(ctx, second.ID)
	if err != nil {
		t.Fatalf("queued second function did not receive reclaimed capacity: %v", err)
	}
	m.Release(acq2, nil)
	if got := poolOf(t, m, first.ID).busy.Load(); got != 0 {
		t.Fatalf("reclamation touched busy first-function workers: %d", got)
	}
}
