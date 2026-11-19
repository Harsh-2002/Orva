package database

import "context"

// Explicit test initialization only. Existing inline authority is copied, never
// reconstructed from SDK booleans or replaced by an empty migration default.
const managedIAMSchema = `
CREATE TABLE IF NOT EXISTS workload_iam_inline (
 function_id TEXT PRIMARY KEY REFERENCES workload_iam_policies(function_id) ON DELETE CASCADE,
 policy_json TEXT NOT NULL CHECK(length(CAST(policy_json AS BLOB)) <= 32768)
);
INSERT INTO workload_iam_inline(function_id,policy_json)
 SELECT function_id,policy_json FROM workload_iam_policies WHERE true
 ON CONFLICT(function_id) DO NOTHING;
CREATE TABLE IF NOT EXISTS workload_iam_managed (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL UNIQUE CHECK(length(name) BETWEEN 1 AND 128),
 definition_revision INTEGER NOT NULL CHECK(definition_revision > 0),
 membership_revision INTEGER NOT NULL CHECK(membership_revision > 0),
 latest_version INTEGER NOT NULL CHECK(latest_version > 0),
 revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1))
);
CREATE TABLE IF NOT EXISTS workload_iam_managed_versions (
 policy_id TEXT NOT NULL REFERENCES workload_iam_managed(id),
 version INTEGER NOT NULL CHECK(version > 0),
 policy_json TEXT NOT NULL CHECK(length(CAST(policy_json AS BLOB)) <= 32768),
 actor_kind TEXT NOT NULL,
 actor_id TEXT NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY(policy_id,version)
);
CREATE TRIGGER IF NOT EXISTS workload_iam_managed_version_immutable
 BEFORE UPDATE ON workload_iam_managed_versions BEGIN
 SELECT RAISE(ABORT,'immutable IAM policy version'); END;
CREATE TABLE IF NOT EXISTS workload_iam_attachments (
 function_id TEXT NOT NULL REFERENCES workload_iam_policies(function_id) ON DELETE CASCADE,
 policy_id TEXT NOT NULL,
 version INTEGER NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 PRIMARY KEY(function_id,policy_id),
 FOREIGN KEY(policy_id,version) REFERENCES workload_iam_managed_versions(policy_id,version)
);
CREATE INDEX IF NOT EXISTS idx_workload_iam_attachment_policy ON workload_iam_attachments(policy_id,function_id);
CREATE TRIGGER IF NOT EXISTS workload_iam_attachment_owner_cap
 BEFORE INSERT ON workload_iam_attachments
 WHEN NOT EXISTS(SELECT 1 FROM workload_iam_attachments WHERE policy_id=NEW.policy_id AND function_id=NEW.function_id)
 AND (SELECT count(*) FROM workload_iam_attachments WHERE policy_id=NEW.policy_id)>=25
 BEGIN SELECT RAISE(ABORT,'IAM attached-owner cap'); END;
CREATE TRIGGER IF NOT EXISTS workload_iam_attachment_policy_cap
 BEFORE INSERT ON workload_iam_attachments
 WHEN NOT EXISTS(SELECT 1 FROM workload_iam_attachments WHERE policy_id=NEW.policy_id AND function_id=NEW.function_id)
 AND (SELECT count(*) FROM workload_iam_attachments WHERE function_id=NEW.function_id)>=8
 BEGIN SELECT RAISE(ABORT,'IAM attached-policy cap'); END;
CREATE TRIGGER IF NOT EXISTS workload_iam_managed_terminal
 BEFORE UPDATE ON workload_iam_managed WHEN OLD.revoked=1 AND
 (NEW.revoked!=1 OR NEW.id!=OLD.id OR NEW.name!=OLD.name OR
 NEW.definition_revision!=OLD.definition_revision OR NEW.latest_version!=OLD.latest_version OR
 NEW.membership_revision!=OLD.membership_revision+1)
 BEGIN SELECT RAISE(ABORT,'IAM policy revoked'); END;
CREATE TABLE IF NOT EXISTS workload_iam_managed_audit (
 policy_id TEXT NOT NULL,
 sequence INTEGER NOT NULL,
 operation TEXT NOT NULL,
 digest TEXT NOT NULL,
 actor_kind TEXT NOT NULL,
 actor_id TEXT NOT NULL,
 changed_at TEXT NOT NULL,
 PRIMARY KEY(policy_id,sequence)
);
CREATE TRIGGER IF NOT EXISTS workload_iam_managed_audit_immutable
 BEFORE UPDATE ON workload_iam_managed_audit BEGIN
 SELECT RAISE(ABORT,'immutable IAM managed audit'); END;
`

func (db *Database) initManagedIAMSchema(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, managedIAMSchema); err != nil {
		return err
	}
	return tx.Commit()
}
