package workloadiam

import (
	"errors"
	"testing"
)

func TestResumeOwnerDeferred(t *testing.T) {
	a, caller, target := authorityFixture(t, 4)
	standing := func(id string) error {
		return a.DoStandingObserved(AsyncOrigin{Kind: "operator", OperatorKind: "api_key", OperatorID: owner, TargetID: id, Trigger: Job},
			func() StandingState { return StandingState{TargetArtifact: "artifact-1"} },
			func(AsyncRevisions) error { return nil })
	}
	kv := func() error {
		return a.Do(executionContext(), request(OpKVGet, key("config")), func() error { return nil })
	}
	call := func() error {
		c, err := a.BeginCall(t.Context(), executionContext(), request(OpInvoke, function(other)))
		if err == nil {
			c.Finish()
		}
		return err
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	deny := func(label string, checks ...error) {
		t.Helper()
		for _, err := range checks {
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("%s: expected denial, got %v", label, err)
			}
		}
	}
	must(kv())
	must(call())
	must(standing(owner))
	must(standing(other))

	waiting := begin(t, a, OpInvoke)
	cancel, err := a.DeactivateOwnerDeferred(owner, caller.Metadata().Revision, func() error { return nil })
	must(err)
	cancel()
	assertCancelled(t, waiting.Context())
	deny("paused caller", kv(), call(), standing(owner))

	generic := nextPolicy(t, caller, false)
	must(a.Publish(caller.Metadata().Revision, generic, func() error { return nil }))
	deny("generic update did not reset pause", kv(), call(), standing(owner))

	m := generic.Metadata()
	m.Revision++
	blocked, err := CompileDeny(m)
	must(err)
	if _, err := a.ResumeOwnerDeferred(generic.Metadata(), nextPolicy(t, generic, true), func() error {
		t.Error("mere empty policy reached persistence")
		return nil
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mere empty policy accepted for resume: %v", err)
	}
	failed := errors.New("persist failed")
	if _, err := a.ResumeOwnerDeferred(generic.Metadata(), blocked, func() error { return failed }); !errors.Is(err, failed) {
		t.Fatalf("failed persistence did not propagate: %v", err)
	}
	deny("failed persistence", kv(), call(), standing(owner))
	cancel, err = a.ResumeOwnerDeferred(generic.Metadata(), blocked, func() error { return nil })
	must(err)
	cancel()
	deny("explicit deny caller", kv(), call(), standing(owner))
	if _, err := a.ResumeOwnerDeferred(generic.Metadata(), blocked, func() error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("resume was reusable: %v", err)
	}
	callerMeta := blocked.Metadata()
	callerMeta.Revision++
	reviewed, err := Compile(callerMeta, caller.policy)
	must(err)
	must(a.Publish(blocked.Metadata().Revision, reviewed, func() error { return nil }))
	must(kv())
	must(standing(owner))
	must(call())

	targetMeta := target.Metadata()
	targetMeta.Revision++
	blockedTarget, err := CompileDeny(targetMeta)
	must(err)
	must(a.Publish(target.Metadata().Revision, blockedTarget, func() error { return nil }))
	deny("explicit deny target", call(), standing(other))
	must(kv())
}
