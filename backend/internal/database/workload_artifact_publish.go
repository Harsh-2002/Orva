package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxWorkloadEnvironmentEntries bounds this staged publisher independently of
// the 8-MiB serialized environment cap shared with artifact snapshots.
const MaxWorkloadEnvironmentEntries = 1024

type WorkloadEnvironmentChange struct {
	WorkloadArtifactChange
	ExpectedFunctionVersion  int
	ExpectedPolicyRevision   uint64
	ExpectedApprovedArtifact string
	Env                      map[string]string
}

// WorkloadEnvironmentPublication owns durable state only, not spawn authority.
type WorkloadEnvironmentPublication struct {
	Artifact WorkloadArtifactRecord
	Policy   *IAMPolicyRecord
	Function *Function
}

func prepareWorkloadEnvironment(env map[string]string) (string, error) {
	if len(env) > MaxWorkloadEnvironmentEntries {
		return "", ErrWorkloadArtifactInvalid
	}
	owned := make(map[string]string, len(env))
	bytes := 0
	for key, value := range env {
		if key == "" || !utf8.ValidString(key) || !utf8.ValidString(value) ||
			strings.ContainsAny(key, "=\x00") || strings.IndexByte(value, 0) >= 0 ||
			len(key) > MaxWorkloadArtifactEnvBytes-bytes {
			return "", ErrWorkloadArtifactInvalid
		}
		bytes += len(key)
		if len(value) > MaxWorkloadArtifactEnvBytes-bytes {
			return "", ErrWorkloadArtifactInvalid
		}
		bytes += len(value)
		owned[key] = value
	}
	data, err := json.Marshal(owned)
	if err != nil || len(data) > MaxWorkloadArtifactEnvBytes {
		return "", ErrWorkloadArtifactInvalid
	}
	if _, err := decodeArtifactEnvironment(data); err != nil {
		return "", ErrWorkloadArtifactInvalid
	}
	return string(data), nil
}

// PublishWorkloadEnvironment invalidates approval; it never rebinds retained grants.
// Legacy CRUD remains unchanged and cannot supply authoritative generations.
func (db *Database) PublishWorkloadEnvironment(ctx context.Context, c WorkloadEnvironmentChange) (*WorkloadEnvironmentPublication, error) {
	// A future mutation service must hold ordered host locks, revalidate live admin
	// authority, then publish the deny snapshot and retirement after durable commit.
	envJSON, err := prepareWorkloadEnvironmentChange(c)
	if err != nil {
		return nil, err
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "publication begin")
	}
	defer tx.Rollback()
	publication, err := publishWorkloadEnvironmentTx(ctx, tx, c, envJSON, time.Time{})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "publication commit")
	}
	return publication, nil
}

func prepareWorkloadEnvironmentChange(c WorkloadEnvironmentChange) (string, error) {
	envJSON, err := prepareWorkloadEnvironment(c.Env)
	if err != nil {
		return "", err
	}
	if err := validateWorkloadArtifactChange(c.WorkloadArtifactChange); err != nil {
		return "", err
	}
	if c.ExpectedFunctionVersion <= 0 || c.ExpectedFunctionVersion == math.MaxInt {
		return "", ErrWorkloadArtifactInvalid
	}
	if c.ExpectedPolicyRevision == 0 || c.ExpectedPolicyRevision > math.MaxInt64 {
		return "", ErrIAMInvalidPolicy
	}
	return envJSON, nil
}

func publishWorkloadEnvironmentTx(ctx context.Context, tx *sql.Tx, c WorkloadEnvironmentChange, envJSON string, authorizationExpiry time.Time) (*WorkloadEnvironmentPublication, error) {
	fn, err := readWorkloadArtifactFunctionTx(ctx, tx, c.OwnerID)
	if err != nil {
		return nil, err
	}
	if fn.Version != c.ExpectedFunctionVersion {
		return nil, ErrWorkloadArtifactConflict
	}
	policy, err := scanIAMPolicy(tx.QueryRowContext(ctx, iamPolicySelect, c.OwnerID))
	if err != nil {
		return nil, workloadPublicationReadError(ctx, err)
	}
	// Stored approval is only a CAS token, never a claim about the current artifact.
	if policy.Revision != c.ExpectedPolicyRevision || policy.ApprovedArtifact != c.ExpectedApprovedArtifact {
		return nil, ErrIAMConflict
	}
	if policy.Revision == math.MaxInt64 || policy.AuthorityEpoch == math.MaxInt64 {
		return nil, ErrIAMRevisionExhausted
	}
	if !authorizationExpiry.IsZero() && !authorizationExpiry.After(time.Now().UTC()) {
		return nil, operatorDenied(ctx)
	}
	artifact, err := rotateWorkloadArtifactTx(ctx, tx, c.WorkloadArtifactChange, "rotate")
	if err != nil {
		return nil, workloadPublicationReadError(ctx, err)
	}
	err = tx.QueryRowContext(ctx, `UPDATE functions SET env_vars=?,version=?,updated_at=?
		WHERE id=? AND version=? AND status!=? RETURNING updated_at`,
		envJSON, fn.Version+1, artifact.UpdatedAt.Format(time.RFC3339Nano),
		c.OwnerID, c.ExpectedFunctionVersion, FunctionStatusDeleting).Scan(&fn.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkloadArtifactConflict
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "publication function")
	}
	policy.Revision++
	policy.AuthorityEpoch++
	policy.ReviewRequired = true
	policy.UpdatedAt = artifact.UpdatedAt
	result, err := tx.ExecContext(ctx, `UPDATE workload_iam_policies
		SET revision=?,authority_epoch=?,review_required=1,updated_at=?
		WHERE function_id=? AND revision=? AND approved_artifact=?`,
		int64(policy.Revision), int64(policy.AuthorityEpoch), policy.UpdatedAt.Format(time.RFC3339Nano),
		c.OwnerID, int64(c.ExpectedPolicyRevision), c.ExpectedApprovedArtifact)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "publication policy")
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return nil, workloadArtifactStorageError(ctx, "publication policy result")
		}
		return nil, ErrIAMConflict
	}
	if err := appendIAMAudit(ctx, tx, policy, c.Actor); err != nil {
		return nil, workloadArtifactStorageError(ctx, "publication audit")
	}
	fn, err = readWorkloadArtifactFunctionTx(ctx, tx, c.OwnerID)
	if err != nil {
		return nil, err
	}
	return &WorkloadEnvironmentPublication{Artifact: *artifact, Policy: policy, Function: fn}, nil
}

func workloadPublicationReadError(ctx context.Context, err error) error {
	for _, sentinel := range []error{
		ErrIAMPolicyNotFound, ErrIAMFunctionNotFound, ErrIAMInvalidPolicy, ErrFunctionDeleting,
		ErrWorkloadArtifactNotFound, ErrWorkloadArtifactInvalid, ErrWorkloadArtifactConflict,
		ErrWorkloadArtifactExhausted,
	} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return workloadArtifactStorageError(ctx, "publication read")
}
