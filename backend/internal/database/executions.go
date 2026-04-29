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
}

type ExecutionLog struct {
	ExecutionID string `json:"execution_id"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
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
// for the batched writer. Non-blocking on the hot path.
func (db *Database) AsyncInsertExecutionFinal(exec *Execution, durationMS int64, statusCode int, errMsg string, responseSize int) {
	coldStart := 0
	if exec.ColdStart {
		coldStart = 1
	}
	db.AsyncExec(`
		INSERT INTO executions (
			id, function_id, status, cold_start, container_id,
			duration_ms, status_code, error_message, response_size, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		exec.ID, exec.FunctionID, exec.Status, coldStart, exec.ContainerID,
		durationMS, statusCode, errMsg, responseSize,
	)
}

// AsyncInsertExecutionLog queues a log row for the batched writer.
func (db *Database) AsyncInsertExecutionLog(log *ExecutionLog) {
	db.AsyncExec(`
		INSERT OR REPLACE INTO execution_logs (execution_id, stdout, stderr)
		VALUES (?, ?, ?)`,
		log.ExecutionID, log.Stdout, log.Stderr,
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

func (db *Database) GetExecution(id string) (*Execution, error) {
	row := db.read.QueryRow(`
		SELECT id, function_id, status, cold_start, duration_ms, status_code,
			request_size, response_size, container_id, error_message, started_at, finished_at
		FROM executions WHERE id = ?`, id)
	return scanExecution(row)
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

	query := "SELECT id, function_id, status, cold_start, duration_ms, status_code, request_size, response_size, container_id, error_message, started_at, finished_at FROM executions WHERE 1=1"
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

// DeleteExecution removes one execution row + its logs (FK CASCADE).
// Used by the bulk-delete endpoint and the per-row delete in the Logs UI.
func (db *Database) DeleteExecution(id string) error {
	// execution_logs has ON DELETE CASCADE; orphan rows aren't a concern.
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
	_, err = db.write.Exec(
		"DELETE FROM executions WHERE started_at < datetime('now', '-' || ? || ' days')",
		retentionDays,
	)
	return err
}

func scanExecution(row *sql.Row) (*Execution, error) {
	var exec Execution
	var coldStart int
	var durationMS, statusCode, reqSize, respSize sql.NullInt64
	var containerID, errMsg sql.NullString
	var finishedAt sql.NullTime

	err := row.Scan(
		&exec.ID, &exec.FunctionID, &exec.Status, &coldStart,
		&durationMS, &statusCode, &reqSize, &respSize,
		&containerID, &errMsg, &exec.StartedAt, &finishedAt,
	)
	if err != nil {
		return nil, err
	}

	exec.ColdStart = coldStart == 1
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
	exec.ContainerID = containerID.String
	exec.ErrorMessage = errMsg.String
	if finishedAt.Valid {
		exec.FinishedAt = &finishedAt.Time
	}
	return &exec, nil
}

func scanExecutionRows(rows *sql.Rows) (*Execution, error) {
	var exec Execution
	var coldStart int
	var durationMS, statusCode, reqSize, respSize sql.NullInt64
	var containerID, errMsg sql.NullString
	var finishedAt sql.NullTime

	err := rows.Scan(
		&exec.ID, &exec.FunctionID, &exec.Status, &coldStart,
		&durationMS, &statusCode, &reqSize, &respSize,
		&containerID, &errMsg, &exec.StartedAt, &finishedAt,
	)
	if err != nil {
		return nil, err
	}

	exec.ColdStart = coldStart == 1
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
	exec.ContainerID = containerID.String
	exec.ErrorMessage = errMsg.String
	if finishedAt.Valid {
		exec.FinishedAt = &finishedAt.Time
	}
	return &exec, nil
}
