package database

import "context"

// Explicit fixture initialization only; absent from Migrate and normal boot.
func (db *Database) initWorkloadExposureSchema(ctx context.Context) error {
	_, err := db.write.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS workload_exposure_versions (
 kind TEXT NOT NULL CHECK(kind IN ('route','inbound')),
 key TEXT NOT NULL CHECK(length(CAST(key AS BLOB)) BETWEEN 1 AND 4096),
 version INTEGER NOT NULL CHECK(typeof(version)='integer' AND version>0),
 PRIMARY KEY(kind,key)
);
CREATE TABLE IF NOT EXISTS workload_exposure_audit (
 id TEXT NOT NULL,
 function_id TEXT NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
 operation TEXT NOT NULL,
 version INTEGER NOT NULL,
 policy_revision INTEGER NOT NULL,
 authority_epoch INTEGER NOT NULL,
 digest TEXT NOT NULL CHECK(length(digest)=64),
 actor_kind TEXT NOT NULL,
 actor_id TEXT NOT NULL,
 changed_at TEXT NOT NULL,
 PRIMARY KEY(function_id,id)
);
CREATE TRIGGER IF NOT EXISTS workload_exposure_audit_immutable
 BEFORE UPDATE ON workload_exposure_audit BEGIN SELECT RAISE(ABORT,'immutable exposure audit'); END;
`)
	return err
}
