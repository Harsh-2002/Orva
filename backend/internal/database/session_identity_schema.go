package database

import (
	"context"
	"database/sql"

	"github.com/Harsh-2002/Orva/internal/ids"
)

const sessionIdentityBatchSize = 128

// initSessionIdentitySchema is test-only, never called by New, Migrate or serve.
// CreateSession assigns IDs only after this explicit schema has been installed.
func (db *Database) initSessionIdentitySchema(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return sessionIdentityError(ctx)
	}
	defer tx.Rollback()
	hasID, err := sessionIdentityColumn(ctx, tx)
	if err != nil {
		return sessionIdentityError(ctx)
	}
	if !hasID {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN id TEXT`); err != nil {
			return sessionIdentityError(ctx)
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_identity ON sessions(id)`); err != nil {
		return sessionIdentityError(ctx)
	}
	var validIndex bool
	if err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM pragma_index_list('sessions')
			WHERE name='idx_sessions_identity' AND "unique"=1 AND partial=0)
		AND (SELECT COUNT(*) FROM pragma_index_info('idx_sessions_identity'))=1
		AND EXISTS(SELECT 1 FROM pragma_index_info('idx_sessions_identity') WHERE name='id')`).Scan(&validIndex); err != nil || !validIndex {
		return sessionIdentityError(ctx)
	}

	// Rowids let the backfill page and update without ever loading bearer tokens.
	var cursor int64
	first := true
	for {
		query := `SELECT rowid, id FROM sessions WHERE rowid > ? ORDER BY rowid LIMIT ?`
		args := []any{cursor, sessionIdentityBatchSize}
		if first {
			query = `SELECT rowid, id FROM sessions ORDER BY rowid LIMIT ?`
			args = []any{sessionIdentityBatchSize}
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return sessionIdentityError(ctx)
		}
		type entry struct {
			rowID int64
			id    sql.NullString
		}
		batch := make([]entry, 0, sessionIdentityBatchSize)
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.rowID, &e.id); err != nil {
				rows.Close()
				return sessionIdentityError(ctx)
			}
			batch = append(batch, e)
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil {
			return sessionIdentityError(ctx)
		}
		if len(batch) == 0 {
			break
		}
		for _, e := range batch {
			if e.id.Valid {
				if !validIAMUUID(e.id.String) {
					return sessionIdentityError(ctx)
				}
				continue
			}
			result, err := tx.ExecContext(ctx, `UPDATE sessions SET id = ? WHERE rowid = ? AND id IS NULL`, ids.New(), e.rowID)
			if err != nil {
				return sessionIdentityError(ctx)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				return sessionIdentityError(ctx)
			}
		}
		cursor = batch[len(batch)-1].rowID
		first = false
	}
	if err := tx.Commit(); err != nil {
		return sessionIdentityError(ctx)
	}
	return nil
}
