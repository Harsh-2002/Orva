package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// RebindPortableRestore is only for a private, offline staging copy. The
// caller has verified rebuilt seals in a new Owner and holds the outer intent.
func (db *Database) RebindPortableRestore(ctx context.Context, prior, owner, operation string,
	rebuilt map[string]WorkloadBuildOutput, runtimes map[string]string,
) error {
	if !validManagedOwnerID(prior) || !validManagedOwnerID(owner) || prior == owner || !validManagedOwnerID(operation) || len(runtimes) > 2 {
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
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, current, err := portableInventoryTx(ctx, tx, prior)
	if err != nil || len(current) != len(rebuilt) {
		return errors.Join(ErrWorkloadArtifactConflict, err)
	}
	for _, d := range current {
		replacement := rebuilt[d.Deployment]
		if !replacement.valid() || replacement.CodeHash != d.Output.CodeHash ||
			replacement.Entrypoint != d.Output.Entrypoint || replacement.RuntimeGeneration != runtimes[d.Runtime] ||
			(d.Output.SourceArchiveSHA256 != "" && replacement.SourceArchiveSHA256 != d.Output.SourceArchiveSHA256) {
			return ErrWorkloadArtifactConflict
		}
		if err := rebindPortableDeployment(ctx, tx, d, replacement); err != nil {
			return fmt.Errorf("rebind deployment %s: %w", d.Deployment, err)
		}
	}
	if _, err := tx.ExecContext(ctx, managedRuntimeSchema+`
		DELETE FROM workload_enrollment_runtimes;
		CREATE TABLE IF NOT EXISTS workload_portable_restore_audit (
		operation TEXT PRIMARY KEY, prior_owner TEXT NOT NULL, new_owner TEXT NOT NULL,
		restore_epoch INTEGER NOT NULL CHECK(restore_epoch>0));`); err != nil {
		return err
	}
	for lang, gen := range runtimes {
		if (lang != "node" && lang != "python") || !validManagedOwnerID(gen) {
			return ErrWorkloadArtifactInvalid
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workload_enrollment_runtimes VALUES(?,?)`, lang, gen); err != nil {
			return err
		}
	}
	var oldEpoch int64
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM workload_recovery WHERE id=1`).Scan(&oldEpoch); err != nil {
		return err
	}
	result, err := applyRecoveryTx(ctx, tx, operation, true)
	if err != nil || result.Epoch <= oldEpoch {
		return errors.Join(ErrWorkloadArtifactConflict, err)
	}
	// Only the disposable staged DB can exchange the permanent binding.
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER workload_owner_binding_no_update;
		DROP TRIGGER workload_owner_binding_no_delete;
		UPDATE workload_owner_binding SET owner=? WHERE id=1 AND owner=?;`+ownerBindingTriggers, owner, prior); err != nil {
		return err
	}
	if bound, err := checkManagedOwnerBinding(ctx, tx, owner); err != nil || !bound {
		return errors.Join(ErrManagedOwnerInvalid, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_portable_restore_audit VALUES(?,?,?,?)`,
		operation, prior, owner, result.Epoch); err != nil {
		return err
	}
	return tx.Commit()
}

func rebindPortableDeployment(ctx context.Context, tx *sql.Tx, d PortableDeployment, output WorkloadBuildOutput) error {
	a, err := readRollbackAssociationTx(ctx, tx, d.Owner, d.Deployment)
	if err != nil {
		return err
	}
	artifact, err := readWorkloadArtifactTx(ctx, tx, d.Owner)
	if err != nil {
		return err
	}
	before := a.digest()
	if artifact.Generation != a.generation {
		if _, err := readDeploymentPromotionTx(ctx, tx, d.Owner, d.Deployment, artifact.Generation, before); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return err
	}
	a.raw = string(raw)
	if _, err := tx.ExecContext(ctx, `UPDATE workload_deployment_capture SET output=? WHERE deployment_id=? AND function_id=?`,
		a.raw, d.Deployment, d.Owner); err != nil {
		return err
	}
	if artifact.Generation != a.generation {
		_, err = tx.ExecContext(ctx, `UPDATE workload_deployment_promotion SET association_digest=?
			WHERE function_id=? AND deployment_id=? AND generation=? AND association_digest=?`, a.digest(), d.Owner, d.Deployment, artifact.Generation, before)
	}
	return err
}
