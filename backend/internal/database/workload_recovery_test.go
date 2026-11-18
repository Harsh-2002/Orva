package database

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestRecoveryLegacyDryRunMigrationAndNoRestartRegrant(t *testing.T) {
	db := newTestDB(t)
	iamTestFunction(t, db, iamTestOwner, false)
	iamTestFunction(t, db, iamTestOther, true)
	report, err := db.RecoveryPreflight(t.Context())
	if err != nil || report.Functions != 2 || report.LegacySDKEnabled != 1 || report.SuggestedPolicy != iamTestEmpty {
		t.Fatal("dry run", err)
	}
	var count int
	if err := db.read.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='workload_recovery'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("dry run mutated schema")
	}
	first, err := db.ApplyRecoveryForFixture(t.Context(), "")
	if err != nil || first.Epoch != 0 || first.Operation != "" {
		t.Fatal("migration", err)
	}
	for _, id := range []string{iamTestOwner, iamTestOther} {
		p, err := db.GetIAMPolicy(t.Context(), id)
		if err != nil || p.PolicyJSON != iamTestEmpty || p.ReviewRequired != (id == iamTestOther) || p.Quarantined {
			t.Fatal("legacy expanded", err)
		}
	}
	op := ids.New()
	r, err := db.ApplyRecoveryForFixture(t.Context(), op)
	if err != nil || r.Epoch != 1 {
		t.Fatal("restore", err)
	}
	p, err := db.GetIAMPolicy(t.Context(), iamTestOwner)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := db.SaveIAMPolicy(t.Context(), iamTestChange(p, iamTestGrant))
	if err != nil {
		t.Fatal(err)
	}
	normal, err := db.ApplyRecoveryForFixture(t.Context(), "")
	if err != nil || normal != r {
		t.Fatal("restart invented restore", err)
	}
	got, err := db.GetIAMPolicy(t.Context(), iamTestOwner)
	if err != nil || !reflect.DeepEqual(got, approved) {
		t.Fatal("restart reset individual review", err)
	}
	other, err := db.GetIAMPolicy(t.Context(), iamTestOther)
	if err != nil || !other.Quarantined {
		t.Fatal("other policy escaped", err)
	}
	replay, err := db.ApplyRecoveryForFixture(t.Context(), op)
	if err != nil || replay != r {
		t.Fatal("same-operation replay", err)
	}
	got, err = db.GetIAMPolicy(t.Context(), iamTestOwner)
	if err != nil || !reflect.DeepEqual(got, approved) {
		t.Fatal("replay reset already committed state")
	}
	again, err := db.ApplyRecoveryForFixture(t.Context(), ids.New())
	if err != nil || again.Epoch != 2 {
		t.Fatal("new restore", err)
	}
	got, err = db.GetIAMPolicy(t.Context(), iamTestOwner)
	if err != nil || !got.Quarantined || got.PolicyJSON != approved.PolicyJSON || got.ApprovedArtifact != approved.ApprovedArtifact {
		t.Fatal("new restore did not retain/quarantine")
	}
}

func TestRecoveryFailedCommitHasNoPartialPolicyAuditOrEpoch(t *testing.T) {
	db, p := iamTestSetup(t)
	if _, err := db.ApplyRecoveryForFixture(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	p, err := db.SaveIAMPolicy(t.Context(), iamTestChange(p, iamTestGrant))
	if err != nil {
		t.Fatal(err)
	}
	audit, err := db.ListIAMPolicyAudit(t.Context(), p.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.write.Exec(`CREATE TABLE recovery_failure_parent(id INTEGER PRIMARY KEY);
	 CREATE TABLE recovery_failure_child(id INTEGER REFERENCES recovery_failure_parent(id) DEFERRABLE INITIALLY DEFERRED);
	 CREATE TRIGGER recovery_failed_commit AFTER UPDATE ON workload_recovery BEGIN
	  INSERT INTO recovery_failure_child VALUES(1); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyRecoveryForFixture(t.Context(), ids.New()); err == nil {
		t.Fatal("deferred COMMIT succeeded")
	}
	iamTestUnchanged(t, db, p, audit)
	var epoch int
	if err := db.read.QueryRow(`SELECT epoch FROM workload_recovery`).Scan(&epoch); err != nil || epoch != 0 {
		t.Fatal("partial epoch")
	}
	if err := db.read.QueryRow(`SELECT count(*) FROM recovery_failure_child`).Scan(&epoch); err != nil || epoch != 0 {
		t.Fatal("partial transaction")
	}
}

func TestRecoveryCounterOverflowAndAuditRetention(t *testing.T) {
	for _, column := range []string{"revision", "authority_epoch"} {
		t.Run(column, func(t *testing.T) {
			db, _ := iamTestSetup(t)
			if _, err := db.ApplyRecoveryForFixture(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			if _, err := db.write.Exec(`UPDATE workload_iam_policies SET `+column+`=?`, int64(math.MaxInt64)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ApplyRecoveryForFixture(t.Context(), ids.New()); !errors.Is(err, ErrIAMRevisionExhausted) {
				t.Fatal("counter wrapped", err)
			}
		})
	}
	db, _ := iamTestSetup(t)
	for i := 0; i < 35; i++ {
		if _, err := db.ApplyRecoveryForFixture(t.Context(), ids.New()); err != nil {
			t.Fatal(err)
		}
	}
	audit, err := db.ListIAMPolicyAudit(t.Context(), iamTestOwner)
	if err != nil || len(audit) != IAMAuditRetention {
		t.Fatal("unbounded recovery audit", err)
	}
}

func TestRecoveryWorkAuditRetentionReplayAndActor(t *testing.T) {
	db := newTestDB(t)
	iamTestFunction(t, db, iamTestOwner, true)
	for _, statement := range []string{
		`INSERT INTO jobs(id,function_id,payload,status,scheduled_at) VALUES('job','` + iamTestOwner + `','{}','pending',CURRENT_TIMESTAMP)`,
		`INSERT INTO cron_schedules(id,function_id,name,cron_expr) VALUES('cron','` + iamTestOwner + `','test','* * * * *')`,
	} {
		if _, err := db.write.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var last string
	for i := 0; i < 35; i++ {
		last = ids.New()
		if _, err := db.ApplyRecoveryForFixture(t.Context(), last); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"async_work_audit", "async_cron_audit"} {
		var count int
		if err := db.read.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE action='restore_quarantine' AND actor_kind='system'`).Scan(&count); err != nil || count != 32 {
			t.Fatal("work audit retention", err)
		}
		if err := db.read.QueryRow(`SELECT count(*) FROM `+table+` WHERE actor_id=?`, last).Scan(&count); err != nil || count != 1 {
			t.Fatal("operation actor", err)
		}
	}
	if _, err := db.ApplyRecoveryForFixture(t.Context(), last); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyRecoveryForFixture(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"async_work_audit", "async_cron_audit"} {
		var count int
		if err := db.read.QueryRow(`SELECT count(*) FROM `+table+` WHERE actor_id=?`, last).Scan(&count); err != nil || count != 1 {
			t.Fatal("replayed audit", err)
		}
	}
	if _, err := db.write.Exec(`UPDATE jobs SET origin_kind='legacy_unknown',authority_state='quarantined',enqueued_by_function_id=?`, iamTestOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyRecoveryForFixture(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.read.QueryRow(`SELECT authority_state FROM jobs`).Scan(&state); err != nil || state != "quarantined" {
		t.Fatal("normal restart rewrote existing quarantine", err)
	}
	for _, table := range []string{"jobs", "cron_schedules"} {
		if _, err := db.write.Exec(`UPDATE `+table+` SET authority_generation=?`, int64(math.MaxInt64)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ApplyRecoveryForFixture(t.Context(), ids.New()); !errors.Is(err, ErrIAMRevisionExhausted) {
			t.Fatal("work generation wrapped", err)
		}
		if _, err := db.write.Exec(`UPDATE ` + table + ` SET authority_generation=1`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecoveryOwnerCapacityDeniesBeforeMigration(t *testing.T) {
	db := newTestDB(t)
	_, err := db.write.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?)
	 INSERT INTO functions(id,name,runtime) SELECT printf('019a8000-0000-7000-8000-%012x',x),printf('owner-%d',x),'node' FROM n`, RecoveryMaxOwners+1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ApplyRecoveryForFixture(t.Context(), ids.New()); err == nil {
		t.Fatal("oversized migration admitted")
	}
	var count int
	if err := db.read.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='workload_recovery'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("capacity failure partially initialized recovery")
	}
}
