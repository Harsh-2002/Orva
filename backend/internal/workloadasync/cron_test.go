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

func cronFixture(t *testing.T) (*Service, func() workloadiam.Context, *database.AsyncCron, database.OperatorCredentialReference, func(bool)) {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "cron.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitCronSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := ids.New()
	if err := db.InsertFunction(&database.Function{ID: id, Name: id, Runtime: "node", Entrypoint: "handler.js", Status: "active", EnvVars: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	prefix := "daily."
	p := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.CronsUpsert},
		Resource: workloadiam.Resource{OwnCron: &workloadiam.CronResource{Prefix: &prefix, TargetID: id}}}}, Inbound: []workloadiam.Acceptance{{CallerID: id, Triggers: []workloadiam.Trigger{workloadiam.Cron}}}}
	meta := workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: "artifact"}
	e, err := workloadiam.Compile(meta, p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := workloadiam.NewAuthority(8, []*workloadiam.Evaluator{e})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: db, Authority: a, CurrentArtifact: func(owner string) string {
		if owner == id {
			return "artifact"
		}
		return ""
	}}
	observe := func() workloadiam.Context {
		return workloadiam.Context{Principal: workloadiam.Principal{FunctionID: id, WorkerID: "worker", ArtifactRevision: "artifact"},
			Phase: workloadiam.Execution, Ready: true, Lease: workloadiam.Lease{ID: "lease", WorkerID: "worker", ExecutionID: id, Active: true}}
	}
	c := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: id, Name: "daily.one", CronExpr: "@every 1s", Enabled: true}, AsyncProvenance: database.AsyncProvenance{CreatorID: id}}
	ref := database.OperatorCredentialReference{Kind: "api_key", ID: ids.New()}
	if err := db.InsertAPIKey(&database.APIKey{ID: ref.ID, Name: "admin", KeyHash: "cron-admin-fixture", Prefix: "fixture", Permissions: `["admin"]`}); err != nil {
		t.Fatal(err)
	}
	set := func(grant bool) {
		t.Helper()
		next := p
		if !grant {
			next.Allow = []workloadiam.Statement{}
		}
		meta.Revision++
		e, err := workloadiam.Compile(meta, next)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Publish(meta.Revision-1, e, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	return s, observe, c, ref, set
}
func TestCronLeaseScopeSelfAndStandingAreDistinct(t *testing.T) {
	s, observe, c, _, _ := cronFixture(t)
	ctx := t.Context()
	for _, kind := range []string{"bootstrap", "no_lease", "unready", "stale", "outside_prefix", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			x := *c
			state := observe()
			switch kind {
			case "bootstrap":
				state.Phase = workloadiam.Bootstrap
				state.Lease = workloadiam.Lease{}
			case "no_lease":
				state.Lease.Active = false
			case "unready":
				state.Ready = false
			case "stale":
				state.Principal.ArtifactRevision = "obsolete"
			case "outside_prefix":
				x.Name = "foreign.one"
			case "foreign":
				x.FunctionID = ids.New()
			}
			if err := s.UpsertCronFunction(ctx, func() workloadiam.Context { return state }, &x); !IsDenied(err) {
				t.Fatal("invalid registration", err)
			}
		})
	}
	if err := s.UpsertCronFunction(ctx, observe, c); err != nil {
		t.Fatal(err)
	}
	if c.OriginKind != "function" || c.ConfigRevision != 1 || c.CreationCallerRevision != 1 {
		t.Fatal("host provenance missing")
	}
	// Standing admission has no execution Context or synthetic lease.
	call, err := s.BeginCron(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	now := time.Now().UTC().Add(time.Minute)
	if err := s.ClaimCron(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	target := workloadiam.TargetState{Active: true, Principal: observe().Principal}
	if err := s.AdmitCron(ctx, c, call, func() workloadiam.TargetState { return target }, now); err != nil {
		t.Fatal(err)
	}
}
func TestCronRevokeWaitRegrantAndResumeFutureOnly(t *testing.T) {
	s, observe, c, ref, set := cronFixture(t)
	ctx := t.Context()
	if err := s.UpsertCronFunction(ctx, observe, c); err != nil {
		t.Fatal(err)
	}
	call, err := s.BeginCron(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	now := time.Now().UTC().Add(time.Minute)
	if err := s.ClaimCron(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	set(false)
	set(true)
	target := workloadiam.TargetState{Active: true, Principal: observe().Principal}
	if err := s.AdmitCron(ctx, c, call, func() workloadiam.TargetState { return target }, now); !IsDenied(err) {
		t.Fatal("revoked registration revived", err)
	}
	if _, err := s.BeginCron(ctx, c); !IsDenied(err) {
		t.Fatal("old row revived on regrant", err)
	}
	if err := s.DB.SuspendAsyncCron(ctx, c, "revoked"); err != nil {
		t.Fatal(err)
	}
	current, err := s.DB.GetAsyncCron(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err := s.MutateCron(ctx, ref, current, "resume"); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.DB.GetAsyncCron(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.AuthorityState != "active" || resumed.NextRunAt == nil || !resumed.NextRunAt.After(before) || resumed.CreationCallerRevision != 1 ||
		resumed.AdmissionCallerRevision != 3 || resumed.ConfigRevision != 2 || resumed.AuthorityGeneration != 3 {
		t.Fatal("resume lost provenance or replayed past schedule")
	}
	if err := s.MutateCron(ctx, ref, current, "resume"); !errors.Is(err, database.ErrAsyncConflict) {
		t.Fatal("stale resume accepted", err)
	}
}
func TestCronFinalCASRejectsEditDisableAndStaleTarget(t *testing.T) {
	for _, action := range []string{"update", "disable", "stale_target", "delete_creator"} {
		t.Run(action, func(t *testing.T) {
			s, observe, c, ref, _ := cronFixture(t)
			ctx := t.Context()
			if err := s.UpsertCronFunction(ctx, observe, c); err != nil {
				t.Fatal(err)
			}
			call, err := s.BeginCron(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer call.Finish()
			now := time.Now().Add(time.Minute)
			if err := s.ClaimCron(ctx, c, now); err != nil {
				t.Fatal(err)
			}
			target := workloadiam.TargetState{Active: true, Principal: observe().Principal}
			if action == "stale_target" {
				target.Principal.ArtifactRevision = "obsolete"
			} else if action == "delete_creator" {
				if err := s.DB.DeleteFunction(ctx, c.CreatorID); err != nil {
					t.Fatal(err)
				}
			} else {
				updated := *c
				updated.Payload = `{"new":true}`
				if err := s.MutateCron(ctx, ref, &updated, action); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.AdmitCron(ctx, c, call, func() workloadiam.TargetState { return target }, now); err == nil {
				t.Fatal("stale firing admitted")
			}
		})
	}
}
func TestCronOperatorWorkOutlivesCredentialButMutationDoesNot(t *testing.T) {
	s, _, c, ref, _ := cronFixture(t)
	ctx := t.Context()
	c.Name = ""
	if err := s.CreateCronOperator(ctx, ref, c); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.DeleteAPIKey(ref.ID); err != nil {
		t.Fatal(err)
	}
	call, err := s.BeginCron(ctx, c)
	if err != nil {
		t.Fatal("credential revocation revoked accepted work", err)
	}
	defer call.Finish()
	if err := s.MutateCron(ctx, ref, c, "disable"); err == nil {
		t.Fatal("revoked credential mutated cron")
	}
}
