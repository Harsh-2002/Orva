package workloadiam

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func callPolicies(t *testing.T, targetID string) (*Evaluator, *Evaluator) {
	t.Helper()
	p := withStatements(statement(Execution, function(targetID), FunctionsInvoke), statement(Execution, key("config"), KVGet))
	if targetID == owner {
		p.Inbound = []Acceptance{{CallerID: owner, Triggers: []Trigger{F2F}}}
		e := compiled(t, p)
		return e, e
	}
	caller := compiled(t, p)
	p = emptyPolicy()
	p.Inbound = []Acceptance{{CallerID: owner, Triggers: []Trigger{F2F, Job}}}
	target, err := Compile(metadata(targetID), p)
	if err != nil {
		t.Fatal(err)
	}
	return caller, target
}

func authorityFixture(t *testing.T, capacity int) (*Authority, *Evaluator, *Evaluator) {
	t.Helper()
	caller, target := callPolicies(t, other)
	a, err := NewAuthority(capacity, []*Evaluator{caller, target})
	if err != nil {
		t.Fatal(err)
	}
	return a, caller, target
}

func targetWorker() TargetState {
	return TargetState{Active: true, Principal: Principal{FunctionID: other, WorkerID: "target-worker", ArtifactRevision: "artifact-1"}}
}

func nextPolicy(t *testing.T, e *Evaluator, deny bool) *Evaluator {
	t.Helper()
	m := e.Metadata()
	m.Revision++
	p := e.policy
	if deny {
		p = emptyPolicy()
	}
	next, err := Compile(m, p)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func begin(t *testing.T, a *Authority, op Operation) *Admission {
	t.Helper()
	call, err := a.BeginCall(context.Background(), executionContext(), request(op, function(other)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(call.Finish)
	return call
}

func assertCancelled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("context not cancelled before acknowledgement")
	}
}

func TestAuthorityConstructionAndZeroDenial(t *testing.T) {
	caller, target := callPolicies(t, other)
	for _, initial := range [][]*Evaluator{{nil}, {{}}, {caller, caller}} {
		if _, err := NewAuthority(1, initial); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid initial policies: %v", err)
		}
	}
	if _, err := NewAuthority(-1, nil); err != ErrInvalid {
		t.Fatal(err)
	}
	for _, a := range []*Authority{nil, {}, mustAuthority(t, 1, nil)} {
		if err := a.Do(executionContext(), request(OpKVGet, key("config")), func() error { t.Fatal("zero effect"); return nil }); err != ErrDenied {
			t.Fatal(err)
		}
		if _, err := a.BeginCall(context.Background(), executionContext(), request(OpInvoke, function(other))); err != ErrDenied {
			t.Fatal(err)
		}
	}
	var call Admission
	assertCancelled(t, call.Context())
	if call.Admit(executionContext(), targetWorker()) != ErrDenied || call.HandoffFrame(executionContext(), targetWorker(), nil, func([]byte) error { return nil }) != ErrDenied {
		t.Fatal("zero admission allowed")
	}
	call.Finish()
	a := mustAuthority(t, 1, []*Evaluator{caller, target})
	if err := a.Do(executionContext(), request(OpKVGet, key("config")), nil); err != ErrDenied {
		t.Fatal(err)
	}
	if err := a.Publish(1, nil, func() error { return nil }); err != ErrInvalid {
		t.Fatal(err)
	}
}

func mustAuthority(t *testing.T, capacity int, es []*Evaluator) *Authority {
	t.Helper()
	a, err := NewAuthority(capacity, es)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestWaitingDoesNotHoldGateAndCannotResumeAfterRegrant(t *testing.T) {
	for _, side := range []string{"caller", "target"} {
		t.Run(side, func(t *testing.T) {
			a, caller, target := authorityFixture(t, 2)
			call := begin(t, a, OpInvoke) // Simulated pool wait; no gate retained.
			e := caller
			if side == "target" {
				e = target
			}
			denied := nextPolicy(t, e, true)
			if err := a.Publish(1, denied, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			assertCancelled(t, call.Context())
			if call.Admit(executionContext(), targetWorker()) != ErrDenied {
				t.Fatal("revoked waiter admitted")
			}
			regrant := nextPolicy(t, e, false)
			m := regrant.Metadata()
			m.Revision = 3
			regrant, err := Compile(m, regrant.policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Publish(2, regrant, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if call.Admit(executionContext(), targetWorker()) != ErrDenied {
				t.Fatal("old waiter resumed after regrant")
			}
			fresh := begin(t, a, OpInvoke)
			if err := fresh.Admit(executionContext(), targetWorker()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAdmittedStreamRevocationAndNestedCancellation(t *testing.T) {
	for _, side := range []string{"caller", "target"} {
		t.Run(side, func(t *testing.T) {
			a, caller, target := authorityFixture(t, 1)
			call := begin(t, a, OpStreamOpen)
			if err := call.Admit(executionContext(), targetWorker()); err != nil {
				t.Fatal(err)
			}
			nested, cancel := context.WithCancel(call.Context())
			defer cancel()
			var frames int
			transfer := func(frame []byte) error { frames++; return nil }
			if err := call.HandoffFrame(executionContext(), targetWorker(), []byte("first"), transfer); err != nil {
				t.Fatal(err)
			}
			e := caller
			if side == "target" {
				e = target
			}
			// Even an unchanged grant conservatively revokes an affected edge.
			if err := a.Publish(1, nextPolicy(t, e, false), func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			assertCancelled(t, nested)
			if call.HandoffFrame(executionContext(), targetWorker(), []byte("late"), transfer) != ErrDenied || frames != 1 {
				t.Fatal("revoked stream transferred a frame")
			}
		})
	}
}

func TestRevokedFlagClosesPreSignalWindow(t *testing.T) {
	a, _, _ := authorityFixture(t, 2)
	for _, admitted := range []bool{false, true} {
		call := begin(t, a, OpStreamOpen)
		if admitted {
			if err := call.Admit(executionContext(), targetWorker()); err != nil {
				t.Fatal(err)
			}
		}
		a.registrationMu.Lock()
		call.revoked = true // Publication's mark, before its out-of-lock signal.
		a.registrationMu.Unlock()
		if call.Context().Err() != nil {
			t.Fatal("test requires an unsignalled context")
		}
		if call.Admit(executionContext(), targetWorker()) != ErrDenied || call.HandoffFrame(executionContext(), targetWorker(), nil, func([]byte) error { t.Fatal("late transfer"); return nil }) != ErrDenied {
			t.Fatal("revocation flag ignored")
		}
	}
}

func TestShortEffectFinishesBeforePublicationAcknowledgement(t *testing.T) {
	a, caller, _ := authorityFixture(t, 1)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	effectCompleted := make(chan struct{})
	go func() {
		finished <- a.Do(executionContext(), request(OpKVGet, key("config")), func() error {
			close(entered)
			<-release
			close(effectCompleted)
			return nil
		})
	}()
	<-entered
	if a.gate.TryLock() {
		a.gate.Unlock()
		t.Fatal("effect did not retain read gate")
	}
	published := make(chan error, 1)
	candidate := nextPolicy(t, caller, true)
	go func() {
		published <- a.Publish(1, candidate, func() error {
			select {
			case <-effectCompleted:
				return nil
			default:
				return errors.New("persist ran before protected effect completed")
			}
		})
	}()
	select {
	case <-published:
		t.Fatal("publication acknowledged during protected effect")
	default:
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if a.Do(executionContext(), request(OpKVGet, key("config")), func() error { t.Fatal("revoked effect"); return nil }) != ErrDenied {
		t.Fatal("short operation used old policy")
	}
}

func TestPublicationFailuresAndRevisionRace(t *testing.T) {
	a, caller, _ := authorityFixture(t, 1)
	call := begin(t, a, OpInvoke)
	candidate := nextPolicy(t, caller, true)
	storageErr := errors.New("storage failed")
	if err := a.Publish(1, candidate, func() error { return storageErr }); err != storageErr {
		t.Fatal(err)
	}
	if call.Context().Err() != nil || a.policies[owner].Metadata().Revision != 1 {
		t.Fatal("failed persistence changed authority")
	}
	if err := call.Admit(executionContext(), targetWorker()); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []uint64{0, 2, ^uint64(0)} {
		if a.Publish(expected, candidate, func() error { t.Fatal("conflict persisted"); return nil }) != ErrConflict {
			t.Fatal("expected conflict")
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	commits := 0 // Serialized by the authority's write gate.
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- a.Publish(1, candidate, func() error { commits++; return nil })
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch err {
		case nil:
			wins++
		case ErrConflict:
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 || commits != 1 {
		t.Fatalf("wins=%d conflicts=%d commits=%d", wins, conflicts, commits)
	}
	assertCancelled(t, call.Context())
}

func TestFreshIdentityAndTargetChecks(t *testing.T) {
	for _, name := range []string{"caller-function", "caller-worker", "caller-lease", "caller-execution", "caller-inactive", "caller-artifact", "target-function", "target-worker", "target-inactive", "target-artifact"} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := authorityFixture(t, 1)
			call := begin(t, a, OpStreamOpen)
			c, target := executionContext(), targetWorker()
			switch name {
			case "caller-function":
				c.Principal.FunctionID = third
			case "caller-worker":
				c.Principal.WorkerID = "replacement"
				c.Lease.WorkerID = "replacement"
			case "caller-lease":
				c.Lease.ID = "replacement"
			case "caller-execution":
				c.Lease.ExecutionID = third
			case "caller-inactive":
				c.Lease.Active = false
			case "caller-artifact":
				c.Principal.ArtifactRevision = "stale"
			case "target-function":
				target.Principal.FunctionID = third
			case "target-worker":
				target.Principal.WorkerID = ""
			case "target-inactive":
				target.Active = false
			case "target-artifact":
				target.Principal.ArtifactRevision = "stale"
			}
			if call.Admit(c, target) != ErrDenied {
				t.Fatal("invalid acquisition admitted")
			}
			if err := call.Admit(executionContext(), targetWorker()); err != nil {
				t.Fatal(err)
			}
			if call.HandoffFrame(c, target, nil, func([]byte) error { t.Fatal("invalid frame transferred"); return nil }) != ErrDenied {
				t.Fatal("invalid frame authorized")
			}
		})
	}
	a, _, _ := authorityFixture(t, 1)
	call := begin(t, a, OpStreamOpen)
	if err := call.Admit(executionContext(), targetWorker()); err != nil {
		t.Fatal(err)
	}
	target := targetWorker()
	target.Principal.WorkerID = "other-valid-worker"
	if call.HandoffFrame(executionContext(), target, nil, func([]byte) error { return nil }) != ErrDenied {
		t.Fatal("stream ownership changed")
	}
	if call.Admit(executionContext(), targetWorker()) != ErrDenied {
		t.Fatal("double admission")
	}
}

func TestSnapshotsAndRequestDoNotAlias(t *testing.T) {
	caller, target := callPolicies(t, other)
	initial := []*Evaluator{caller, target}
	a := mustAuthority(t, 1, initial)
	initial[0] = nil
	caller.policy.Allow[0].Resource.Function.ID = third
	target.policy.Inbound[0].CallerID = third
	r := request(OpInvoke, function(other))
	call, err := a.BeginCall(context.Background(), executionContext(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	r.Resource.Function.ID = third
	if err := call.Admit(executionContext(), targetWorker()); err != nil {
		t.Fatal(err)
	}
	candidate := nextPolicy(t, a.policies[owner], false)
	if err := a.Publish(1, candidate, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	candidate.policy.Allow[0].Resource.Function.ID = third
	call.Finish()
	fresh := begin(t, a, OpInvoke)
	if err := fresh.Admit(executionContext(), targetWorker()); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityParentCancellationAndFinish(t *testing.T) {
	a, _, _ := authorityFixture(t, 1)
	parent, cancel := context.WithCancel(context.Background())
	call, err := a.BeginCall(parent, executionContext(), request(OpInvoke, function(other)))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	assertCancelled(t, call.Context())
	if call.Admit(executionContext(), targetWorker()) != ErrDenied {
		t.Fatal("cancelled parent admitted")
	}
	if _, err := a.BeginCall(context.Background(), executionContext(), request(OpInvoke, function(other))); err != ErrCapacity {
		t.Fatal(err)
	}
	call.Finish()
	call.Finish()
	if len(a.registrations) != 0 {
		t.Fatal("registration leaked")
	}
	fresh := begin(t, a, OpInvoke)
	if err := fresh.Admit(executionContext(), targetWorker()); err != nil {
		t.Fatal(err)
	}
	if fresh.HandoffFrame(executionContext(), targetWorker(), nil, func([]byte) error { t.Fatal("buffered call transferred stream frame"); return nil }) != ErrDenied {
		t.Fatal("non-stream handoff")
	}
	fresh.Finish()
	assertCancelled(t, fresh.Context())
	if _, err := a.BeginCall(parent, executionContext(), request(OpInvoke, function(other))); err != ErrDenied {
		t.Fatal(err)
	}
	zeroCapacity, _, _ := authorityFixture(t, 0)
	if _, err := zeroCapacity.BeginCall(context.Background(), executionContext(), request(OpInvoke, function(other))); err != ErrCapacity {
		t.Fatal(err)
	}
}

func TestSelfCallsAndShortEdgeAcceptance(t *testing.T) {
	e, _ := callPolicies(t, owner)
	a := mustAuthority(t, 1, []*Evaluator{e})
	call, err := a.BeginCall(context.Background(), executionContext(), request(OpInvoke, function(owner)))
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	target := TargetState{Active: true, Principal: executionContext().Principal}
	if err := call.Admit(executionContext(), target); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(1, nextPolicy(t, e, true), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	assertCancelled(t, call.Context())

	p := withStatements(statement(Execution, function(other), JobsEnqueue))
	caller := compiled(t, p)
	_, accepted := callPolicies(t, other)
	for _, targetPolicy := range []*Evaluator{nil, accepted} {
		es := []*Evaluator{caller}
		if targetPolicy != nil {
			es = append(es, targetPolicy)
		}
		a := mustAuthority(t, 1, es)
		called := false
		err := a.Do(executionContext(), request(OpJobsEnqueue, function(other)), func() error { called = true; return nil })
		if (err == nil) != (targetPolicy != nil) || called != (targetPolicy != nil) {
			t.Fatalf("acceptance err=%v called=%v", err, called)
		}
		if a.Do(executionContext(), request(OpInvoke, function(other)), func() error { t.Fatal("call bypass"); return nil }) != ErrDenied {
			t.Fatal("call via Do")
		}
	}
}

func TestHandoffBoundsAndCallbackErrors(t *testing.T) {
	a, _, _ := authorityFixture(t, 1)
	call := begin(t, a, OpStreamOpen)
	if err := call.Admit(executionContext(), targetWorker()); err != nil {
		t.Fatal(err)
	}
	transferErr := errors.New("buffer full")
	if err := call.HandoffFrame(executionContext(), targetWorker(), []byte("frame"), func([]byte) error { return transferErr }); err != transferErr {
		t.Fatal(err)
	}
	if call.HandoffFrame(executionContext(), targetWorker(), make([]byte, MaxHandoffBytes+1), func([]byte) error { t.Fatal("oversize transfer"); return nil }) != ErrDenied {
		t.Fatal("unbounded frame")
	}
	if call.HandoffFrame(executionContext(), targetWorker(), nil, nil) != ErrDenied {
		t.Fatal("nil transfer")
	}
	if err := a.Do(executionContext(), request(OpKVGet, key("config")), func() error { return transferErr }); err != transferErr {
		t.Fatal(err)
	}
}

func TestCancellationAllowsReentryAndDoesNotTouchUnrelatedCalls(t *testing.T) {
	a, caller, target := authorityFixture(t, 2)
	thirdPolicy, err := Compile(metadata(third), emptyPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(0, thirdPolicy, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	call := begin(t, a, OpStreamOpen)
	reentered := make(chan error, 1)
	stop := context.AfterFunc(call.Context(), func() {
		reentered <- a.Do(executionContext(), request(OpKVGet, key("config")), func() error { return nil })
	})
	defer stop()
	if err := a.Publish(1, nextPolicy(t, thirdPolicy, true), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if call.Context().Err() != nil {
		t.Fatal("unrelated publication cancelled edge")
	}
	if err := a.Publish(1, nextPolicy(t, target, true), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := <-reentered; err != nil {
		t.Fatal(err)
	}
	if a.policies[owner].Metadata() != caller.Metadata() {
		t.Fatal("unrelated caller policy changed")
	}
}

func TestConcurrentAdmissionFrameAndPublication(t *testing.T) {
	for range 32 {
		a, caller, _ := authorityFixture(t, 1)
		call := begin(t, a, OpStreamOpen)
		candidate := nextPolicy(t, caller, true)
		start, results := make(chan struct{}), make(chan error, 3)
		persisted := false // All accesses occur under the IAM gate.
		go func() {
			<-start
			results <- call.Admit(executionContext(), targetWorker())
		}()
		go func() {
			<-start
			results <- call.HandoffFrame(executionContext(), targetWorker(), nil, func([]byte) error {
				if persisted {
					return errors.New("handoff after publication")
				}
				return nil
			})
		}()
		go func() {
			<-start
			results <- a.Publish(1, candidate, func() error { persisted = true; return nil })
		}()
		close(start)
		for range 3 {
			if err := <-results; err != nil && err != ErrDenied {
				t.Fatal(err)
			}
		}
		assertCancelled(t, call.Context())
		if call.HandoffFrame(executionContext(), targetWorker(), nil, func([]byte) error { t.Fatal("post-ack transfer"); return nil }) != ErrDenied {
			t.Fatal("post-ack handoff permitted")
		}
		call.Finish()
	}
}
