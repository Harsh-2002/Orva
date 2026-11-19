package database

import "context"

// Explicit opt-in initialization only; absent from Migrate and normal boot.
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
CREATE TABLE IF NOT EXISTS workload_route_publications (
 path TEXT PRIMARY KEY REFERENCES routes(path) ON DELETE CASCADE,
 version INTEGER NOT NULL CHECK(typeof(version)='integer' AND version>0),
 function_id TEXT NOT NULL,
 methods TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS workload_route_unpublish_on_update
 AFTER UPDATE ON routes BEGIN DELETE FROM workload_route_publications WHERE path IN (OLD.path,NEW.path); END;
CREATE TRIGGER IF NOT EXISTS workload_route_unpublish_on_insert
 AFTER INSERT ON routes BEGIN DELETE FROM workload_route_publications WHERE path=NEW.path; END;
CREATE TABLE IF NOT EXISTS workload_inbound_publications (
 id TEXT PRIMARY KEY REFERENCES inbound_webhooks(id) ON DELETE CASCADE,
 version INTEGER NOT NULL CHECK(typeof(version)='integer' AND version>0),
 function_id TEXT NOT NULL,
 signature_format TEXT NOT NULL,
 signature_header TEXT NOT NULL,
 active INTEGER NOT NULL CHECK(active IN (0,1)),
 secret_digest TEXT NOT NULL CHECK(length(secret_digest)=64)
);
CREATE TRIGGER IF NOT EXISTS workload_inbound_unpublish_on_update
 AFTER UPDATE ON inbound_webhooks BEGIN DELETE FROM workload_inbound_publications WHERE id IN (OLD.id,NEW.id); END;
CREATE TRIGGER IF NOT EXISTS workload_inbound_unpublish_on_insert
 AFTER INSERT ON inbound_webhooks BEGIN DELETE FROM workload_inbound_publications WHERE id=NEW.id; END;
`)
	return err
}
