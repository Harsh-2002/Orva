package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
)

var (
	ErrWorkloadArtifactInvalid   = errors.New("invalid workload artifact metadata")
	ErrWorkloadArtifactNotFound  = errors.New("workload artifact generation not found")
	ErrWorkloadArtifactConflict  = errors.New("workload artifact generation conflict")
	ErrWorkloadArtifactExhausted = errors.New("workload artifact revision or epoch exhausted")
	ErrWorkloadArtifactStorage   = errors.New("workload artifact storage failure")
)

// WorkloadArtifactRecord is host generation metadata, not policy or approval.
// Actor/ChangeKind describe only the last mutation, not a retained audit history.
type WorkloadArtifactRecord struct {
	OwnerID    string
	Revision   uint64
	Epoch      uint64
	Generation string
	Actor      IAMActorReference
	ChangeKind string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// WorkloadArtifactChange is an exact CAS against host metadata, not IAM revision.
type WorkloadArtifactChange struct {
	OwnerID            string
	ExpectedRevision   uint64
	ExpectedEpoch      uint64
	ExpectedGeneration string
	Actor              IAMActorReference
}

func validateWorkloadArtifactChange(c WorkloadArtifactChange) error {
	if !validIAMUUID(c.OwnerID) || !validIAMUUID(c.ExpectedGeneration) ||
		c.ExpectedRevision == 0 || c.ExpectedRevision > math.MaxInt64 ||
		c.ExpectedEpoch == 0 || c.ExpectedEpoch > math.MaxInt64 ||
		validateIAMActor(c.Actor) != nil {
		return ErrWorkloadArtifactInvalid
	}
	return nil
}

// SQL diagnostics may contain trigger text or stored values; never expose them.
func workloadArtifactStorageError(ctx context.Context, operation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("artifact %s: %w", operation, ErrWorkloadArtifactStorage)
}

const workloadArtifactSelect = `SELECT function_id,revision,epoch,generation,actor_kind,actor_id,change_kind,created_at,updated_at FROM workload_artifact_generations WHERE function_id=?`

func readWorkloadArtifactTx(ctx context.Context, tx *sql.Tx, ownerID string) (*WorkloadArtifactRecord, error) {
	r := &WorkloadArtifactRecord{}
	var revision, epoch int64
	var created, updated string
	err := tx.QueryRowContext(ctx, workloadArtifactSelect, ownerID).Scan(
		&r.OwnerID, &revision, &epoch, &r.Generation, &r.Actor.Kind, &r.Actor.ID, &r.ChangeKind, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkloadArtifactNotFound
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "read generation")
	}
	if r.OwnerID != ownerID || !validIAMUUID(r.OwnerID) || !validIAMUUID(r.Generation) ||
		revision <= 0 || epoch <= 0 || validateIAMActor(r.Actor) != nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	switch r.ChangeKind {
	case "initialize", "rotate", "secret_upsert", "secret_delete":
	default:
		return nil, ErrWorkloadArtifactInvalid
	}
	r.Revision, r.Epoch = uint64(revision), uint64(epoch)
	if r.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	if r.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	return r, nil
}

// NewWorkloadArtifact explicitly initializes a fresh generation without grants.
// Legacy CRUD does not maintain this staged record; do not use it for admission.
func (db *Database) NewWorkloadArtifact(ctx context.Context, ownerID string, actor IAMActorReference) (*WorkloadArtifactRecord, error) {
	if !validIAMUUID(ownerID) || validateIAMActor(actor) != nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "create")
	}
	defer tx.Rollback()
	if err := iamFunctionExists(ctx, tx, ownerID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	r := &WorkloadArtifactRecord{
		OwnerID: ownerID, Revision: 1, Epoch: 1, Generation: ids.New(),
		Actor: actor, ChangeKind: "initialize", CreatedAt: now, UpdatedAt: now,
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO workload_artifact_generations
		(function_id,revision,epoch,generation,actor_kind,actor_id,change_kind,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(function_id) DO NOTHING`,
		r.OwnerID, int64(r.Revision), int64(r.Epoch), r.Generation, actor.Kind, actor.ID,
		r.ChangeKind, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "create")
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return nil, workloadArtifactStorageError(ctx, "create result")
		}
		return nil, ErrWorkloadArtifactConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "create commit")
	}
	return r, nil
}

// rotateWorkloadArtifactTx never commits. Future code/config publication must
// update its rows and policy rebind in this same transaction, not registry.Set.
func rotateWorkloadArtifactTx(ctx context.Context, tx *sql.Tx, c WorkloadArtifactChange, kind string) (*WorkloadArtifactRecord, error) {
	if err := validateWorkloadArtifactChange(c); err != nil {
		return nil, err
	}
	if err := iamFunctionExists(ctx, tx, c.OwnerID); err != nil {
		return nil, err
	}
	r, err := readWorkloadArtifactTx(ctx, tx, c.OwnerID)
	if err != nil {
		return nil, err
	}
	if r.Revision != c.ExpectedRevision || r.Epoch != c.ExpectedEpoch || r.Generation != c.ExpectedGeneration {
		return nil, ErrWorkloadArtifactConflict
	}
	if r.Revision == math.MaxInt64 || r.Epoch == math.MaxInt64 {
		return nil, ErrWorkloadArtifactExhausted
	}
	r.Revision++
	r.Epoch++
	r.Generation, r.Actor, r.ChangeKind, r.UpdatedAt = ids.New(), c.Actor, kind, time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE workload_artifact_generations
		SET revision=?,epoch=?,generation=?,actor_kind=?,actor_id=?,change_kind=?,updated_at=?
		WHERE function_id=? AND revision=? AND epoch=? AND generation=?`,
		int64(r.Revision), int64(r.Epoch), r.Generation, r.Actor.Kind, r.Actor.ID, r.ChangeKind,
		r.UpdatedAt.Format(time.RFC3339Nano), c.OwnerID, int64(c.ExpectedRevision), int64(c.ExpectedEpoch), c.ExpectedGeneration)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "rotate")
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return nil, workloadArtifactStorageError(ctx, "rotate result")
		}
		return nil, ErrWorkloadArtifactConflict
	}
	return r, nil
}

// RotateWorkloadArtifact rotates even for identical inputs. It publishes no
// config, runtime identity, host state or policy approval.
func (db *Database) RotateWorkloadArtifact(ctx context.Context, c WorkloadArtifactChange) (*WorkloadArtifactRecord, error) {
	return db.mutateWorkloadArtifact(ctx, c, "rotate", "", "")
}

// UpsertWorkloadArtifactSecret accepts opaque ciphertext from a trusted encryptor.
func (db *Database) UpsertWorkloadArtifactSecret(ctx context.Context, c WorkloadArtifactChange, key, encrypted string) (*WorkloadArtifactRecord, error) {
	if key == "" || encrypted == "" {
		return nil, ErrWorkloadArtifactInvalid
	}
	return db.mutateWorkloadArtifact(ctx, c, "secret_upsert", key, encrypted)
}

func (db *Database) DeleteWorkloadArtifactSecret(ctx context.Context, c WorkloadArtifactChange, key string) (*WorkloadArtifactRecord, error) {
	if key == "" {
		return nil, ErrWorkloadArtifactInvalid
	}
	return db.mutateWorkloadArtifact(ctx, c, "secret_delete", key, "")
}

func (db *Database) mutateWorkloadArtifact(ctx context.Context, c WorkloadArtifactChange, kind, key, encrypted string) (*WorkloadArtifactRecord, error) {
	if err := validateWorkloadArtifactChange(c); err != nil {
		return nil, err
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "begin mutation")
	}
	defer tx.Rollback()
	r, err := rotateWorkloadArtifactTx(ctx, tx, c, kind)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "secret_upsert":
		_, err = tx.ExecContext(ctx, `INSERT INTO function_secrets (function_id,key,value_encrypted)
			VALUES (?,?,?) ON CONFLICT(function_id,key) DO UPDATE SET
			value_encrypted=excluded.value_encrypted,updated_at=CURRENT_TIMESTAMP`, c.OwnerID, key, encrypted)
	case "secret_delete":
		_, err = tx.ExecContext(ctx, `DELETE FROM function_secrets WHERE function_id=? AND key=?`, c.OwnerID, key)
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "secret mutation")
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "mutation commit")
	}
	return r, nil
}
