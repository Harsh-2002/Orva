package database

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestPortableRestoreRebindsExactPromotionAndQuarantines(t *testing.T) {
	ctx := t.Context()
	db := newTestDB(t)
	prior, next, operation, owner, deploy := ids.New(), ids.New(), ids.New(), ids.New(), ids.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	text := func(value any) string {
		data, err := json.Marshal(value)
		must(err)
		return string(data)
	}
	must(db.BindManagedOwner(ctx, prior))
	original := deploymentOutput()
	must(db.InsertFunction(&Function{ID: owner, Name: "portable", Status: "active", SDK: true,
		Runtime: "node", Entrypoint: original.Entrypoint, ActiveDeploymentID: deploy, CodeHash: original.CodeHash,
		EnvVars: map[string]string{}}))
	must(db.InsertDeployment(&Deployment{ID: deploy, FunctionID: owner, Status: "succeeded", Version: 1, CodeHash: original.CodeHash}))
	_, err := db.ApplyRecoveryForFixture(ctx, "")
	must(err)
	must(db.InitManagedServingSchemas(ctx))
	// The recovered test function has no artifact yet; seed the captured
	// deployment generation separately from the installed promotion.
	initial, current := ids.New(), ids.New()
	operatorExec(t, db, `INSERT INTO workload_artifact_generations
		(function_id,revision,epoch,generation,actor_kind,actor_id,change_kind,created_at,updated_at)
		VALUES(?,1,1,?,'system',?,'initialize',?,?)`, owner, current, prior, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	const grant = `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"probe"}},"context":"execution"}],"inbound":[]}`
	operatorExec(t, db, `UPDATE workload_iam_policies SET approved_artifact=?,review_required=0,policy_json=? WHERE function_id=?`,
		strings.Repeat("e", 64), grant, owner)
	operatorExec(t, db, `UPDATE workload_iam_inline SET policy_json=? WHERE function_id=?`, grant, owner)
	operatorExec(t, db, `INSERT INTO workload_deployment_capture VALUES(?,?,?,?,?)`, deploy, owner,
		text(deploymentProvenance{Owner: owner, Runtime: "node", Entrypoint: original.Entrypoint,
			Artifact: WorkloadArtifactRecord{OwnerID: owner, Generation: initial}}), text(original), initial)
	tx, err := db.ReadDB().BeginTx(ctx, nil)
	must(err)
	a, err := readRollbackAssociationTx(ctx, tx, owner, deploy)
	must(err)
	must(tx.Rollback())
	operatorExec(t, db, `INSERT INTO workload_deployment_promotion VALUES(?,?,?,?,1)`, owner, deploy, current, a.digest())
	replacement := original
	replacement.CodeGeneration, replacement.RuntimeGeneration = ids.New(), ids.New()
	rebuilt := map[string]WorkloadBuildOutput{deploy: replacement}
	runtimes := map[string]string{"node": replacement.RuntimeGeneration}
	operatorExec(t, db, `CREATE TRIGGER stop_rebind BEFORE UPDATE ON workload_owner_binding BEGIN SELECT raise(ABORT,'fault'); END`)
	if err := db.RebindPortableRestore(ctx, prior, next, operation, rebuilt, runtimes); err == nil {
		t.Fatal("faulted rebind passed")
	}
	must(RequireManagedOwnerBinding(ctx, db.Path(), prior))
	p, err := db.PrepareWorkloadDeploymentInstallation(ctx, deploy)
	must(err)
	if p.Output() != original || p.Policy().Quarantined {
		t.Fatal("failed transaction partially published")
	}
	operatorExec(t, db, `DROP TRIGGER stop_rebind`)
	must(db.RebindPortableRestore(ctx, prior, next, operation, rebuilt, runtimes))
	if err := RequireManagedOwnerBinding(ctx, db.Path(), prior); !errors.Is(err, ErrManagedOwnerMismatch) {
		t.Fatalf("prior binding retained: %v", err)
	}
	must(RequireManagedOwnerBinding(ctx, db.Path(), next))
	p, err = db.PrepareWorkloadDeploymentInstallation(ctx, deploy)
	must(err)
	if p.Output() != replacement || !p.Policy().Quarantined || !p.Policy().ReviewRequired ||
		p.Policy().ApprovedArtifact != IAMRestoredUnapprovedArtifact || p.Policy().PolicyJSON != grant || p.Snapshot().Function.Status != "active" {
		t.Fatal("restored deployment or deny policy did not match")
	}
	var id string
	var epoch int
	must(db.ReadDB().QueryRow(`SELECT prior_owner,restore_epoch FROM workload_portable_restore_audit WHERE operation=? AND new_owner=?`,
		operation, next).Scan(&id, &epoch))
	if id != prior || epoch != 1 {
		t.Fatal("restore audit missing")
	}
}
