package database

import (
	"context"
	"time"
)

type IAMAttachedOwner struct {
	OwnerID string `json:"owner_id"`
	Version uint64 `json:"version,string"`
	Enabled bool   `json:"enabled"`
}

type IAMManagedAudit struct {
	Sequence  uint64            `json:"sequence,string"`
	Operation string            `json:"operation"`
	Digest    string            `json:"digest"`
	Actor     IAMActorReference `json:"actor"`
	ChangedAt time.Time         `json:"changed_at"`
}

func (db *Database) ListIAMAttachedOwners(ctx context.Context, id string) ([]IAMAttachedOwner, error) {
	if !validIAMUUID(id) {
		return nil, ErrIAMInvalidPolicy
	}
	rows, err := db.read.QueryContext(ctx, `SELECT function_id,version,enabled FROM workload_iam_attachments WHERE policy_id=? ORDER BY function_id LIMIT 26`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMAttachedOwner{}
	for rows.Next() {
		var a IAMAttachedOwner
		if err := rows.Scan(&a.OwnerID, &a.Version, &a.Enabled); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if len(out) > MaxIAMPolicyOwners {
		return nil, ErrIAMInvalidPolicy
	}
	return out, rows.Err()
}

func (db *Database) ListIAMManagedAudit(ctx context.Context, id string) ([]IAMManagedAudit, error) {
	if !validIAMUUID(id) {
		return nil, ErrIAMInvalidPolicy
	}
	rows, err := db.read.QueryContext(ctx, `SELECT sequence,operation,digest,actor_kind,actor_id,changed_at FROM workload_iam_managed_audit WHERE policy_id=? ORDER BY sequence DESC LIMIT 32`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMManagedAudit{}
	for rows.Next() {
		var a IAMManagedAudit
		var changed string
		if err := rows.Scan(&a.Sequence, &a.Operation, &a.Digest, &a.Actor.Kind, &a.Actor.ID, &changed); err != nil {
			return nil, err
		}
		if a.ChangedAt, err = time.Parse(time.RFC3339Nano, changed); err != nil {
			return nil, ErrIAMInvalidPolicy
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
