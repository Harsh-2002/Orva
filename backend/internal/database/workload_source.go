package database

import (
	"context"
	"database/sql"
)

type WorkloadSource struct {
	Owner, Runtime, Entrypoint string
	Output                     WorkloadBuildOutput
}

// ReadWorkloadDeploymentSource resolves a retained deployment without consulting
// the current Factory or any legacy source directory.
func (db *Database) ReadWorkloadDeploymentSource(ctx context.Context, owner, deployment string) (WorkloadSource, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return WorkloadSource{}, err
	}
	defer tx.Rollback()
	a, err := readRollbackAssociationTx(ctx, tx, owner, deployment)
	if err != nil {
		return WorkloadSource{}, err
	}
	return WorkloadSource{Owner: owner, Runtime: a.runtime, Entrypoint: a.authored, Output: a.output}, nil
}

// ReadWorkloadSource returns the exact metadata for display, not admission.
// Paused and pending functions still have an authored source.
func (db *Database) ReadWorkloadSource(ctx context.Context, owner string) (WorkloadSource, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return WorkloadSource{}, err
	}
	defer tx.Rollback()
	fn, err := readWorkloadArtifactFunctionTx(ctx, tx, owner)
	if err != nil {
		return WorkloadSource{}, err
	}
	source := WorkloadSource{Owner: fn.ID, Runtime: fn.Runtime, Entrypoint: fn.Entrypoint}
	if fn.ActiveDeploymentID == "" {
		if fn.Status != "created" {
			return WorkloadSource{}, ErrWorkloadArtifactConflict
		}
		return source, nil
	}
	artifact, err := readWorkloadArtifactTx(ctx, tx, owner)
	if err != nil {
		return WorkloadSource{}, err
	}
	association, _, err := readResumeAssociationTx(ctx, tx, fn, artifact.Generation)
	if err != nil {
		return WorkloadSource{}, err
	}
	source.Output = association.output
	return source, nil
}
