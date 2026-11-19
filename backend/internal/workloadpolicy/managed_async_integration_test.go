package workloadpolicy

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func TestManagedPublicationCancelsStandingJobs(t *testing.T) {
	for _, operation := range []string{"detach", "revoke"} {
		for _, overlap := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/inline-overlap=%v", operation, overlap), func(t *testing.T) {
				f := managedFixtureForTest(t, false)
				grant := fmt.Sprintf(`{"schema_version":1,"allow":[{"actions":["jobs.enqueue"],"resource":{"function":{"id":%q}},"context":"execution"}],"inbound":[]}`, f.other)
				accept := fmt.Sprintf(`{"schema_version":1,"allow":[],"inbound":[{"caller_id":%q,"triggers":["job"]}]}`, f.owner)
				if _, err := f.service.MutateManaged(t.Context(), f.ref, database.IAMManagedMutation{
					Operation: "inline", Owners: f.expectations(t, f.other), PolicyJSON: []byte(accept),
				}); err != nil {
					t.Fatal(err)
				}
				if overlap {
					if _, err := f.service.MutateManaged(t.Context(), f.ref, database.IAMManagedMutation{
						Operation: "inline", Owners: f.expectations(t, f.owner), PolicyJSON: []byte(grant),
					}); err != nil {
						t.Fatal(err)
					}
				}
				policy := f.create(t, "queued-job-source", grant)
				policy = f.change(t, policy, "attach", f.owner).Policy
				creator, err := f.db.GetIAMPolicy(t.Context(), f.owner)
				if err != nil {
					t.Fatal(err)
				}
				target, err := f.db.GetIAMPolicy(t.Context(), f.other)
				if err != nil {
					t.Fatal(err)
				}
				origin := workloadiam.AsyncOrigin{
					Kind: "function", CreatorID: f.owner, TargetID: f.other, Trigger: workloadiam.Job,
					ExpectedCreatorRevision: creator.Revision, ExpectedTargetRevision: target.Revision,
				}
				observe := func() workloadiam.StandingState {
					return workloadiam.StandingState{
						CreatorArtifact: f.states[f.owner].Artifact,
						TargetArtifact:  f.states[f.other].Artifact,
					}
				}
				waiting, err := f.gate.BeginAsyncObserved(t.Context(), origin, observe)
				if err != nil {
					t.Fatal(err)
				}
				defer waiting.Finish()
				stale := f.expectations(t, f.owner)
				stale[0].Revision++
				if _, err := f.service.MutateManaged(t.Context(), f.ref, database.IAMManagedMutation{
					Operation: operation, PolicyID: policy.ID, DefinitionRevision: policy.DefinitionRevision,
					MembershipRevision: policy.MembershipRevision, Owners: stale,
				}); !errors.Is(err, database.ErrIAMConflict) {
					t.Fatalf("stale mutation did not conflict: %v", err)
				}
				if waiting.Context().Err() != nil {
					t.Fatal("failed publication cancelled standing work")
				}

				reentered := make(chan struct{})
				stop := context.AfterFunc(waiting.Context(), func() {
					lock := f.service.registry.MutationLock(f.owner)
					lock.Lock()
					defer lock.Unlock()
					_ = f.gate.ChangeHostState(func() {})
					close(reentered)
				})
				defer stop()
				f.change(t, policy, operation, f.owner)
				if waiting.Context().Err() == nil {
					t.Fatal("managed publication acknowledged before standing work was cancelled")
				}
				select {
				case <-reentered:
				case <-time.After(5 * time.Second):
					t.Fatal("cancellation callback could not reenter owner lock and IAM gate")
				}
				origin.ExpectedCreatorRevision, origin.ExpectedTargetRevision = 0, 0
				effects := 0
				err = f.gate.DoStandingObserved(origin, observe, func(workloadiam.AsyncRevisions) error {
					effects++
					return nil
				})
				if overlap {
					if err != nil || effects != 1 {
						t.Fatal("removing managed source erased surviving inline authority")
					}
				} else if !errors.Is(err, workloadiam.ErrDenied) || effects != 0 {
					t.Fatal("removed source still admitted fresh standing work")
				}
			})
		}
	}
}
