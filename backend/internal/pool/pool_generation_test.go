package pool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// spawnTestWorker uses a tiny shell process in place of nsjail. sandbox.Spawn
// still constructs the real Worker pipes/process group, which makes Kill and
// dead-state assertions representative without requiring namespaces or root.
func spawnTestWorker(t *testing.T) *sandbox.Worker {
	t.Helper()
	tmp := t.TempDir()
	rootfs := filepath.Join(tmp, "rootfs")
	codeDir := filepath.Join(tmp, "code")
	if err := os.MkdirAll(filepath.Join(rootfs, "node", "usr", "local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rootfs, "node", "opt", "orva"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootfs, "node", "usr", "local", "bin", "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootfs, "node", "opt", "orva", "adapter.js"), []byte("// test adapter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeNsjail := filepath.Join(tmp, "fake-nsjail")
	if err := os.WriteFile(fakeNsjail, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := sandbox.Spawn(context.Background(), sandbox.ExecConfig{
		Language:  sandbox.Node,
		CodeDir:   codeDir,
		Timeout:   time.Second,
		NsjailBin: fakeNsjail,
		RootfsDir: rootfs,
	})
	if err != nil {
		t.Fatalf("spawn fake worker: %v", err)
	}
	t.Cleanup(func() { _ = w.Kill() })
	return w
}

func TestAcquireRejectsWorkerSpawnedByRetiredGeneration(t *testing.T) {
	p := testPool("fn-spawn-retire", nil, 0)
	p.dynamicMax.Store(1)
	w := spawnTestWorker(t)
	spawnStarted := make(chan struct{})
	allowSpawn := make(chan struct{})
	p.spawnFn = func(context.Context) (*sandbox.Worker, error) {
		close(spawnStarted)
		<-allowSpawn
		return w, nil
	}

	result := make(chan error, 1)
	go func() {
		_, err := p.acquire(context.Background())
		result <- err
	}()
	<-spawnStarted
	p.mu.Lock()
	p.closing.Store(true)
	p.mu.Unlock()
	close(allowSpawn)

	select {
	case err := <-result:
		if !errors.Is(err, errPoolRetired) {
			t.Fatalf("acquire error: want errPoolRetired, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not finish after generation retirement")
	}
	if got := p.busy.Load(); got != 0 {
		t.Fatalf("retired spawn left busy accounting: got %d", got)
	}
	if got := len(p.idle); got != 0 {
		t.Fatalf("retired spawn published an idle worker: got %d", got)
	}
	if !w.IsDead() {
		t.Fatal("worker spawned by retired generation was not killed")
	}
}

func TestRetirementWakesGenerationWaiters(t *testing.T) {
	p := testPool("fn-waiters", nil, 0)
	p.max = 1
	p.dynamicMax.Store(1)
	p.busy.Store(1)
	p.concSem <- struct{}{}

	slotResult := make(chan error, 1)
	go func() { slotResult <- p.acquireSlot(context.Background()) }()

	workerResult := make(chan error, 1)
	go func() {
		_, err := p.acquire(context.Background())
		workerResult <- err
	}()

	p.mu.Lock()
	p.markRetired()
	p.mu.Unlock()

	for name, result := range map[string]<-chan error{
		"concurrency slot": slotResult,
		"idle worker":      workerResult,
	} {
		select {
		case err := <-result:
			if !errors.Is(err, errPoolRetired) {
				t.Errorf("%s waiter: want errPoolRetired, got %v", name, err)
			}
		case <-time.After(time.Second):
			t.Errorf("%s waiter was not awakened by retirement", name)
		}
	}
}

func TestRejectPolicyObservesRetirementBeforeReportingBusy(t *testing.T) {
	p := testPool("fn-reject-retire", nil, 0)
	p.concPolicy = "reject"
	p.concSem <- struct{}{}

	// Model retirement winning immediately after acquireSlot's initial
	// closing check. A closed retirement channel must take precedence over
	// the reject select's default ErrFunctionBusy path.
	close(p.retired)
	if err := p.acquireSlot(context.Background()); !errors.Is(err, errPoolRetired) {
		t.Fatalf("reject acquire: want errPoolRetired, got %v", err)
	}
}

func TestReaperStopsWhenGenerationRetires(t *testing.T) {
	p := testPool("fn-reaper", nil, 0)
	m := &Manager{cfg: ManagerConfig{ReapInterval: time.Hour}, shutdown: make(chan struct{})}
	done := make(chan struct{})
	m.wg.Add(1)
	go func() {
		m.reap(p)
		close(done)
	}()
	p.mu.Lock()
	p.markRetired()
	p.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reaper did not stop after generation retirement")
	}
}

func testPool(fnID string, hm *hostMemTracker, memoryBytes int64) *functionPool {
	return &functionPool{
		fnID:        fnID,
		max:         4,
		memoryBytes: memoryBytes,
		hostMem:     hm,
		idle:        make(chan *sandbox.Worker, 4),
		retired:     make(chan struct{}),
		concSem:     make(chan struct{}, 1),
	}
}

func TestRetiredAcquireReleasesToExactGeneration(t *testing.T) {
	const (
		fnID        = "fn-generation"
		memoryBytes = int64(128 << 20)
	)
	hm := &hostMemTracker{}
	hm.reserved.Store(memoryBytes)
	limiter := sandbox.NewLimiter(1)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire host limiter: %v", err)
	}
	m := &Manager{hostMem: hm, limiter: limiter}

	oldPool := testPool(fnID, hm, memoryBytes)
	oldPool.busy.Store(1)
	oldPool.concSem <- struct{}{}
	m.pools.Store(fnID, oldPool)

	w := spawnTestWorker(t)
	acq := &AcquireResult{Worker: w, ColdStart: true, pool: oldPool}

	// Retire the generation while its worker is busy, then install a new pool
	// under the same function ID before the old request releases.
	m.RefreshForDeploy(fnID)
	if !oldPool.closing.Load() {
		t.Fatal("retired pool was not marked closing")
	}
	newPool := testPool(fnID, hm, memoryBytes)
	newPool.busy.Store(1)
	newPool.concSem <- struct{}{}
	m.pools.Store(fnID, newPool)

	m.Release(acq, nil)

	if got := oldPool.busy.Load(); got != 0 {
		t.Fatalf("old generation busy: want 0, got %d", got)
	}
	if got := len(oldPool.concSem); got != 0 {
		t.Fatalf("old generation semaphore was not released: len=%d", got)
	}
	if got := len(oldPool.idle); got != 0 {
		t.Fatalf("stale worker was requeued into old generation: len=%d", got)
	}
	if got := newPool.busy.Load(); got != 1 {
		t.Fatalf("new generation busy accounting was changed: want 1, got %d", got)
	}
	if got := len(newPool.concSem); got != 1 {
		t.Fatalf("new generation semaphore was changed: want 1, got %d", got)
	}
	if got := len(newPool.idle); got != 0 {
		t.Fatalf("stale worker crossed into new generation: len=%d", got)
	}
	if !w.IsDead() {
		t.Fatal("worker from retired generation was not killed")
	}
	if got := hm.reserved.Load(); got != 0 {
		t.Fatalf("retired worker memory reservation leaked: got %d", got)
	}
	if active, _ := limiter.Stats(); active != 0 {
		t.Fatalf("host limiter slot was not released: active=%d", active)
	}

	// A duplicate defer/release must not corrupt counters or release the host
	// limiter twice.
	m.Release(acq, nil)
	if got := oldPool.busy.Load(); got != 0 {
		t.Fatalf("duplicate release changed busy count: got %d", got)
	}
	if active, _ := limiter.Stats(); active != 0 {
		t.Fatalf("duplicate release changed host limiter: active=%d", active)
	}
}

func TestRetireKillsIdleWorkersAndBalancesReservations(t *testing.T) {
	const (
		fnID        = "fn-idle-retire"
		memoryBytes = int64(64 << 20)
	)
	hm := &hostMemTracker{}
	hm.reserved.Store(memoryBytes)
	m := &Manager{hostMem: hm}
	p := testPool(fnID, hm, memoryBytes)
	w := spawnTestWorker(t)
	p.idle <- w
	m.pools.Store(fnID, p)

	m.DrainAndRemove(fnID)

	if !p.closing.Load() {
		t.Fatal("drained pool was not marked closing")
	}
	if _, ok := m.pools.Load(fnID); ok {
		t.Fatal("drained pool remains addressable by function ID")
	}
	if !w.IsDead() {
		t.Fatal("idle worker was not killed during retirement")
	}
	if got := hm.reserved.Load(); got != 0 {
		t.Fatalf("idle worker memory reservation leaked: got %d", got)
	}
}

func TestConcurrentRetireAndReleaseCannotStrandWorker(t *testing.T) {
	const (
		fnID        = "fn-release-race"
		memoryBytes = int64(32 << 20)
	)
	hm := &hostMemTracker{}
	hm.reserved.Store(memoryBytes)
	m := &Manager{hostMem: hm}
	p := testPool(fnID, hm, memoryBytes)
	p.busy.Store(1)
	p.concSem <- struct{}{}
	m.pools.Store(fnID, p)

	w := spawnTestWorker(t)
	acq := &AcquireResult{Worker: w, pool: p}

	// Hold the lifecycle lock until Release has reached its publication
	// boundary, then race it against retirement. Whichever wins the lock is a
	// valid ordering: Release parks first and retirement drains it, or
	// retirement closes first and Release kills it directly.
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

	retireDone := make(chan struct{})
	go func() {
		m.RefreshForDeploy(fnID)
		close(retireDone)
	}()
	p.mu.Unlock()

	select {
	case <-releaseDone:
	case <-time.After(2 * time.Second):
		t.Fatal("release deadlocked with retirement")
	}
	select {
	case <-retireDone:
	case <-time.After(2 * time.Second):
		t.Fatal("retirement deadlocked with release")
	}

	if got := len(p.idle); got != 0 {
		t.Fatalf("retired generation retained a worker: len=%d", got)
	}
	if !w.IsDead() {
		t.Fatal("worker survived concurrent generation retirement")
	}
	if got := hm.reserved.Load(); got != 0 {
		t.Fatalf("worker memory reservation leaked: got %d", got)
	}
}
