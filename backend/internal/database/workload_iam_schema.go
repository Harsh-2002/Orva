package database

import (
	"context"
	"fmt"
)

// initWorkloadIAMSchema is deliberately NOT called by New, Migrate or serve.
func (db *Database) initWorkloadIAMSchema(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("IAM schema: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS workload_iam_policies (
 function_id TEXT PRIMARY KEY REFERENCES functions(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(typeof(revision) = 'integer' AND revision > 0),
 authority_epoch INTEGER NOT NULL CHECK(typeof(authority_epoch) = 'integer' AND authority_epoch > 0),
 approved_artifact TEXT NOT NULL CHECK(length(approved_artifact) BETWEEN 1 AND 256),
 review_required INTEGER NOT NULL CHECK(review_required IN (0,1)),
 quarantined INTEGER NOT NULL CHECK(quarantined IN (0,1)),
 policy_json TEXT NOT NULL CHECK(length(CAST(policy_json AS BLOB)) <= 32768),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workload_iam_audit (
 function_id TEXT NOT NULL REFERENCES workload_iam_policies(function_id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(typeof(revision) = 'integer' AND revision > 0),
 authority_epoch INTEGER NOT NULL CHECK(typeof(authority_epoch) = 'integer' AND authority_epoch > 0),
 digest TEXT NOT NULL CHECK(length(digest) = 64),
 actor_kind TEXT NOT NULL CHECK(actor_kind IN ('api_key','session','oauth','system')),
 actor_id TEXT NOT NULL CHECK(length(actor_id) = 36),
 changed_at TEXT NOT NULL,
 PRIMARY KEY(function_id, revision)
);
CREATE TRIGGER IF NOT EXISTS workload_iam_audit_immutable
BEFORE UPDATE ON workload_iam_audit BEGIN
 SELECT RAISE(ABORT, 'IAM audit records are immutable');
END;
`)
	if err != nil {
		return fmt.Errorf("IAM schema: %w", err)
	}
	return tx.Commit()
}
