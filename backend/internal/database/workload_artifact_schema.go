package database

import (
	"context"
	"fmt"
)

// initWorkloadArtifactSchema is test-only; normal opening/migration stays inert.
func (db *Database) initWorkloadArtifactSchema(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return workloadArtifactStorageError(ctx, "schema")
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS workload_artifact_generations (
 function_id TEXT PRIMARY KEY REFERENCES functions(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(typeof(revision) = 'integer' AND revision > 0),
 epoch INTEGER NOT NULL CHECK(typeof(epoch) = 'integer' AND epoch > 0),
 generation TEXT NOT NULL CHECK(length(generation) = 36),
 actor_kind TEXT NOT NULL CHECK(actor_kind IN ('api_key','session','oauth','system')),
 actor_id TEXT NOT NULL CHECK(length(actor_id) = 36),
 change_kind TEXT NOT NULL CHECK(change_kind IN ('initialize','rotate','secret_upsert','secret_delete')),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);`)
	if err != nil {
		return workloadArtifactStorageError(ctx, "schema")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("artifact schema commit: %w", ErrWorkloadArtifactStorage)
	}
	return nil
}
