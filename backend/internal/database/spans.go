package database

import (
	"database/sql"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/trace"
)

// UserSpan is one orva.trace.span() call captured from inside a function.
// User spans are strictly additive — the parent execution row in
// `executions` still carries the canonical "this execution ran" span;
// user_spans rows describe substages inside that execution.
type UserSpan struct {
	ID            string     `json:"id"`
	TraceID       string     `json:"trace_id"`
	ParentSpanID  string     `json:"parent_span_id"`
	ExecutionID   string     `json:"execution_id"`
	Name          string     `json:"name"`
	StartedAt     time.Time  `json:"started_at"`
	DurationMS    int64      `json:"duration_ms"`
	Attributes    string     `json:"attributes,omitempty"`
	Status        string     `json:"status"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	OffsetMS      int64      `json:"offset_ms"`
}

// AsyncInsertUserSpan queues an insert of a user-defined span. The
// caller has already computed offset_ms (relative to the parent
// execution's start) so the dashboard waterfall doesn't need a join at
// render time.
func (db *Database) AsyncInsertUserSpan(s *UserSpan) {
	if s.ID == "" {
		s.ID = trace.NewSpanID()
	}
	if s.Status == "" {
		s.Status = "ok"
	}
	db.AsyncExec(`
		INSERT INTO user_spans (id, trace_id, parent_span_id, execution_id,
		                       name, started_at, duration_ms, attributes,
		                       status, error_message, offset_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TraceID, s.ParentSpanID, s.ExecutionID,
		s.Name, s.StartedAt, s.DurationMS, nullableString(s.Attributes),
		s.Status, nullableString(s.ErrorMessage), s.OffsetMS,
	)
}

// ListUserSpansByTrace returns every user span attached to any execution
// inside the given trace. Ordering is (started_at ASC, id ASC) so the
// frontend can render the waterfall deterministically when offset_ms
// collisions happen.
func (db *Database) ListUserSpansByTrace(traceID string) ([]*UserSpan, error) {
	rows, err := db.read.Query(`
		SELECT id, trace_id, parent_span_id, execution_id, name, started_at,
		       duration_ms, attributes, status, error_message, offset_ms
		FROM user_spans
		WHERE trace_id = ?
		ORDER BY started_at ASC, id ASC`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*UserSpan, 0, 16)
	for rows.Next() {
		var s UserSpan
		var attrs, errMsg sql.NullString
		if err := rows.Scan(&s.ID, &s.TraceID, &s.ParentSpanID, &s.ExecutionID,
			&s.Name, &s.StartedAt, &s.DurationMS, &attrs,
			&s.Status, &errMsg, &s.OffsetMS); err != nil {
			return nil, err
		}
		s.Attributes = attrs.String
		s.ErrorMessage = errMsg.String
		out = append(out, &s)
	}
	return out, rows.Err()
}

// ListUserSpansByExecution returns the user spans attached to a single
// execution row — used by the execution-detail panel in the dashboard.
func (db *Database) ListUserSpansByExecution(execID string) ([]*UserSpan, error) {
	rows, err := db.read.Query(`
		SELECT id, trace_id, parent_span_id, execution_id, name, started_at,
		       duration_ms, attributes, status, error_message, offset_ms
		FROM user_spans
		WHERE execution_id = ?
		ORDER BY started_at ASC, id ASC`, execID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*UserSpan, 0, 8)
	for rows.Next() {
		var s UserSpan
		var attrs, errMsg sql.NullString
		if err := rows.Scan(&s.ID, &s.TraceID, &s.ParentSpanID, &s.ExecutionID,
			&s.Name, &s.StartedAt, &s.DurationMS, &attrs,
			&s.Status, &errMsg, &s.OffsetMS); err != nil {
			return nil, err
		}
		s.Attributes = attrs.String
		s.ErrorMessage = errMsg.String
		out = append(out, &s)
	}
	return out, rows.Err()
}
