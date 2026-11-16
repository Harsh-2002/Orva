package workloadiam

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestHostStateTransitionHoldsExclusiveAdmission(t *testing.T) {
	a, _, _ := authorityFixture(t, 1)
	called := 0
	if err := a.ChangeHostState(func() {
		called++
		if a.gate.TryRLock() {
			a.gate.RUnlock()
			t.Error("state changed without exclusive admission")
		}
	}); err != nil || called != 1 {
		t.Fatalf("state transition calls=%d error=%v", called, err)
	}
	if !a.gate.TryLock() {
		t.Fatal("transition retained admission gate")
	}
	a.gate.Unlock()
}

func TestHostStateTransitionWaitsForAdmittedShortEffect(t *testing.T) {
	a, _, _ := authorityFixture(t, 1)
	entered := make(chan struct{})
	release := make(chan struct{})
	effectDone := make(chan error, 1)
	transitionDone := make(chan error, 1)
	var running atomic.Bool
	go func() {
		effectDone <- a.Do(executionContext(), request(OpKVGet, key("config")), func() error {
			running.Store(true)
			close(entered)
			<-release
			running.Store(false)
			return nil
		})
	}()
	<-entered
	go func() {
		transitionDone <- a.ChangeHostState(func() {
			if running.Load() {
				t.Error("state invalidated before admitted short effect completed")
			}
		})
	}()
	close(release)
	if err := <-effectDone; err != nil {
		t.Fatal(err)
	}
	if err := <-transitionDone; err != nil {
		t.Fatal(err)
	}
}

func TestInvalidHostStateTransitionDoesNotRun(t *testing.T) {
	for _, a := range []*Authority{nil, {}} {
		if err := a.ChangeHostState(func() { t.Error("invalid coordinator changed state") }); err != ErrInvalid {
			t.Fatalf("invalid coordinator: %v", err)
		}
	}
	a, _, _ := authorityFixture(t, 1)
	if err := a.ChangeHostState(nil); err != ErrInvalid {
		t.Fatalf("nil transition: %v", err)
	}
}

func TestObservedCallPanicDoesNotRetainAdmissionLocks(t *testing.T) {
	a, _, _ := authorityFixture(t, 1)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("observer panic did not propagate")
			}
		}()
		_, _ = a.BeginCallObserved(context.Background(), func() Context { panic("observer") }, request(OpInvoke, function(other)))
	}()
	if !a.gate.TryLock() {
		t.Fatal("observer panic retained the admission gate")
	}
	a.gate.Unlock()
	if !a.registrationMu.TryLock() {
		t.Fatal("observer panic retained the registration lock")
	}
	defer a.registrationMu.Unlock()
	if len(a.registrations) != 0 {
		t.Fatal("observer panic reserved a call")
	}
}
