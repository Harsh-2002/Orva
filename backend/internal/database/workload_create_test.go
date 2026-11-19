package database

import (
	"errors"
	"testing"
)

func TestCreateManagedWorkloadCanCaptureFirstDeployment(t *testing.T) {
	db, ref, _ := deploymentFixture(t, "api_key")
	operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]'`)
	ctx := t.Context()
	create, err := PrepareWorkloadCreation(WorkloadCreateRequest{Name: "fresh-owner", Runtime: "python"})
	if err != nil {
		t.Fatal(err)
	}
	fn, err := db.CreateWorkloadAsOperator(ctx, ref, create)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetWorkloadArtifactSnapshot(ctx, fn.ID)
	if err != nil || fn.Status != "created" || !fn.SDK || snapshot.Artifact.Revision != 1 {
		t.Fatalf("missing initial artifact state: %v", err)
	}
	source, err := db.GetIAMSources(ctx, fn.ID)
	if err != nil || source.Record.Revision != 1 || source.Record.ApprovedArtifact != IAMUnboundArtifact || source.Record.PolicyJSON != RecoveryEmptyPolicy {
		t.Fatalf("new owner did not start with unbound empty authority: %v", err)
	}
	_ = captureDeployment(t, db, ref, fn.ID)
	duplicate, err := PrepareWorkloadCreation(WorkloadCreateRequest{Name: fn.Name, Runtime: "node"})
	if err != nil {
		t.Fatal(err)
	}
	if err := createManagedFailure(t, db, ref, duplicate); !errors.Is(err, ErrWorkloadNameConflict) {
		t.Fatal(err)
	}
}

func TestCreateManagedWorkloadRevokedWriterRollsBack(t *testing.T) {
	db, ref, _ := deploymentFixture(t, "api_key")
	operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]'`)
	p, err := PrepareWorkloadCreation(WorkloadCreateRequest{Name: "revoked-writer", Runtime: "node"})
	if err != nil {
		t.Fatal(err)
	}
	operatorExec(t, db, `CREATE TRIGGER revoke_writer AFTER INSERT ON workload_iam_policies
		BEGIN UPDATE api_keys SET permissions='["read"]'; END`)
	if err := createManagedFailure(t, db, ref, p); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal(err)
	}
}

func createManagedFailure(t *testing.T, db *Database, ref OperatorCredentialReference, p *WorkloadCreation) error {
	t.Helper()
	fn, err := db.CreateWorkloadAsOperator(t.Context(), ref, p)
	if err == nil || fn != nil {
		t.Fatal("failed creation returned a function")
	}
	var count int
	q := `SELECT (SELECT COUNT(*) FROM functions WHERE id=?1)
		+ (SELECT COUNT(*) FROM workload_artifact_generations WHERE function_id=?1)
		+ (SELECT COUNT(*) FROM workload_iam_policies WHERE function_id=?1)
		+ (SELECT COUNT(*) FROM workload_iam_inline WHERE function_id=?1)
		+ (SELECT COUNT(*) FROM workload_iam_audit WHERE function_id=?1)`
	if e := db.ReadDB().QueryRowContext(t.Context(), q, p.OwnerID()).Scan(&count); e != nil || count != 0 {
		t.Fatalf("failed creation left durable owner state: count=%d err=%v", count, e)
	}
	if _, live := db.liveFunctions.Load(p.OwnerID()); live {
		t.Fatal("failed creation left owner live")
	}
	return err
}
