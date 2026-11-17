package database

import (
	"context"
	"errors"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"sync"
	"testing"
	"time"
)

func asyncDB(t *testing.T) *Database {
	t.Helper()
	db := newTestDB(t)
	for _, id := range []string{iamTestOwner, iamTestOther, iamTestActorID} {
		iamTestFunction(t, db, id, true)
	}
	if err := db.InitAsyncSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}
func asyncJob(creator, key string) *AsyncJob {
	return &AsyncJob{Job: Job{FunctionID: iamTestOther, Payload: []byte(`{}`), IdempotencyKey: key},
		AsyncProvenance: AsyncProvenance{OriginKind: "function", CreatorID: creator, CreationCallerRevision: 1, CreationTargetRevision: 1}}
}
func TestAsyncSchemaInertAndLegacyReview(t *testing.T) {
	db := newTestDB(t)
	var n int
	if err := db.read.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='job_idempotency_v2'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	iamTestFunction(t, db, iamTestOwner, true)
	iamTestFunction(t, db, iamTestOther, true)
	known := &Job{FunctionID: iamTestOther, EnqueuedByFunctionID: iamTestOwner, Payload: []byte("{}")}
	unknown := &Job{FunctionID: iamTestOther, Payload: []byte("{}")}
	for _, j := range []*Job{known, unknown} {
		if err := db.EnqueueJob(j); err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err := db.read.QueryRow(`SELECT sql FROM sqlite_master WHERE name='idx_jobs_idempotency'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.InitAsyncSchemaForFixture(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	a, err := db.GetAsyncJob(t.Context(), known.ID)
	if err != nil || a.OriginKind != "function" || a.AuthorityState != "suspended" {
		t.Fatal(a, err)
	}
	b, err := db.GetAsyncJob(t.Context(), unknown.ID)
	if err != nil || b.OriginKind != "legacy_unknown" || b.AuthorityState != "quarantined" {
		t.Fatal(b, err)
	}
	var after string
	_ = db.read.QueryRow(`SELECT sql FROM sqlite_master WHERE name='idx_jobs_idempotency'`).Scan(&after)
	if before != after {
		t.Fatal("legacy index changed")
	}
}
func TestAsyncIdempotencyIsolationExpiryRollback(t *testing.T) {
	db := asyncDB(t)
	ctx := t.Context()
	legacy := &Job{FunctionID: iamTestOther, IdempotencyKey: "equal", Payload: []byte("{}")}
	if err := db.EnqueueJob(legacy); err != nil {
		t.Fatal(err)
	}
	jobs := []*AsyncJob{asyncJob(iamTestOwner, "equal"), asyncJob(iamTestActorID, "equal")}
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Go(func() {
			if err := db.EnqueueAsyncJob(ctx, j, nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if jobs[0].ID == jobs[1].ID || jobs[0].ID == legacy.ID || jobs[1].ID == legacy.ID {
		t.Fatal("creator or legacy collision")
	}
	for _, j := range jobs {
		replay := asyncJob(j.CreatorID, "equal")
		if err := db.EnqueueAsyncJob(ctx, replay, nil); err != nil || !replay.Replayed || replay.ID != j.ID {
			t.Fatal(replay, err)
		}
		var key any
		if err := db.read.QueryRow(`SELECT idempotency_key FROM jobs WHERE id=?`, j.ID).Scan(&key); err != nil || key != nil {
			t.Fatal("new job used legacy index", err)
		}
	}
	if _, err := db.write.Exec(`UPDATE job_idempotency_v2 SET expires_at=?`, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	fresh := asyncJob(iamTestOwner, "equal")
	if err := db.EnqueueAsyncJob(ctx, fresh, nil); err != nil || fresh.Replayed || fresh.ID == jobs[0].ID {
		t.Fatal(fresh, err)
	}
	old, err := db.GetAsyncJob(ctx, jobs[0].ID)
	if err != nil || old.IdempotencyKey != "equal" {
		t.Fatal("expired reservation erased original retry scope", err)
	}
	_, err = db.write.Exec(`CREATE TRIGGER async_test_fail BEFORE INSERT ON job_idempotency_v2 WHEN NEW.raw_key='rollback' BEGIN SELECT RAISE(ABORT,'fixture rollback');END`)
	if err != nil {
		t.Fatal(err)
	}
	failed := asyncJob(iamTestOwner, "rollback")
	if err := db.EnqueueAsyncJob(ctx, failed, nil); err == nil {
		t.Fatal("expected rollback")
	}
	if _, err := db.GetJob(failed.ID); err == nil {
		t.Fatal("orphan job committed")
	}
	if n := iamTestCount(t, db, "job_idempotency_v2"); n != 2 {
		t.Fatalf("orphan reservation: %d", n)
	}
}
func TestAsyncClaimSuspensionDoesNotSpendAttempt(t *testing.T) {
	db := asyncDB(t)
	j := asyncJob(iamTestOwner, "")
	if err := db.EnqueueAsyncJob(t.Context(), j, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimAsyncJob(t.Context(), j, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.SuspendAsyncJob(t.Context(), j, "policy_denied"); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAsyncJob(t.Context(), j.ID)
	if err != nil || got.Attempts != 0 || got.Status != "pending" || got.AuthorityState != "suspended" {
		t.Fatal(got, err)
	}
	if err := db.StartAsyncJob(t.Context(), j); !errors.Is(err, ErrAsyncConflict) {
		t.Fatal("obsolete final admission", err)
	}
}
func TestAsyncOperatorLiveAdminAndResumePreservesOrigin(t *testing.T) {
	db := asyncDB(t)
	ctx := t.Context()
	ref := OperatorCredentialReference{Kind: "api_key", ID: iamTestActorID}
	if err := db.InsertAPIKey(&APIKey{ID: ref.ID, Name: "fixture", KeyHash: "async-fixture-not-a-bearer", Prefix: "fixture", Permissions: `["read"]`}); err != nil {
		t.Fatal(err)
	}
	j := asyncJob(iamTestOwner, "")
	if err := db.EnqueueAsyncJob(ctx, j, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SuspendAsyncJob(ctx, j, "policy_denied"); err != nil {
		t.Fatal(err)
	}
	j, _ = db.GetAsyncJob(ctx, j.ID)
	if err := db.MutateAsyncJobAsOperator(ctx, ref, j, "resume", workloadiam.AsyncRevisions{Creator: 1, Target: 1}); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`UPDATE api_keys SET permissions='["admin"]' WHERE id=?`, ref.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateAsyncJobAsOperator(ctx, ref, j, "resume", workloadiam.AsyncRevisions{Creator: 1, Target: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetAsyncJob(ctx, j.ID)
	if got.OriginKind != "function" || got.CreatorID != iamTestOwner || got.OperatorID != "" {
		t.Fatal("admin elevated function origin")
	}
	if err := db.MarkJobSuccess(j.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateAsyncJobAsOperator(ctx, ref, got, "resume", workloadiam.AsyncRevisions{Creator: 1, Target: 1}); !errors.Is(err, ErrAsyncConflict) {
		t.Fatal("terminal resume", err)
	}
	op := asyncJob("", "")
	if err := db.EnqueueAsyncJob(ctx, op, &ref); err != nil {
		t.Fatal(err)
	}
	if op.OriginKind != "operator" || op.CreatorID != "" || op.OperatorID != ref.ID {
		t.Fatal("operator origin missing")
	}
	if _, err := db.write.Exec(`DELETE FROM api_keys WHERE id=?`, ref.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimAsyncJob(ctx, op, time.Now()); err != nil {
		t.Fatal("accepted durable work tied to expired credential", err)
	}
	if err := db.MutateAsyncJobAsOperator(context.Background(), ref, op, "delete", workloadiam.AsyncRevisions{}); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal("revoked operator mutated", err)
	}
}

func TestAsyncConcurrentSameCreatorAndOperatorScopes(t *testing.T) {
	db := asyncDB(t)
	jobs := make([]*AsyncJob, 8)
	var wg sync.WaitGroup
	for i := range jobs {
		jobs[i] = asyncJob(iamTestOwner, "concurrent")
		wg.Go(func() {
			if err := db.EnqueueAsyncJob(t.Context(), jobs[i], nil); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for _, j := range jobs {
		if j.ID != jobs[0].ID {
			t.Fatal("same creator raced into duplicate jobs")
		}
	}
	if n := iamTestCount(t, db, "job_idempotency_v2"); n != 1 {
		t.Fatal(n)
	}
	for _, id := range []string{iamTestOwner, iamTestActorID} {
		if err := db.InsertAPIKey(&APIKey{ID: id, Name: id, KeyHash: id, Prefix: "fixture", Permissions: `["admin"]`}); err != nil {
			t.Fatal(err)
		}
		j := asyncJob("", "concurrent")
		if err := db.EnqueueAsyncJob(t.Context(), j, &OperatorCredentialReference{Kind: "api_key", ID: id}); err != nil {
			t.Fatal(err)
		}
		if j.ID == jobs[0].ID || j.Replayed {
			t.Fatal("operator/function scopes collided")
		}
	}
	if n := iamTestCount(t, db, "job_idempotency_v2"); n != 3 {
		t.Fatal(n)
	}
}

func TestAsyncCredentialCASAndAuditFailureRollback(t *testing.T) {
	for _, action := range []string{"create", "resume", "stale", "expired"} {
		t.Run(action, func(t *testing.T) {
			db := asyncDB(t)
			ctx := t.Context()
			ref := OperatorCredentialReference{Kind: "api_key", ID: iamTestActorID}
			key := &APIKey{ID: ref.ID, Name: "fixture", KeyHash: "not-a-bearer", Prefix: "fixture", Permissions: `["admin"]`}
			if action == "expired" {
				past := time.Now().Add(-time.Second)
				key.ExpiresAt = &past
			}
			if err := db.InsertAPIKey(key); err != nil {
				t.Fatal(err)
			}
			j := asyncJob(iamTestOwner, "rollback-scope")
			if action == "create" || action == "expired" {
				if action == "create" {
					if _, err := db.write.Exec(`CREATE TRIGGER async_audit_abort BEFORE INSERT ON async_work_audit BEGIN SELECT RAISE(ABORT,'fixture rollback');END;`); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.EnqueueAsyncJob(ctx, j, &ref); err == nil {
					t.Fatal("invalid credential/failed audit committed")
				}
				if n := iamTestCount(t, db, "jobs"); n != 0 {
					t.Fatal("orphan job", n)
				}
				if n := iamTestCount(t, db, "job_idempotency_v2"); n != 0 {
					t.Fatal("orphan reservation", n)
				}
				return
			}
			if err := db.EnqueueAsyncJob(ctx, j, nil); err != nil {
				t.Fatal(err)
			}
			if err := db.SuspendAsyncJob(ctx, j, "policy_denied"); err != nil {
				t.Fatal(err)
			}
			stored, err := db.GetAsyncJob(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			candidate := stored
			if action == "stale" {
				candidate = j
			} else {
				if _, err := db.write.Exec(`CREATE TRIGGER async_audit_abort BEFORE INSERT ON async_work_audit BEGIN SELECT RAISE(ABORT,'fixture rollback');END;`); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.MutateAsyncJobAsOperator(ctx, ref, candidate, "resume", workloadiam.AsyncRevisions{Creator: 2, Target: 2}); err == nil {
				t.Fatal("failed transaction committed")
			}
			got, err := db.GetAsyncJob(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.AuthorityState != "suspended" || got.AuthorityGeneration != stored.AuthorityGeneration || got.AdmissionCallerRevision != 1 || got.Attempts != 0 {
				t.Fatal("failed CAS/audit changed job")
			}
			if n := iamTestCount(t, db, "async_work_audit"); n != 0 {
				t.Fatal("orphan audit", n)
			}
		})
	}
}
