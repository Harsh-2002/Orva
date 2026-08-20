package database

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Execution struct {
	ID           string     `json:"id"`
	FunctionID   string     `json:"function_id"`
	Status       string     `json:"status"`
	ColdStart    bool       `json:"cold_start"`
	DurationMS   *int64     `json:"duration_ms"`
	StatusCode   *int       `json:"status_code"`
	RequestSize  *int       `json:"request_size"`
	ResponseSize *int       `json:"response_size"`
	ContainerID  string     `json:"container_id"`
	ErrorMessage string     `json:"error_message"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	// ReplayOf points at the original execution's id when this row was
	// produced by POST /api/v1/executions/{id}/replay. NULL on first-class
	// invocations; non-NULL only on replays. v0.4 A3.
	ReplayOf *string `json:"replay_of,omitempty"`

	// v0.5 tracing: each execution row is a span in a causal trace.
	// TraceID groups every execution that resulted (directly or via F2F /
	// jobs) from the same top-level invocation. ParentSpanID chains them
	// into a tree. Trigger captures how this span was started; one of
	// "http" / "cron" / "job" / "f2f" / "webhook" / "inbound" / "replay".
	TraceID          string `json:"trace_id,omitempty"`
	SpanID           string `json:"span_id,omitempty"`
	ParentSpanID     string `json:"parent_span_id,omitempty"`
	Trigger          string `json:"trigger,omitempty"`
	ParentFunctionID string `json:"parent_function_id,omitempty"`
	IsOutlier        bool   `json:"is_outlier"`
	BaselineP95MS    *int64 `json:"baseline_p95_ms,omitempty"`
}

// TraceContext bundles the four pieces of trace state that flow with a
// request through every internal hop. TraceID stays constant inside a
// trace; SpanID identifies this execution; ParentSpanID points at the
// caller. Trigger is set per-span based on the entry point. Use
// NewRootTraceContext when starting a fresh trace and ChildOf when
// extending an existing one (F2F / job pickup / cron-scheduled).
type TraceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Trigger      string
}

type ExecutionLog struct {
	ExecutionID string `json:"execution_id"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
}

// ExecutionRequest is the captured envelope replayed by the dashboard's
// Replay button (v0.4 A3). HeadersJSON is post-redaction; sensitive values
// (Authorization, Cookie, X-Orva-API-Key, X-Orva-Internal-Token,
// Proxy-Authorization) are replaced with the literal string "[REDACTED]"
// before serialisation. Truncated=true means Body is incomplete — replay
// will refuse those rows with HTTP 410 Gone.
type ExecutionRequest struct {
	ExecutionID string `json:"execution_id"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	HeadersJSON string `json:"headers_json"`
	Body        []byte `json:"body"`
	Truncated   bool   `json:"truncated"`
	CapturedAt  int64  `json:"captured_at"` // unix millis
}

func (db *Database) InsertExecution(exec *Execution) error {
	coldStart := 0
	if exec.ColdStart {
		coldStart = 1
	}
	_, err := db.write.Exec(`
		INSERT INTO executions (id, function_id, status, cold_start, container_id)
		VALUES (?, ?, ?, ?, ?)`,
		exec.ID, exec.FunctionID, exec.Status, coldStart, exec.ContainerID,
	)
	return err
}

// InsertExecutionFinal writes a completed execution row in one statement,
// eliminating the status=running insert + update pair. The in-flight view
// is already available via the ActiveRequests gauge.
func (db *Database) InsertExecutionFinal(exec *Execution, durationMS int64, statusCode int, errMsg string, responseSize int) error {
	coldStart := 0
	if exec.ColdStart {
		coldStart = 1
	}
	_, err := db.write.Exec(`
		INSERT INTO executions (
			id, function_id, status, cold_start, container_id,
			duration_ms, status_code, error_message, response_size, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		exec.ID, exec.FunctionID, exec.Status, coldStart, exec.ContainerID,
		durationMS, statusCode, errMsg, responseSize,
	)
	return err
}

// AsyncInsertExecutionFinal queues the final-write of a completed execution
// for the bounded critical writer. Enqueue applies deadline-aware backpressure
// only when saturated; commit remains off the hot request path. Trace fields are
// taken from exec.TraceID/SpanID/ParentSpanID/Trigger/ParentFunctionID;
// callers populate them before calling. IsOutlier + BaselineP95MS are NOT
// written here — the baseline package back-writes them via UpdateOutlier
// once the execution has been recorded against its function's baseline.
//
// started_at uses exec.StartedAt when non-zero; otherwise CURRENT_TIMESTAMP.
// Setting it explicitly matters for the trace tree: under the async batch
// writer, child spans (F2F callees) often commit BEFORE their parent
// (parent commits only after the response is sent), so a default-on-insert
// timestamp would invert causal ordering. Callers measure start time at
// the top of their handler and pass it down.
func (db *Database) AsyncInsertExecutionFinal(exec *Execution, durationMS int64, statusCode int, errMsg string, responseSize int) {
	coldStart := 0
	if exec.ColdStart {
		coldStart = 1
	}
	startedAt := executionStartTime(exec.StartedAt)
	db.AsyncExec(`
		INSERT INTO executions (
			id, function_id, status, cold_start, container_id,
			duration_ms, status_code, error_message, response_size,
			started_at, finished_at,
			trace_id, span_id, parent_span_id, trigger, parent_function_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?)`,
		exec.ID, exec.FunctionID, exec.Status, coldStart, exec.ContainerID,
		durationMS, statusCode, errMsg, responseSize,
		startedAt,
		nullableString(exec.TraceID), nullableString(exec.SpanID),
		nullableString(exec.ParentSpanID), nullableString(exec.Trigger),
		nullableString(exec.ParentFunctionID),
	)
}

// executionStartTime returns either the explicit started_at (UTC) or the
// current time when the caller didn't track it. We pass UTC into SQLite
// to match the schema's DATETIME convention; the column stores the raw
// string so timezone is meaningful.
func executionStartTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

// nullableString returns nil for the empty string so SQLite stores NULL
// rather than the literal "" — keeps trace queries clean
// (`WHERE trace_id IS NULL` works) and shrinks the index B-tree.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// UpdateOutlier back-writes the baseline-derived fields onto an existing
// execution row after the baseline package classifies it. Idempotent —
// the baseline only fires once per execution finalize.
func (db *Database) UpdateOutlier(execID string, isOutlier bool, baselineP95MS int64) {
	flag := 0
	if isOutlier {
		flag = 1
	}
	db.AsyncExec(
		"UPDATE executions SET is_outlier = ?, baseline_p95_ms = ? WHERE id = ?",
		flag, baselineP95MS, execID,
	)
}

// WarmBaselineSeed represents the data needed to warm a per-function
// baseline at startup. Populated by ListBaselineSeed; the caller is
// metrics.Baselines.Warm.
type WarmBaselineSeed struct {
	FunctionID string
	DurationMS int64
}

// ListBaselineSeed returns up to baselineSamples × functions worth of
// recent successful warm executions, suitable for warming the per-fn
// rolling P95 buffers at startup. We pull only successful warm
// executions (cold_start = 0, status = 'success', duration_ms NOT NULL)
// because cold starts and errors are excluded from the baseline at
// runtime — warming with them would skew the first few minutes of
// post-start outlier classification.
func (db *Database) ListBaselineSeed(perFnSamples int) ([]WarmBaselineSeed, error) {
	if perFnSamples <= 0 {
		perFnSamples = 100
	}
	// Window-function-style: take the most recent N rows per function.
	// SQLite supports ROW_NUMBER() since 3.25 and modernc/sqlite is
	// well past that.
	rows, err := db.read.Query(`
		SELECT function_id, duration_ms FROM (
			SELECT function_id, duration_ms,
				ROW_NUMBER() OVER (PARTITION BY function_id ORDER BY started_at DESC) AS rn
			FROM executions
			WHERE status = 'success' AND cold_start = 0 AND duration_ms IS NOT NULL
		) WHERE rn <= ?
	`, perFnSamples)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var seed []WarmBaselineSeed
	for rows.Next() {
		var s WarmBaselineSeed
		if err := rows.Scan(&s.FunctionID, &s.DurationMS); err != nil {
			return nil, err
		}
		seed = append(seed, s)
	}
	return seed, rows.Err()
}

// AsyncInsertExecutionLog queues a log row for the batched writer.
func (db *Database) AsyncInsertExecutionLog(log *ExecutionLog) {
	db.AsyncExec(`
		INSERT OR REPLACE INTO execution_logs (execution_id, stdout, stderr)
		VALUES (?, ?, ?)`,
		log.ExecutionID, log.Stdout, log.Stderr,
	)
}

// AsyncInsertExecutionRequest queues a captured-request row for the
// bounded critical writer (v0.4 A3). execution_requests intentionally has no
// foreign key because capture happens before the final execution row exists.
func (db *Database) AsyncInsertExecutionRequest(req *ExecutionRequest) {
	truncated := 0
	if req.Truncated {
		truncated = 1
	}
	// Telemetry, not critical. This is the single largest payload the writer
	// ever queues -- a captured request body up to replay_capture_max_bytes
	// (1 MiB default) -- and capture is explicitly best-effort, so it does
	// not belong in the queue whose whole point is not losing anything.
	db.AsyncExecTelemetry(`
		INSERT OR REPLACE INTO execution_requests (
			execution_id, method, path, headers_json, body, truncated, captured_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		req.ExecutionID, req.Method, req.Path, req.HeadersJSON,
		req.Body, truncated, req.CapturedAt,
	)
}

// GetExecutionRequest returns the captured request envelope for an
// execution, or (nil, sql.ErrNoRows) if capture was disabled at the time
// or the row was purged with the parent execution.
func (db *Database) GetExecutionRequest(id string) (*ExecutionRequest, error) {
	var req ExecutionRequest
	var truncated int
	var body []byte
	err := db.read.QueryRow(`
		SELECT execution_id, method, path, headers_json, body, truncated, captured_at
		FROM execution_requests WHERE execution_id = ?`, id,
	).Scan(&req.ExecutionID, &req.Method, &req.Path, &req.HeadersJSON,
		&body, &truncated, &req.CapturedAt)
	if err != nil {
		return nil, err
	}
	req.Body = body
	req.Truncated = truncated == 1
	return &req, nil
}

// AsyncInsertExecutionFinalReplay mirrors AsyncInsertExecutionFinal but
// also stores the replay_of pointer. Separate function so the hot
// invoke path doesn't pay the cost of an always-NULL parameter on every
// call. Trace fields ride along the same as AsyncInsertExecutionFinal.
func (db *Database) AsyncInsertExecutionFinalReplay(exec *Execution, durationMS int64, statusCode int, errMsg string, responseSize int, replayOf string) {
	coldStart := 0
	if exec.ColdStart {
		coldStart = 1
	}
	startedAt := executionStartTime(exec.StartedAt)
	db.AsyncExec(`
		INSERT INTO executions (
			id, function_id, status, cold_start, container_id,
			duration_ms, status_code, error_message, response_size,
			started_at, finished_at, replay_of,
			trace_id, span_id, parent_span_id, trigger, parent_function_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?)`,
		exec.ID, exec.FunctionID, exec.Status, coldStart, exec.ContainerID,
		durationMS, statusCode, errMsg, responseSize,
		startedAt, replayOf,
		nullableString(exec.TraceID), nullableString(exec.SpanID),
		nullableString(exec.ParentSpanID), nullableString(exec.Trigger),
		nullableString(exec.ParentFunctionID),
	)
}

func (db *Database) UpdateExecution(id, status string, durationMS int64, statusCode int, errMsg string, responseSize int) error {
	_, err := db.write.Exec(`
		UPDATE executions SET
			status = ?, duration_ms = ?, status_code = ?,
			error_message = ?, response_size = ?, finished_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		status, durationMS, statusCode, errMsg, responseSize, id,
	)
	return err
}

// executionSelectColumns is the canonical column list shared by every
// SELECT against executions. Keeping it in one place makes adding columns
// safe; mismatched scan/select pairs are the #1 source of trace-data
// regressions.
const executionSelectColumns = `
	id, function_id, status, cold_start, duration_ms, status_code,
	request_size, response_size, container_id, error_message,
	started_at, finished_at, replay_of,
	trace_id, span_id, parent_span_id, trigger, parent_function_id,
	is_outlier, baseline_p95_ms`

func (db *Database) GetExecution(id string) (*Execution, error) {
	row := db.read.QueryRow(
		"SELECT "+executionSelectColumns+" FROM executions WHERE id = ?", id,
	)
	return scanExecution(row)
}

// ListByTraceID returns every execution that shares a trace_id, ordered
// by (started_at, id) so tied timestamps remain deterministic. Backed by
// idx_executions_trace_started.
func (db *Database) ListByTraceID(traceID string) ([]*Execution, error) {
	rows, err := db.read.Query(
		"SELECT "+executionSelectColumns+` FROM executions WHERE trace_id = ?
		 ORDER BY julianday(replace(started_at, ' +0000 UTC', 'Z')) ASC, id ASC`,
		traceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*Execution
	for rows.Next() {
		exec, err := scanExecutionRows(rows)
		if err != nil {
			return nil, err
		}
		execs = append(execs, exec)
	}
	return execs, rows.Err()
}

// TraceSummary is one trace-wide row for the trace list. Root* identifies the
// local root: the earliest execution whose parent span is absent from this
// trace. That definition keeps an inbound W3C trace visible while retaining
// its external parent id.
type TraceSummary struct {
	TraceID              string
	RootSpanID           string
	RootFunctionID       string
	FunctionName         string
	ExternalParentSpanID string
	Trigger              string
	StartedAt            time.Time
	DurationMS           int64
	Status               string
	StatusCode           int
	IsOutlier            bool
	SpanCount            int
	ErrorCount           int
	ColdStartCount       int
}

type ListTraceSummariesParams struct {
	Function        string
	Since           string // ISO8601 inclusive, applied to trace start
	Until           string // ISO8601 exclusive, applied to trace start
	Status          string
	OutlierOnly     bool
	Limit           int
	BeforeStartedAt string
	BeforeTraceID   string
	LegacyBefore    bool
}

// ListTraceSummaries aggregates status, timing, outliers, and counts across
// every execution and user span in each trace. The stable sort/cursor is
// (started_at DESC, trace_id DESC). Function filtering matches any execution
// in the trace by exact immutable id or exact current function name.
func (db *Database) ListTraceSummaries(p ListTraceSummariesParams) ([]TraceSummary, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	q := `
		WITH execution_rollup AS (
			SELECT trace_id,
				MIN(julianday(replace(started_at, ' +0000 UTC', 'Z'))) AS start_jd,
				MAX(julianday(replace(started_at, ' +0000 UTC', 'Z')) + COALESCE(duration_ms, 0) / 86400000.0) AS end_jd,
				COUNT(*) AS execution_count,
				SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS error_count,
				SUM(CASE WHEN cold_start = 1 THEN 1 ELSE 0 END) AS cold_start_count,
				MAX(is_outlier) AS has_outlier
			FROM executions
			WHERE trace_id IS NOT NULL
			GROUP BY trace_id
		),
		user_rollup AS (
			SELECT trace_id,
				MIN(julianday(replace(started_at, ' +0000 UTC', 'Z'))) AS start_jd,
				MAX(julianday(replace(started_at, ' +0000 UTC', 'Z')) + duration_ms / 86400000.0) AS end_jd,
				COUNT(*) AS span_count,
				SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS error_count
			FROM user_spans
			GROUP BY trace_id
		),
		local_roots AS (
			SELECT e.*,
				ROW_NUMBER() OVER (
					PARTITION BY e.trace_id
					ORDER BY julianday(replace(e.started_at, ' +0000 UTC', 'Z')) ASC, e.id ASC
				) AS root_rank
			FROM executions e
			WHERE e.trace_id IS NOT NULL
			  AND NOT EXISTS (
				SELECT 1 FROM executions parent
				WHERE parent.trace_id = e.trace_id
				  AND parent.span_id = e.parent_span_id
			  )
		),
		summaries AS (
			SELECT r.trace_id,
				r.span_id AS root_span_id,
				r.function_id AS root_function_id,
				COALESCE(f.name, '') AS function_name,
				CASE WHEN r.parent_span_id IS NOT NULL THEN r.parent_span_id ELSE '' END AS external_parent_span_id,
				COALESCE(r.trigger, '') AS trigger,
				r.started_at,
				er.start_jd AS started_jd,
				CAST(ROUND((
					MAX(er.end_jd, COALESCE(ur.end_jd, er.end_jd)) -
					MIN(er.start_jd, COALESCE(ur.start_jd, er.start_jd))
				) * 86400000.0) AS INTEGER) AS duration_ms,
				CASE WHEN er.error_count + COALESCE(ur.error_count, 0) > 0 THEN 'error' ELSE 'success' END AS status,
				COALESCE((
					SELECT e2.status_code FROM executions e2
					WHERE e2.trace_id = r.trace_id AND e2.status = 'error' AND e2.status_code IS NOT NULL
					ORDER BY e2.started_at ASC, e2.id ASC LIMIT 1
				), r.status_code, 0) AS status_code,
				er.has_outlier AS is_outlier,
				er.execution_count + COALESCE(ur.span_count, 0) AS span_count,
				er.error_count + COALESCE(ur.error_count, 0) AS error_count,
				er.cold_start_count
			FROM local_roots r
			JOIN execution_rollup er ON er.trace_id = r.trace_id
			LEFT JOIN user_rollup ur ON ur.trace_id = r.trace_id
			LEFT JOIN functions f ON f.id = r.function_id
			WHERE r.root_rank = 1
		)
		SELECT trace_id, root_span_id, root_function_id, function_name,
			external_parent_span_id, trigger, started_at, duration_ms,
			status, status_code, is_outlier, span_count, error_count,
			cold_start_count
		FROM summaries
		WHERE 1=1`
	args := []any{}
	if p.Function != "" {
		q += ` AND EXISTS (
			SELECT 1 FROM executions match_exec
			LEFT JOIN functions match_fn ON match_fn.id = match_exec.function_id
			WHERE match_exec.trace_id = summaries.trace_id
			  AND (match_exec.function_id = ? OR match_fn.name = ?)
		)`
		args = append(args, p.Function, p.Function)
	}
	if p.Status != "" {
		q += " AND status = ?"
		args = append(args, p.Status)
	}
	if p.OutlierOnly {
		q += " AND is_outlier = 1"
	}
	if p.Since != "" {
		q += " AND started_jd >= julianday(?)"
		args = append(args, p.Since)
	}
	if p.Until != "" {
		q += " AND started_jd < julianday(?)"
		args = append(args, p.Until)
	}
	if p.BeforeStartedAt != "" {
		if p.LegacyBefore || p.BeforeTraceID == "" {
			q += " AND started_jd < julianday(?)"
			args = append(args, p.BeforeStartedAt)
		} else {
			q += ` AND (
				started_jd < julianday(?) OR
				(started_jd = julianday(?) AND trace_id < ?)
			)`
			args = append(args, p.BeforeStartedAt, p.BeforeStartedAt, p.BeforeTraceID)
		}
	}
	q += " ORDER BY started_jd DESC, trace_id DESC LIMIT ?"
	args = append(args, p.Limit)

	rows, err := db.read.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []TraceSummary
	for rows.Next() {
		var s TraceSummary
		var outlier int
		if err := rows.Scan(
			&s.TraceID, &s.RootSpanID, &s.RootFunctionID, &s.FunctionName,
			&s.ExternalParentSpanID, &s.Trigger, &s.StartedAt, &s.DurationMS,
			&s.Status, &s.StatusCode, &outlier, &s.SpanCount, &s.ErrorCount,
			&s.ColdStartCount,
		); err != nil {
			return nil, err
		}
		s.IsOutlier = outlier == 1
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

type traceCursor struct {
	Version   int    `json:"v"`
	StartedAt string `json:"started_at"`
	TraceID   string `json:"trace_id"`
}

// EncodeTraceCursor returns an opaque, URL-safe cursor for the stable trace
// ordering tuple. It deliberately omits padding so it stays query-string safe.
func EncodeTraceCursor(startedAt time.Time, traceID string) string {
	payload, _ := json.Marshal(traceCursor{
		Version: 1, StartedAt: startedAt.Format(time.RFC3339Nano), TraceID: traceID,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeTraceCursor accepts v1 opaque cursors and, during the migration
// window, legacy RFC3339 timestamp cursors.
func DecodeTraceCursor(value string) (startedAt, traceID string, legacy bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false, nil
	}
	if raw, decodeErr := base64.RawURLEncoding.DecodeString(value); decodeErr == nil {
		var cursor traceCursor
		if jsonErr := json.Unmarshal(raw, &cursor); jsonErr == nil &&
			cursor.Version == 1 && cursor.StartedAt != "" && cursor.TraceID != "" {
			if _, parseErr := time.Parse(time.RFC3339Nano, cursor.StartedAt); parseErr != nil {
				return "", "", false, fmt.Errorf("invalid trace cursor timestamp: %w", parseErr)
			}
			return cursor.StartedAt, cursor.TraceID, false, nil
		}
	}
	if _, parseErr := time.Parse(time.RFC3339Nano, value); parseErr == nil {
		return value, "", true, nil
	}
	return "", "", false, fmt.Errorf("invalid trace cursor")
}

type ListExecutionsParams struct {
	FunctionID string
	Status     string
	Since      string // ISO8601, inclusive lower bound on started_at
	Until      string // ISO8601, exclusive upper bound on started_at
	Search     string // substring against error_message + container_id
	Limit      int
	Offset     int
}

type ListExecutionsResult struct {
	Executions []*Execution `json:"executions"`
	Total      int          `json:"total"`
}

func (db *Database) ListExecutions(params ListExecutionsParams) (*ListExecutionsResult, error) {
	if params.Limit <= 0 {
		params.Limit = 50
	}
	// Clamp the upper end too. Every sibling list endpoint does; this one
	// only floored, so ?limit=100000000 materialised the whole table.
	if params.Limit > 1000 {
		params.Limit = 1000
	}

	query := "SELECT " + executionSelectColumns + " FROM executions WHERE 1=1"
	countQuery := "SELECT COUNT(*) FROM executions WHERE 1=1"
	var args []any

	if params.FunctionID != "" {
		query += " AND function_id = ?"
		countQuery += " AND function_id = ?"
		args = append(args, params.FunctionID)
	}
	if params.Status != "" {
		query += " AND status = ?"
		countQuery += " AND status = ?"
		args = append(args, params.Status)
	}
	// Compare as time, not as text. started_at is stored space-separated
	// (Go's time layout, hence the ' +0000 UTC' fixups elsewhere in this
	// file) while every client sends RFC3339 with a 'T'. ' ' (0x20) sorts
	// below 'T' (0x54), so a raw string comparison puts every row from the
	// cutoff's OWN DATE below the cutoff: "last 1 hour" returned nothing at
	// all, and executions prune over-deleted by up to a day at the boundary.
	// The sibling trace query already does this correctly.
	if params.Since != "" {
		query += " AND julianday(replace(started_at, ' +0000 UTC', 'Z')) >= julianday(?)"
		countQuery += " AND julianday(replace(started_at, ' +0000 UTC', 'Z')) >= julianday(?)"
		args = append(args, params.Since)
	}
	if params.Until != "" {
		query += " AND julianday(replace(started_at, ' +0000 UTC', 'Z')) < julianday(?)"
		countQuery += " AND julianday(replace(started_at, ' +0000 UTC', 'Z')) < julianday(?)"
		args = append(args, params.Until)
	}
	if params.Search != "" {
		query += " AND (error_message LIKE ? OR container_id LIKE ?)"
		countQuery += " AND (error_message LIKE ? OR container_id LIKE ?)"
		like := "%" + params.Search + "%"
		args = append(args, like, like)
	}

	var total int
	if err := db.read.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, err
	}

	query += " ORDER BY started_at DESC LIMIT ? OFFSET ?"
	args = append(args, params.Limit, params.Offset)

	rows, err := db.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*Execution
	for rows.Next() {
		exec, err := scanExecutionRows(rows)
		if err != nil {
			return nil, err
		}
		execs = append(execs, exec)
	}

	return &ListExecutionsResult{Executions: execs, Total: total}, nil
}

func (db *Database) InsertExecutionLog(log *ExecutionLog) error {
	_, err := db.write.Exec(`
		INSERT OR REPLACE INTO execution_logs (execution_id, stdout, stderr)
		VALUES (?, ?, ?)`,
		log.ExecutionID, log.Stdout, log.Stderr,
	)
	return err
}

func (db *Database) GetExecutionLog(executionID string) (*ExecutionLog, error) {
	var log ExecutionLog
	err := db.read.QueryRow(
		"SELECT execution_id, stdout, stderr FROM execution_logs WHERE execution_id = ?",
		executionID,
	).Scan(&log.ExecutionID, &log.Stdout, &log.Stderr)
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// DeleteExecution removes one execution row + its logs (FK CASCADE on
// execution_logs) and its captured request envelope (manual cleanup —
// execution_requests dropped its FK in v0.4 to avoid async-batch FK
// failures on the hot path; see dropExecutionRequestsFK).
func (db *Database) DeleteExecution(id string) (bool, error) {
	// Delete every child first, by the same list the purge uses. Two of them
	// -- user_spans and execution_log_entries -- carry no declared FK, so
	// CASCADE cannot reach them and they were left as permanently
	// unreachable rows. The comment a few lines below has warned about
	// exactly this; executionChildTables is the single list both paths use.
	for _, t := range executionChildTables {
		if _, err := db.write.Exec("DELETE FROM "+t+" WHERE execution_id = ?", id); err != nil {
			return false, fmt.Errorf("delete %s: %w", t, err)
		}
	}
	res, err := db.write.Exec(`DELETE FROM executions WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// executionChildTables are every table keyed by execution_id. Only
// execution_logs has ON DELETE CASCADE; the rest are cleaned up by hand here,
// deliberately, so the purge does not depend on PRAGMA foreign_keys being on
// for the writer connection.
//
// Keep this list exhaustive. A child table missing from it is not merely
// un-purged: once its parent executions row is deleted nothing can ever join
// the rows back, so they become permanently unreachable garbage that no later
// purge, DeleteExecution, or query will reclaim. user_spans and
// execution_log_entries — the two tables user code writes to via
// orva.trace.span() and orva.log.* — were missed exactly that way, which meant
// retention left the fastest-growing tables growing.
var executionChildTables = []string{
	"execution_logs",
	"execution_requests",
	"user_spans",
	"execution_log_entries",
}

func (db *Database) PurgeOldExecutions(retentionDays int) error {
	// One cutoff for the whole purge. Evaluating datetime('now', …) per
	// statement lets a row cross the boundary mid-purge and orphan its
	// children.
	var cutoff string
	if err := db.read.QueryRow(
		"SELECT datetime('now', '-' || ? || ' days')", retentionDays).Scan(&cutoff); err != nil {
		return err
	}

	for _, table := range executionChildTables {
		if _, err := db.write.Exec(
			"DELETE FROM "+table+" WHERE execution_id IN ("+
				"SELECT id FROM executions WHERE started_at < ?)", cutoff); err != nil {
			return fmt.Errorf("purge %s: %w", table, err)
		}
	}

	_, err := db.write.Exec("DELETE FROM executions WHERE started_at < ?", cutoff)
	return err
}

// scanExecutionFields uses the package-level rowScanner interface
// (defined in functions.go) so both *sql.Row and *sql.Rows share one
// scan implementation. Adding a column to executionSelectColumns means
// editing only this function; both call sites ride along automatically.
func scanExecutionFields(s rowScanner) (*Execution, error) {
	var exec Execution
	var coldStart, isOutlier int
	var durationMS, statusCode, reqSize, respSize, baselineP95 sql.NullInt64
	var containerID, errMsg, replayOf, traceID, spanID, parentSpanID, trigger, parentFnID sql.NullString
	var finishedAt sql.NullTime

	err := s.Scan(
		&exec.ID, &exec.FunctionID, &exec.Status, &coldStart,
		&durationMS, &statusCode, &reqSize, &respSize,
		&containerID, &errMsg, &exec.StartedAt, &finishedAt, &replayOf,
		&traceID, &spanID, &parentSpanID, &trigger, &parentFnID,
		&isOutlier, &baselineP95,
	)
	if err != nil {
		return nil, err
	}

	exec.ColdStart = coldStart == 1
	exec.IsOutlier = isOutlier == 1
	if durationMS.Valid {
		v := durationMS.Int64
		exec.DurationMS = &v
	}
	if statusCode.Valid {
		v := int(statusCode.Int64)
		exec.StatusCode = &v
	}
	if reqSize.Valid {
		v := int(reqSize.Int64)
		exec.RequestSize = &v
	}
	if respSize.Valid {
		v := int(respSize.Int64)
		exec.ResponseSize = &v
	}
	if baselineP95.Valid {
		v := baselineP95.Int64
		exec.BaselineP95MS = &v
	}
	exec.ContainerID = containerID.String
	exec.ErrorMessage = errMsg.String
	if finishedAt.Valid {
		exec.FinishedAt = &finishedAt.Time
	}
	if replayOf.Valid && replayOf.String != "" {
		v := replayOf.String
		exec.ReplayOf = &v
	}
	exec.TraceID = traceID.String
	exec.SpanID = spanID.String
	exec.ParentSpanID = parentSpanID.String
	exec.Trigger = trigger.String
	exec.ParentFunctionID = parentFnID.String
	return &exec, nil
}

func scanExecution(row *sql.Row) (*Execution, error)       { return scanExecutionFields(row) }
func scanExecutionRows(rows *sql.Rows) (*Execution, error) { return scanExecutionFields(rows) }
