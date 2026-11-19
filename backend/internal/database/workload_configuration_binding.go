package database

import (
	"context"
	"database/sql"
	"time"
)

// WorkloadSealedPair names the exact host-verified outputs, not author inputs.
type WorkloadSealedPair struct {
	CodeGeneration, CodeDigest, RuntimeGeneration, RuntimeDigest string
}

func (o WorkloadBuildOutput) SealedPair() WorkloadSealedPair {
	return WorkloadSealedPair{o.CodeGeneration, o.CodeDigest, o.RuntimeGeneration, o.RuntimeDigest}
}

func checkBoundConfigurationTx(ctx context.Context, tx *sql.Tx, before *WorkloadArtifactSnapshot, pair WorkloadSealedPair, installed bool) (rollbackAssociation, error) {
	fn := before.Function
	status := "inactive"
	if installed {
		status = "active"
	}
	if !fn.SDK || fn.Status != status {
		return rollbackAssociation{}, ErrWorkloadArtifactConflict
	}
	mode, err := workloadPauseModeTx(ctx, tx)
	if err != nil {
		return rollbackAssociation{}, err
	}
	if !mode {
		return rollbackAssociation{}, ErrWorkloadPauseUnavailable
	}
	a, current, err := readResumeAssociationTx(ctx, tx, fn, before.Artifact.Generation)
	if err != nil {
		return rollbackAssociation{}, err
	}
	if a.output.SealedPair() != pair || current != installed {
		return rollbackAssociation{}, ErrWorkloadArtifactConflict
	}
	return a, nil
}

func stageBoundConfigurationTx(ctx context.Context, tx *sql.Tx, before *WorkloadArtifactSnapshot, out *WorkloadEnvironmentPublication, a rollbackAssociation) error {
	fn := before.Function
	_, err := tx.ExecContext(ctx, `INSERT INTO workload_deployment_promotion(function_id,deployment_id,generation,association_digest,installed)
		VALUES(?,?,?,?,0) ON CONFLICT(function_id) DO UPDATE
		SET deployment_id=excluded.deployment_id,generation=excluded.generation,association_digest=excluded.association_digest,installed=0`,
		fn.ID, fn.ActiveDeploymentID, out.Artifact.Generation, a.digest())
	if err == nil {
		err = tx.QueryRowContext(ctx, `UPDATE functions SET status='inactive' WHERE id=? AND status='active' RETURNING status`, fn.ID).
			Scan(&out.Function.Status)
	}
	if err != nil {
		return workloadArtifactStorageError(ctx, "configuration association")
	}
	return nil
}

// ActivateBoundWorkloadConfiguration is the durable half of deny-only install.
// The caller holds the owner/IAM gates and has already verified pair leases.
func (db *Database) ActivateBoundWorkloadConfiguration(ctx context.Context, ref OperatorCredentialReference, p *WorkloadConfigurationPlan, pair WorkloadSealedPair) (*Function, error) {
	if p == nil || p.before == nil || p.sources.Record == nil || !p.sources.Record.ReviewRequired {
		return nil, ErrWorkloadArtifactInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration activation begin")
	}
	defer tx.Rollback()
	_, expiry, err := db.checkWorkloadConfigurationTx(ctx, tx, ref, p, true)
	if err != nil {
		return nil, err
	}
	a, err := checkBoundConfigurationTx(ctx, tx, p.before, pair, false)
	if err != nil {
		return nil, err
	}
	fn := p.before.Function.Clone()
	err = tx.QueryRowContext(ctx, `UPDATE workload_deployment_promotion SET installed=1
		WHERE function_id=? AND deployment_id=? AND generation=? AND association_digest=? AND installed=0 RETURNING installed`,
		fn.ID, fn.ActiveDeploymentID, p.before.Artifact.Generation, a.digest()).Scan(new(int))
	if err == nil {
		err = tx.QueryRowContext(ctx, `UPDATE functions SET status='active' WHERE id=? AND status='inactive' RETURNING status`, fn.ID).
			Scan(&fn.Status)
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration activation")
	}
	if _, _, err := authorizeOperatorTx(ctx, tx, ref); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration activation commit")
	}
	return fn, nil
}
