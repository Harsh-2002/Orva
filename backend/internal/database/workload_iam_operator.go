package database

import (
	"context"
	"errors"
)

// SaveIAMPolicyAsOperator rechecks live admin authority in the policy/audit transaction.
// Current artifact approval and ordered live publication remain service obligations.
func (db *Database) SaveIAMPolicyAsOperator(ctx context.Context, ref OperatorCredentialReference, c IAMPolicyChange) (*IAMPolicyRecord, error) {
	actor, ok := operatorReferenceActor(ref)
	if !ok || (c.Actor != (IAMActorReference{}) && c.Actor != actor) {
		return nil, operatorDenied(ctx)
	}
	c.Actor = actor
	canonical, err := prepareIAMPolicyChange(c)
	if err != nil {
		return nil, err
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, operatorDenied(ctx)
	}
	defer tx.Rollback()
	if _, _, err := authorizeOperatorTx(ctx, tx, ref); err != nil {
		return nil, err
	}
	r, err := saveIAMPolicyTx(ctx, tx, c, canonical)
	if err != nil {
		if ctx.Err() != nil {
			return nil, operatorDenied(ctx)
		}
		for _, sentinel := range []error{ErrIAMConflict, ErrIAMPolicyNotFound, ErrIAMFunctionNotFound, ErrIAMInvalidPolicy, ErrIAMRevisionExhausted, ErrFunctionDeleting} {
			if errors.Is(err, sentinel) {
				return nil, sentinel
			}
		}
		return nil, workloadArtifactStorageError(ctx, "policy save")
	}
	// Recheck credential mutations made by transaction-local triggers as well as expiry.
	_, _, err = authorizeOperatorTx(ctx, tx, ref)
	if err != nil || ctx.Err() != nil {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		if ctx.Err() != nil {
			return nil, operatorDenied(ctx)
		}
		return nil, workloadArtifactStorageError(ctx, "policy commit")
	}
	return r, nil
}
