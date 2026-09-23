package pool

import (
	"errors"
	"testing"
)

func TestRequestChargeAccountsForUnknownAndLargeBodies(t *testing.T) {
	const capBytes = 6 << 20
	if got := requestCharge(0, capBytes); got != requestBaseCharge {
		t.Fatalf("empty body charge = %d", got)
	}
	if got := requestCharge(1024, capBytes); got != requestBaseCharge+requestBodyCopies*1024 {
		t.Fatalf("known body charge = %d", got)
	}
	for _, size := range []int64{-1, capBytes, capBytes + 1} {
		if got := requestCharge(size, capBytes); got != requestBaseCharge+requestBodyCopies*capBytes {
			t.Fatalf("body size %d charge = %d", size, got)
		}
	}
}

func TestRequestBudgetScalesWithMemoryAndPreservesAnotherFunctionShare(t *testing.T) {
	hm := &hostMemTracker{totalBytes: 4 << 30}
	hm.availBytes.Store(4 << 30)
	b := newRequestBudget(hm)
	global, per := b.queueLimits()
	if global <= 1024 || per <= 256 || per >= global {
		t.Fatalf("resource-derived queue limits = %d/%d", global, per)
	}
	charge := b.limit - b.limit/4
	releaseA, ok := b.reserve("a", charge)
	if !ok {
		t.Fatal("function A could not claim its fair share")
	}
	if _, ok := b.reserve("a", 1); ok {
		t.Fatal("function A consumed the share reserved for other functions")
	}
	releaseB, ok := b.reserve("b", b.limit/4)
	if !ok {
		t.Fatal("function B could not use reserved headroom")
	}
	releaseA()
	releaseB()
	if b.used != 0 || len(b.byFunction) != 0 {
		t.Fatalf("budget leaked: used=%d functions=%d", b.used, len(b.byFunction))
	}
}

func TestRequestBudgetRejectsPhysicalPressureAndRecovers(t *testing.T) {
	hm := &hostMemTracker{totalBytes: 1 << 30}
	hm.availBytes.Store(100 << 20)
	b := newRequestBudget(hm)
	if _, ok := b.reserve("a", requestBaseCharge); ok {
		t.Fatal("admitted a request with less than the OS reserve available")
	}
	hm.availBytes.Store(900 << 20)
	release, ok := b.reserve("a", requestBaseCharge)
	if !ok {
		t.Fatal("did not recover after memory pressure cleared")
	}
	release()
}

func TestReserveIngressRejectsBeforeBodyAllocationAndReleases(t *testing.T) {
	hm := &hostMemTracker{totalBytes: 1 << 30}
	hm.availBytes.Store(1 << 30)
	m := &Manager{requestBudget: newRequestBudget(hm)}
	// A known or chunked body too large for the daemon share is rejected
	// before the proxy reads it. Neither attempt may retain a reservation.
	for _, length := range []int64{64 << 20, -1} {
		if _, err := m.ReserveIngress("fn", length, 64<<20); !errors.Is(err, ErrInvocationQueueFull) {
			t.Fatalf("body length %d: expected queue rejection, got %v", length, err)
		}
	}
	if m.requestBudget.used != 0 {
		t.Fatalf("rejected body leaked %d bytes", m.requestBudget.used)
	}
	release, err := m.ReserveIngress("fn", 1024, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	if m.requestBudget.used == 0 {
		t.Fatal("accepted request did not reserve memory")
	}
	release()
	if m.requestBudget.used != 0 {
		t.Fatalf("release leaked %d bytes", m.requestBudget.used)
	}
	m.closing.Store(true)
	if _, err := m.ReserveIngress("fn", 0, 64<<20); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("closing manager admitted request: %v", err)
	}
}
