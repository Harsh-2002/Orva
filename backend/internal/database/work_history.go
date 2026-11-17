package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/internal/ids"
)

const (
	HistoryMaxRows    = 100000
	HistoryMaxBytes   = 64 << 20
	HistoryEntryBytes = 8192
	HistoryBatch      = 64
	HistoryRetention  = 90 * 24 * time.Hour
)

var (
	ErrHistoryCapacity = errors.New("work history capacity exhausted")
	ErrHistoryRequired = errors.New("work history deletion preparation required")
	ErrHistoryInput    = errors.New("invalid work history request")
)

// WorkHistory contains bounded metadata only. It is never a dispatch input.
type WorkHistory struct {
	Kind             string          `json:"kind"`
	OriginalID       string          `json:"original_id"`
	TargetID         string          `json:"target_id"`
	DeletionID       string          `json:"deletion_id"`
	Origin           AsyncProvenance `json:"origin"`
	Status           string          `json:"status"`
	Reason           string          `json:"reason"`
	CreatedAt        time.Time       `json:"created_at"`
	ScheduledAt      *time.Time      `json:"scheduled_at,omitempty"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	Attempts         int             `json:"attempts,omitempty"`
	MaxAttempts      int             `json:"max_attempts,omitempty"`
	Name             string          `json:"name,omitempty"`
	Expression       string          `json:"expression,omitempty"`
	Timezone         string          `json:"timezone,omitempty"`
	Enabled          bool            `json:"enabled"`
	NextRunAt        *time.Time      `json:"next_run_at,omitempty"`
	LastRunAt        *time.Time      `json:"last_run_at,omitempty"`
	UpdatedAt        *time.Time      `json:"updated_at,omitempty"`
	ConfigRevision   uint64          `json:"config_revision,string"`
	PayloadBytes     int64           `json:"payload_bytes"`
	ArchivedAt       time.Time       `json:"archived_at"`
	EligibleAt       time.Time       `json:"eligible_at"`
	Executable       bool            `json:"executable"`
	PayloadAvailable bool            `json:"payload_available"`
}

type HistoryEntry struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Metadata json.RawMessage `json:"metadata"`
	Reserved bool            `json:"reserved"`
}

type historyDeletionMetadata struct {
	Operation      string                  `json:"operation"`
	OwnerID        string                  `json:"owner_id"`
	Actor          IAMActorReference       `json:"actor"`
	At             time.Time               `json:"at"`
	ManagedSources []IAMOwnerDeletionAudit `json:"managed_sources,omitempty"`
}

// InitHistorySchemaForFixture is explicit opt-in, never a boot migration.
func (db *Database) InitHistorySchemaForFixture(ctx context.Context) error {
	_, err := db.write.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS work_history (
 kind TEXT NOT NULL, original_id TEXT NOT NULL, target_id TEXT NOT NULL,
 deletion_id TEXT NOT NULL, metadata TEXT NOT NULL,
 metadata_bytes INTEGER NOT NULL CHECK(metadata_bytes=length(CAST(metadata AS BLOB)) AND metadata_bytes BETWEEN 1 AND 8192),
 reserved INTEGER NOT NULL CHECK(reserved IN (0,1)), eligible_at DATETIME NOT NULL,
 PRIMARY KEY(kind,original_id)
);
CREATE INDEX IF NOT EXISTS work_history_target ON work_history(target_id,original_id);
CREATE TABLE IF NOT EXISTS work_history_capacity(
 id INTEGER PRIMARY KEY CHECK(id=1),
 rows_used INTEGER NOT NULL CHECK(rows_used BETWEEN 0 AND 100000),
 bytes_used INTEGER NOT NULL CHECK(bytes_used BETWEEN 0 AND 67108864),
 rows_reserved INTEGER NOT NULL CHECK(rows_reserved BETWEEN 0 AND rows_used),
 bytes_reserved INTEGER NOT NULL CHECK(bytes_reserved BETWEEN 0 AND bytes_used)
);
INSERT OR IGNORE INTO work_history_capacity VALUES(1,0,0,0,0);
CREATE TABLE IF NOT EXISTS work_history_deletions(
 owner_id TEXT PRIMARY KEY, deletion_id TEXT NOT NULL UNIQUE,
 actor_kind TEXT NOT NULL, actor_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('reserved','ready','complete'))
);
CREATE TRIGGER IF NOT EXISTS work_history_no_job_reuse BEFORE INSERT ON jobs
 WHEN EXISTS(SELECT 1 FROM work_history WHERE kind='job' AND original_id=NEW.id)
 BEGIN SELECT RAISE(ABORT,'archived job ID'); END;
CREATE TRIGGER IF NOT EXISTS work_history_no_cron_reuse BEFORE INSERT ON cron_schedules
 WHEN EXISTS(SELECT 1 FROM work_history WHERE kind='cron' AND original_id=NEW.id)
 BEGIN SELECT RAISE(ABORT,'archived schedule ID'); END;`)
	if err == nil {
		db.historyEnabled.Store(true)
	}
	return err
}

func historyJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 || len(b) > HistoryEntryBytes || !utf8.Valid(b) {
		return nil, ErrHistoryInput
	}
	return b, nil
}

func historyReserve(ctx context.Context, tx *sql.Tx, rows, bytes int64, reserved bool) error {
	var res sql.Result
	var err error
	if reserved {
		res, err = tx.ExecContext(ctx, `UPDATE work_history_capacity SET rows_used=rows_used+?,bytes_used=bytes_used+?,
 rows_reserved=rows_reserved+?,bytes_reserved=bytes_reserved+?
 WHERE id=1 AND rows_used+?<=? AND bytes_used+?<=?`, rows, bytes, rows, bytes, rows, HistoryMaxRows, bytes, HistoryMaxBytes)
	} else {
		res, err = tx.ExecContext(ctx, `UPDATE work_history_capacity SET rows_used=rows_used+?,bytes_used=bytes_used+?
 WHERE id=1 AND rows_used+?<=? AND bytes_used+?<=?`, rows, bytes, rows, HistoryMaxRows, bytes, HistoryMaxBytes)
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrHistoryCapacity
	}
	return err
}

func historyPut(ctx context.Context, tx *sql.Tx, kind, id, target, deletion string, raw []byte, reserved bool, eligible time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO work_history VALUES(?,?,?,?,?,?,?,?)`,
		kind, id, target, deletion, string(raw), len(raw), boolToInt(reserved), eligible)
	return err
}

// ReserveHistoryDeletion captures exact canonical metadata and reserves its full
// capacity before closing admission. No payload, error text or idempotency key is read.
func (db *Database) ReserveHistoryDeletion(ctx context.Context, ref OperatorCredentialReference, owner string) error {
	return db.ReserveHistoryDeletionWithManaged(ctx, ref, owner, nil)
}

func (db *Database) ReserveHistoryDeletionWithManaged(ctx context.Context, ref OperatorCredentialReference, owner string, plan *IAMOwnerDeletionPlan) error {
	if !db.historyEnabled.Load() || !validIAMUUID(owner) {
		return ErrHistoryRequired
	}
	db.lifecycleMu.Lock()
	defer db.lifecycleMu.Unlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actor, _, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return err
	}
	var existing, deletionID, actorKind, actorID string
	err = tx.QueryRowContext(ctx, `SELECT state,deletion_id,actor_kind,actor_id FROM work_history_deletions WHERE owner_id=?`, owner).Scan(&existing, &deletionID, &actorKind, &actorID)
	if err == nil {
		if _, err = validateOwnerDeletion(ctx, tx, plan, owner, actor, deletionID); err != nil {
			return err
		}
		var raw, target string
		if err = tx.QueryRowContext(ctx, `SELECT metadata,target_id FROM work_history WHERE kind='delete' AND original_id=? AND deletion_id=?`,
			deletionID, deletionID).Scan(&raw, &target); err != nil {
			return err
		}
		var root historyDeletionMetadata
		if json.Unmarshal([]byte(raw), &root) != nil {
			return ErrAsyncConflict
		}
		canonical, e := historyJSON(root)
		if e != nil || string(canonical) != raw || target != owner || root.OwnerID != owner || root.Operation != "delete" ||
			root.Actor.Kind != actorKind || root.Actor.ID != actorID || !validIAMUUID(deletionID) {
			return ErrAsyncConflict
		}
		var status string
		e = tx.QueryRowContext(ctx, `SELECT status FROM functions WHERE id=?`, owner).Scan(&status)
		if (e == nil && status != FunctionStatusDeleting) || (e != nil && existing != "complete") {
			return ErrAsyncConflict
		}
		if _, _, err = authorizeOperatorTx(ctx, tx, ref); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	managedAudits, err := validateOwnerDeletion(ctx, tx, plan, owner, actor, "")
	if err != nil {
		return err
	}
	if err = iamFunctionExists(ctx, tx, owner); err != nil {
		return err
	}
	var count int64
	err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM
 (SELECT 1 FROM jobs WHERE function_id=? AND status IN ('pending','running') LIMIT 100001))+
 (SELECT count(*) FROM (SELECT 1 FROM cron_schedules WHERE function_id=? LIMIT 100001))`, owner, owner).Scan(&count)
	if err != nil {
		return err
	}
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT rows_used FROM work_history_capacity WHERE id=1`).Scan(&used); err != nil {
		return err
	}
	if count+1 > HistoryMaxRows-used {
		return ErrHistoryCapacity
	}
	now := time.Now().UTC()
	deletion := ids.New()
	items, err := captureHistory(ctx, tx, owner, deletion, now)
	if err != nil {
		return err
	}
	if int64(len(items)) != count {
		return ErrAsyncConflict
	}
	root, err := historyJSON(historyDeletionMetadata{"delete", owner, actor, now, managedAudits})
	if err != nil {
		return err
	}
	total := int64(len(root))
	raw := make([][]byte, len(items))
	for i, item := range items {
		raw[i], err = historyJSON(item)
		if err != nil {
			return err
		}
		total += int64(len(raw[i]))
	}
	if err = historyReserve(ctx, tx, count+1, total, true); err != nil {
		return err
	}
	if err = historyPut(ctx, tx, "delete", deletion, owner, deletion, root, true, now.Add(HistoryRetention)); err != nil {
		return err
	}
	for i, item := range items {
		if err = historyPut(ctx, tx, item.Kind, item.OriginalID, owner, deletion, raw[i], true, item.EligibleAt); err != nil {
			return err
		}
	}
	if err = commitOwnerDeletion(ctx, tx, plan, managedAudits, actor, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO work_history_deletions VALUES(?,?,?,?,'reserved')`, owner, deletion, actor.Kind, actor.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE functions SET status='deleting' WHERE id=?`, owner)
	if err != nil {
		return err
	}
	// Creator-only work remains live but permanently retains its original UUID.
	_, err = tx.ExecContext(ctx, `UPDATE jobs SET authority_state='suspended',authority_reason='creator_missing',
 authority_generation=authority_generation+1 WHERE creator_id=? AND function_id<>? AND status IN ('pending','running')`, owner, owner)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cron_schedules SET authority_state='suspended',authority_reason='creator_missing',
 authority_generation=authority_generation+1,next_run_at=NULL WHERE creator_id=? AND function_id<>?`, owner, owner)
	if err != nil {
		return err
	}
	if _, _, err = authorizeOperatorTx(ctx, tx, ref); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	db.liveFunctions.Delete(owner)
	return nil
}

func captureHistory(ctx context.Context, tx *sql.Tx, owner, deletion string, now time.Time) ([]WorkHistory, error) {
	for _, table := range []string{"jobs", "cron_schedules"} {
		fields := `length(CAST(authority_state AS BLOB))+length(CAST(authority_reason AS BLOB))+
 length(CAST(origin_kind AS BLOB))+length(CAST(creator_id AS BLOB))+
 length(CAST(operator_kind AS BLOB))+length(CAST(operator_id AS BLOB))`
		if table == "cron_schedules" {
			fields += `+length(CAST(name AS BLOB))+length(CAST(cron_expr AS BLOB))+length(CAST(timezone AS BLOB))+length(CAST(COALESCE(last_status,'') AS BLOB))`
		}
		var bad bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE function_id=? AND (`+fields+`)>2048)`, owner).Scan(&bad); err != nil {
			return nil, err
		}
		if bad {
			return nil, ErrHistoryInput
		}
	}
	out := []WorkHistory{}
	rows, err := tx.QueryContext(ctx, `SELECT id,status,created_at,scheduled_at,started_at,finished_at,attempts,max_attempts,
 authority_state,authority_reason,origin_kind,creator_id,operator_kind,operator_id,
 creation_caller_revision,creation_target_revision,authority_generation,admission_caller_revision,admission_target_revision,
 length(CAST(payload AS BLOB)) FROM jobs WHERE function_id=? AND status IN ('pending','running') ORDER BY id`, owner)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		h := WorkHistory{Kind: "job", TargetID: owner, DeletionID: deletion, Reason: "target_deleted", ArchivedAt: now, EligibleAt: now.Add(HistoryRetention)}
		var scheduled time.Time
		var start, finish sql.NullTime
		o := &h.Origin
		err = rows.Scan(&h.OriginalID, &h.Status, &h.CreatedAt, &scheduled, &start, &finish, &h.Attempts, &h.MaxAttempts,
			&o.AuthorityState, &o.AuthorityReason, &o.OriginKind, &o.CreatorID, &o.OperatorKind, &o.OperatorID,
			&o.CreationCallerRevision, &o.CreationTargetRevision, &o.AuthorityGeneration, &o.AdmissionCallerRevision, &o.AdmissionTargetRevision, &h.PayloadBytes)
		if err != nil {
			rows.Close()
			return nil, err
		}
		h.ScheduledAt = &scheduled
		if start.Valid {
			h.StartedAt = &start.Time
		}
		if finish.Valid {
			h.FinishedAt = &finish.Time
		}
		out = append(out, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,name,cron_expr,timezone,enabled,last_run_at,next_run_at,
 COALESCE(last_status,''),created_at,updated_at,config_revision,authority_state,authority_reason,
 origin_kind,creator_id,operator_kind,operator_id,creation_caller_revision,creation_target_revision,
 authority_generation,admission_caller_revision,admission_target_revision,length(CAST(payload AS BLOB))
 FROM cron_schedules WHERE function_id=? ORDER BY id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		h := WorkHistory{Kind: "cron", TargetID: owner, DeletionID: deletion, Reason: "target_deleted", ArchivedAt: now, EligibleAt: now.Add(HistoryRetention)}
		o := &h.Origin
		var last, next sql.NullTime
		var updated time.Time
		err = rows.Scan(&h.OriginalID, &h.Name, &h.Expression, &h.Timezone, &h.Enabled, &last, &next, &h.Status,
			&h.CreatedAt, &updated, &h.ConfigRevision, &o.AuthorityState, &o.AuthorityReason, &o.OriginKind,
			&o.CreatorID, &o.OperatorKind, &o.OperatorID, &o.CreationCallerRevision, &o.CreationTargetRevision,
			&o.AuthorityGeneration, &o.AdmissionCallerRevision, &o.AdmissionTargetRevision, &h.PayloadBytes)
		if err != nil {
			return nil, err
		}
		h.UpdatedAt = &updated
		if last.Valid {
			h.LastRunAt = &last.Time
		}
		if next.Valid {
			h.NextRunAt = &next.Time
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DrainHistoryDeletion atomically archives and removes each bounded live batch.
func (db *Database) DrainHistoryDeletion(ctx context.Context, owner string) error {
	for {
		done, err := db.historyBatch(ctx, owner)
		if err != nil || done {
			return err
		}
	}
}
func (db *Database) historyBatch(ctx context.Context, owner string) (bool, error) {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var deletion, state string
	if err = tx.QueryRowContext(ctx, `SELECT deletion_id,state FROM work_history_deletions WHERE owner_id=?`, owner).Scan(&deletion, &state); err != nil {
		return false, err
	}
	if state == "complete" || state == "ready" {
		return true, tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,original_id,metadata_bytes FROM work_history WHERE deletion_id=? AND reserved=1 AND kind IN ('job','cron') ORDER BY kind,original_id LIMIT ?`, deletion, HistoryBatch)
	if err != nil {
		return false, err
	}
	type item struct {
		kind, id string
		size     int64
	}
	items := []item{}
	var total int64
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.kind, &x.id, &x.size); err != nil {
			rows.Close()
			return false, err
		}
		items = append(items, x)
		total += x.size
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, x := range items {
		table := "jobs"
		if x.kind == "cron" {
			table = "cron_schedules"
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=? AND function_id=?`, x.id, owner); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE work_history SET reserved=0 WHERE kind=? AND original_id=? AND reserved=1`, x.kind, x.id); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE work_history_capacity SET rows_reserved=rows_reserved-?,bytes_reserved=bytes_reserved-? WHERE id=1`, len(items), total); err != nil {
		return false, err
	}
	done := len(items) < HistoryBatch
	if done {
		_, err = tx.ExecContext(ctx, `UPDATE work_history_deletions SET state='ready' WHERE owner_id=?`, owner)
		if err != nil {
			return false, err
		}
	}
	return done, tx.Commit()
}

func (db *Database) historyDeleteReady(ctx context.Context, owner string) error {
	if !db.historyEnabled.Load() {
		return nil
	}
	var state string
	if db.read.QueryRowContext(ctx, `SELECT state FROM work_history_deletions WHERE owner_id=?`, owner).Scan(&state) != nil || (state != "ready" && state != "complete") {
		return ErrHistoryRequired
	}
	return nil
}
func historyFinishTx(ctx context.Context, tx *sql.Tx, owner string) error {
	var deletion string
	if err := tx.QueryRowContext(ctx, `SELECT deletion_id FROM work_history_deletions WHERE owner_id=?`, owner).Scan(&deletion); err != nil {
		return err
	}
	var size int64
	var reserved bool
	if err := tx.QueryRowContext(ctx, `SELECT metadata_bytes,reserved FROM work_history WHERE kind='delete' AND original_id=?`, deletion).Scan(&size, &reserved); err != nil {
		return err
	}
	if reserved {
		if _, err := tx.ExecContext(ctx, `UPDATE work_history SET reserved=0 WHERE kind='delete' AND original_id=?`, deletion); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE work_history_capacity SET rows_reserved=rows_reserved-1,bytes_reserved=bytes_reserved-? WHERE id=1`, size); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE work_history_deletions SET state='complete' WHERE owner_id=?`, owner)
	return err
}
