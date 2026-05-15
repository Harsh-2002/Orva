package database

import (
	"database/sql"
	"time"
)

// LogEntry is one structured log line emitted by orva.log.* inside a
// function. The runtime SDK serialises the entry as a single
// `__ORVA_LOG_JSON__{...}` line on stderr; proxy.Forward extracts it
// during stderr drain and queues an insert here.
type LogEntry struct {
	ID          int64     `json:"id"`
	ExecutionID string    `json:"execution_id"`
	TraceID     string    `json:"trace_id,omitempty"`
	SpanID      string    `json:"span_id,omitempty"`
	TS          time.Time `json:"ts"`
	Level       string    `json:"level"`
	Message     string    `json:"message"`
	Fields      string    `json:"fields,omitempty"`
}

// AsyncInsertLogEntry queues an insert into execution_log_entries. The
// async writer batches these alongside execution finalization, keeping
// log writes off the hot proxy path.
func (db *Database) AsyncInsertLogEntry(e *LogEntry) {
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	if e.Level == "" {
		e.Level = "info"
	}
	db.AsyncExec(`
		INSERT INTO execution_log_entries (execution_id, trace_id, span_id, ts,
		                                   level, message, fields)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ExecutionID, nullableString(e.TraceID), nullableString(e.SpanID),
		e.TS, e.Level, e.Message, nullableString(e.Fields),
	)
}

// ListLogEntriesByExecution returns the structured log lines for one
// execution in chronological order.
func (db *Database) ListLogEntriesByExecution(execID string) ([]*LogEntry, error) {
	rows, err := db.read.Query(`
		SELECT id, execution_id, trace_id, span_id, ts, level, message, fields
		FROM execution_log_entries
		WHERE execution_id = ?
		ORDER BY ts ASC, id ASC`, execID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogEntries(rows)
}

// ListLogEntriesByTrace returns the log lines from every execution in a
// trace, ordered chronologically across the whole tree. Used by the
// trace-detail "log lane" view in the dashboard.
func (db *Database) ListLogEntriesByTrace(traceID string) ([]*LogEntry, error) {
	rows, err := db.read.Query(`
		SELECT id, execution_id, trace_id, span_id, ts, level, message, fields
		FROM execution_log_entries
		WHERE trace_id = ?
		ORDER BY ts ASC, id ASC`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogEntries(rows)
}

func scanLogEntries(rows *sql.Rows) ([]*LogEntry, error) {
	out := make([]*LogEntry, 0, 16)
	for rows.Next() {
		var e LogEntry
		var traceID, spanID, fields sql.NullString
		if err := rows.Scan(&e.ID, &e.ExecutionID, &traceID, &spanID,
			&e.TS, &e.Level, &e.Message, &fields); err != nil {
			return nil, err
		}
		e.TraceID = traceID.String
		e.SpanID = spanID.String
		e.Fields = fields.String
		out = append(out, &e)
	}
	return out, rows.Err()
}
