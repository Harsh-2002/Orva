package database

import (
	"context"
	"fmt"
)

// InitAsyncSchemaForFixture is internal opt-in, never called by Migrate/serve.
func (db *Database) InitAsyncSchemaForFixture(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	columns := []struct{ name, definition string }{
		{"authority_state", "TEXT NOT NULL DEFAULT 'quarantined' CHECK(authority_state IN ('active','suspended','quarantined'))"},
		{"authority_reason", "TEXT NOT NULL DEFAULT 'legacy_unknown' CHECK(length(authority_reason)<=64)"},
		{"origin_kind", "TEXT NOT NULL DEFAULT 'legacy_unknown' CHECK(origin_kind IN ('function','operator','legacy_unknown'))"},
		{"creator_id", "TEXT NOT NULL DEFAULT ''"},
		{"operator_kind", "TEXT NOT NULL DEFAULT ''"},
		{"operator_id", "TEXT NOT NULL DEFAULT ''"},
		{"creation_caller_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"creation_target_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"authority_generation", "INTEGER NOT NULL DEFAULT 1 CHECK(authority_generation>0)"},
		{"admission_caller_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"admission_target_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"async_idempotency_key", "TEXT NOT NULL DEFAULT '' CHECK(length(CAST(async_idempotency_key AS BLOB))<=256)"},
	}
	for _, table := range []string{"jobs"} {
		rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return err
		}
		exists := map[string]bool{}
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var def any
			if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
				rows.Close()
				return err
			}
			exists[name] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, c := range columns {
			if !exists[c.name] {
				if _, err := tx.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+c.name+" "+c.definition); err != nil {
					return fmt.Errorf("async schema: %w", err)
				}
			}
		}
	}
	_, err = tx.ExecContext(ctx, `
UPDATE jobs SET origin_kind='function',creator_id=enqueued_by_function_id,
 authority_state='suspended',authority_reason='legacy_review'
 WHERE origin_kind='legacy_unknown' AND COALESCE(enqueued_by_function_id,'')<>'';
CREATE TABLE IF NOT EXISTS job_idempotency_v2 (
 origin_kind TEXT NOT NULL CHECK(origin_kind IN ('function','operator')),
 creator_identity TEXT NOT NULL,
 target_id TEXT NOT NULL,
 raw_key TEXT NOT NULL CHECK(length(CAST(raw_key AS BLOB)) BETWEEN 1 AND 256),
 expires_at DATETIME NOT NULL,
 job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
 PRIMARY KEY(origin_kind,creator_identity,target_id,raw_key)
);
CREATE TABLE IF NOT EXISTS async_work_audit (
 id INTEGER PRIMARY KEY,
 job_id TEXT NOT NULL,
 action TEXT NOT NULL,
 actor_kind TEXT NOT NULL,
 actor_id TEXT NOT NULL,
 origin_kind TEXT NOT NULL,
 creator_id TEXT NOT NULL,
 target_id TEXT NOT NULL,
 changed_at DATETIME NOT NULL
);
CREATE TRIGGER IF NOT EXISTS async_creator_delete BEFORE DELETE ON functions BEGIN
 UPDATE jobs SET authority_state='suspended',authority_reason='creator_missing',
  authority_generation=authority_generation+1 WHERE creator_id=OLD.id AND origin_kind='function' AND status IN ('pending','running');
END;`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
