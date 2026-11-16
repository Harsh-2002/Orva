package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	MaxWorkloadArtifactSecrets         = 1024
	MaxWorkloadArtifactCiphertextBytes = 12 << 20
	MaxWorkloadArtifactEnvBytes        = 8 << 20
)

// WorkloadArtifactSnapshot owns exact durable inputs, not a validated spawn path
// or runtime identity. Decryption, lifetime pinning and publication remain external.
type WorkloadArtifactSnapshot struct {
	Artifact WorkloadArtifactRecord
	Function *Function
	Secrets  []SecretRow
}

func (s *WorkloadArtifactSnapshot) Clone() *WorkloadArtifactSnapshot {
	if s == nil {
		return nil
	}
	copy := *s
	copy.Function = s.Function.Clone()
	copy.Secrets = slices.Clone(s.Secrets)
	return &copy
}

// GetWorkloadArtifactSnapshot never initializes missing metadata or skips rows.
func (db *Database) GetWorkloadArtifactSnapshot(ctx context.Context, ownerID string) (*WorkloadArtifactSnapshot, error) {
	if !validIAMUUID(ownerID) {
		return nil, ErrWorkloadArtifactInvalid
	}
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "snapshot begin")
	}
	defer tx.Rollback()
	s, err := readWorkloadArtifactSnapshotTx(ctx, tx, ownerID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "snapshot commit")
	}
	return s, nil
}

func readWorkloadArtifactSnapshotTx(ctx context.Context, tx *sql.Tx, ownerID string) (*WorkloadArtifactSnapshot, error) {
	r, err := readWorkloadArtifactTx(ctx, tx, ownerID)
	if err != nil {
		return nil, err
	}
	fn, err := readWorkloadArtifactFunctionTx(ctx, tx, ownerID)
	if err != nil {
		return nil, err
	}
	var count, bytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(key AS BLOB))+length(CAST(value_encrypted AS BLOB))),0) FROM function_secrets WHERE function_id=?`, ownerID).Scan(&count, &bytes); err != nil {
		return nil, workloadArtifactStorageError(ctx, "snapshot secret bounds")
	}
	if count > MaxWorkloadArtifactSecrets || bytes > MaxWorkloadArtifactCiphertextBytes {
		return nil, ErrWorkloadArtifactInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT function_id,key,value_encrypted FROM function_secrets WHERE function_id=? ORDER BY key`, ownerID)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "snapshot secrets")
	}
	defer rows.Close()
	secrets := []SecretRow{}
	for rows.Next() {
		var row SecretRow
		if err := rows.Scan(&row.FunctionID, &row.Key, &row.ValueEncrypted); err != nil {
			return nil, workloadArtifactStorageError(ctx, "snapshot secret row")
		}
		if row.FunctionID != ownerID || row.Key == "" || !utf8.ValidString(row.Key) || strings.IndexByte(row.Key, 0) >= 0 || row.ValueEncrypted == "" {
			return nil, ErrWorkloadArtifactInvalid
		}
		secrets = append(secrets, row)
	}
	if err := rows.Err(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "snapshot secret rows")
	}
	return &WorkloadArtifactSnapshot{Artifact: *r, Function: fn.Clone(), Secrets: slices.Clone(secrets)}, nil
}

func readWorkloadArtifactFunctionTx(ctx context.Context, tx *sql.Tx, ownerID string) (*Function, error) {
	var envBytes int64
	err := tx.QueryRowContext(ctx, `SELECT length(CAST(env_vars AS BLOB)) FROM functions WHERE id=?`, ownerID).Scan(&envBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIAMFunctionNotFound
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "snapshot environment bounds")
	}
	if envBytes > MaxWorkloadArtifactEnvBytes {
		return nil, ErrWorkloadArtifactInvalid
	}
	var fn Function
	var envJSON string
	var image, codeHash sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,name,description,runtime,entrypoint,image,timeout_ms,memory_mb,cpus,env_vars,
		network_mode,sdk,max_concurrency,concurrency_policy,auth_mode,rate_limit_per_min,version,status,code_hash,
		image_size,active_deployment_id,run_entrypoint,created_at,updated_at FROM functions WHERE id=?`, ownerID).Scan(
		&fn.ID, &fn.Name, &fn.Description, &fn.Runtime, &fn.Entrypoint, &image,
		&fn.TimeoutMS, &fn.MemoryMB, &fn.CPUs, &envJSON, &fn.NetworkMode, &fn.SDK,
		&fn.MaxConcurrency, &fn.ConcurrencyPolicy, &fn.AuthMode, &fn.RateLimitPerMin,
		&fn.Version, &fn.Status, &codeHash, &fn.ImageSize, &fn.ActiveDeploymentID, &fn.RunEntrypoint, &fn.CreatedAt, &fn.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIAMFunctionNotFound
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "snapshot function")
	}
	if fn.Status == FunctionStatusDeleting {
		return nil, ErrFunctionDeleting
	}
	// Unlike legacy scanners, an invalid environment cannot become partial config.
	if err := json.Unmarshal([]byte(envJSON), &fn.EnvVars); err != nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	fn.Image, fn.CodeHash = image.String, codeHash.String
	return &fn, nil
}
