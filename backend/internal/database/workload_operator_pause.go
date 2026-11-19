package database

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// ErrWorkloadPauseUnavailable means explicit operator-pause mode was not provisioned.
// It is a fail-closed configuration error, not an implicit legacy fallback.
var ErrWorkloadPauseUnavailable = errors.New("workload operator pause mode unavailable")

const workloadPauseModeKey = "workload_operator_pause_mode"

// WorkloadPauseRequest carries the exact reviewed artifact and authority the
// caller last observed. A pause never resumes or rebinds authority by itself.
type WorkloadPauseRequest struct {
	OwnerID                string
	ExpectedGeneration     string
	ExpectedPolicyRevision uint64
	ExpectedAuthorityEpoch uint64
}

// WorkloadOperatorPause records persisted intent, not a serving decision by itself.
type WorkloadOperatorPause struct {
	OwnerID            string
	ArtifactGeneration string
	PolicyRevision     uint64
	AuthorityEpoch     uint64
	Actor              IAMActorReference
	PausedAt           time.Time
}

// InitWorkloadOperatorPauseSchema is explicit fixture provisioning, never normal boot.
// The marker makes schema absence a configuration error instead of a silent legacy path.
func (db *Database) InitWorkloadOperatorPauseSchema(ctx context.Context) error {
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return workloadArtifactStorageError(ctx, "operator pause schema begin")
	}
	defer tx.Rollback()
	enabled, err := workloadPauseModeTx(ctx, tx)
	if err != nil || enabled {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workload_operator_pause (
		function_id TEXT PRIMARY KEY REFERENCES functions(id) ON DELETE CASCADE,
		artifact_generation TEXT NOT NULL CHECK(length(artifact_generation) BETWEEN 1 AND 64),
		policy_revision INTEGER NOT NULL CHECK(policy_revision > 0),
		authority_epoch INTEGER NOT NULL CHECK(authority_epoch > 0),
		actor_kind TEXT NOT NULL CHECK(length(actor_kind) BETWEEN 1 AND 64),
		actor_id TEXT NOT NULL CHECK(length(actor_id) BETWEEN 1 AND 64),
		paused_at TEXT NOT NULL
	)`)
	if err != nil {
		return workloadArtifactStorageError(ctx, "operator pause schema")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO system_config(key,value) VALUES(?, '1') ON CONFLICT(key) DO NOTHING`, workloadPauseModeKey)
	if err != nil {
		return workloadArtifactStorageError(ctx, "operator pause mode")
	}
	if _, err := workloadPauseModeTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return workloadArtifactStorageError(ctx, "operator pause schema commit")
	}
	return nil
}

// workloadPauseModeTx distinguishes an unprovisioned legacy instance from an
// enabled pause mode whose table is missing. Only the former keeps legacy rules.
func workloadPauseModeTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM system_config WHERE key=?`, workloadPauseModeKey).Scan(&value)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, workloadArtifactStorageError(ctx, "operator pause mode read")
	}
	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='workload_operator_pause'`).Scan(&tables); err != nil {
		return false, workloadArtifactStorageError(ctx, "operator pause schema check")
	}
	if errors.Is(err, sql.ErrNoRows) && tables == 0 {
		return false, nil
	}
	if value != "1" || tables != 1 {
		return false, ErrWorkloadPauseUnavailable
	}
	return true, nil
}

func workloadPauseRecordTx(ctx context.Context, tx *sql.Tx, owner string) (*WorkloadOperatorPause, bool, error) {
	var p WorkloadOperatorPause
	var revision, epoch int64
	var actorKind, actorID, paused string
	err := tx.QueryRowContext(ctx, `SELECT function_id,artifact_generation,policy_revision,authority_epoch,
		actor_kind,actor_id,paused_at FROM workload_operator_pause WHERE function_id=?`, owner).
		Scan(&p.OwnerID, &p.ArtifactGeneration, &revision, &epoch, &actorKind, &actorID, &paused)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, workloadArtifactStorageError(ctx, "operator pause read")
	}
	p.Actor = IAMActorReference{Kind: actorKind, ID: actorID}
	if p.OwnerID != owner || !validIAMUUID(p.OwnerID) || !validIAMUUID(p.ArtifactGeneration) ||
		revision <= 0 || epoch <= 0 || validateIAMActor(p.Actor) != nil {
		return nil, false, ErrWorkloadArtifactInvalid
	}
	p.PolicyRevision, p.AuthorityEpoch = uint64(revision), uint64(epoch)
	if p.PausedAt, err = time.Parse(time.RFC3339Nano, paused); err != nil {
		return nil, false, ErrWorkloadArtifactInvalid
	}
	return &p, true, nil
}

// workloadRequireUnpausedTx runs only after mode resolution; denied owners never
// fall back to their legacy function status.
func workloadRequireUnpausedTx(ctx context.Context, tx *sql.Tx, owner string) error {
	_, paused, err := workloadPauseRecordTx(ctx, tx, owner)
	if err != nil {
		return err
	}
	if paused {
		return ErrWorkloadArtifactConflict
	}
	return nil
}

func workloadCheckPauseTx(ctx context.Context, tx *sql.Tx, owner string) error {
	enabled, err := workloadPauseModeTx(ctx, tx)
	if err != nil || !enabled {
		return err
	}
	return workloadRequireUnpausedTx(ctx, tx, owner)
}

// workloadPendingReviewTx proves the inactive status came from this exact
// published generation awaiting installation, not from an operator pause.
func workloadPendingReviewTx(ctx context.Context, tx *sql.Tx, owner string, fn *Function, generation string) (bool, error) {
	if fn.ActiveDeploymentID == "" || generation == "" {
		return false, nil
	}
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments d
		JOIN workload_deployment_capture c ON c.deployment_id=d.id
		WHERE d.id=? AND d.function_id=? AND d.status='succeeded' AND d.phase='review_required'
		AND c.function_id=? AND c.published_generation=?`, fn.ActiveDeploymentID, owner, owner, generation).Scan(&count)
	if err != nil {
		return false, workloadArtifactStorageError(ctx, "pending review evidence")
	}
	if count == 1 {
		return true, nil
	}
	association, err := readRollbackAssociationTx(ctx, tx, owner, fn.ActiveDeploymentID)
	if errors.Is(err, ErrWorkloadArtifactConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	installed, err := readDeploymentPromotionTx(ctx, tx, owner, fn.ActiveDeploymentID, generation, association.digest())
	if errors.Is(err, ErrWorkloadArtifactConflict) {
		return false, nil
	}
	return !installed && err == nil, err
}

// WorkloadOperatorPaused exposes the durable intent for inspection. A disabled
// mode reports false; an enabled-but-missing schema is an error.
func (db *Database) WorkloadOperatorPaused(ctx context.Context, owner string) (bool, error) {
	if !validIAMUUID(owner) {
		return false, ErrWorkloadArtifactInvalid
	}
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, workloadArtifactStorageError(ctx, "operator pause inspect begin")
	}
	defer tx.Rollback()
	mode, err := workloadPauseModeTx(ctx, tx)
	if err != nil || !mode {
		return false, err
	}
	_, paused, err := workloadPauseRecordTx(ctx, tx, owner)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, workloadArtifactStorageError(ctx, "operator pause inspect commit")
	}
	return paused, nil
}

// PauseWorkloadFunctionAsOperator persists intent and advances the authority
// epoch under exact CAS. It does not install, review, resume or delete output.
func (db *Database) PauseWorkloadFunctionAsOperator(ctx context.Context, ref OperatorCredentialReference,
	req WorkloadPauseRequest) (*WorkloadOperatorPause, *Function, error) {
	if !validIAMUUID(req.OwnerID) || !validIAMUUID(req.ExpectedGeneration) ||
		req.ExpectedPolicyRevision == 0 || req.ExpectedPolicyRevision > math.MaxInt64 ||
		req.ExpectedAuthorityEpoch == 0 || req.ExpectedAuthorityEpoch > math.MaxInt64 {
		return nil, nil, ErrWorkloadArtifactInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, workloadArtifactStorageError(ctx, "operator pause begin")
	}
	defer tx.Rollback()
	mode, err := workloadPauseModeTx(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if !mode {
		return nil, nil, ErrWorkloadPauseUnavailable
	}
	sources, err := readSources(ctx, tx, req.OwnerID)
	if err != nil {
		return nil, nil, workloadPublicationReadError(ctx, err)
	}
	required, err := deploymentPermission(sources)
	if err != nil {
		return nil, nil, err
	}
	actor, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, required)
	if err != nil {
		return nil, nil, err
	}
	artifact, err := readWorkloadArtifactTx(ctx, tx, req.OwnerID)
	if err != nil {
		return nil, nil, err
	}
	fn, err := readWorkloadArtifactFunctionTx(ctx, tx, req.OwnerID)
	if err != nil {
		return nil, nil, err
	}
	policy := sources.Record
	if _, err := deploymentSourceDigest(sources); err != nil {
		return nil, nil, err
	}
	if artifact.Generation != req.ExpectedGeneration ||
		policy.Revision != req.ExpectedPolicyRevision || policy.AuthorityEpoch != req.ExpectedAuthorityEpoch {
		return nil, nil, ErrWorkloadArtifactConflict
	}
	if policy.AuthorityEpoch >= math.MaxInt64 || fn.Version <= 0 || fn.Version == math.MaxInt {
		return nil, nil, ErrWorkloadArtifactExhausted
	}
	if _, paused, err := workloadPauseRecordTx(ctx, tx, req.OwnerID); err != nil {
		return nil, nil, err
	} else if paused {
		return nil, nil, ErrWorkloadArtifactConflict
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE workload_iam_policies
		SET authority_epoch=authority_epoch+1,updated_at=?
		WHERE function_id=? AND revision=? AND authority_epoch=?`,
		stamp, req.OwnerID, int64(policy.Revision), int64(policy.AuthorityEpoch))
	if err != nil {
		return nil, nil, workloadArtifactStorageError(ctx, "operator pause authority")
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return nil, nil, workloadArtifactStorageError(ctx, "operator pause authority result")
		}
		return nil, nil, ErrWorkloadArtifactConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE functions SET status='inactive',version=version+1,updated_at=?
		WHERE id=? AND version=? AND status!=?`,
		stamp, req.OwnerID, fn.Version, FunctionStatusDeleting)
	if err != nil {
		return nil, nil, workloadArtifactStorageError(ctx, "operator pause function")
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return nil, nil, workloadArtifactStorageError(ctx, "operator pause function result")
		}
		return nil, nil, ErrWorkloadArtifactConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workload_operator_pause(function_id,artifact_generation,
		policy_revision,authority_epoch,actor_kind,actor_id,paused_at) VALUES(?,?,?,?,?,?,?)`,
		req.OwnerID, req.ExpectedGeneration, int64(policy.Revision), int64(policy.AuthorityEpoch+1),
		actor.Kind, actor.ID, stamp)
	if err != nil {
		return nil, nil, workloadArtifactStorageError(ctx, "operator pause record")
	}
	fn, err = readWorkloadArtifactFunctionTx(ctx, tx, req.OwnerID)
	if err != nil {
		return nil, nil, err
	}
	if _, _, err := authorizeOperatorPermissionTx(ctx, tx, ref, required); err != nil {
		return nil, nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, workloadArtifactStorageError(ctx, "operator pause commit")
	}
	return &WorkloadOperatorPause{OwnerID: req.OwnerID, ArtifactGeneration: req.ExpectedGeneration,
		PolicyRevision: policy.Revision, AuthorityEpoch: policy.AuthorityEpoch + 1,
		Actor: actor, PausedAt: now}, fn, nil
}
