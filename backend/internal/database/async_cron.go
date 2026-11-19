package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

type AsyncCron struct {
	CronSchedule
	AsyncProvenance
	ConfigRevision uint64 `json:"config_revision,string"`
	FunctionName   string `json:"function_name"`
}

func (c *AsyncCron) Origin() workloadiam.AsyncOrigin {
	return workloadiam.AsyncOrigin{Kind: c.OriginKind, CreatorID: c.CreatorID, OperatorKind: c.OperatorKind,
		OperatorID: c.OperatorID, TargetID: c.FunctionID, Trigger: workloadiam.Cron, ScheduleName: c.Name,
		ExpectedCreatorRevision: c.AdmissionCallerRevision, ExpectedTargetRevision: c.AdmissionTargetRevision}
}

// InitCronSchemaForFixture never runs at boot, including in the jobs-only fixture.
func (db *Database) InitCronSchemaForFixture(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := initCronSchemaTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func initCronSchemaTx(ctx context.Context, tx *sql.Tx) error {
	columns := []struct{ name, definition string }{
		{"authority_state", "TEXT NOT NULL DEFAULT 'quarantined' CHECK(authority_state IN ('active','suspended','quarantined'))"},
		{"authority_reason", "TEXT NOT NULL DEFAULT 'legacy_unknown' CHECK(length(authority_reason)<=64)"},
		{"origin_kind", "TEXT NOT NULL DEFAULT 'legacy_unknown' CHECK(origin_kind IN ('function','operator','legacy_unknown'))"},
		{"creator_id", "TEXT NOT NULL DEFAULT ''"}, {"operator_kind", "TEXT NOT NULL DEFAULT ''"}, {"operator_id", "TEXT NOT NULL DEFAULT ''"},
		{"creation_caller_revision", "INTEGER NOT NULL DEFAULT 0"}, {"creation_target_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"authority_generation", "INTEGER NOT NULL DEFAULT 1 CHECK(authority_generation>0)"},
		{"admission_caller_revision", "INTEGER NOT NULL DEFAULT 0"}, {"admission_target_revision", "INTEGER NOT NULL DEFAULT 0"},
		{"config_revision", "INTEGER NOT NULL DEFAULT 1 CHECK(config_revision>0)"},
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(cron_schedules)")
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for rows.Next() {
		var cid, nn, pk int
		var n, typ string
		var def any
		if err := rows.Scan(&cid, &n, &typ, &nn, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		exists[n] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range columns {
		if !exists[c.name] {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE cron_schedules ADD COLUMN "+c.name+" "+c.definition); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS async_cron_audit(
	 id INTEGER PRIMARY KEY, schedule_id TEXT NOT NULL, action TEXT NOT NULL,
	 actor_kind TEXT NOT NULL, actor_id TEXT NOT NULL, origin_kind TEXT NOT NULL,
	 creator_id TEXT NOT NULL, target_id TEXT NOT NULL, config_revision INTEGER NOT NULL, changed_at DATETIME NOT NULL);
	 CREATE INDEX IF NOT EXISTS idx_async_cron_due ON cron_schedules(authority_state,enabled,next_run_at);
	 CREATE TRIGGER IF NOT EXISTS async_cron_creator_delete BEFORE DELETE ON functions BEGIN
	 UPDATE cron_schedules SET authority_state='suspended',authority_reason='creator_missing',
	  authority_generation=authority_generation+1,next_run_at=NULL
	  WHERE creator_id=OLD.id AND origin_kind='function' AND authority_state<>'quarantined';
	 END;`)
	if err != nil {
		return err
	}
	return nil
}

const asyncCronColumns = `c.id,c.function_id,c.name,c.cron_expr,c.timezone,c.enabled,c.last_run_at,c.next_run_at,
 COALESCE(c.last_status,''),COALESCE(c.last_error,''),c.payload,c.created_at,c.updated_at,
 c.authority_state,c.authority_reason,c.origin_kind,c.creator_id,c.operator_kind,c.operator_id,
 c.creation_caller_revision,c.creation_target_revision,c.authority_generation,c.admission_caller_revision,c.admission_target_revision,c.config_revision,
 COALESCE((SELECT name FROM functions f WHERE f.id=c.function_id),'')`

func scanAsyncCron(row interface{ Scan(...any) error }) (*AsyncCron, error) {
	var c AsyncCron
	var enabled int
	var last, next sql.NullTime
	err := row.Scan(&c.ID, &c.FunctionID, &c.Name, &c.CronExpr, &c.Timezone, &enabled, &last, &next,
		&c.LastStatus, &c.LastError, &c.Payload, &c.CreatedAt, &c.UpdatedAt,
		&c.AuthorityState, &c.AuthorityReason, &c.OriginKind, &c.CreatorID, &c.OperatorKind, &c.OperatorID,
		&c.CreationCallerRevision, &c.CreationTargetRevision, &c.AuthorityGeneration, &c.AdmissionCallerRevision, &c.AdmissionTargetRevision, &c.ConfigRevision, &c.FunctionName)
	c.Enabled = enabled == 1
	if last.Valid {
		c.LastRunAt = &last.Time
	}
	if next.Valid {
		c.NextRunAt = &next.Time
	}
	return &c, err
}
func (db *Database) GetAsyncCron(ctx context.Context, id string) (*AsyncCron, error) {
	return scanAsyncCron(db.read.QueryRowContext(ctx, `SELECT `+asyncCronColumns+` FROM cron_schedules c WHERE c.id=?`, id))
}
func (db *Database) ListAsyncCrons(ctx context.Context, fn string, due *time.Time) ([]*AsyncCron, error) {
	q := `SELECT ` + asyncCronColumns + ` FROM cron_schedules c WHERE (?='' OR c.function_id=?)`
	args := []any{fn, fn}
	if due != nil {
		q += ` AND c.enabled=1 AND c.authority_state='active' AND c.next_run_at<=?`
		args = append(args, due.UTC())
	}
	q += ` ORDER BY c.next_run_at,c.id LIMIT 500`
	rows, err := db.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AsyncCron{}
	for rows.Next() {
		c, err := scanAsyncCron(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func validCronInput(c *AsyncCron) bool {
	return c != nil && validIAMUUID(c.FunctionID) && utf8.ValidString(c.Name) && utf8.RuneCountInString(c.Name) <= 256 &&
		len(c.CronExpr) > 0 && len(c.CronExpr) <= 1024 && len(c.Timezone) <= 256 && len(c.Payload) <= 1<<20 && json.Valid([]byte(c.Payload))
}
func cronAuditTx(ctx context.Context, tx *sql.Tx, c *AsyncCron, action string, actor IAMActorReference) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO async_cron_audit(schedule_id,action,actor_kind,actor_id,origin_kind,creator_id,target_id,config_revision,changed_at)
	 VALUES(?,?,?,?,?,?,?,?,?)`, c.ID, action, actor.Kind, actor.ID, c.OriginKind, c.CreatorID, c.FunctionID, c.ConfigRevision, time.Now().UTC())
	return err
}

// SaveAsyncCron receives only host-validated inputs; operator edits preserve origin.
func (db *Database) SaveAsyncCron(ctx context.Context, c *AsyncCron, ref *OperatorCredentialReference, revs workloadiam.AsyncRevisions, action string) error {
	if !validCronInput(c) || revs.Target == 0 || revs.Target > math.MaxInt64 || revs.Creator > math.MaxInt64 {
		return ErrAsyncConflict
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var actor IAMActorReference
	var expiry time.Time
	if ref != nil {
		actor, expiry, err = authorizeOperatorTx(ctx, tx, *ref)
		if err != nil {
			return err
		}
	}
	if err := iamFunctionExists(ctx, tx, c.FunctionID); err != nil {
		return err
	}
	fresh := action == "create" || action == "upsert"
	if action == "upsert" {
		if ref != nil || c.OriginKind != "function" || c.CreatorID != c.FunctionID || strings.TrimSpace(c.Name) == "" || revs.Creator == 0 || c.OperatorID != "" || c.OperatorKind != "" {
			return ErrAsyncConflict
		}
		existing, err := scanAsyncCron(tx.QueryRowContext(ctx, `SELECT `+asyncCronColumns+` FROM cron_schedules c WHERE c.function_id=? AND c.name=?`, c.FunctionID, c.Name))
		if err == nil {
			if existing.OriginKind != "function" || existing.CreatorID != c.CreatorID || existing.AuthorityState != "active" ||
				existing.AdmissionCallerRevision != revs.Creator || existing.AdmissionTargetRevision != revs.Target {
				return ErrAsyncConflict
			}
			c.ID = existing.ID
			c.ConfigRevision = existing.ConfigRevision
			c.AuthorityGeneration = existing.AuthorityGeneration
			c.AsyncProvenance = existing.AsyncProvenance
			c.CreatedAt = existing.CreatedAt
			fresh = false
		} else if err != sql.ErrNoRows {
			return err
		}
		if fresh {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM cron_schedules WHERE function_id=? AND name<>''`, c.FunctionID).Scan(&n); err != nil {
				return err
			}
			if n >= 25 {
				return ErrAsyncConflict
			}
		}
	}
	if action != "upsert" && ref == nil {
		return ErrOperatorDenied
	}
	if fresh {
		if action == "create" {
			c.OriginKind = "operator"
			c.CreatorID = ""
			c.OperatorKind = actor.Kind
			c.OperatorID = actor.ID
		}
		c.ID = NewCronID()
		c.AuthorityState = "active"
		c.AuthorityReason = ""
		c.AuthorityGeneration = 1
		c.ConfigRevision = 1
		c.CreationCallerRevision = revs.Creator
		c.CreationTargetRevision = revs.Target
		c.AdmissionCallerRevision = revs.Creator
		c.AdmissionTargetRevision = revs.Target
		c.CreatedAt = time.Now().UTC()
		c.UpdatedAt = c.CreatedAt
		_, err = tx.ExecContext(ctx, `INSERT INTO cron_schedules(id,function_id,name,cron_expr,timezone,enabled,payload,next_run_at,created_at,updated_at,
		 authority_state,authority_reason,origin_kind,creator_id,operator_kind,operator_id,creation_caller_revision,creation_target_revision,
		 authority_generation,admission_caller_revision,admission_target_revision,config_revision) VALUES(?,?,?,?,?,?,?,?,?,?,'active','',?,?,?,?,?,?,1,?,?,1)`,
			c.ID, c.FunctionID, c.Name, c.CronExpr, c.Timezone, boolToInt(c.Enabled), c.Payload, nullTime(c.NextRunAt), c.CreatedAt, c.UpdatedAt,
			c.OriginKind, c.CreatorID, c.OperatorKind, c.OperatorID, revs.Creator, revs.Target, revs.Creator, revs.Target)
	} else {
		if c.ConfigRevision >= math.MaxInt64 || c.AuthorityGeneration >= math.MaxInt64 {
			return ErrAsyncConflict
		}
		where := ` WHERE id=? AND function_id=? AND config_revision=? AND authority_generation=?`
		args := []any{c.ID, c.FunctionID, c.ConfigRevision, c.AuthorityGeneration}
		var res sql.Result
		switch action {
		case "delete":
			res, err = tx.ExecContext(ctx, `DELETE FROM cron_schedules`+where, args...)
		case "resume":
			res, err = tx.ExecContext(ctx, `UPDATE cron_schedules SET authority_state='active',authority_reason='',enabled=1,
			 admission_caller_revision=?,admission_target_revision=?,authority_generation=authority_generation+1,
			 config_revision=config_revision+1,next_run_at=?,updated_at=?`+where+` AND authority_state='suspended' AND origin_kind<>'legacy_unknown'`,
				append([]any{revs.Creator, revs.Target, nullTime(c.NextRunAt), time.Now().UTC()}, args...)...)
		case "update", "upsert":
			res, err = tx.ExecContext(ctx, `UPDATE cron_schedules SET cron_expr=?,timezone=?,enabled=?,payload=?,next_run_at=?,updated_at=?,
			 config_revision=config_revision+1`+where+` AND authority_state='active' AND origin_kind<>'legacy_unknown'`,
				append([]any{c.CronExpr, c.Timezone, boolToInt(c.Enabled), c.Payload, nullTime(c.NextRunAt), time.Now().UTC()}, args...)...)
		case "disable":
			res, err = tx.ExecContext(ctx, `UPDATE cron_schedules SET enabled=0,next_run_at=NULL,config_revision=config_revision+1,updated_at=?`+where,
				append([]any{time.Now().UTC()}, args...)...)
		default:
			return ErrAsyncConflict
		}
		if err = asyncChanged(res, err); err != nil {
			return err
		}
		c.ConfigRevision++
	}
	if err != nil {
		return err
	}
	if ref != nil {
		if err := cronAuditTx(ctx, tx, c, action, actor); err != nil {
			return err
		}
	}
	return asyncCommit(ctx, tx, expiry)
}

func (db *Database) SuspendAsyncCron(ctx context.Context, c *AsyncCron, reason string) error {
	if reason != "policy_denied" && reason != "revoked" && reason != "creator_missing" && reason != "target_missing" {
		return ErrAsyncConflict
	}
	res, err := db.write.ExecContext(ctx, `UPDATE cron_schedules SET authority_state='suspended',authority_reason=?,
	 authority_generation=authority_generation+1,next_run_at=NULL WHERE id=? AND config_revision=? AND authority_generation=? AND authority_state='active'`,
		reason, c.ID, c.ConfigRevision, c.AuthorityGeneration)
	return asyncChanged(res, err)
}
func (db *Database) AdvanceAsyncCron(ctx context.Context, c *AsyncCron, now, next time.Time) error {
	res, err := db.write.ExecContext(ctx, `UPDATE cron_schedules SET next_run_at=? WHERE id=? AND config_revision=? AND authority_generation=?
	 AND enabled=1 AND authority_state='active' AND next_run_at<=?`, next.UTC(), c.ID, c.ConfigRevision, c.AuthorityGeneration, now.UTC())
	if err := asyncChanged(res, err); err != nil {
		return err
	}
	c.NextRunAt = &next
	return nil
}
func (db *Database) AdmitAsyncCron(ctx context.Context, c *AsyncCron, at time.Time) error {
	res, err := db.write.ExecContext(ctx, `UPDATE cron_schedules SET last_run_at=? WHERE id=? AND config_revision=? AND authority_generation=?
	 AND enabled=1 AND authority_state='active' AND next_run_at=?
	 AND EXISTS(SELECT 1 FROM functions f WHERE f.id=cron_schedules.function_id AND f.status<>'deleting')
	 AND (origin_kind<>'function' OR EXISTS(SELECT 1 FROM functions f WHERE f.id=cron_schedules.creator_id AND f.status<>'deleting'))`,
		at.UTC(), c.ID, c.ConfigRevision, c.AuthorityGeneration, nullTime(c.NextRunAt))
	return asyncChanged(res, err)
}
func (db *Database) FinishAsyncCron(ctx context.Context, c *AsyncCron, at time.Time, status, msg string) error {
	res, err := db.write.ExecContext(ctx, `UPDATE cron_schedules SET last_status=?,last_error=?,updated_at=? WHERE id=? AND config_revision=?
	 AND authority_generation=? AND last_run_at=?`, status, msg, time.Now().UTC(), c.ID, c.ConfigRevision, c.AuthorityGeneration, at.UTC())
	return asyncChanged(res, err)
}
