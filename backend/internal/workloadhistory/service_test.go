package workloadhistory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

type fixture struct {
	s              *Service
	a              *workloadasync.Service
	db             *database.Database
	sql            *sql.DB
	reg            *registry.Registry
	ref            auth.OperatorReference
	ctx            context.Context
	caller, target string
	job            *database.AsyncJob
	cron           *database.AsyncCron
}

func setup(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err = db.InitAsyncSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.InitCronSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.InitHistorySchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	f := &fixture{db: db, sql: conn, reg: registry.New(db), caller: ids.New(), target: ids.New(), ref: auth.OperatorReference{Kind: "api_key", ID: ids.New()}}
	f.ctx = auth.WithOperator(t.Context(), &auth.Principal{Kind: f.ref.Kind, ID: f.ref.ID})
	if err = db.InsertAPIKey(&database.APIKey{ID: f.ref.ID, Name: "history-admin", KeyHash: "fixture-only", Prefix: "fixture", Permissions: `["read","admin"]`}); err != nil {
		t.Fatal(err)
	}
	compiled := []*workloadiam.Evaluator{}
	for i, id := range []string{f.caller, f.target} {
		if err = f.reg.Set(&database.Function{ID: id, Name: []string{"creator", "target"}[i], Runtime: "node", Entrypoint: "handler.js", Status: "active", EnvVars: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		p := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}}
		if i == 0 {
			p.Allow = []workloadiam.Statement{{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.JobsEnqueue}, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: f.target}}}}
		} else {
			p.Inbound = []workloadiam.Acceptance{{CallerID: f.caller, Triggers: []workloadiam.Trigger{workloadiam.Job}}}
		}
		e, eerr := workloadiam.Compile(workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: "artifact"}, p)
		if eerr != nil {
			t.Fatal(eerr)
		}
		compiled = append(compiled, e)
	}
	a, err := workloadiam.NewAuthority(256, compiled)
	if err != nil {
		t.Fatal(err)
	}
	f.s = &Service{DB: db, Authority: a, CurrentArtifact: func(string) string { return "artifact" }}
	f.a = &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: f.s.CurrentArtifact}
	f.reg.DeletionHook = f.s
	f.job = f.addJob(t, f.caller, f.target)
	f.cron = &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: f.target, Name: "daily", CronExpr: "@every 1h", Timezone: "UTC", Enabled: true, Payload: `{"private":"old-cron-payload"}`}}
	if err = f.a.CreateCronOperator(f.ctx, f.ref, f.cron); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) addJob(t *testing.T, creator, target string) *database.AsyncJob {
	t.Helper()
	j := &database.AsyncJob{Job: database.Job{FunctionID: target, Payload: []byte(`{"private":"old-job-payload"}`), MaxAttempts: 3, IdempotencyKey: "sensitive-dedupe-key"},
		AsyncProvenance: database.AsyncProvenance{OriginKind: "function", CreatorID: creator, CreationCallerRevision: 1, CreationTargetRevision: 1}}
	// Different records use distinct raw keys; duplicate scope intentionally deduplicates.
	j.IdempotencyKey += ids.New()
	if err := f.db.EnqueueAsyncJob(f.ctx, j, nil); err != nil {
		t.Fatal(err)
	}
	return j
}
func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.sql.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.sql.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func (f *fixture) replacement(t *testing.T, name string) string {
	t.Helper()
	id := ids.New()
	if err := f.reg.Set(&database.Function{ID: id, Name: name, Runtime: "python", Entrypoint: "handler.py", Status: "active", EnvVars: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: "artifact"}, workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.Authority.Publish(0, e, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	return id
}
func TestHistoryDeleteInspectReissue(t *testing.T) {
	f := setup(t)
	waiting, err := f.a.BeginJob(f.ctx, f.job)
	if err != nil {
		t.Fatal(err)
	}
	defer waiting.Finish()
	drained := 0
	if err = f.reg.DeleteWithDrain(f.ctx, f.target, func() { drained++ }); err != nil {
		t.Fatal(err)
	}
	if drained != 1 || waiting.Context().Err() == nil {
		t.Fatal("delete did not retire after durable closure/cancel standing registration")
	}
	if _, err = f.a.BeginJob(f.ctx, f.job); !errors.Is(err, workloadiam.ErrDenied) {
		t.Fatal("retained work admitted after deletion", err)
	}
	if _, err = f.db.GetAsyncJob(f.ctx, f.job.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("archived job remained live", err)
	}
	if _, err = f.db.GetAsyncCron(f.ctx, f.cron.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("archived schedule remained live", err)
	}
	for _, action := range []string{"resume", "retry"} {
		if err = f.a.MutateJob(f.ctx, f.ref, f.job.ID, action); err == nil {
			t.Fatal("archive was mutable as live work")
		}
	}
	h, err := f.s.List(f.ctx, f.target, database.HistorySelector{}, 100)
	if err != nil || len(h) != 3 {
		t.Fatal("history absent", len(h), err)
	}
	for _, e := range h {
		if e.Reserved || strings.Contains(string(e.Metadata), "old-job-payload") || strings.Contains(string(e.Metadata), "old-cron-payload") || strings.Contains(string(e.Metadata), "sensitive-dedupe-key") {
			t.Fatal("payload/key retained or history not committed")
		}
		if e.Kind == "delete" {
			continue
		}
		var m database.WorkHistory
		if err = json.Unmarshal(e.Metadata, &m); err != nil || m.Executable || m.PayloadAvailable || m.TargetID != f.target || m.PayloadBytes == 0 || m.Reason != "target_deleted" {
			t.Fatal("invalid inspect-only metadata", err)
		}
	}
	if f.count(t, `SELECT count(*) FROM job_idempotency_v2 WHERE job_id=?`, f.job.ID) != 0 {
		t.Fatal("old idempotency pointer survived")
	}
	freshTarget := f.replacement(t, "target")
	if freshTarget == f.target {
		t.Fatal("recreated name reused UUID")
	}
	r := database.HistoryReissue{Source: database.HistorySelector{Kind: "job", ID: f.job.ID}, TargetID: freshTarget,
		Payload: json.RawMessage(`{"fresh":"reviewed"}`), ScheduledAt: time.Now().UTC(), MaxAttempts: 2, Reviewed: true, Confirm: true}
	newID, err := f.s.Reissue(f.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.db.GetAsyncJob(f.ctx, newID)
	if err != nil || job.ID == f.job.ID || job.FunctionID != freshTarget || job.OriginKind != "operator" || job.OperatorID != f.ref.ID ||
		job.CreatorID != "" || string(job.Payload) != string(r.Payload) || job.IdempotencyKey != "" || job.CreationCallerRevision != 0 {
		t.Fatal("reissue adopted old work/origin/payload", err)
	}
	call, err := f.a.BeginJob(f.ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	if err = f.a.ClaimJob(f.ctx, job, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = f.a.AdmitJob(f.ctx, job, call, func() workloadiam.TargetState {
		return workloadiam.TargetState{Active: true, Principal: workloadiam.Principal{FunctionID: freshTarget, WorkerID: "host-fixture", ArtifactRevision: "artifact"}}
	}); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT count(*) FROM work_history WHERE kind='reissue' AND original_id=?`, newID) != 1 {
		t.Fatal("missing origin-change audit")
	}
	if f.count(t, `SELECT count(*) FROM async_work_audit WHERE job_id=? AND action='reissue' AND actor_id=?`, newID, f.ref.ID) != 1 {
		t.Fatal("missing operator audit")
	}
	t.Log("target UUID deleted; job+cron metadata only; retained admission revoked; same-name fresh UUID; new operator job/fresh payload admitted; origin-change audit committed")
}

func TestHistoryCapacityAndCredentialDenialBeforeClosure(t *testing.T) {
	for _, mode := range []string{"rows", "bytes", "expired", "revoked", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			call, err := f.a.BeginJob(f.ctx, f.job)
			if err != nil {
				t.Fatal(err)
			}
			defer call.Finish()
			ctx := f.ctx
			switch mode {
			case "rows":
				f.exec(t, `UPDATE work_history_capacity SET rows_used=? WHERE id=1`, database.HistoryMaxRows-1)
			case "bytes":
				f.exec(t, `UPDATE work_history_capacity SET bytes_used=? WHERE id=1`, database.HistoryMaxBytes-1)
			case "expired":
				f.exec(t, `UPDATE api_keys SET expires_at=? WHERE id=?`, time.Now().UTC().Add(-time.Minute), f.ref.ID)
			case "revoked":
				f.exec(t, `DELETE FROM api_keys WHERE id=?`, f.ref.ID)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			drained := false
			if err = f.reg.DeleteWithDrain(ctx, f.target, func() { drained = true }); err == nil {
				t.Fatal("expected refusal")
			}
			fn, err := f.reg.Get(f.target)
			if err != nil || fn.Status != "active" || drained || call.Context().Err() != nil {
				t.Fatal("denial changed active/cache/admission/pool state", err)
			}
			if f.count(t, `SELECT count(*) FROM work_history`) != 0 || f.count(t, `SELECT count(*) FROM work_history_deletions`) != 0 {
				t.Fatal("denial retained a partial reservation")
			}
		})
	}
}

func TestHistoryFailedCommitAndLiveCommitRevocation(t *testing.T) {
	for _, mode := range []string{"commit", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			call, err := f.a.BeginJob(f.ctx, f.job)
			if err != nil {
				t.Fatal(err)
			}
			defer call.Finish()
			if mode == "commit" {
				f.exec(t, `CREATE TABLE force_bad_history(x TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER fail_history AFTER INSERT ON work_history BEGIN INSERT INTO force_bad_history VALUES('missing'); END;`)
			} else {
				f.exec(t, `CREATE TRIGGER fail_history AFTER INSERT ON work_history BEGIN UPDATE api_keys SET permissions='["read"]'; END;`)
			}
			if err = f.reg.Delete(f.ctx, f.target); err == nil {
				t.Fatal("expected commit/authority failure")
			}
			fn, err := f.db.GetFunction(f.target)
			if err != nil || fn.Status != "active" || call.Context().Err() != nil || f.count(t, `SELECT rows_used+bytes_used FROM work_history_capacity`) != 0 ||
				f.count(t, `SELECT count(*) FROM work_history`) != 0 {
				t.Fatal("failed transaction changed source/authority/capacity", err)
			}
			f.exec(t, `DROP TRIGGER fail_history`)
			if err = f.reg.Delete(f.ctx, f.target); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoryPartialBatchRetryAndReservedPurge(t *testing.T) {
	f := setup(t)
	var failID string
	for i := 0; i < 70; i++ {
		j := f.addJob(t, f.caller, f.target)
		if i == 64 {
			failID = j.ID
		}
	}
	f.exec(t, `CREATE TRIGGER fail_batch BEFORE DELETE ON jobs WHEN OLD.id='`+failID+`' BEGIN SELECT RAISE(ABORT,'injected batch failure'); END;`)
	if err := f.reg.Delete(f.ctx, f.target); err == nil {
		t.Fatal("expected partial batch failure")
	}
	fn, err := f.db.GetFunction(f.target)
	if err != nil || fn.Status != "deleting" {
		t.Fatal("partial failure was not resumable deleting", err)
	}
	if f.count(t, `SELECT count(*) FROM work_history WHERE reserved=0`) != database.HistoryBatch {
		t.Fatal("first batch did not commit atomically")
	}
	var rootID string
	if err = f.sql.QueryRow(`SELECT deletion_id FROM work_history_deletions WHERE owner_id=?`, f.target).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE work_history SET eligible_at=?`, time.Now().UTC().Add(-time.Hour))
	if err = f.s.Purge(f.ctx, []database.HistorySelector{{Kind: "delete", ID: rootID}}, true); err == nil {
		t.Fatal("purge consumed reserved capacity/root")
	}
	count := f.count(t, `SELECT count(*) FROM work_history`)
	f.exec(t, `DROP TRIGGER fail_batch`)
	if err = f.reg.Delete(f.ctx, f.target); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT count(*) FROM work_history`) != count || f.count(t, `SELECT rows_reserved+bytes_reserved FROM work_history_capacity`) != 0 {
		t.Fatal("retry duplicated history or leaked reservation")
	}
	if err = f.reg.Delete(f.ctx, f.target); err != nil {
		t.Fatal("completed deletion retry", err)
	}
	if f.count(t, `SELECT count(*) FROM work_history`) != count {
		t.Fatal("completed retry duplicated history")
	}
}

func TestHistoryCreatorDeletionSuspendsRetainedUUID(t *testing.T) {
	f := setup(t)
	if err := f.reg.Delete(f.ctx, f.caller); err != nil {
		t.Fatal(err)
	}
	j, err := f.db.GetAsyncJob(f.ctx, f.job.ID)
	if err != nil || j.CreatorID != f.caller || j.AuthorityState != "suspended" || j.AuthorityReason != "creator_missing" {
		t.Fatal("creator deletion lost provenance or target-live work", err)
	}
	f.replacement(t, "creator")
	if err = f.a.MutateJob(f.ctx, f.ref, j.ID, "resume"); !errors.Is(err, workloadiam.ErrDenied) {
		t.Fatal("same-name creator resurrected old UUID", err)
	}
}

func TestHistoryPermissionsConfirmAndPurgeEligibility(t *testing.T) {
	f := setup(t)
	if err := f.reg.Delete(f.ctx, f.target); err != nil {
		t.Fatal(err)
	}
	target := f.replacement(t, "target")
	req := database.HistoryReissue{Source: database.HistorySelector{Kind: "job", ID: f.job.ID}, TargetID: target, Payload: json.RawMessage(`{}`),
		ScheduledAt: time.Now().UTC(), MaxAttempts: 1, Reviewed: true, Confirm: true}
	sel := []database.HistorySelector{req.Source}
	if err := f.s.Purge(f.ctx, sel, true); err == nil {
		t.Fatal("fresh history was purgeable before 90 days")
	}
	if _, err := f.s.Reissue(f.ctx, database.HistoryReissue{Source: req.Source, TargetID: target, Reviewed: true, Confirm: true}); err == nil {
		t.Fatal("reissue filled in missing payload/config")
	}
	noConfirm := req
	noConfirm.Confirm = false
	if _, err := f.s.Reissue(f.ctx, noConfirm); err == nil {
		t.Fatal("confirm=false reissued")
	}
	if err := f.s.Purge(f.ctx, sel, false); err == nil {
		t.Fatal("confirm=false purged")
	}
	for _, perms := range []string{`["read"]`, `["write"]`} {
		f.exec(t, `UPDATE api_keys SET permissions=? WHERE id=?`, perms, f.ref.ID)
		if _, err := f.s.Reissue(f.ctx, req); !errors.Is(err, database.ErrOperatorDenied) {
			t.Fatal("non-admin reissued", err)
		}
		if err := f.s.Purge(f.ctx, sel, true); !errors.Is(err, database.ErrOperatorDenied) {
			t.Fatal("non-admin purged", err)
		}
		_, err := f.s.List(f.ctx, "", database.HistorySelector{}, 100)
		if (perms == `["read"]` && err != nil) || (perms == `["write"]` && err == nil) {
			t.Fatal("read permission mismatch", err)
		}
	}
	f.exec(t, `UPDATE api_keys SET permissions='["read","admin"]' WHERE id=?`, f.ref.ID)
	forged := auth.WithOperator(f.ctx, &auth.Principal{Kind: "channel", ID: f.ref.ID})
	if _, err := f.s.Reissue(forged, req); err == nil {
		t.Fatal("channel forged operator reissue")
	}
	f.exec(t, `UPDATE work_history SET eligible_at=? WHERE kind='job' AND original_id=?`, time.Now().UTC().Add(-time.Hour), f.job.ID)
	if _, err := f.s.Get(f.ctx, req.Source); err != nil {
		t.Fatal("expired record disappeared before explicit purge", err)
	}
	if err := f.s.Purge(f.ctx, sel, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Reissue(f.ctx, req); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("purged source resolved as live/reissue", err)
	}
	if f.count(t, `SELECT count(*) FROM work_history WHERE kind='purge'`) != 1 {
		t.Fatal("purge not audited")
	}
}
