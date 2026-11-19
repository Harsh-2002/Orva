package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"time"
)

// IAMOwnerDeletionAudit is metadata, not retained policy authority.
type IAMOwnerDeletionAudit struct {
	OwnerID          string `json:"owner_id"`
	PolicyID         string `json:"policy_id"`
	OwnerRevision    uint64 `json:"owner_revision,string"`
	AuthorityEpoch   uint64 `json:"authority_epoch,string"`
	MembershipBefore uint64 `json:"membership_before,string"`
	MembershipAfter  uint64 `json:"membership_after,string"`
	Version          uint64 `json:"version,string"`
	Enabled          bool   `json:"enabled"`
	Digest           string `json:"digest"`
}

// IAMOwnerDeletionPlan owns a bounded source CAS. Only the managed service may
// consume it under the owner/definition locks and the common IAM gate.
type IAMOwnerDeletionPlan struct {
	owner    string
	source   IAMSourceSnapshot
	policies []IAMManagedPolicy
	actor    IAMActorReference
	retryID  string
}

func (p *IAMOwnerDeletionPlan) PolicyIDs() []string {
	if p == nil {
		return nil
	}
	out := make([]string, len(p.policies))
	for i, v := range p.policies {
		out[i] = v.ID
	}
	slices.Sort(out)
	return out
}

func (db *Database) PrepareIAMOwnerDeletion(ctx context.Context, ref OperatorCredentialReference, owner string) (*IAMOwnerDeletionPlan, error) {
	if !db.historyEnabled.Load() || !validIAMUUID(owner) {
		return nil, ErrHistoryRequired
	}
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	actor, _, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	p := &IAMOwnerDeletionPlan{owner: owner, actor: actor}
	err = tx.QueryRowContext(ctx, `SELECT deletion_id FROM work_history_deletions WHERE owner_id=?`, owner).Scan(&p.retryID)
	if err == nil {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM workload_iam_attachments WHERE function_id=?`, owner).Scan(&n); err != nil {
			return nil, err
		}
		if n != 0 {
			return nil, ErrIAMConflict
		}
		return p, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	p.source, err = readSources(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	for _, a := range p.source.Attachments {
		policy, err := readManaged(ctx, tx, a.PolicyID)
		if err != nil {
			return nil, err
		}
		if policy.MembershipRevision >= math.MaxInt64 {
			return nil, ErrIAMRevisionExhausted
		}
		p.policies = append(p.policies, *policy)
	}
	return p, nil
}

func historyManagedAttachments(ctx context.Context, tx *sql.Tx, owner string) (int, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='workload_iam_attachments')`).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workload_iam_attachments WHERE function_id=?`, owner).Scan(&n)
	return n, err
}

func validateOwnerDeletion(ctx context.Context, tx *sql.Tx, p *IAMOwnerDeletionPlan, owner string, actor IAMActorReference, retry string) ([]IAMOwnerDeletionAudit, error) {
	n, err := historyManagedAttachments(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	if p == nil {
		if n != 0 {
			return nil, ErrHistoryRequired
		}
		return nil, nil
	}
	if p.owner != owner || p.actor != actor || p.retryID != retry {
		return nil, ErrIAMConflict
	}
	if retry != "" {
		if n != 0 {
			return nil, ErrIAMConflict
		}
		return nil, nil
	}
	current, err := readSources(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	if !sameSourceState(current, p.source) || len(p.policies) != len(current.Attachments) {
		return nil, ErrIAMConflict
	}
	audits := make([]IAMOwnerDeletionAudit, len(p.policies))
	for i, before := range p.policies {
		now, err := readManaged(ctx, tx, before.ID)
		if err != nil {
			return nil, err
		}
		if *now != before {
			return nil, ErrIAMConflict
		}
		a := current.Attachments[i]
		if a.PolicyID != before.ID {
			return nil, ErrIAMConflict
		}
		entry := IAMOwnerDeletionAudit{OwnerID: owner, PolicyID: before.ID, OwnerRevision: current.Record.Revision,
			AuthorityEpoch: current.Record.AuthorityEpoch, MembershipBefore: before.MembershipRevision,
			MembershipAfter: before.MembershipRevision + 1, Version: a.Version, Enabled: a.Enabled}
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		entry.Digest = hex.EncodeToString(sum[:])
		audits[i] = entry
	}
	return audits, nil
}

func commitOwnerDeletion(ctx context.Context, tx *sql.Tx, p *IAMOwnerDeletionPlan, audits []IAMOwnerDeletionAudit, actor IAMActorReference, now time.Time) error {
	for i, a := range audits {
		before := p.policies[i]
		res, err := tx.ExecContext(ctx, `UPDATE workload_iam_managed SET membership_revision=membership_revision+1
 WHERE id=? AND definition_revision=? AND membership_revision=? AND latest_version=? AND revoked=?`,
			before.ID, before.DefinitionRevision, before.MembershipRevision, before.LatestVersion, before.Revoked)
		if err = asyncChanged(res, err); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx, `DELETE FROM workload_iam_attachments WHERE function_id=? AND policy_id=? AND version=? AND enabled=?`,
			a.OwnerID, a.PolicyID, a.Version, a.Enabled)
		if err = asyncChanged(res, err); err != nil {
			return err
		}
		if err = appendManagedAuditTx(ctx, tx, a.PolicyID, "owner_delete", a.Digest, actor, now); err != nil {
			return err
		}
	}
	return nil
}

func appendManagedAuditTx(ctx context.Context, tx *sql.Tx, id, operation, digest string, actor IAMActorReference, at time.Time) error {
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM workload_iam_managed_audit WHERE policy_id=?`, id).Scan(&seq); err != nil {
		return err
	}
	if seq == math.MaxInt64 {
		return ErrIAMRevisionExhausted
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_managed_audit(policy_id,sequence,operation,digest,actor_kind,actor_id,changed_at)
 VALUES(?,?,?,?,?,?,?)`, id, seq+1, operation, digest, actor.Kind, actor.ID, at.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM workload_iam_managed_audit WHERE policy_id=? AND sequence NOT IN
 (SELECT sequence FROM workload_iam_managed_audit WHERE policy_id=? ORDER BY sequence DESC LIMIT 32)`, id, id)
	return err
}
