package database

import "context"

// CheckIAMOperator resolves token-free, trusted authentication against live rows.
// Read and admin are independent requirements; channel references never qualify.
func (db *Database) CheckIAMOperator(ctx context.Context, ref OperatorCredentialReference, mutation bool) error {
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		return operatorDenied(ctx)
	}
	defer tx.Rollback()
	if _, _, err := authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
		return err
	}
	if mutation {
		if _, _, err := authorizeOperatorTx(ctx, tx, ref); err != nil {
			return err
		}
	}
	return nil
}
