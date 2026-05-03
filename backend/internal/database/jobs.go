package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// Job is a queued background invocation. Status transitions:
//
//	pending → running → succeeded | failed (terminal)
//
// A failed run with attempts < max_attempts is reset to pending with
// scheduled_at advanced by the worker's exponential backoff.
type Job struct {
	ID           string     `json:"id"`
	FunctionID   string     `json:"function_id"`
	Payload      []byte     `json:"payload"`
	Status       string     `json:"status"`
	ScheduledAt  time.Time  `json:"scheduled_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Attempts     int        `json:"attempts"`
	MaxAttempts  int        `json:"max_attempts"`
	LastError    string     `json:"last_error,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`

	// FunctionName is filled by the LIST endpoint via JOIN — not stored
	// on the row itself. Empty if the join couldn't find a match (deleted
	// function with FK still cascading).
	FunctionName string `json:"function_name,omitempty"`

	// v0.5 tracing: jobs preserve the trace + parent span of whatever
	// enqueued them so the eventual execution lands in the same trace
	// as the caller. EnqueuedByFunctionID is denormalised so list views
	// can show "enqueued by send-summary" without a join.
	TraceID                 string `json:"trace_id,omitempty"`
	ParentSpanID            string `json:"parent_span_id,omitempty"`
	EnqueuedByFunctionID    string `json:"enqueued_by_function_id,omitempty"`
}

// NewJobID returns a fresh job id (job_<12-hex>).
func NewJobID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "job_" + hex.EncodeToString(b[:])
}

// EnqueueJob inserts a pending job. scheduledAt zero defaults to "now".
func (db *Database) EnqueueJob(j *Job) error {
	if j.ID == "" {
		j.ID = NewJobID()
	}
	if j.Status == "" {
		j.Status = "pending"
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 3
	}
	if j.ScheduledAt.IsZero() {
		j.ScheduledAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	j.CreatedAt = now
	_, err := db.write.Exec(`
		INSERT INTO jobs (id, function_id, payload, status, scheduled_at,
		                  attempts, max_attempts, last_error, created_at,
		                  trace_id, parent_span_id, enqueued_by_function_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.FunctionID, j.Payload, j.Status, j.ScheduledAt,
		j.Attempts, j.MaxAttempts, j.LastError, j.CreatedAt,
		nullableString(j.TraceID), nullableString(j.ParentSpanID),
		nullableString(j.EnqueuedByFunctionID))
	return err
}

// GetJob returns a single job by id (without function_name JOIN).
func (db *Database) GetJob(id string) (*Job, error) {
	var j Job
	var started, finished sql.NullTime
	var lastErr, traceID, parentSpanID, enqBy sql.NullString
	err := db.read.QueryRow(`
		SELECT id, function_id, payload, status, scheduled_at, started_at, finished_at,
		       attempts, max_attempts, last_error, created_at,
		       trace_id, parent_span_id, enqueued_by_function_id
		FROM jobs WHERE id = ?`, id,
	).Scan(&j.ID, &j.FunctionID, &j.Payload, &j.Status, &j.ScheduledAt,
		&started, &finished, &j.Attempts, &j.MaxAttempts, &lastErr, &j.CreatedAt,
		&traceID, &parentSpanID, &enqBy)
	if err != nil {
		return nil, err
	}
	j.TraceID = traceID.String
	j.ParentSpanID = parentSpanID.String
	j.EnqueuedByFunctionID = enqBy.String
	if started.Valid {
		t := started.Time
		j.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time
		j.FinishedAt = &t
	}
	j.LastError = lastErr.String
	return &j, nil
}

// ListJobs returns recent jobs joined with function_name. Optional
// status / function filters; default limit 50.
func (db *Database) ListJobs(status, functionID string, limit int) ([]*Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `
		SELECT j.id, j.function_id, j.payload, j.status, j.scheduled_at,
		       j.started_at, j.finished_at, j.attempts, j.max_attempts,
		       j.last_error, j.created_at, COALESCE(f.name, ''),
		       j.trace_id, j.parent_span_id, j.enqueued_by_function_id
		FROM jobs j
		LEFT JOIN functions f ON f.id = j.function_id
		WHERE 1=1`
	args := []any{}
	if status != "" {
		q += " AND j.status = ?"
		args = append(args, status)
	}
	if functionID != "" {
		q += " AND j.function_id = ?"
		args = append(args, functionID)
	}
	q += " ORDER BY j.created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.read.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Job
	for rows.Next() {
		var j Job
		var started, finished sql.NullTime
		var lastErr, traceID, parentSpanID, enqBy sql.NullString
		if err := rows.Scan(&j.ID, &j.FunctionID, &j.Payload, &j.Status, &j.ScheduledAt,
			&started, &finished, &j.Attempts, &j.MaxAttempts, &lastErr, &j.CreatedAt, &j.FunctionName,
			&traceID, &parentSpanID, &enqBy); err != nil {
			return nil, err
		}
		if started.Valid {
			t := started.Time
			j.StartedAt = &t
		}
		if finished.Valid {
			t := finished.Time
			j.FinishedAt = &t
		}
		j.LastError = lastErr.String
		j.TraceID = traceID.String
		j.ParentSpanID = parentSpanID.String
		j.EnqueuedByFunctionID = enqBy.String
		out = append(out, &j)
	}
	return out, rows.Err()
}

// ClaimDueJobs atomically marks up to `limit` pending jobs as running
// and returns them. Uses a single UPDATE...RETURNING so two scheduler
// ticks racing don't claim the same row.
func (db *Database) ClaimDueJobs(now time.Time, limit int) ([]*Job, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.write.Query(`
		UPDATE jobs
		SET status = 'running',
		    started_at = ?,
		    attempts = attempts + 1
		WHERE id IN (
			SELECT id FROM jobs
			WHERE status = 'pending' AND scheduled_at <= ?
			ORDER BY scheduled_at ASC LIMIT ?
		)
		RETURNING id, function_id, payload, status, scheduled_at, started_at, finished_at,
		          attempts, max_attempts, COALESCE(last_error, ''), created_at,
		          COALESCE(trace_id, ''), COALESCE(parent_span_id, ''),
		          COALESCE(enqueued_by_function_id, '')`,
		now.UTC(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Job
	for rows.Next() {
		var j Job
		var started, finished sql.NullTime
		if err := rows.Scan(&j.ID, &j.FunctionID, &j.Payload, &j.Status, &j.ScheduledAt,
			&started, &finished, &j.Attempts, &j.MaxAttempts, &j.LastError, &j.CreatedAt,
			&j.TraceID, &j.ParentSpanID, &j.EnqueuedByFunctionID); err != nil {
			return nil, err
		}
		if started.Valid {
			t := started.Time
			j.StartedAt = &t
		}
		if finished.Valid {
			t := finished.Time
			j.FinishedAt = &t
		}
		out = append(out, &j)
	}
	return out, rows.Err()
}

// MarkJobSuccess records a successful completion. Idempotent — calling
// twice for the same id keeps the row at 'succeeded'.
func (db *Database) MarkJobSuccess(id string) error {
	_, err := db.write.Exec(`
		UPDATE jobs SET status = 'succeeded', finished_at = ?, last_error = NULL
		WHERE id = ?`, time.Now().UTC(), id)
	return err
}

// MarkJobFailure either retries (status=pending with backoff) or marks
// the job permanently failed depending on attempts vs max_attempts.
func (db *Database) MarkJobFailure(id string, errMsg string, attempts, maxAttempts int) error {
	now := time.Now().UTC()
	if attempts >= maxAttempts {
		_, err := db.write.Exec(`
			UPDATE jobs SET status = 'failed', finished_at = ?, last_error = ?
			WHERE id = ?`, now, errMsg, id)
		return err
	}
	// Exponential backoff capped at 1h. attempt=1 → 2s, 2 → 4s, 3 → 8s, ...
	delaySec := 1 << attempts
	if delaySec > 3600 {
		delaySec = 3600
	}
	next := now.Add(time.Duration(delaySec) * time.Second)
	_, err := db.write.Exec(`
		UPDATE jobs SET status = 'pending', started_at = NULL, last_error = ?, scheduled_at = ?
		WHERE id = ?`, errMsg, next, id)
	return err
}

// RetryJob resets a terminal job back to pending so it runs on the next
// scheduler tick. Used by the dashboard's "Retry" button.
func (db *Database) RetryJob(id string) error {
	_, err := db.write.Exec(`
		UPDATE jobs SET status = 'pending', scheduled_at = ?, attempts = 0,
		                started_at = NULL, finished_at = NULL, last_error = NULL
		WHERE id = ?`, time.Now().UTC(), id)
	return err
}

// DeleteJob removes a single job row.
func (db *Database) DeleteJob(id string) error {
	_, err := db.write.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	return err
}
