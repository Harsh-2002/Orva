package database

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

type LegacyEnrollmentFunction struct {
	ID, Generation, LegacyHash, Revision string
	DeploymentID, ApprovedArtifact       string
	LegacySDK                            bool
	Excluded                             bool
	Build                                WorkloadBuildOutput
}

type LegacyEnrollment struct {
	Owner, Operation, PlanDigest string
	Functions                    []LegacyEnrollmentFunction
	Runtimes                     map[string]string
}

const enrollmentSchema = `
CREATE TABLE workload_enrollment (
 id INTEGER PRIMARY KEY CHECK(id=1),
 operation TEXT NOT NULL CHECK(length(operation)=36),
 plan_digest TEXT NOT NULL CHECK(length(plan_digest)=64)
);
CREATE TABLE workload_enrollment_functions (
 function_id TEXT PRIMARY KEY REFERENCES functions(id) ON DELETE CASCADE,
 legacy_hash TEXT NOT NULL,
 revision_digest TEXT NOT NULL CHECK(length(revision_digest)=64),
 excluded INTEGER NOT NULL CHECK(excluded IN (0,1)),
 note TEXT NOT NULL
);` + managedRuntimeSchema

const managedRuntimeSchema = `
CREATE TABLE IF NOT EXISTS workload_enrollment_runtimes (
 runtime TEXT PRIMARY KEY CHECK(runtime IN ('node','python')),
 generation TEXT NOT NULL CHECK(length(generation)=36)
);`

func enrollmentSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// CommitLegacyEnrollment is only for a stopped instance after the outer
// enrollment intent and verified runtime/code production. It never opens admission.
func (db *Database) CommitLegacyEnrollment(ctx context.Context, p LegacyEnrollment) error {
	if !validManagedOwnerID(p.Owner) || !validManagedOwnerID(p.Operation) || !enrollmentSHA(p.PlanDigest) ||
		len(p.Functions) > 256 || len(p.Runtimes) > 2 {
		return ErrManagedOwnerInvalid
	}
	db.lifecycleMu.Lock()
	defer db.lifecycleMu.Unlock()
	conn, err := db.write.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA synchronous=NORMAL`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var managed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE lower(name) GLOB 'workload_*')`).Scan(&managed); err != nil {
		return err
	}
	if managed {
		return ErrManagedOwnerInvalid
	}
	var report RecoveryReport
	if err := recoveryPreflight(ctx, tx, &report); err != nil {
		return err
	}
	if report.Functions != int64(len(p.Functions)) {
		return ErrWorkloadArtifactConflict
	}
	for _, schema := range []string{workloadIAMSchema, recoverySchema, workloadArtifactSchema, workloadDeploymentSchema, enrollmentSchema} {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
	}
	if err := initAsyncSchemaTx(ctx, tx); err != nil {
		return err
	}
	if err := initCronSchemaTx(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
 UPDATE jobs SET origin_kind=CASE WHEN COALESCE(enqueued_by_function_id,'')='' THEN 'legacy_unknown' ELSE 'function' END,
	creator_id=COALESCE(enqueued_by_function_id,''),operator_kind='',operator_id='',
	authority_state=CASE WHEN COALESCE(enqueued_by_function_id,'')='' THEN 'quarantined' ELSE 'suspended' END,
	authority_reason=CASE WHEN COALESCE(enqueued_by_function_id,'')='' THEN 'legacy_unknown' ELSE 'legacy_review' END,
	creation_caller_revision=0,creation_target_revision=0,admission_caller_revision=0,admission_target_revision=0;
 UPDATE cron_schedules SET origin_kind='legacy_unknown',creator_id='',operator_kind='',operator_id='',
	authority_state='quarantined',authority_reason='legacy_unknown',next_run_at=NULL,
	creation_caller_revision=0,creation_target_revision=0,admission_caller_revision=0,admission_target_revision=0;
 UPDATE deployments SET status='failed',finished_at=CURRENT_TIMESTAMP,
	error_message='abandoned: offline enrollment does not resume legacy builds' WHERE status IN ('queued','building');`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_recovery VALUES(1,1,0,?)`, p.Operation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_enrollment VALUES(1,?,?)`, p.Operation, p.PlanDigest); err != nil {
		return err
	}
	for lang, generation := range p.Runtimes {
		if !validManagedOwnerID(generation) || (lang != "node" && lang != "python") {
			return ErrWorkloadArtifactInvalid
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workload_enrollment_runtimes VALUES(?,?)`, lang, generation); err != nil {
			return err
		}
	}
	for _, entry := range p.Functions {
		if err := enrollLegacyFunctionTx(ctx, tx, p, entry); err != nil {
			return fmt.Errorf("enroll database function %s: %w", entry.ID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, managedIAMSchema); err != nil {
		return err
	}
	if err := bindManagedOwnerTx(ctx, tx, p.Owner); err != nil {
		return err
	}
	if bound, err := checkManagedOwnerBinding(ctx, tx, p.Owner); err != nil || !bound {
		return errors.Join(ErrManagedOwnerInvalid, err)
	}
	return tx.Commit()
}

func enrollLegacyFunctionTx(ctx context.Context, tx *sql.Tx, plan LegacyEnrollment, e LegacyEnrollmentFunction) error {
	if !validManagedOwnerID(e.ID) || !validManagedOwnerID(e.Generation) || !enrollmentSHA(e.Revision) {
		return ErrWorkloadArtifactInvalid
	}
	var sdk bool
	var runtime, entry, status, oldHash, deployment string
	var version int
	err := tx.QueryRowContext(ctx, `SELECT runtime,entrypoint,status,COALESCE(code_hash,''),COALESCE(active_deployment_id,''),sdk,version
		FROM functions WHERE id=?`, e.ID).Scan(&runtime, &entry, &status, &oldHash, &deployment, &sdk, &version)
	if err != nil || oldHash != e.LegacyHash || sdk != e.LegacySDK {
		return errors.Join(ErrWorkloadArtifactConflict, err)
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	actor := IAMActorReference{Kind: "system", ID: plan.Operation}
	r := IAMPolicyRecord{
		Metadata:       workloadiam.Metadata{OwnerID: e.ID, Revision: 1, ApprovedArtifact: IAMUnboundArtifact},
		AuthorityEpoch: 1, ReviewRequired: sdk, Quarantined: e.Excluded, PolicyJSON: RecoveryEmptyPolicy, CreatedAt: now, UpdatedAt: now,
	}
	note := "legacy enrollment; no grants imported"
	if sdk {
		note += "; legacy SDK enabled; policy review required"
	}
	if e.Excluded {
		note += "; explicitly excluded from enrollment"
		if _, err := tx.ExecContext(ctx, `UPDATE functions SET status='inactive' WHERE id=?`, e.ID); err != nil {
			return err
		}
	} else {
		if deployment != e.DeploymentID || !validManagedOwnerID(deployment) || status != "active" || !e.Build.valid() ||
			e.Build.CodeHash != oldHash || e.Build.RuntimeGeneration != plan.Runtimes[runtime] ||
			(!sdk && !enrollmentSHA(e.ApprovedArtifact)) || (sdk && e.ApprovedArtifact != "") {
			return ErrWorkloadArtifactConflict
		}
		var succeeded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM deployments WHERE id=? AND function_id=? AND status='succeeded')`,
			deployment, e.ID).Scan(&succeeded); err != nil || !succeeded {
			return errors.Join(ErrWorkloadArtifactConflict, err)
		}
		if !sdk {
			r.ApprovedArtifact = e.ApprovedArtifact
		}
		run := ""
		if e.Build.Entrypoint != entry {
			run = e.Build.Entrypoint
		}
		if _, err := tx.ExecContext(ctx, `UPDATE functions SET sdk=1,run_entrypoint=? WHERE id=?`, run, e.ID); err != nil {
			return err
		}
		provenance, err := json.Marshal(deploymentProvenance{Owner: e.ID, Runtime: runtime, Entrypoint: entry, FunctionVersion: version,
			Artifact: WorkloadArtifactRecord{OwnerID: e.ID, Revision: 1, Epoch: 1, Generation: e.Generation}})
		if err != nil {
			return err
		}
		output, err := json.Marshal(e.Build)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workload_deployment_capture VALUES(?,?,?,?,?)`,
			deployment, e.ID, string(provenance), string(output), e.Generation); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_policies
		(function_id,revision,authority_epoch,approved_artifact,review_required,quarantined,policy_json,created_at,updated_at)
		VALUES(?,1,1,?,?,?,?,?,?)`, e.ID, r.ApprovedArtifact, r.ReviewRequired, r.Quarantined, r.PolicyJSON, stamp, stamp); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_artifact_generations
		(function_id,revision,epoch,generation,actor_kind,actor_id,change_kind,created_at,updated_at)
		VALUES(?,1,1,?,'system',?,'initialize',?,?)`, e.ID, e.Generation, actor.ID, stamp, stamp); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_legacy VALUES(?,?)`, e.ID, sdk); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_enrollment_functions VALUES(?,?,?,?,?)`,
		e.ID, oldHash, e.Revision, e.Excluded, note); err != nil {
		return err
	}
	return appendIAMAudit(ctx, tx, &r, actor)
}

// RecordManagedRuntimeGenerations fills missing selections under the outer
// managed startup lock, after the caller has verified the sealed artifacts.
func (db *Database) RecordManagedRuntimeGenerations(ctx context.Context, generations map[string]string) error {
	if len(generations) == 0 {
		return nil
	}
	for runtime, generation := range generations {
		if (runtime != "node" && runtime != "python") || !validManagedOwnerID(generation) {
			return ErrManagedOwnerInvalid
		}
	}
	conn, err := db.write.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA synchronous=NORMAL`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, managedRuntimeSchema); err != nil {
		return err
	}
	for runtime, generation := range generations {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workload_enrollment_runtimes(runtime,generation) VALUES(?,?)
			 ON CONFLICT(runtime) DO NOTHING`, runtime, generation); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LegacyEnrollmentRuntimes returns the durable managed runtime selections.
func (db *Database) LegacyEnrollmentRuntimes(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string)
	var exists bool
	if err := db.read.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name='workload_enrollment_runtimes' AND type='table')`).
		Scan(&exists); err != nil || !exists {
		return out, err
	}
	rows, err := db.read.QueryContext(ctx, `SELECT runtime,generation FROM workload_enrollment_runtimes LIMIT 3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var runtime, generation string
		if err := rows.Scan(&runtime, &generation); err != nil {
			return nil, err
		}
		if (runtime != "node" && runtime != "python") || !validManagedOwnerID(generation) || out[runtime] != "" {
			return nil, ErrManagedOwnerInvalid
		}
		out[runtime] = generation
	}
	return out, rows.Err()
}
