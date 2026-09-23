package pool

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

func TestQueueCounterNeverExceedsBound(t *testing.T) {
	var counter atomic.Int64
	const limit int64 = 7
	for i := int64(0); i < limit; i++ {
		if !reserveQueueCounter(&counter, limit) {
			t.Fatalf("reservation %d rejected below limit", i)
		}
	}
	if reserveQueueCounter(&counter, limit) {
		t.Fatal("reservation above function limit accepted")
	}
	if got := counter.Load(); got != limit {
		t.Fatalf("counter = %d, want %d", got, limit)
	}
}

func TestQueuedAcquireDoesNotConsumeHostExecutionSlot(t *testing.T) {
	m, reg := egressTestManager(t)
	m.limiter = sandbox.NewLimiter(1)
	fn := registerFn(t, reg, "queue-no-host-slot", "none")
	p, err := m.getOrCreatePool(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	p.requestSpawn = func() {} // no worker arrives; model a saturated pool
	p.busy.Store(1)
	p.dynamicMax.Store(1)

	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, acquireErr := m.Acquire(context.Background(), fn.ID)
		done <- acquireErr
	}()
	deadline := time.After(time.Second)
	for p.queued.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("request never entered queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if active, _ := m.limiter.Stats(); active != 0 {
		t.Fatalf("queued request occupied host execution slot: %d", active)
	}
	if err := <-done; !errors.Is(err, ErrInvocationQueueFull) {
		t.Fatalf("queue expiry = %v, want ErrInvocationQueueFull", err)
	}
	if elapsed := time.Since(started); elapsed < invocationQueueWait || elapsed > invocationQueueWait+time.Second {
		t.Fatalf("queue waited %s, want approximately %s", elapsed, invocationQueueWait)
	}
	if got := m.queued.Load(); got != 0 {
		t.Fatalf("global queued count leaked: %d", got)
	}
	if got := p.queued.Load(); got != 0 {
		t.Fatalf("function queued count leaked: %d", got)
	}
}

func TestQueueAdmissionPreservesParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	if got := queueAdmissionError(parent, ErrPoolAtCapacity); !errors.Is(got, context.Canceled) {
		t.Fatalf("parent cancellation became overload: %v", got)
	}
}

func TestPoolDoesNotPublishAdapterBeforeReady(t *testing.T) {
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t)
	if err := os.WriteFile(m.tmpl.NsjailBin,
		[]byte("#!/bin/sh\nsleep 0.1\nprintf '\\000\\000\\000\\020{\"type\":\"ready\"}'\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fn := registerFn(t, reg, "adapter-readiness", "none")
	started := time.Now()
	acq, err := m.Acquire(context.Background(), fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Release(acq, nil)
	if elapsed := time.Since(started); elapsed < 90*time.Millisecond {
		t.Fatalf("worker was published before adapter ready: %s", elapsed)
	}
}

func TestAsyncSpawnFailureWakesQueuedInvocationWithCause(t *testing.T) {
	want := errors.New("egress policy unavailable")
	p := testPool("spawn-failure", nil, 0)
	p.spawnErrorCh = make(chan struct{})
	p.requestSpawn = func() { p.notifySpawnError(want) }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := p.acquire(ctx)
	if !errors.Is(err, want) {
		t.Fatalf("acquire error = %v, want original spawn failure", err)
	}
}

func TestManagerPropagatesAsynchronousEgressPolicyFailure(t *testing.T) {
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t)
	want := errors.New("policy compile failed")
	m.SetEgressPolicy(func() (string, string, error) { return "", "", want })
	hm := &hostMemTracker{totalBytes: 256 << 20, reservationPct: 0.8, cpuWorkers: 8, stop: make(chan struct{})}
	hm.availBytes.Store(256 << 20)
	m.hostMem = hm
	m.scaler = newScaler(m, hm)
	go m.scaler.run()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = m.Shutdown(ctx)
	})
	fn := registerFn(t, reg, "policy-spawn-failure", "egress")
	_, err := m.Acquire(context.Background(), fn.ID)
	if !errors.Is(err, want) {
		t.Fatalf("Acquire = %v, want original policy failure", err)
	}
}
