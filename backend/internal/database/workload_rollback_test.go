package database

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func rollbackFixture(t *testing.T) (*Database, OperatorCredentialReference, string, string, WorkloadBuildOutput) {
	t.Helper()
	db, ref, owner := deploymentFixture(t, "api_key")
	if err := db.InitWorkloadOperatorPauseSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	publish := func(o WorkloadBuildOutput) string {
		t.Helper()
		p := captureDeployment(t, db, ref, owner)
		if err := db.RecordWorkloadBuildOutput(t.Context(), p.ID, o); err != nil {
			t.Fatal(err)
		}
		if _, err := db.PublishWorkloadDeployment(t.Context(), p.ID, o); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	target := deploymentOutput()
	target.Entrypoint = "dist/handler.js"
	id := publish(target)
	next := deploymentOutput()
	next.CodeHash = strings.Repeat("d", 64)
	publish(next)
	return db, ref, owner, id, target
}

func TestManagedRollbackPromotesWithoutRewritingHistory(t *testing.T) {
	db, ref, owner, target, output := rollbackFixture(t)
	ctx := t.Context()
	operatorExec(t, db, `UPDATE functions SET sdk=1,env_vars='{"CURRENT":"1"}',memory_mb=192`)
	if err := db.UpsertSecret(owner, "CURRENT", "fixture ciphertext"); err != nil {
		t.Fatal(err)
	}
	before := artifactTestSnapshot(t, db, owner)
	sources, err := db.GetIAMSources(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	history, err := db.ListDeploymentsForFunction(owner, 10)
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.PrepareWorkloadRollback(ctx, ref, owner, target)
	if err != nil {
		t.Fatal(err)
	}
	if p.Output() != output {
		t.Fatal("prepare did not return the historical output")
	}
	got, err := db.CommitWorkloadRollback(ctx, ref, p, output)
	if err != nil {
		t.Fatal(err)
	}
	want := before.Function.Clone()
	want.Version++
	want.UpdatedAt = got.Function.UpdatedAt
	want.ActiveDeploymentID, want.CodeHash, want.RunEntrypoint, want.Status = target, output.CodeHash, output.Entrypoint, "inactive"
	if !reflect.DeepEqual(got.Function, want) {
		t.Fatal("rollback changed function configuration outside publication fields")
	}
	after := artifactTestSnapshot(t, db, owner)
	if after.Artifact.Generation == before.Artifact.Generation || !reflect.DeepEqual(after.Secrets, before.Secrets) ||
		!got.Policy.ReviewRequired || got.Policy.Revision != sources.Record.Revision+1 ||
		got.Policy.AuthorityEpoch != sources.Record.AuthorityEpoch+1 || got.Policy.PolicyJSON != sources.Record.PolicyJSON ||
		got.Policy.ApprovedArtifact != sources.Record.ApprovedArtifact || got.Policy.Quarantined != sources.Record.Quarantined {
		t.Fatal("publication did not preserve current state and rotate into review-required deny")
	}
	if _, err := db.PrepareWorkloadRollback(ctx, ref, owner, target); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("already-selected deployment: %v", err)
	}
	rollback := operatorTestMutation(t, db, target)
	if _, err := db.PrepareWorkloadDeploymentInstallation(ctx, target); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("installation with altered promotion association: %v", err)
	}
	rollback()
	install, err := db.PrepareWorkloadDeploymentInstallation(ctx, target)
	if err != nil || install.Output() != output {
		t.Fatalf("prepare separate install: %v", err)
	}
	if _, err := db.ActivateWorkloadDeploymentInstallation(ctx, ref, install); err != nil {
		t.Fatal(err)
	}
	afterHistory, err := db.ListDeploymentsForFunction(owner, 10)
	if err != nil || !reflect.DeepEqual(history, afterHistory) {
		t.Fatalf("rollback and install rewrote deployment history: %v", err)
	}
}

func operatorTestMutation(t *testing.T, db *Database, id string) func() {
	t.Helper()
	var output string
	if err := db.read.QueryRow(`SELECT output FROM workload_deployment_capture WHERE deployment_id=?`, id).Scan(&output); err != nil {
		t.Fatal(err)
	}
	operatorExec(t, db, `UPDATE workload_deployment_capture SET output=json_set(output,'$.CodeGeneration',?) WHERE deployment_id=?`, ids.New(), id)
	return func() {
		operatorExec(t, db, `UPDATE workload_deployment_capture SET output=? WHERE deployment_id=?`, output, id)
	}
}

func TestManagedRollbackRejectsWithoutPartialPublication(t *testing.T) {
	for _, tt := range []struct {
		name, sql string
		want      error
	}{
		{"ciphertext", `UPDATE function_secrets SET value_encrypted='new ciphertext'`, ErrWorkloadArtifactConflict},
		{"configuration", `UPDATE functions SET timeout_ms=2345`, ErrWorkloadArtifactConflict},
		{"source epoch", `UPDATE workload_iam_policies SET authority_epoch=authority_epoch+1`, ErrIAMConflict},
		{"association", `UPDATE workload_deployment_capture SET output=json_set(output,'$.CodeGeneration','` + ids.New() + `')`, ErrWorkloadArtifactConflict},
		{"credential revoked", `DELETE FROM api_keys`, ErrOperatorDenied},
		{"privileged writer", `UPDATE api_keys SET permissions='["write"]'`, ErrOperatorDenied},
		{"commit failure", `CREATE TABLE rollback_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
			CREATE TRIGGER reject_promotion AFTER INSERT ON workload_deployment_promotion
			BEGIN INSERT INTO rollback_guard VALUES('missing-owner'); END`, ErrWorkloadArtifactStorage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, ref, owner, target, output := rollbackFixture(t)
			ctx := t.Context()
			if err := db.UpsertSecret(owner, "CURRENT", "fixture ciphertext"); err != nil {
				t.Fatal(err)
			}
			p, err := db.PrepareWorkloadRollback(ctx, ref, owner, target)
			if err != nil {
				t.Fatal(err)
			}
			operatorExec(t, db, tt.sql)
			before := artifactTestSnapshot(t, db, owner)
			policy, _ := db.GetIAMPolicy(ctx, owner)
			audit, _ := db.ListIAMPolicyAudit(ctx, owner)
			if _, err := db.CommitWorkloadRollback(ctx, ref, p, output); !errors.Is(err, tt.want) {
				t.Fatalf("commit error = %v; want %v", err, tt.want)
			}
			artifactTestUnchanged(t, db, before)
			after, _ := db.GetIAMPolicy(ctx, owner)
			afterAudit, _ := db.ListIAMPolicyAudit(ctx, owner)
			if !reflect.DeepEqual(policy, after) || !reflect.DeepEqual(audit, afterAudit) {
				t.Fatal("failed rollback altered policy or audit")
			}
			var count int
			if err := db.read.QueryRow(`SELECT COUNT(*) FROM workload_deployment_promotion`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed rollback left promotion state: %d %v", count, err)
			}
		})
	}
}

func TestManagedRollbackRejectsPauseAndUnknownTarget(t *testing.T) {
	db, ref, owner, target, output := rollbackFixture(t)
	ctx := t.Context()
	if _, err := db.PrepareWorkloadRollback(ctx, ref, owner, ids.New()); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("unknown target: %v", err)
	}
	if _, err := db.PrepareWorkloadRollback(ctx, ref, owner, "name"); !errors.Is(err, ErrWorkloadArtifactInvalid) {
		t.Fatalf("non-UUID target: %v", err)
	}
	p, err := db.PrepareWorkloadRollback(ctx, ref, owner, target)
	if err != nil {
		t.Fatal(err)
	}
	a := artifactTestSnapshot(t, db, owner).Artifact
	if _, _, err := db.PauseWorkloadFunctionAsOperator(ctx, ref, WorkloadPauseRequest{
		OwnerID: owner, ExpectedGeneration: a.Generation, ExpectedPolicyRevision: p.policy.Revision, ExpectedAuthorityEpoch: p.policy.AuthorityEpoch,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PrepareWorkloadRollback(ctx, ref, owner, target); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("prepare while paused: %v", err)
	}
	before := artifactTestSnapshot(t, db, owner)
	if _, err := db.CommitWorkloadRollback(ctx, ref, p, output); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("commit while paused: %v", err)
	}
	artifactTestUnchanged(t, db, before)
}
