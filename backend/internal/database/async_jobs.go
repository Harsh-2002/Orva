package database

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

var ErrAsyncConflict = errors.New("async work state conflict")

func (db *Database) CanManageAsync(ctx context.Context, ref OperatorCredentialReference) bool {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	_, _, err = authorizeOperatorTx(ctx, tx, ref)
	return err == nil
}

type AsyncProvenance struct {
	AuthorityState          string `json:"authority_state"`
	AuthorityReason         string `json:"authority_reason"`
	OriginKind              string `json:"origin_kind"`
	CreatorID               string `json:"creator_id,omitempty"`
	OperatorKind            string `json:"operator_kind,omitempty"`
	OperatorID              string `json:"operator_id,omitempty"`
	CreationCallerRevision  uint64 `json:"creation_caller_revision,string"`
	CreationTargetRevision  uint64 `json:"creation_target_revision,string"`
	AuthorityGeneration     uint64 `json:"authority_generation,string"`
	AdmissionCallerRevision uint64 `json:"admission_caller_revision,string"`
	AdmissionTargetRevision uint64 `json:"admission_target_revision,string"`
}

type AsyncJob struct {
	Job
	AsyncProvenance
}

func (j *AsyncJob) Origin() workloadiam.AsyncOrigin {
	return workloadiam.AsyncOrigin{Kind: j.OriginKind, CreatorID: j.CreatorID, OperatorKind: j.OperatorKind,
		OperatorID: j.OperatorID, TargetID: j.FunctionID, Trigger: workloadiam.Job,
		ExpectedCreatorRevision: j.AdmissionCallerRevision, ExpectedTargetRevision: j.AdmissionTargetRevision}
}

const asyncJobColumns = `j.id,j.function_id,j.payload,j.status,j.scheduled_at,j.started_at,j.finished_at,
 j.attempts,j.max_attempts,COALESCE(j.last_error,''),j.created_at,
 COALESCE(j.trace_id,''),COALESCE(j.parent_span_id,''),COALESCE(j.enqueued_by_function_id,''),
 j.authority_state,j.authority_reason,j.origin_kind,j.creator_id,j.operator_kind,j.operator_id,
 j.creation_caller_revision,j.creation_target_revision,j.authority_generation,j.admission_caller_revision,j.admission_target_revision,
 COALESCE(NULLIF(j.async_idempotency_key,''),j.idempotency_key,''),
 COALESCE(j.idempotency_window_seconds,0)`

func scanAsyncJob(row interface{ Scan(...any) error }) (*AsyncJob, error) {
	var j AsyncJob
	var start, finish sql.NullTime
	err := row.Scan(&j.ID, &j.FunctionID, &j.Payload, &j.Status, &j.ScheduledAt, &start, &finish,
		&j.Attempts, &j.MaxAttempts, &j.LastError, &j.CreatedAt, &j.TraceID, &j.ParentSpanID, &j.EnqueuedByFunctionID,
		&j.AuthorityState, &j.AuthorityReason, &j.OriginKind, &j.CreatorID, &j.OperatorKind, &j.OperatorID,
		&j.CreationCallerRevision, &j.CreationTargetRevision, &j.AuthorityGeneration, &j.AdmissionCallerRevision, &j.AdmissionTargetRevision, &j.IdempotencyKey, &j.IdempotencyWindowSeconds)
	if start.Valid {
		j.StartedAt = &start.Time
	}
	if finish.Valid {
		j.FinishedAt = &finish.Time
	}
	return &j, err
}

func (db *Database) GetAsyncJob(ctx context.Context, id string) (*AsyncJob, error) {
	return scanAsyncJob(db.read.QueryRowContext(ctx, `SELECT `+asyncJobColumns+` FROM jobs j WHERE j.id=?`, id))
}

func (db *Database) ListAsyncJobs(ctx context.Context, status, fn string, limit int, due *time.Time) ([]*AsyncJob, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := `SELECT ` + asyncJobColumns + ` FROM jobs j WHERE 1=1`
	var args []any
	if status != "" {
		q += ` AND j.status=?`
		args = append(args, status)
	}
	if fn != "" {
		q += ` AND j.function_id=?`
		args = append(args, fn)
	}
	if due != nil {
		q += ` AND j.authority_state='active' AND j.scheduled_at<=?`
		args = append(args, due.UTC())
	}
	q += ` ORDER BY j.scheduled_at,j.id LIMIT ?`
	args = append(args, limit)
	rows, err := db.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AsyncJob, 0)
	for rows.Next() {
		j, err := scanAsyncJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// EnqueueAsyncJob is called under SDK admission or trusted operator service
// admission. Operator provenance is derived again from a live DB credential.
func (db *Database) EnqueueAsyncJob(ctx context.Context, j *AsyncJob, ref *OperatorCredentialReference) error {
	if j == nil || !validIAMUUID(j.FunctionID) || len(j.Payload) > 1<<20 ||
		len(j.IdempotencyKey) > 256 || !utf8.ValidString(j.IdempotencyKey) ||
		j.MaxAttempts > 100 || j.IdempotencyWindowSeconds > 604800 {
		return ErrAsyncConflict
	}
	if j.ID == "" {
		j.ID = NewJobID()
	}
	if !validIAMUUID(j.ID) {
		return ErrAsyncConflict
	}
	j.Status = "pending"
	j.Attempts = 0
	j.AuthorityState = "active"
	j.AuthorityReason = ""
	j.AuthorityGeneration = 1
	now := time.Now().UTC()
	j.CreatedAt = now
	if j.ScheduledAt.IsZero() {
		j.ScheduledAt = now
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 3
	}
	if j.IdempotencyWindowSeconds <= 0 {
		j.IdempotencyWindowSeconds = 86400
	}
	if len(j.Payload) == 0 {
		j.Payload = []byte("{}")
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var expiry time.Time
	if ref != nil {
		actor, until, err := authorizeOperatorTx(ctx, tx, *ref)
		if err != nil {
			return err
		}
		expiry = until
		j.OriginKind = "operator"
		j.CreatorID = ""
		j.EnqueuedByFunctionID = ""
		j.OperatorKind = actor.Kind
		j.OperatorID = actor.ID
		j.CreationCallerRevision = 0
	} else if j.OriginKind != "function" || !validIAMUUID(j.CreatorID) || j.OperatorID != "" || j.OperatorKind != "" ||
		j.CreationCallerRevision == 0 || j.CreationCallerRevision > math.MaxInt64 {
		return ErrAsyncConflict
	}
	if j.CreationTargetRevision == 0 || j.CreationTargetRevision > math.MaxInt64 {
		return ErrAsyncConflict
	}
	if err := iamFunctionExists(ctx, tx, j.FunctionID); err != nil {
		return err
	}
	identity := j.CreatorID
	if j.OriginKind == "operator" {
		identity = j.OperatorKind + ":" + j.OperatorID
	} else {
		if err := iamFunctionExists(ctx, tx, j.CreatorID); err != nil {
			return err
		}
		j.EnqueuedByFunctionID = j.CreatorID
	}
	if j.IdempotencyKey != "" {
		var hit string
		err := tx.QueryRowContext(ctx, `SELECT job_id FROM job_idempotency_v2 WHERE origin_kind=? AND creator_identity=? AND target_id=? AND raw_key=? AND expires_at>?`,
			j.OriginKind, identity, j.FunctionID, j.IdempotencyKey, now).Scan(&hit)
		if err == nil {
			existing, err := scanAsyncJob(tx.QueryRowContext(ctx, `SELECT `+asyncJobColumns+` FROM jobs j WHERE j.id=?`, hit))
			if err != nil {
				return err
			}
			if err := asyncCommit(ctx, tx, expiry); err != nil {
				return err
			}
			*j = *existing
			j.Replayed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM job_idempotency_v2 WHERE origin_kind=? AND creator_identity=? AND target_id=? AND raw_key=? AND expires_at<=?`,
			j.OriginKind, identity, j.FunctionID, j.IdempotencyKey, now)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs (id,function_id,payload,status,scheduled_at,attempts,max_attempts,created_at,
	 trace_id,parent_span_id,enqueued_by_function_id,idempotency_key,idempotency_window_seconds,
	 authority_state,authority_reason,origin_kind,creator_id,operator_kind,operator_id,
	 creation_caller_revision,creation_target_revision,authority_generation,admission_caller_revision,admission_target_revision,async_idempotency_key)
	 VALUES (?,?,?,'pending',?,0,?,?,?,?,?,NULL,?,'active','',?,?,?,?,?,?,1,?,?,?)`,
		j.ID, j.FunctionID, j.Payload, j.ScheduledAt, j.MaxAttempts, j.CreatedAt, nullableString(j.TraceID), nullableString(j.ParentSpanID),
		nullableString(j.EnqueuedByFunctionID), j.IdempotencyWindowSeconds, j.OriginKind, j.CreatorID, j.OperatorKind, j.OperatorID,
		j.CreationCallerRevision, j.CreationTargetRevision, j.CreationCallerRevision, j.CreationTargetRevision, j.IdempotencyKey)
	if err != nil {
		return err
	}
	if j.IdempotencyKey != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO job_idempotency_v2 VALUES (?,?,?,?,?,?)`, j.OriginKind, identity, j.FunctionID,
			j.IdempotencyKey, now.Add(time.Duration(j.IdempotencyWindowSeconds)*time.Second), j.ID)
		if err != nil {
			return err
		}
	}
	if ref != nil {
		if err := auditAsyncTx(ctx, tx, j, "create", IAMActorReference{Kind: j.OperatorKind, ID: j.OperatorID}); err != nil {
			return err
		}
	}
	if err := asyncCommit(ctx, tx, expiry); err != nil {
		return err
	}
	j.AdmissionCallerRevision = j.CreationCallerRevision
	j.AdmissionTargetRevision = j.CreationTargetRevision
	return nil
}

func asyncCommit(ctx context.Context, tx *sql.Tx, expiry time.Time) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !expiry.IsZero() && !expiry.After(time.Now().UTC()) {
		return ErrOperatorDenied
	}
	return tx.Commit()
}

func auditAsyncTx(ctx context.Context, tx *sql.Tx, j *AsyncJob, action string, actor IAMActorReference) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO async_work_audit(job_id,action,actor_kind,actor_id,origin_kind,creator_id,target_id,changed_at)
	 VALUES (?,?,?,?,?,?,?,?)`, j.ID, action, actor.Kind, actor.ID, j.OriginKind, j.CreatorID, j.FunctionID, time.Now().UTC())
	return err
}

// ClaimAsyncJob reserves a row without consuming an execution attempt. Final
// acquired-target admission must call StartAsyncJob before Dispatch.
func (db *Database) ClaimAsyncJob(ctx context.Context, j *AsyncJob, now time.Time) error {
	res, err := db.write.ExecContext(ctx, `UPDATE jobs SET status='running',started_at=? WHERE id=? AND status='pending'
	 AND scheduled_at<=? AND authority_state='active' AND authority_generation=?
	 AND EXISTS(SELECT 1 FROM functions f WHERE f.id=jobs.function_id AND f.status<>'deleting')
	 AND (origin_kind<>'function' OR EXISTS(SELECT 1 FROM functions f WHERE f.id=jobs.creator_id AND f.status<>'deleting'))`,
		now.UTC(), j.ID, now.UTC(), j.AuthorityGeneration)
	return asyncChanged(res, err)
}
func asyncChanged(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrAsyncConflict
	}
	return nil
}
func (db *Database) StartAsyncJob(ctx context.Context, j *AsyncJob) error {
	res, err := db.write.ExecContext(ctx, `UPDATE jobs SET attempts=attempts+1 WHERE id=? AND status='running'
	 AND authority_state='active' AND authority_generation=? AND attempts=? AND attempts<max_attempts
	 AND EXISTS(SELECT 1 FROM functions f WHERE f.id=jobs.function_id AND f.status<>'deleting')
	 AND (origin_kind<>'function' OR EXISTS(SELECT 1 FROM functions f WHERE f.id=jobs.creator_id AND f.status<>'deleting'))`,
		j.ID, j.AuthorityGeneration, j.Attempts)
	if err := asyncChanged(res, err); err != nil {
		return err
	}
	j.Attempts++
	return nil
}
func (db *Database) DeferAsyncJob(ctx context.Context, j *AsyncJob) error {
	res, err := db.write.ExecContext(ctx, `UPDATE jobs SET status='pending',started_at=NULL WHERE id=? AND status='running'
	 AND authority_generation=? AND attempts=?`, j.ID, j.AuthorityGeneration, j.Attempts)
	return asyncChanged(res, err)
}
func (db *Database) SuspendAsyncJob(ctx context.Context, j *AsyncJob, reason string) error {
	if reason != "policy_denied" && reason != "creator_missing" && reason != "target_missing" && reason != "revoked" {
		return ErrAsyncConflict
	}
	res, err := db.write.ExecContext(ctx, `UPDATE jobs SET authority_state='suspended',authority_reason=?,
	 authority_generation=authority_generation+1,status='pending',started_at=NULL
	 WHERE id=? AND status IN ('pending','running') AND authority_state='active' AND authority_generation=?`,
		reason, j.ID, j.AuthorityGeneration)
	return asyncChanged(res, err)
}

// MutateAsyncJobAsOperator preserves provenance for retry/resume. Only the
// management service may revalidate current standing grants before calling.
func (db *Database) MutateAsyncJobAsOperator(ctx context.Context, ref OperatorCredentialReference, j *AsyncJob, action string, revs workloadiam.AsyncRevisions) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actor, expiry, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return err
	}
	if action == "resume" || action == "retry" {
		if revs.Target == 0 || revs.Target > math.MaxInt64 || (j.OriginKind == "function" && (revs.Creator == 0 || revs.Creator > math.MaxInt64)) {
			return ErrAsyncConflict
		}
		if err := iamFunctionExists(ctx, tx, j.FunctionID); err != nil {
			return err
		}
		if j.OriginKind == "function" {
			if err := iamFunctionExists(ctx, tx, j.CreatorID); err != nil {
				return err
			}
		}
	}
	var res sql.Result
	switch action {
	case "resume":
		res, err = tx.ExecContext(ctx, `UPDATE jobs SET authority_state='active',authority_reason='',authority_generation=authority_generation+1,
		 admission_caller_revision=?,admission_target_revision=?
		 WHERE id=? AND status='pending' AND authority_state='suspended' AND origin_kind<>'legacy_unknown' AND authority_generation=?`, revs.Creator, revs.Target, j.ID, j.AuthorityGeneration)
	case "retry":
		res, err = tx.ExecContext(ctx, `UPDATE jobs SET status='pending',attempts=0,started_at=NULL,finished_at=NULL,last_error=NULL,scheduled_at=?,
		 authority_generation=authority_generation+1,admission_caller_revision=?,admission_target_revision=?
		 WHERE id=? AND status='failed' AND authority_state='active'
		 AND origin_kind<>'legacy_unknown' AND authority_generation=?`, time.Now().UTC(), revs.Creator, revs.Target, j.ID, j.AuthorityGeneration)
	case "delete":
		res, err = tx.ExecContext(ctx, `DELETE FROM jobs WHERE id=? AND authority_generation=?`, j.ID, j.AuthorityGeneration)
	default:
		return ErrAsyncConflict
	}
	if err := asyncChanged(res, err); err != nil {
		return err
	}
	if err := auditAsyncTx(ctx, tx, j, action, actor); err != nil {
		return err
	}
	return asyncCommit(ctx, tx, expiry)
}
