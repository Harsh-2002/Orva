package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

type HistorySelector struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func validHistorySelector(s HistorySelector) bool {
	return validIAMUUID(s.ID) && (s.Kind == "job" || s.Kind == "cron" || s.Kind == "delete" || s.Kind == "reissue" || s.Kind == "purge")
}

func scanHistory(row interface{ Scan(...any) error }) (*HistoryEntry, error) {
	var e HistoryEntry
	var raw string
	err := row.Scan(&e.Kind, &e.ID, &raw, &e.Reserved)
	e.Metadata = json.RawMessage(raw)
	return &e, err
}

func (db *Database) ListWorkHistory(ctx context.Context, ref OperatorCredentialReference, target string, after HistorySelector, limit int) ([]HistoryEntry, error) {
	if !db.historyEnabled.Load() || (target != "" && !validIAMUUID(target)) || limit < 1 || limit > 100 ||
		(after != (HistorySelector{}) && !validHistorySelector(after)) {
		return nil, ErrHistoryInput
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err = authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,original_id,metadata,reserved FROM work_history
 WHERE (?='' OR target_id=?) AND (kind,original_id)>(?,?) ORDER BY kind,original_id LIMIT ?`, target, target, after.Kind, after.ID, limit)
	if err != nil {
		return nil, err
	}
	out := []HistoryEntry{}
	for rows.Next() {
		e, eerr := scanHistory(rows)
		if eerr != nil {
			rows.Close()
			return nil, eerr
		}
		out = append(out, *e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if _, _, err = authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (db *Database) GetWorkHistory(ctx context.Context, ref OperatorCredentialReference, s HistorySelector) (*HistoryEntry, error) {
	if !db.historyEnabled.Load() || !validHistorySelector(s) {
		return nil, ErrHistoryInput
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, _, err = authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
		return nil, err
	}
	e, err := scanHistory(tx.QueryRowContext(ctx, `SELECT kind,original_id,metadata,reserved FROM work_history WHERE kind=? AND original_id=?`, s.Kind, s.ID))
	if err != nil {
		return nil, err
	}
	if _, _, err = authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return e, nil
}

// PurgeWorkHistory never evicts reserved records. Ninety days is eligibility,
// not TTL; even expired rows persist until this explicit audited transaction.
func (db *Database) PurgeWorkHistory(ctx context.Context, ref OperatorCredentialReference, selected []HistorySelector, confirm bool) error {
	if !db.historyEnabled.Load() || !confirm || len(selected) < 1 || len(selected) > 32 {
		return ErrHistoryInput
	}
	seen := map[HistorySelector]bool{}
	for _, s := range selected {
		if !validHistorySelector(s) || seen[s] {
			return ErrHistoryInput
		}
		seen[s] = true
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actor, _, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var total int64
	for _, s := range selected {
		var size int64
		var reserved bool
		var eligible time.Time
		if err = tx.QueryRowContext(ctx, `SELECT metadata_bytes,reserved,eligible_at FROM work_history WHERE kind=? AND original_id=?`, s.Kind, s.ID).Scan(&size, &reserved, &eligible); err != nil {
			return err
		}
		if reserved || eligible.After(now) {
			return ErrHistoryInput
		}
		total += size
		if _, err = tx.ExecContext(ctx, `DELETE FROM work_history WHERE kind=? AND original_id=?`, s.Kind, s.ID); err != nil {
			return err
		}
		if s.Kind == "delete" {
			if _, err = tx.ExecContext(ctx, `DELETE FROM work_history_deletions WHERE deletion_id=? AND state='complete'`, s.ID); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE work_history_capacity SET rows_used=rows_used-?,bytes_used=bytes_used-? WHERE id=1`, len(selected), total); err != nil {
		return err
	}
	raw, err := historyJSON(struct {
		Operation string            `json:"operation"`
		Selected  []HistorySelector `json:"selected"`
		Actor     IAMActorReference `json:"actor"`
		At        time.Time         `json:"at"`
	}{"purge", selected, actor, now})
	if err != nil {
		return err
	}
	if err = historyReserve(ctx, tx, 1, int64(len(raw)), false); err != nil {
		return err
	}
	if err = historyPut(ctx, tx, "purge", ids.New(), "", "", raw, false, now.Add(HistoryRetention)); err != nil {
		return err
	}
	if _, _, err = authorizeOperatorTx(ctx, tx, ref); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

// HistoryReissue requires a new payload and complete new configuration. No old
// payload, idempotency key, policy approval or original work ID can be reused.
type HistoryReissue struct {
	Source      HistorySelector `json:"source"`
	TargetID    string          `json:"target_id"`
	Payload     json.RawMessage `json:"payload"`
	ScheduledAt time.Time       `json:"scheduled_at"`
	MaxAttempts int             `json:"max_attempts"`
	Name        string          `json:"name"`
	Expression  string          `json:"expression"`
	Timezone    string          `json:"timezone"`
	Enabled     bool            `json:"enabled"`
	Reviewed    bool            `json:"reviewed"`
	Confirm     bool            `json:"confirm"`
}

func (db *Database) ReissueWorkHistory(ctx context.Context, ref OperatorCredentialReference, r HistoryReissue, revs workloadiam.AsyncRevisions, next *time.Time) (string, error) {
	if !db.historyEnabled.Load() || !r.Reviewed || !r.Confirm || !validHistorySelector(r.Source) ||
		(r.Source.Kind != "job" && r.Source.Kind != "cron") || !validIAMUUID(r.TargetID) || len(r.Payload) == 0 ||
		len(r.Payload) > 1<<20 || !json.Valid(r.Payload) || revs.Target == 0 || revs.Creator != 0 {
		return "", ErrHistoryInput
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	actor, _, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return "", err
	}
	e, err := scanHistory(tx.QueryRowContext(ctx, `SELECT kind,original_id,metadata,reserved FROM work_history WHERE kind=? AND original_id=?`, r.Source.Kind, r.Source.ID))
	if err != nil {
		return "", err
	}
	var old WorkHistory
	if e.Reserved || json.Unmarshal(e.Metadata, &old) != nil || old.Executable || old.PayloadAvailable || old.TargetID == r.TargetID {
		return "", ErrHistoryInput
	}
	canonical, err := historyJSON(old)
	if err != nil || string(canonical) != string(e.Metadata) || old.Kind != r.Source.Kind ||
		old.OriginalID != r.Source.ID || !validIAMUUID(old.TargetID) || !validIAMUUID(old.DeletionID) {
		return "", ErrHistoryInput
	}
	if err = iamFunctionExists(ctx, tx, r.TargetID); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	newID := ids.New()
	if r.Source.Kind == "job" {
		if r.ScheduledAt.IsZero() || r.MaxAttempts < 1 || r.MaxAttempts > 100 || r.Name != "" || r.Expression != "" || r.Timezone != "" || r.Enabled {
			return "", ErrHistoryInput
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,function_id,payload,status,scheduled_at,attempts,max_attempts,created_at,
 authority_state,authority_reason,origin_kind,creator_id,operator_kind,operator_id,creation_caller_revision,
 creation_target_revision,authority_generation,admission_caller_revision,admission_target_revision)
 VALUES(?,?,?,'pending',?,0,?,?,'active','','operator','',?,?,0,?,1,0,?)`,
			newID, r.TargetID, []byte(r.Payload), r.ScheduledAt.UTC(), r.MaxAttempts, now, actor.Kind, actor.ID, revs.Target, revs.Target)
		if err != nil {
			return "", err
		}
		j := &AsyncJob{Job: Job{ID: newID, FunctionID: r.TargetID}, AsyncProvenance: AsyncProvenance{OriginKind: "operator", OperatorKind: actor.Kind, OperatorID: actor.ID}}
		if err = auditAsyncTx(ctx, tx, j, "reissue", actor); err != nil {
			return "", err
		}
	} else {
		c := &AsyncCron{CronSchedule: CronSchedule{Name: r.Name, CronExpr: r.Expression, Timezone: r.Timezone, Payload: string(r.Payload), FunctionID: r.TargetID, Enabled: r.Enabled}}
		if !validCronInput(c) || r.ScheduledAt != (time.Time{}) || r.MaxAttempts != 0 || r.Name == "" || r.Timezone == "" ||
			(r.Enabled && (next == nil || !next.After(now))) || (!r.Enabled && next != nil) {
			return "", ErrHistoryInput
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cron_schedules(id,function_id,name,cron_expr,timezone,enabled,payload,next_run_at,created_at,updated_at,
 authority_state,authority_reason,origin_kind,creator_id,operator_kind,operator_id,creation_caller_revision,creation_target_revision,
 authority_generation,admission_caller_revision,admission_target_revision,config_revision)
 VALUES(?,?,?,?,?,?,?,?,?,?,'active','','operator','',?,?,0,?,1,0,?,1)`,
			newID, r.TargetID, r.Name, r.Expression, r.Timezone, boolToInt(r.Enabled), string(r.Payload), nullTime(next), now, now, actor.Kind, actor.ID, revs.Target, revs.Target)
		if err != nil {
			return "", err
		}
		c.ID = newID
		c.OriginKind = "operator"
		c.OperatorKind = actor.Kind
		c.OperatorID = actor.ID
		c.ConfigRevision = 1
		if err = cronAuditTx(ctx, tx, c, "reissue", actor); err != nil {
			return "", err
		}
	}
	hash := sha256.Sum256(r.Payload)
	raw, err := historyJSON(struct {
		Operation     string            `json:"operation"`
		Source        HistorySelector   `json:"source"`
		OldOrigin     AsyncProvenance   `json:"old_origin"`
		NewOrigin     string            `json:"new_origin"`
		NewID         string            `json:"new_id"`
		TargetID      string            `json:"target_id"`
		Actor         IAMActorReference `json:"actor"`
		PayloadBytes  int               `json:"payload_bytes"`
		PayloadSHA256 string            `json:"payload_sha256"`
		At            time.Time         `json:"at"`
	}{"reissue", r.Source, old.Origin, "operator", newID, r.TargetID, actor, len(r.Payload), hex.EncodeToString(hash[:]), now})
	if err != nil {
		return "", err
	}
	if err = historyReserve(ctx, tx, 1, int64(len(raw)), false); err != nil {
		return "", err
	}
	if err = historyPut(ctx, tx, "reissue", newID, r.TargetID, old.DeletionID, raw, false, now.Add(HistoryRetention)); err != nil {
		return "", err
	}
	if _, _, err = authorizeOperatorTx(ctx, tx, ref); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return newID, nil
}
