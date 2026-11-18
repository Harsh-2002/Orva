package database

import "context"

type HistoryCapacity struct {
	RowsUsed             int64 `json:"rows_used,string"`
	BytesUsed            int64 `json:"bytes_used,string"`
	RowsReserved         int64 `json:"rows_reserved,string"`
	BytesReserved        int64 `json:"bytes_reserved,string"`
	MaxRows              int64 `json:"max_rows,string"`
	MaxBytes             int64 `json:"max_bytes,string"`
	PurgeEligibilityDays int   `json:"purge_eligibility_days"`
	CanManage            bool  `json:"can_manage"`
}

func (db *Database) WorkHistoryCapacity(ctx context.Context, ref OperatorCredentialReference) (*HistoryCapacity, error) {
	if !db.historyEnabled.Load() {
		return nil, ErrHistoryRequired
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err = authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
		return nil, err
	}
	out := &HistoryCapacity{MaxRows: HistoryMaxRows, MaxBytes: HistoryMaxBytes, PurgeEligibilityDays: 90}
	if err = tx.QueryRowContext(ctx, `SELECT rows_used,bytes_used,rows_reserved,bytes_reserved FROM work_history_capacity WHERE id=1`).
		Scan(&out.RowsUsed, &out.BytesUsed, &out.RowsReserved, &out.BytesReserved); err != nil {
		return nil, err
	}
	_, _, adminErr := authorizeOperatorTx(ctx, tx, ref)
	out.CanManage = adminErr == nil
	if _, _, err = authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
