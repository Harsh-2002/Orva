package workloadiam

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestDoBatchObservedAdmission(t *testing.T) {
	a, _, _ := authorityFixture(t, 0)
	r := request(OpKVGet, key("config"))
	for _, batch := range [][]Request{nil, {}, {r, request(OpKVGet, key("forbidden"))}, {request(OpKVIncr, key("config"))}, make([]Request, MaxBatchOperations+1)} {
		if err := a.DoBatchObserved(executionContext, batch, func() error { t.Error("denied batch ran effect"); return nil }); err != ErrDenied {
			t.Fatal(err)
		}
	}
	observations, effects := 0, 0
	want := errors.New("effect failed")
	err := a.DoBatchObserved(func() Context {
		observations++
		if a.gate.TryLock() {
			a.gate.Unlock()
			t.Error("observed before admission")
		}
		return executionContext()
	}, []Request{r, r}, func() error {
		effects++
		if a.gate.TryLock() {
			a.gate.Unlock()
			t.Error("effect outside admission")
		}
		return want
	})
	if err != want || observations != 1 || effects != 1 {
		t.Fatalf("error=%v observations=%d effects=%d", err, observations, effects)
	}
	for _, invalid := range []*Authority{nil, {}} {
		if err := invalid.DoBatchObserved(executionContext, []Request{r}, func() error { t.Error("invalid authority ran effect"); return nil }); err != ErrDenied {
			t.Fatal(err)
		}
	}
	if a.DoBatchObserved(nil, []Request{r}, func() error { return nil }) != ErrDenied || a.DoBatchObserved(executionContext, []Request{r}, nil) != ErrDenied {
		t.Fatal("nil callbacks accepted")
	}
}

func TestDoBatchObservedSerializesPublicationAndHostTransition(t *testing.T) {
	a, caller, _ := authorityFixture(t, 0)
	entered, release := make(chan struct{}), make(chan struct{})
	effectDone, publishDone, transitionDone := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	var effectRunning atomic.Bool
	go func() {
		effectDone <- a.DoBatchObserved(executionContext, []Request{request(OpKVGet, key("config"))}, func() error {
			effectRunning.Store(true)
			close(entered)
			<-release
			effectRunning.Store(false)
			return nil
		})
	}()
	<-entered
	next := nextPolicy(t, caller, true)
	go func() {
		publishDone <- a.Publish(1, next, func() error {
			if effectRunning.Load() {
				t.Error("publication overlapped admitted transaction")
			}
			return nil
		})
	}()
	go func() {
		transitionDone <- a.ChangeHostState(func() {
			if effectRunning.Load() {
				t.Error("host invalidation overlapped admitted transaction")
			}
		})
	}()
	close(release)
	for _, done := range []chan error{effectDone, publishDone, transitionDone} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if err := a.DoBatchObserved(executionContext, []Request{request(OpKVGet, key("config"))}, func() error { t.Error("revoked batch ran"); return nil }); err != ErrDenied {
		t.Fatal(err)
	}
}

func TestDoBatchObservedPanicUnlocks(t *testing.T) {
	a, _, _ := authorityFixture(t, 0)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("missing observer panic")
			}
		}()
		_ = a.DoBatchObserved(func() Context { panic("observer") }, []Request{request(OpKVGet, key("config"))}, func() error { return nil })
	}()
	if !a.gate.TryLock() {
		t.Fatal("panic retained admission gate")
	}
	a.gate.Unlock()
}
