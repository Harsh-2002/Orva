package workloadasync

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func asyncFixture(t *testing.T) (*Service, *database.AsyncJob, database.OperatorCredentialReference, func(bool)) {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitAsyncSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	caller, target := ids.New(), ids.New()
	for _, id := range []string{caller, target} {
		if err := db.InsertFunction(&database.Function{ID: id, Name: id, Runtime: "node", Entrypoint: "handler.js", Status: "active", EnvVars: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
	}
	p := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.JobsEnqueue},
		Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: target}}}}, Inbound: []workloadiam.Acceptance{}}
	m := workloadiam.Metadata{OwnerID: caller, Revision: 1, ApprovedArtifact: "artifact"}
	compile := func(m workloadiam.Metadata, p workloadiam.Policy) *workloadiam.Evaluator {
		t.Helper()
		e, err := workloadiam.Compile(m, p)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	targetPolicy := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{{CallerID: caller, Triggers: []workloadiam.Trigger{workloadiam.Job}}}}
	a, err := workloadiam.NewAuthority(8, []*workloadiam.Evaluator{compile(m, p), compile(workloadiam.Metadata{OwnerID: target, Revision: 1, ApprovedArtifact: "artifact"}, targetPolicy)})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: db, Authority: a, CurrentArtifact: func(string) string { return "artifact" }}
	j := &database.AsyncJob{Job: database.Job{FunctionID: target, Payload: []byte("{}"), MaxAttempts: 2, IdempotencyKey: "origin-key"},
		AsyncProvenance: database.AsyncProvenance{OriginKind: "function", CreatorID: caller, CreationCallerRevision: 1, CreationTargetRevision: 1}}
	if err := db.EnqueueAsyncJob(t.Context(), j, nil); err != nil {
		t.Fatal(err)
	}
	ref := database.OperatorCredentialReference{Kind: "api_key", ID: ids.New()}
	if err := db.InsertAPIKey(&database.APIKey{ID: ref.ID, Name: "admin", KeyHash: "unit-fixture-not-a-bearer", Prefix: "fixture", Permissions: `["admin"]`}); err != nil {
		t.Fatal(err)
	}
	setGrant := func(grant bool) {
		t.Helper()
		m.Revision++
		next := p
		if !grant {
			next.Allow = []workloadiam.Statement{}
		}
		if err := a.Publish(m.Revision-1, compile(m, next), func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	return s, j, ref, setGrant
}

func TestAsyncPendingRegrantDoesNotReplayAndRetryNeverElevates(t *testing.T) {
	s, j, ref, setGrant := asyncFixture(t)
	setGrant(false)
	setGrant(true)
	if _, err := s.BeginJob(t.Context(), j); !IsDenied(err) {
		t.Fatal("regrant replayed untouched pending job", err)
	}
	if err := s.DB.SuspendAsyncJob(t.Context(), j, "policy_denied"); err != nil {
		t.Fatal(err)
	}
	if err := s.MutateJob(t.Context(), ref, j.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	j, err := s.DB.GetAsyncJob(t.Context(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.CreationCallerRevision != 1 || j.AdmissionCallerRevision != 3 {
		t.Fatal("creation provenance mutated or resume not rebound")
	}
	call, err := s.BeginJob(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	if err := s.ClaimJob(call.Context(), j, time.Now()); err != nil {
		t.Fatal(err)
	}
	target := workloadiam.TargetState{Active: true, Principal: workloadiam.Principal{FunctionID: j.FunctionID, WorkerID: "worker", ArtifactRevision: "artifact"}}
	if err := s.AdmitJob(call.Context(), j, call, func() workloadiam.TargetState { return target }); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.MarkJobFailure(j.ID, "failed", 2, 2); err != nil {
		t.Fatal(err)
	}
	setGrant(false)
	if err := s.MutateJob(t.Context(), ref, j.ID, "retry"); !IsDenied(err) {
		t.Fatal("admin retry bypassed caller policy", err)
	}
	denied, err := s.DB.GetAsyncJob(t.Context(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if denied.Attempts != j.Attempts || denied.Status != "failed" {
		t.Fatal("denied retry spent an attempt or reopened terminal work")
	}
	setGrant(true)
	if err := s.MutateJob(t.Context(), ref, j.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	retry, err := s.DB.GetAsyncJob(t.Context(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retry.OriginKind != "function" || retry.CreatorID != j.CreatorID || retry.OperatorID != "" || retry.IdempotencyKey != "origin-key" || retry.AdmissionCallerRevision != 5 {
		t.Fatal("retry changed origin/key scope or failed to bind explicit revalidation")
	}
}

func TestAsyncDeletedCreatorAndTargetDenyEvenWithStaleObserver(t *testing.T) {
	for _, which := range []string{"creator", "target"} {
		t.Run(which, func(t *testing.T) {
			s, j, ref, _ := asyncFixture(t)
			call, err := s.BeginJob(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			defer call.Finish()
			if err := s.ClaimJob(call.Context(), j, time.Now()); err != nil {
				t.Fatal(err)
			}
			id := j.CreatorID
			if which == "target" {
				id = j.FunctionID
			}
			if err := s.DB.DeleteFunction(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if err := s.DB.InsertFunction(&database.Function{ID: ids.New(), Name: id, Runtime: "node", Entrypoint: "handler.js", Status: "active", EnvVars: map[string]string{}}); err != nil {
				t.Fatal(err)
			}
			target := workloadiam.TargetState{Active: true, Principal: workloadiam.Principal{FunctionID: j.FunctionID, WorkerID: "worker", ArtifactRevision: "artifact"}}
			if err := s.AdmitJob(call.Context(), j, call, func() workloadiam.TargetState { return target }); !errors.Is(err, database.ErrAsyncConflict) {
				t.Fatal("deleted UUID admitted", err)
			}
			if err := s.MutateJob(t.Context(), ref, j.ID, "resume"); err == nil {
				t.Fatal("same-name replacement reauthorized old UUID")
			}
		})
	}
}
