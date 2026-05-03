package database

import (
	"database/sql"
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
	TraceID            string `json:"trace_id,omitempty"`
	SpanID             string `json:"span_id,omitempty"`
	ParentSpanID       string `json:"parent_span_id,omitempty"`
	Trigger            string `json:"trigger,omitempty"`
	ParentFunctionID   string `json:"parent_function_id,omitempty"`
	IsOutlier          bool   `json:"is_outlier"`
	BaselineP95MS      *int64 `json:"baseline_p95_ms,omitempty"`
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
// for the batched writer. Non-blocking on the hot path. Trace fields are
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
// batched writer (v0.4 A3). Mirrors the pattern of AsyncInsertExecutionFinal:
// the proxy hot path never blocks on the SQLite writer. Foreign-key
// satisfaction relies on the executions row landing first via the same
// queue — under the batched-tx model both rows commit together.
func (db *Database) AsyncInsertExecutionRequest(req *ExecutionRequest) {
	truncated := 0
	if req.Truncated {
		truncated = 1
	}
	db.AsyncExec(`
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
// by started_at ASC so the root span is first and children follow in
// causal order. Backed by idx_executions_trace_id.
func (db *Database) ListByTraceID(traceID string) ([]*Execution, error) {
	rows, err := db.read.Query(
		"SELECT "+executionSelectColumns+" FROM executions WHERE trace_id = ? ORDER BY started_at ASC",
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

// ListRootSpans returns recent root spans (parent_span_id IS NULL)
// optionally filtered by function_id, since/until window, and outlier
// flag. Used by the Traces list view. Cursor pagination is by started_at.
type ListRootSpansParams struct {
	FunctionID   string
	Since        string // ISO8601 inclusive
	Until        string // ISO8601 exclusive
	Status       string
	OutlierOnly  bool
	Limit        int
	BeforeCursor string // started_at lower bound for "next page"
}

func (db *Database) ListRootSpans(p ListRootSpansParams) ([]*Execution, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	q := "SELECT " + executionSelectColumns + " FROM executions WHERE trace_id IS NOT NULL AND parent_span_id IS NULL"
	args := []any{}
	if p.FunctionID != "" {
		q += " AND function_id = ?"
		args = append(args, p.FunctionID)
	}
	if p.Status != "" {
		q += " AND status = ?"
		args = append(args, p.Status)
	}
	if p.OutlierOnly {
		q += " AND is_outlier = 1"
	}
	if p.Since != "" {
		q += " AND started_at >= ?"
		args = append(args, p.Since)
	}
	if p.Until != "" {
		q += " AND started_at < ?"
		args = append(args, p.Until)
	}
	if p.BeforeCursor != "" {
		q += " AND started_at < ?"
		args = append(args, p.BeforeCursor)
	}
	q += " ORDER BY started_at DESC LIMIT ?"
	args = append(args, p.Limit)

	rows, err := db.read.Query(q, args...)
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
	if params.Since != "" {
		query += " AND started_at >= ?"
		countQuery += " AND started_at >= ?"
		args = append(args, params.Since)
	}
	if params.Until != "" {
		query += " AND started_at < ?"
		countQuery += " AND started_at < ?"
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
func (db *Database) DeleteExecution(id string) error {
	if _, err := db.write.Exec("DELETE FROM execution_requests WHERE execution_id = ?", id); err != nil {
		return err
	}
	_, err := db.write.Exec("DELETE FROM executions WHERE id = ?", id)
	return err
}

func (db *Database) PurgeOldExecutions(retentionDays int) error {
	_, err := db.write.Exec(`
		DELETE FROM execution_logs WHERE execution_id IN (
			SELECT id FROM executions WHERE started_at < datetime('now', '-' || ? || ' days')
		)`, retentionDays)
	if err != nil {
		return err
	}
	// v0.4 A3: same explicit cleanup for the captured-request rows so
	// PurgeOldExecutions doesn't rely on PRAGMA foreign_keys being on
	// for the writer connection.
	_, err = db.write.Exec(`
		DELETE FROM execution_requests WHERE execution_id IN (
			SELECT id FROM executions WHERE started_at < datetime('now', '-' || ? || ' days')
		)`, retentionDays)
	if err != nil {
		return err
	}
	_, err = db.write.Exec(
		"DELETE FROM executions WHERE started_at < datetime('now', '-' || ? || ' days')",
		retentionDays,
	)
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

func scanExecution(row *sql.Row) (*Execution, error)     { return scanExecutionFields(row) }
func scanExecutionRows(rows *sql.Rows) (*Execution, error) { return scanExecutionFields(rows) }
