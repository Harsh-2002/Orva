package workloadhistory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func (f *fixture) ledger(t *testing.T) {
	t.Helper()
	var rows, bytes, reserved, reservedBytes int
	if err := f.sql.QueryRow(`SELECT rows_used,bytes_used,rows_reserved,bytes_reserved FROM work_history_capacity`).Scan(&rows, &bytes, &reserved, &reservedBytes); err != nil {
		t.Fatal(err)
	}
	if rows != f.count(t, `SELECT count(*) FROM work_history`) || bytes != f.count(t, `SELECT COALESCE(sum(length(CAST(metadata AS BLOB))),0) FROM work_history`) ||
		reserved != f.count(t, `SELECT count(*) FROM work_history WHERE reserved=1`) || reservedBytes != f.count(t, `SELECT COALESCE(sum(metadata_bytes),0) FROM work_history WHERE reserved=1`) {
		t.Fatal("canonical byte/row reservation ledger mismatch")
	}
}

func TestHistoryConcurrentActorIsolation(t *testing.T) {
	f := setup(t)
	second := f.replacement(t, "second-target")
	f.addJob(t, f.caller, second)
	actorB := ids.New()
	if err := f.db.InsertAPIKey(&database.APIKey{ID: actorB, Name: "actor-b", KeyHash: "fixture-b", Prefix: "fixture", Permissions: `["read","admin"]`}); err != nil {
		t.Fatal(err)
	}
	ctxB := auth.WithOperator(t.Context(), &auth.Principal{Kind: "api_key", ID: actorB})
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for _, c := range []struct {
		ctx context.Context
		id  string
	}{{f.ctx, f.target}, {ctxB, second}} {
		wg.Add(1)
		go func(ctx context.Context, id string) { defer wg.Done(); failures <- f.reg.Delete(ctx, id) }(c.ctx, c.id)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ owner, actor string }{{f.target, f.ref.ID}, {second, actorB}} {
		items, err := f.s.List(f.ctx, c.owner, database.HistorySelector{}, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range items {
			if item.Kind != "delete" {
				continue
			}
			var root struct {
				Actor database.IAMActorReference `json:"actor"`
			}
			if json.Unmarshal(item.Metadata, &root) != nil || root.Actor.ID != c.actor || root.Actor.Kind != "api_key" {
				t.Fatal("request actor leaked across deletion")
			}
			found = true
		}
		if !found {
			t.Fatal("missing per-request deletion audit")
		}
	}
	f.ledger(t)
}

func TestHistoryClosedAdmissionConcurrentOperations(t *testing.T) {
	f := setup(t)
	waiting, err := f.a.BeginJob(f.ctx, f.job)
	if err != nil {
		t.Fatal(err)
	}
	defer waiting.Finish()
	marked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- f.reg.DeleteWithDrain(f.ctx, f.target, func() { close(marked); <-release }) }()
	<-marked
	// These operations must complete while retirement is paused, not wait on
	// an IAM gate held across the process/pool callback.
	effect := make(chan error, 1)
	go func() { _, err := f.s.List(f.ctx, f.target, database.HistorySelector{}, 100); effect <- err }()
	select {
	case err := <-effect:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(3 * time.Second):
		t.Error("pool callback held IAM/DB gate")
	}
	if waiting.Context().Err() == nil {
		t.Error("standing registration was not cancelled before retirement")
	}
	if _, err := f.a.BeginJob(f.ctx, f.job); !errors.Is(err, workloadiam.ErrDenied) {
		t.Error("late standing admission", err)
	}
	if err := f.a.MutateJob(f.ctx, f.ref, f.job.ID, "resume"); !errors.Is(err, workloadiam.ErrDenied) {
		t.Error("late resume", err)
	}
	j := &database.AsyncJob{Job: database.Job{FunctionID: f.target, Payload: []byte(`{}`)}}
	if err := f.a.EnqueueOperator(f.ctx, f.ref, j); !errors.Is(err, workloadiam.ErrDenied) {
		t.Error("late enqueue", err)
	}
	e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: f.target, Revision: 1, ApprovedArtifact: "artifact"},
		workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.Authority.Publish(0, e, func() error { t.Error("late policy persisted"); return nil }); !errors.Is(err, workloadiam.ErrDenied) {
		t.Error("retired UUID accepted policy publication", err)
	}
	items, err := f.s.List(f.ctx, f.target, database.HistorySelector{}, 100)
	if err != nil {
		t.Error(err)
	}
	for _, item := range items {
		if err = f.s.Purge(f.ctx, []database.HistorySelector{{Kind: item.Kind, ID: item.ID}}, true); err == nil {
			t.Error("pending reservation purge succeeded")
		}
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	f.ledger(t)
}

func TestHistoryMissingReferenceAndExactRetry(t *testing.T) {
	f := setup(t)
	if err := f.reg.Delete(t.Context(), f.target); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatal("missing trusted context accepted", err)
	}
	fn, err := f.db.GetFunction(f.target)
	if err != nil || fn.Status != "active" || f.count(t, `SELECT count(*) FROM work_history`) != 0 {
		t.Fatal("missing reference changed state", err)
	}
	cancel, err := f.s.Prepare(f.ctx, f.target)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	f.exec(t, `UPDATE work_history_deletions SET actor_id=? WHERE owner_id=?`, ids.New(), f.target)
	if err = f.reg.Delete(f.ctx, f.target); !errors.Is(err, database.ErrAsyncConflict) {
		t.Fatal("mismatching persisted reservation accepted on retry", err)
	}
	f.ledger(t)
}

func TestHistoryReissueRollbackAtCommit(t *testing.T) {
	for _, mode := range []string{"commit", "expiry", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			if err := f.reg.Delete(f.ctx, f.target); err != nil {
				t.Fatal(err)
			}
			target := f.replacement(t, "target")
			before := f.count(t, `SELECT count(*) FROM work_history`)
			switch mode {
			case "commit":
				f.exec(t, `CREATE TABLE bad_reissue(x TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER break_reissue AFTER INSERT ON jobs BEGIN INSERT INTO bad_reissue VALUES('missing'); END;`)
			case "expiry":
				f.exec(t, `CREATE TRIGGER break_reissue AFTER INSERT ON jobs BEGIN UPDATE api_keys SET expires_at='2000-01-01 00:00:00'; END;`)
			case "revoke":
				f.exec(t, `CREATE TRIGGER break_reissue AFTER INSERT ON jobs BEGIN DELETE FROM api_keys; END;`)
			}
			r := database.HistoryReissue{Source: database.HistorySelector{Kind: "job", ID: f.job.ID}, TargetID: target, Payload: json.RawMessage(`{"fresh":true}`),
				ScheduledAt: time.Now().UTC(), MaxAttempts: 1, Confirm: true, Reviewed: true}
			id, err := f.s.Reissue(f.ctx, r)
			if err == nil || id != "" || f.count(t, `SELECT count(*) FROM jobs WHERE function_id=?`, target) != 0 ||
				f.count(t, `SELECT count(*) FROM work_history`) != before {
				t.Fatal("failed reissue committed work/audit", err)
			}
			f.ledger(t)
		})
	}
}

func TestHistoryProcessFaultAfterReservation(t *testing.T) {
	f := setup(t)
	var path string
	if err := f.sql.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestHistoryProcessFaultHelper$")
	command.Env = append(os.Environ(), "ORVA_HISTORY_FAULT_PATH="+path, "ORVA_HISTORY_FAULT_OWNER="+f.target, "ORVA_HISTORY_FAULT_ACTOR="+f.ref.ID)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 43 {
		t.Fatalf("expected bounded child process fault at reservation, got %v: %s", err, output)
	}
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.InitHistorySchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.db = db
	f.s.DB = db
	f.a.DB = db
	f.reg = registry.New(db)
	f.reg.DeletionHook = f.s
	fn, err := db.GetFunction(f.target)
	if err != nil || fn.Status != "deleting" || f.count(t, `SELECT count(*) FROM work_history WHERE reserved=1`) != 3 {
		t.Fatal("child fault lost durable progress", err)
	}
	if err = f.reg.Delete(f.ctx, f.target); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT count(*) FROM work_history`) != 3 {
		t.Fatal("process-fault retry duplicated tombstones")
	}
	f.ledger(t)
	t.Log("real child exit after committed reservation; same-UUID deletion retry completes without duplicate accounting; not power-loss/reboot acceptance")
}

func TestHistoryProcessFaultHelper(t *testing.T) {
	path := os.Getenv("ORVA_HISTORY_FAULT_PATH")
	if path == "" {
		t.Skip("bounded child only")
	}
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.InitHistorySchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	a, err := workloadiam.NewAuthority(8, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: db, Authority: a}
	reg := registry.New(db)
	reg.DeletionHook = s
	ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: "api_key", ID: os.Getenv("ORVA_HISTORY_FAULT_ACTOR")})
	if err = reg.DeleteWithDrain(ctx, os.Getenv("ORVA_HISTORY_FAULT_OWNER"), func() { os.Exit(43) }); err != nil {
		t.Fatal(err)
	}
	t.Fatal("child did not reach post-reservation fault")
}
