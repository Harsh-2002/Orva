package workloadiam

import (
	"context"
	"testing"
	"time"
)

// All waits below are test barriers, not production observer behavior.
func observedResult[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("observer barrier timed out")
		var zero T
		return zero
	}
}

func observedAttempt(t *testing.T, name string, a *Authority) func(func() (Context, TargetState)) error {
	t.Helper()
	switch name {
	case "Do":
		return func(observe func() (Context, TargetState)) error {
			return a.DoObserved(func() Context { c, _ := observe(); return c }, request(OpKVGet, key("config")), func() error { return nil })
		}
	case "BeginCall":
		return func(observe func() (Context, TargetState)) error {
			call, err := a.BeginCallObserved(context.Background(), func() Context { c, _ := observe(); return c }, request(OpStreamOpen, function(other)))
			if call != nil {
				call.Finish()
			}
			return err
		}
	case "Admit":
		return begin(t, a, OpStreamOpen).AdmitObserved
	case "HandoffFrame":
		call := begin(t, a, OpStreamOpen)
		if err := call.Admit(executionContext(), targetWorker()); err != nil {
			t.Fatal(err)
		}
		return func(observe func() (Context, TargetState)) error {
			return call.HandoffFrameObserved(observe, []byte("frame"), func([]byte) error { return nil })
		}
	default:
		t.Fatalf("unknown attempt %q", name)
		return nil
	}
}

func TestObservedSnapshotUnderGateBeforeRegistrationLock(t *testing.T) {
	for _, name := range []string{"Do", "BeginCall", "Admit", "HandoffFrame"} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := authorityFixture(t, 2)
			run := observedAttempt(t, name, a)
			observed, result := make(chan struct{}, 1), make(chan error, 1)
			count := 0
			a.registrationMu.Lock()
			locked := true
			defer func() {
				if locked {
					a.registrationMu.Unlock()
				}
			}()
			go func() {
				result <- run(func() (Context, TargetState) {
					count++
					if a.gate.TryLock() {
						a.gate.Unlock()
						t.Error("observer ran without the read gate")
					}
					observed <- struct{}{}
					return executionContext(), targetWorker()
				})
			}()
			// Receipt while registrationMu is held proves the observer precedes it.
			observedResult(t, observed)
			a.registrationMu.Unlock()
			locked = false
			if err := observedResult(t, result); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("observations = %d, want exactly one", count)
			}
		})
	}
}

func TestObservedSnapshotFollowsPrecedingPublication(t *testing.T) {
	for _, name := range []string{"Do", "BeginCall", "Admit", "HandoffFrame"} {
		t.Run(name, func(t *testing.T) {
			a, caller, _ := authorityFixture(t, 2)
			run := observedAttempt(t, name, a)
			candidate := nextPolicy(t, caller, false)
			entered, release := make(chan struct{}), make(chan struct{})
			published, result := make(chan error, 1), make(chan error, 1)
			state := executionContext() // Read/write only under the gate.
			go func() {
				published <- a.Publish(1, candidate, func() error {
					close(entered)
					<-release
					state.Lease.Active = false
					return nil
				})
			}()
			observedResult(t, entered)
			started := make(chan struct{})
			count := 0
			go func() {
				close(started)
				result <- run(func() (Context, TargetState) {
					count++
					if state.Lease.Active || a.policies[owner].Metadata().Revision != 2 {
						t.Error("observer preceded publication of policy and host state")
					}
					return state, targetWorker()
				})
			}()
			observedResult(t, started)
			close(release)
			if err := observedResult(t, published); err != nil {
				t.Fatal(err)
			}
			if err := observedResult(t, result); err != ErrDenied {
				t.Fatalf("stale observation permitted: %v", err)
			}
			if count != 1 {
				t.Fatalf("observations = %d, want exactly one", count)
			}
		})
	}
}

func TestNilObserversDenyWithoutEffectsOrRegistration(t *testing.T) {
	a, _, _ := authorityFixture(t, 2)
	effect := func() error { t.Error("nil observer effect"); return nil }
	if err := a.DoObserved(nil, request(OpKVGet, key("config")), effect); err != ErrDenied {
		t.Fatal(err)
	}
	call, err := a.BeginCallObserved(context.Background(), nil, request(OpInvoke, function(other)))
	if err != ErrDenied || call != nil || len(a.registrations) != 0 {
		t.Fatalf("nil observer registered: call=%v err=%v count=%d", call, err, len(a.registrations))
	}
	call = begin(t, a, OpStreamOpen)
	if err := call.AdmitObserved(nil); err != ErrDenied || call.admitted {
		t.Fatalf("nil observer admitted: %v", err)
	}
	if err := call.Admit(executionContext(), targetWorker()); err != nil {
		t.Fatal(err)
	}
	if err := call.HandoffFrameObserved(nil, nil, func([]byte) error { return effect() }); err != ErrDenied {
		t.Fatal(err)
	}
	if len(a.registrations) != 1 {
		t.Fatal("nil observer changed registrations")
	}
	var absent *Admission
	if absent.AdmitObserved(nil) != ErrDenied || absent.HandoffFrameObserved(nil, nil, nil) != ErrDenied {
		t.Fatal("nil admission allowed")
	}
}

func TestObservedRevalidationRejectsChangedCallerAndTarget(t *testing.T) {
	changes := []struct {
		name   string
		change func(*Context, *TargetState)
	}{
		{"phase", func(c *Context, _ *TargetState) { c.Phase = Bootstrap; c.Ready = false; c.Lease = Lease{} }},
		{"readiness", func(c *Context, _ *TargetState) { c.Ready = false }},
		{"lease-cancelled", func(c *Context, _ *TargetState) { c.Lease.Active = false }},
		{"lease-generation", func(c *Context, _ *TargetState) { c.Lease.ID = "replacement" }},
		{"execution", func(c *Context, _ *TargetState) { c.Lease.ExecutionID = third }},
		{"caller-function", func(c *Context, _ *TargetState) { c.Principal.FunctionID = third }},
		{"caller-worker", func(c *Context, _ *TargetState) {
			c.Principal.WorkerID = "replacement"
			c.Lease.WorkerID = "replacement"
		}},
		{"caller-artifact", func(c *Context, _ *TargetState) { c.Principal.ArtifactRevision = "artifact-2" }},
		{"target-inactive", func(_ *Context, target *TargetState) { target.Active = false }},
		{"target-function", func(_ *Context, target *TargetState) { target.Principal.FunctionID = third }},
		{"target-artifact", func(_ *Context, target *TargetState) { target.Principal.ArtifactRevision = "artifact-2" }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			a, _, _ := authorityFixture(t, 1)
			state, target := executionContext(), targetWorker()
			call, err := a.BeginCallObserved(context.Background(), func() Context { return state }, request(OpStreamOpen, function(other)))
			if err != nil {
				t.Fatal(err)
			}
			defer call.Finish()
			change.change(&state, &target)
			observe := func() (Context, TargetState) { return state, target }
			if call.AdmitObserved(observe) != ErrDenied {
				t.Fatal("changed state admitted")
			}
			if err := call.AdmitObserved(func() (Context, TargetState) { return executionContext(), targetWorker() }); err != nil {
				t.Fatal(err)
			}
			if call.HandoffFrameObserved(observe, nil, func([]byte) error { t.Error("changed state transferred"); return nil }) != ErrDenied {
				t.Fatal("changed state handed off")
			}
		})
	}
}

func TestObservedSnapshotsAndSelectorsAreCopied(t *testing.T) {
	a, _, _ := authorityFixture(t, 1)
	state, target := executionContext(), targetWorker()
	r := request(OpStreamOpen, function(other))
	observations := 0
	call, err := a.BeginCallObserved(context.Background(), func() Context { observations++; return state }, r)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	r.Resource.Function.ID = third
	state.Lease.ID = "replacement"
	if call.caller != executionContext() || call.targetID != other || call.request.Resource.Function.ID != other {
		t.Fatal("caller snapshot or selector aliases its source")
	}
	if call.AdmitObserved(func() (Context, TargetState) { return state, target }) != ErrDenied {
		t.Fatal("original lease identity replaced")
	}
	state = executionContext()
	observe := func() (Context, TargetState) { observations++; return state, target }
	if err := call.AdmitObserved(observe); err != nil {
		t.Fatal(err)
	}
	target.Principal.WorkerID = "replacement-target"
	if call.target != targetWorker().Principal {
		t.Fatal("target snapshot aliases its source")
	}
	if call.HandoffFrameObserved(observe, nil, func([]byte) error { t.Error("replacement target transferred"); return nil }) != ErrDenied {
		t.Fatal("stream target identity replaced")
	}
	target = targetWorker()
	if err := call.HandoffFrameObserved(observe, nil, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if observations != 4 {
		t.Fatalf("observations = %d, want 4 (no retained observers)", observations)
	}
}

func TestObservedCancelledRegistrationCannotAdmitOrTransfer(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		a, _, _ := authorityFixture(t, 1)
		parent, cancel := context.WithCancel(context.Background())
		call, err := a.BeginCallObserved(parent, executionContext, request(OpStreamOpen, function(other)))
		if err != nil {
			t.Fatal(err)
		}
		defer call.Finish()
		observe := func() (Context, TargetState) { return executionContext(), targetWorker() }
		if admitted {
			if err := call.AdmitObserved(observe); err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		if call.AdmitObserved(observe) != ErrDenied || call.HandoffFrameObserved(observe, nil, func([]byte) error { t.Error("cancelled frame"); return nil }) != ErrDenied {
			t.Fatal("cancelled registration remained live")
		}
	}
}
