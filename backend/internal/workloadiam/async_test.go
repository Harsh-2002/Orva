package workloadiam

import (
	"context"
	"testing"
)

func asyncAuthority(t *testing.T) (*Authority, *Evaluator, *Evaluator) {
	t.Helper()
	caller, target := callPolicies(t, other)
	p := caller.policy
	p.Allow = append(p.Allow, statement(Execution, function(other), JobsEnqueue))
	caller, err := Compile(caller.metadata, p)
	if err != nil {
		t.Fatal(err)
	}
	a := mustAuthority(t, 4, []*Evaluator{caller, target})
	return a, caller, target
}
func functionOrigin() AsyncOrigin {
	return AsyncOrigin{Kind: "function", CreatorID: owner, TargetID: other, Trigger: Job}
}
func standing() StandingState {
	return StandingState{CreatorArtifact: "artifact-1", TargetArtifact: "artifact-1"}
}
func TestAsyncStandingWithoutFabricatedLease(t *testing.T) {
	a, _, _ := asyncAuthority(t)
	effects := 0
	if err := a.DoStandingObserved(functionOrigin(), standing, func(revs AsyncRevisions) error {
		effects++
		if revs.Creator != 1 || revs.Target != 1 {
			t.Fatal(revs)
		}
		return nil
	}); err != nil || effects != 1 {
		t.Fatal(err, effects)
	}
	for _, o := range []AsyncOrigin{{}, {Kind: "legacy_unknown", TargetID: other, Trigger: Job},
		{Kind: "function", CreatorID: owner, TargetID: other, Trigger: Cron},
		{Kind: "operator", CreatorID: owner, OperatorKind: "api_key", OperatorID: owner, TargetID: other, Trigger: Job}} {
		if err := a.DoStandingObserved(o, standing, func(AsyncRevisions) error { t.Fatal("invalid origin ran"); return nil }); err != ErrDenied {
			t.Fatal(err)
		}
	}
	op := AsyncOrigin{Kind: "operator", OperatorKind: "api_key", OperatorID: owner, TargetID: other, Trigger: Job}
	if err := a.DoStandingObserved(op, standing, func(AsyncRevisions) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestAsyncRevokeBeforeAndAfterWaitAndRegrant(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "batch"}[batch], func(t *testing.T) {
			a, caller, target := asyncAuthority(t)
			call, err := a.BeginAsyncObserved(context.Background(), functionOrigin(), standing)
			if err != nil {
				t.Fatal(err)
			}
			defer call.Finish()
			next := nextPolicy(t, target, true)
			if batch {
				err = a.PublishBatch([]Publication{{Expected: 1, Candidate: next}}, func() error { return nil })
			} else {
				err = a.Publish(1, next, func() error { return nil })
			}
			if err != nil {
				t.Fatal(err)
			}
			assertCancelled(t, call.Context())
			grant, err := Compile(Metadata{OwnerID: other, Revision: 3, ApprovedArtifact: "artifact-1"}, target.policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Publish(2, grant, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if err := call.AdmitObserved(func() (StandingState, TargetState) { return standing(), targetWorker() }, func() error { t.Fatal("revoked operation revived"); return nil }); err != ErrDenied {
				t.Fatal(err)
			}
			fresh, err := a.BeginAsyncObserved(t.Context(), functionOrigin(), standing)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Finish()
			stale := targetWorker()
			stale.Principal.ArtifactRevision = "obsolete"
			if err := fresh.AdmitObserved(func() (StandingState, TargetState) { return standing(), stale }, func() error { t.Fatal("stale target admitted"); return nil }); err != ErrDenied {
				t.Fatal(err)
			}
			if err := fresh.AdmitObserved(func() (StandingState, TargetState) { return standing(), targetWorker() }, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			if err := a.Publish(1, nextPolicy(t, caller, true), func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			assertCancelled(t, fresh.Context())
		})
	}
}
