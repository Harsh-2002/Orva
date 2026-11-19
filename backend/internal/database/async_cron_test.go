package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func cronDB(t *testing.T) *Database {
	t.Helper()
	db := asyncDB(t)
	if err := db.InitCronSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}
func cronRow() *AsyncCron {
	next := time.Now().UTC().Add(time.Hour)
	return &AsyncCron{CronSchedule: CronSchedule{FunctionID: iamTestOwner, Name: "daily.one", CronExpr: "@every 1s", Timezone: "UTC", Enabled: true, Payload: "{}", NextRunAt: &next},
		AsyncProvenance: AsyncProvenance{OriginKind: "function", CreatorID: iamTestOwner}}
}
func cronRef(t *testing.T, db *Database) OperatorCredentialReference {
	t.Helper()
	ref := OperatorCredentialReference{Kind: "api_key", ID: iamTestActorID}
	if err := db.InsertAPIKey(&APIKey{ID: ref.ID, Name: "cron-admin", KeyHash: "fixture-cron", Prefix: "fixture", Permissions: `["admin"]`}); err != nil {
		t.Fatal(err)
	}
	return ref
}

var cronRevs = workloadiam.AsyncRevisions{Creator: 1, Target: 1}

func TestCronSchemaExplicitHistoricQuarantine(t *testing.T) {
	db := asyncDB(t)
	ctx := t.Context()
	c := cronRow()
	if err := db.InsertCronSchedule(&c.CronSchedule); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAsyncCron(ctx, c.ID); err == nil {
		t.Fatal("jobs initialization activated cron")
	}
	if err := db.InitCronSchemaForFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.InitCronSchemaForFixture(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAsyncCron(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginKind != "legacy_unknown" || got.AuthorityState != "quarantined" || got.CreatorID != "" || got.OperatorID != "" {
		t.Fatal("name implied origin")
	}
	rows, err := db.ListAsyncCrons(ctx, "", ptrTime(time.Now().Add(2*time.Hour)))
	if err != nil || len(rows) != 0 {
		t.Fatal("historic cron selected", err)
	}
	ref := cronRef(t, db)
	if err := db.SaveAsyncCron(ctx, got, &ref, cronRevs, "resume"); !errors.Is(err, ErrAsyncConflict) {
		t.Fatal("quarantined resume", err)
	}
}
func ptrTime(t time.Time) *time.Time { return &t }

func TestCronStorageCASAndResultsNeverOverwriteEdit(t *testing.T) {
	db := cronDB(t)
	ctx := t.Context()
	c := cronRow()
	if err := db.SaveAsyncCron(ctx, c, nil, cronRevs, "upsert"); err != nil {
		t.Fatal(err)
	}
	old, err := db.GetAsyncCron(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(2 * time.Hour)
	next := now.Add(time.Hour)
	if err := db.AdvanceAsyncCron(ctx, old, now, next); err != nil {
		t.Fatal(err)
	}
	ref := cronRef(t, db)
	edit, err := db.GetAsyncCron(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	edit.Payload = `{"new":true}`
	if err := db.SaveAsyncCron(ctx, edit, &ref, cronRevs, "update"); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitAsyncCron(ctx, old, now); !errors.Is(err, ErrAsyncConflict) {
		t.Fatal("stale edit admitted", err)
	}
	if err := db.FinishAsyncCron(ctx, old, now, "ok", ""); !errors.Is(err, ErrAsyncConflict) {
		t.Fatal("old completion overwrote edit", err)
	}
	current, err := db.GetAsyncCron(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ConfigRevision != 2 || current.CreationCallerRevision != 1 || current.Payload != edit.Payload {
		t.Fatal("config/provenance not preserved")
	}
	if err := db.SaveAsyncCron(ctx, current, &ref, cronRevs, "disable"); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitAsyncCron(ctx, edit, now); !errors.Is(err, ErrAsyncConflict) {
		t.Fatal("disabled queued firing admitted", err)
	}
}

func TestCronUpsertCannotAdoptOrResumeAndCapIsTransactional(t *testing.T) {
	db := cronDB(t)
	ctx := t.Context()
	c := cronRow()
	if err := db.SaveAsyncCron(ctx, c, nil, cronRevs, "upsert"); err != nil {
		t.Fatal(err)
	}
	if err := db.SuspendAsyncCron(ctx, c, "policy_denied"); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAsyncCron(ctx, cronRow(), nil, cronRevs, "upsert"); !errors.Is(err, ErrAsyncConflict) {
		t.Fatal("upsert resumed suspended", err)
	}
	for i := 1; i < 25; i++ {
		c := cronRow()
		c.Name = time.Unix(int64(i), 0).Format(time.RFC3339)
		if err := db.SaveAsyncCron(ctx, c, nil, cronRevs, "upsert"); err != nil {
			t.Fatal(err)
		}
	}
	c = cronRow()
	c.Name = "overflow"
	if err := db.SaveAsyncCron(ctx, c, nil, cronRevs, "upsert"); !errors.Is(err, ErrAsyncConflict) {
		t.Fatal("cap not applied", err)
	}
}

func TestCronOperatorCommitCredentialAndAuditRollback(t *testing.T) {
	db := cronDB(t)
	ctx := t.Context()
	ref := cronRef(t, db)
	c := cronRow()
	c.Name = ""
	if _, err := db.write.Exec(`CREATE TRIGGER cron_audit_fail BEFORE INSERT ON async_cron_audit BEGIN SELECT RAISE(ABORT,'fixture rollback');END`); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAsyncCron(ctx, c, &ref, cronRevs, "create"); err == nil {
		t.Fatal("audit failure committed")
	}
	rows, err := db.ListAsyncCrons(ctx, "", nil)
	if err != nil || len(rows) != 0 {
		t.Fatal("orphan schedule", err)
	}
	if _, err := db.write.Exec(`DROP TRIGGER cron_audit_fail`); err != nil {
		t.Fatal(err)
	} // Test-only trigger, never a migration.
	c = cronRow()
	c.Name = ""
	if err := db.SaveAsyncCron(ctx, c, &ref, cronRevs, "create"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`DELETE FROM api_keys WHERE id=?`, ref.ID); err != nil {
		t.Fatal(err)
	}
	old, _ := db.GetAsyncCron(ctx, c.ID)
	if err := db.SaveAsyncCron(ctx, old, &ref, cronRevs, "disable"); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal("revoked key mutated", err)
	}
	got, _ := db.GetAsyncCron(context.Background(), c.ID)
	if got.ConfigRevision != 1 || !got.Enabled || got.OriginKind != "operator" || got.OperatorID != ref.ID || got.CreatorID != "" {
		t.Fatal("failed mutation changed accepted operator work")
	}
}
