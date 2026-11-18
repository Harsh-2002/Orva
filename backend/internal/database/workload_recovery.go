package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

const (
	RecoveryMaxOwners      = 10000
	RecoveryMaxWork        = 100000
	RecoveryEmptyPolicy    = `{"schema_version":1,"allow":[],"inbound":[]}`
	recoveryMigrationActor = "019a8000-0000-7000-8000-000000000000"
)

// RecoveryReport contains metadata only, never source, payloads or credentials.
type RecoveryReport struct {
	Functions        int64  `json:"functions"`
	LegacySDKEnabled int64  `json:"legacy_sdk_enabled"`
	Jobs             int64  `json:"jobs"`
	Crons            int64  `json:"crons"`
	StaleBuilds      int64  `json:"stale_builds"`
	SuggestedPolicy  string `json:"suggested_policy"`
}

type RecoveryResult struct {
	Epoch     int64
	Operation string
}

// RecoveryPreflight is read-only and also works before the explicit IAM schema.
func (db *Database) RecoveryPreflight(ctx context.Context) (RecoveryReport, error) {
	var r RecoveryReport
	err := recoveryPreflight(ctx, db.read, &r)
	r.SuggestedPolicy = RecoveryEmptyPolicy
	return r, err
}

func recoveryPreflight(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, r *RecoveryReport) error {
	err := q.QueryRowContext(ctx, `SELECT
	 (SELECT count(*) FROM (SELECT 1 FROM functions LIMIT ?)),
	 (SELECT count(*) FROM (SELECT 1 FROM functions WHERE sdk<>0 LIMIT ?)),
	 (SELECT count(*) FROM (SELECT 1 FROM jobs LIMIT ?)),
	 (SELECT count(*) FROM (SELECT 1 FROM cron_schedules LIMIT ?)),
	 (SELECT count(*) FROM (SELECT 1 FROM deployments WHERE status IN ('queued','building') LIMIT ?))`,
		RecoveryMaxOwners+1, RecoveryMaxOwners+1, RecoveryMaxWork+1, RecoveryMaxWork+1, RecoveryMaxWork+1).
		Scan(&r.Functions, &r.LegacySDKEnabled, &r.Jobs, &r.Crons, &r.StaleBuilds)
	if err == nil && (r.Functions > RecoveryMaxOwners || r.Jobs > RecoveryMaxWork || r.Crons > RecoveryMaxWork || r.StaleBuilds > RecoveryMaxWork) {
		return errors.New("recovery transaction capacity exceeded")
	}
	return err
}

const recoverySchema = `
CREATE TABLE IF NOT EXISTS workload_recovery (
 id INTEGER PRIMARY KEY CHECK(id=1), migrated INTEGER NOT NULL CHECK(migrated=1),
 epoch INTEGER NOT NULL CHECK(typeof(epoch)='integer' AND epoch>=0),
 operation TEXT NOT NULL CHECK(length(operation) IN (0,36))
);
CREATE TABLE IF NOT EXISTS workload_iam_legacy (
 function_id TEXT PRIMARY KEY REFERENCES functions(id) ON DELETE CASCADE,
 sdk_was_enabled INTEGER NOT NULL CHECK(sdk_was_enabled IN (0,1))
);
`

// ApplyRecoveryForFixture is an opt-in, admission-closed startup transaction.
// It does not load policies, resume builds, adopt artifacts or open admission.
func (db *Database) ApplyRecoveryForFixture(ctx context.Context, operation string) (RecoveryResult, error) {
	if operation != "" && !validIAMUUID(operation) {
		return RecoveryResult{}, ErrIAMInvalidPolicy
	}
	if _, err := db.RecoveryPreflight(ctx); err != nil {
		return RecoveryResult{}, err
	}
	db.lifecycleMu.Lock()
	defer db.lifecycleMu.Unlock()
	conn, err := db.write.Conn(ctx)
	if err != nil {
		return RecoveryResult{}, err
	}
	defer conn.Close()
	// Recovery must survive power loss before the external marker is removed.
	if _, err := conn.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
		return RecoveryResult{}, err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA synchronous=NORMAL`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryResult{}, err
	}
	defer tx.Rollback()
	if err := recoveryPreflight(ctx, tx, &RecoveryReport{}); err != nil {
		return RecoveryResult{}, err
	}
	for _, schema := range []string{workloadIAMSchema, recoverySchema} {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return RecoveryResult{}, err
		}
	}
	var result RecoveryResult
	err = tx.QueryRowContext(ctx, `SELECT epoch,operation FROM workload_recovery WHERE id=1`).Scan(&result.Epoch, &result.Operation)
	first := errors.Is(err, sql.ErrNoRows)
	if err != nil && !first {
		return RecoveryResult{}, err
	}
	if result.Epoch < 0 || result.Epoch == math.MaxInt64 {
		return RecoveryResult{}, ErrIAMRevisionExhausted
	}
	if first {
		if err := initAsyncSchemaTx(ctx, tx); err != nil {
			return RecoveryResult{}, err
		}
		if err := initCronSchemaTx(ctx, tx); err != nil {
			return RecoveryResult{}, err
		}
	}
	quarantine := operation != "" && (first || result.Operation != operation)
	if quarantine {
		if err := quarantineIAMTx(ctx, tx, operation); err != nil {
			return RecoveryResult{}, err
		}
		result.Epoch++
		result.Operation = operation
	}
	if first {
		if err := migrateLegacyIAMTx(ctx, tx, operation, quarantine); err != nil {
			return RecoveryResult{}, err
		}
	}
	if first {
		if _, err := tx.ExecContext(ctx, managedIAMSchema); err != nil {
			return RecoveryResult{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deployments SET status='failed',finished_at=CURRENT_TIMESTAMP,
	 error_message=COALESCE(NULLIF(error_message,''),'abandoned: recovery does not resume queued or running builds')
	 WHERE status IN ('queued','building')`); err != nil {
		return RecoveryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_recovery VALUES(1,1,?,?)
	 ON CONFLICT(id) DO UPDATE SET epoch=excluded.epoch,operation=excluded.operation`, result.Epoch, result.Operation); err != nil {
		return RecoveryResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RecoveryResult{}, fmt.Errorf("recovery commit: %w", err)
	}
	return result, nil
}

func migrateLegacyIAMTx(ctx context.Context, tx *sql.Tx, operation string, quarantine bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,sdk FROM functions ORDER BY id LIMIT ?`, RecoveryMaxOwners+1)
	if err != nil {
		return err
	}
	type legacy struct {
		id  string
		sdk bool
	}
	owners := []legacy{}
	for rows.Next() {
		var o legacy
		if err := rows.Scan(&o.id, &o.sdk); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(owners) > RecoveryMaxOwners {
		return errors.New("recovery owner capacity exceeded")
	}
	actor := IAMActorReference{Kind: "system", ID: recoveryMigrationActor}
	if operation != "" {
		actor.ID = operation
	}
	now := time.Now().UTC()
	for _, o := range owners {
		if !validIAMUUID(o.id) {
			return ErrIAMInvalidPolicy
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_policies
		 (function_id,revision,authority_epoch,approved_artifact,review_required,quarantined,policy_json,created_at,updated_at)
		 VALUES(?,1,1,?,?,?,?,?,?) ON CONFLICT(function_id) DO NOTHING`,
			o.id, IAMUnboundArtifact, o.sdk, quarantine, RecoveryEmptyPolicy, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 1 {
			r := &IAMPolicyRecord{Metadata: workloadiam.Metadata{OwnerID: o.id, Revision: 1, ApprovedArtifact: IAMUnboundArtifact},
				AuthorityEpoch: 1, ReviewRequired: o.sdk, Quarantined: quarantine, PolicyJSON: RecoveryEmptyPolicy, CreatedAt: now, UpdatedAt: now}
			if err := appendIAMAudit(ctx, tx, r, actor); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_legacy VALUES(?,?)`, o.id, o.sdk); err != nil {
				return err
			}
		}
	}
	return nil
}

func quarantineIAMTx(ctx context.Context, tx *sql.Tx, operation string) error {
	// Scan bounded identifiers, then validate canonical retained state per owner.
	rows, err := tx.QueryContext(ctx, `SELECT function_id FROM workload_iam_policies ORDER BY function_id LIMIT ?`, RecoveryMaxOwners+1)
	if err != nil {
		return err
	}
	owners := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(owners) > RecoveryMaxOwners {
		return errors.New("recovery owner capacity exceeded")
	}
	for _, id := range owners {
		r, err := scanIAMPolicy(tx.QueryRowContext(ctx, iamPolicySelect, id))
		if err != nil {
			return err
		}
		if r.Revision == math.MaxInt64 || r.AuthorityEpoch == math.MaxInt64 {
			return ErrIAMRevisionExhausted
		}
		r.Revision++
		r.AuthorityEpoch++
		r.Quarantined = true
		r.UpdatedAt = time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `UPDATE workload_iam_policies SET revision=?,authority_epoch=?,quarantined=1,updated_at=? WHERE function_id=?`,
			int64(r.Revision), int64(r.AuthorityEpoch), r.UpdatedAt.Format(time.RFC3339Nano), id); err != nil {
			return err
		}
		if err := appendIAMAudit(ctx, tx, r, IAMActorReference{Kind: "system", ID: operation}); err != nil {
			return err
		}
	}
	for _, table := range []string{"jobs", "cron_schedules"} {
		var invalid int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE typeof(authority_generation)<>'integer' OR authority_generation<1 OR authority_generation>=?`, int64(math.MaxInt64)).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return ErrIAMRevisionExhausted
		}
	}
	var invalid int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM cron_schedules WHERE typeof(config_revision)<>'integer' OR config_revision<1 OR config_revision>=?`, int64(math.MaxInt64)).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return ErrIAMRevisionExhausted
	}
	// No status, payload or origin is rewritten; old dispatch CAS is invalidated.
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET authority_state='quarantined',authority_reason='restore',
	 authority_generation=authority_generation+1,admission_caller_revision=0,admission_target_revision=0;
	 UPDATE cron_schedules SET authority_state='quarantined',authority_reason='restore',next_run_at=NULL,
	 authority_generation=authority_generation+1,config_revision=config_revision+1,
	 admission_caller_revision=0,admission_target_revision=0;`); err != nil {
		return err
	}
	return auditRecoveryWorkTx(ctx, tx, operation)
}

func auditRecoveryWorkTx(ctx context.Context, tx *sql.Tx, operation string) error {
	for _, spec := range []struct{ work, audit, key string }{
		{"jobs", "async_work_audit", "job_id"},
		{"cron_schedules", "async_cron_audit", "schedule_id"},
	} {
		if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_recovery_`+spec.audit+` ON `+spec.audit+`(`+spec.key+`,action,id)`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM `+spec.work+` ORDER BY id LIMIT ?`, RecoveryMaxWork+1)
		if err != nil {
			return err
		}
		workIDs := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			workIDs = append(workIDs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(workIDs) > RecoveryMaxWork {
			return errors.New("recovery work capacity exceeded")
		}
		columns, values := "", ""
		if spec.work == "cron_schedules" {
			columns = ",config_revision"
			values = ",config_revision"
		}
		insert, err := tx.PrepareContext(ctx, `INSERT INTO `+spec.audit+`(`+spec.key+`,action,actor_kind,actor_id,origin_kind,creator_id,target_id,changed_at`+columns+`)
		 SELECT id,'restore_quarantine','system',?,origin_kind,creator_id,function_id,?`+values+` FROM `+spec.work+` WHERE id=?`)
		if err != nil {
			return err
		}
		prune, err := tx.PrepareContext(ctx, `DELETE FROM `+spec.audit+` WHERE `+spec.key+`=? AND action='restore_quarantine'
		 AND id NOT IN (SELECT id FROM `+spec.audit+` WHERE `+spec.key+`=? AND action='restore_quarantine' ORDER BY id DESC LIMIT ?)`)
		if err != nil {
			insert.Close()
			return err
		}
		for _, id := range workIDs {
			if _, err = insert.ExecContext(ctx, operation, time.Now().UTC(), id); err != nil {
				break
			}
			if _, err = prune.ExecContext(ctx, id, id, IAMAuditRetention); err != nil {
				break
			}
		}
		insert.Close()
		prune.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
