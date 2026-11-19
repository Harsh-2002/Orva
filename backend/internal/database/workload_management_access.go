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

// CheckOperatorPermission requires exactly one permission of the live operator,
// matching a legacy route's required tier. Sessions hold every tier; channel
// references never qualify.
func (db *Database) CheckOperatorPermission(ctx context.Context, ref OperatorCredentialReference, permission string) error {
	switch permission {
	case "read", "write", "admin":
	default:
		return operatorDenied(ctx)
	}
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		return operatorDenied(ctx)
	}
	defer tx.Rollback()
	_, _, err = authorizeOperatorPermissionTx(ctx, tx, ref, permission)
	return err
}
