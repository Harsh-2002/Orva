package database

import (
	"database/sql"
	"time"
)

// Deployment is one build attempt for a function — queued, building, or
// terminal (succeeded/failed). Replaces the synchronous build-on-request
// behaviour of the old deploy endpoint.
type Deployment struct {
	ID                 string     `json:"id"`
	FunctionID         string     `json:"function_id"`
	Version            int64      `json:"version"`
	Status             string     `json:"status"`
	Phase              string     `json:"phase"`
	ErrorMessage       string     `json:"error_message,omitempty"`
	SubmittedAt        time.Time  `json:"submitted_at"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	DurationMS         *int64     `json:"duration_ms,omitempty"`
	CodeHash           string     `json:"code_hash,omitempty"`
	Source             string     `json:"source,omitempty"`               // "deploy" | "rollback"
	ParentDeploymentID *string    `json:"parent_deployment_id,omitempty"` // set when Source=="rollback"
}

// BuildLogLine is an append-only log entry for a deployment. Sequence numbers
// are per-deployment, monotonically increasing — clients can request
// `from_seq` for incremental polling.
type BuildLogLine struct {
	Seq    int64     `json:"seq"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
	TS     time.Time `json:"ts"`
}

// InsertDeployment creates the initial row for a deployment. The new
// columns (code_hash, source, parent_deployment_id) are written even for
// in-progress builds — code_hash starts empty and is filled by
// SetDeploymentCodeHash when the build resolves it; source defaults to
// "deploy" for normal builds and is set to "rollback" by the rollback
// handler.
func (db *Database) InsertDeployment(d *Deployment) error {
	source := d.Source
	if source == "" {
		source = "deploy"
	}
	var parent any
	if d.ParentDeploymentID != nil {
		parent = *d.ParentDeploymentID
	}
	_, err := db.write.Exec(
		`INSERT INTO deployments
		 (id, function_id, version, status, phase, code_hash, source, parent_deployment_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.FunctionID, d.Version, d.Status, d.Phase, d.CodeHash, source, parent,
	)
	return err
}

// SetDeploymentCodeHash writes the resolved code_hash onto an in-flight
// deployment row. Builder.Build computes the hash from the tarball before
// extraction; this surfaces it on the deployment record so the UI can
// show it and rollback can target it by deployment_id.
func (db *Database) SetDeploymentCodeHash(id, codeHash string) error {
	_, err := db.write.Exec(
		`UPDATE deployments SET code_hash = ? WHERE id = ?`,
		codeHash, id,
	)
	return err
}

// FinishRollbackDeployment closes a synthetic "rollback" deployment row in
// one shot — the rollback handler doesn't go through the build queue, so
// we can't rely on UpdateDeploymentPhase + FinishDeployment from the
// queue path.
func (db *Database) FinishRollbackDeployment(id string, durationMS int64) error {
	_, err := db.write.Exec(
		`UPDATE deployments SET
			status = 'succeeded', phase = 'done', duration_ms = ?,
			started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
			finished_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		durationMS, id,
	)
	return err
}

// UpdateDeploymentPhase bumps the phase label without terminating.
func (db *Database) UpdateDeploymentPhase(id, phase string) error {
	_, err := db.write.Exec(
		`UPDATE deployments SET phase = ?,
		 started_at = COALESCE(started_at, CURRENT_TIMESTAMP)
		 WHERE id = ?`,
		phase, id,
	)
	return err
}

// FinishDeployment terminates a deployment as succeeded or failed.
func (db *Database) FinishDeployment(id, status, errMsg string, durationMS int64) error {
	_, err := db.write.Exec(
		`UPDATE deployments SET
			status = ?, error_message = ?, duration_ms = ?,
			finished_at = CURRENT_TIMESTAMP, phase = 'done'
		 WHERE id = ?`,
		status, errMsg, durationMS, id,
	)
	return err
}

// GetDeployment returns a deployment by id.
func (db *Database) GetDeployment(id string) (*Deployment, error) {
	row := db.read.QueryRow(
		`SELECT id, function_id, version, status, COALESCE(phase, ''),
		        COALESCE(error_message, ''), submitted_at, started_at,
		        finished_at, duration_ms,
		        COALESCE(code_hash, ''), COALESCE(source, 'deploy'), parent_deployment_id
		 FROM deployments WHERE id = ?`, id,
	)
	return scanDeployment(row)
}

// ListDeploymentsForFunction returns the latest N deployments for a function.
func (db *Database) ListDeploymentsForFunction(fnID string, limit int) ([]*Deployment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.read.Query(
		`SELECT id, function_id, version, status, COALESCE(phase, ''),
		        COALESCE(error_message, ''), submitted_at, started_at,
		        finished_at, duration_ms,
		        COALESCE(code_hash, ''), COALESCE(source, 'deploy'), parent_deployment_id
		 FROM deployments
		 WHERE function_id = ?
		 ORDER BY submitted_at DESC LIMIT ?`, fnID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Deployment
	for rows.Next() {
		d, err := scanDeploymentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// AppendBuildLog writes one log line. Seq numbers are provided by the caller
// to guarantee monotonicity across concurrent writers for the same
// deployment (the build worker owns the deployment, so that's trivial).
func (db *Database) AppendBuildLog(deploymentID string, seq int64, stream, line string) error {
	_, err := db.write.Exec(
		`INSERT INTO build_logs (deployment_id, seq, stream, line)
		 VALUES (?, ?, ?, ?)`,
		deploymentID, seq, stream, line,
	)
	return err
}

// GetBuildLogs returns lines with seq > fromSeq, up to `limit` lines.
func (db *Database) GetBuildLogs(deploymentID string, fromSeq int64, limit int) ([]*BuildLogLine, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := db.read.Query(
		`SELECT seq, stream, line, ts FROM build_logs
		 WHERE deployment_id = ? AND seq > ?
		 ORDER BY seq ASC LIMIT ?`, deploymentID, fromSeq, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BuildLogLine
	for rows.Next() {
		var l BuildLogLine
		if err := rows.Scan(&l.Seq, &l.Stream, &l.Line, &l.TS); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, nil
}

func scanDeployment(row *sql.Row) (*Deployment, error) {
	var d Deployment
	var started, finished sql.NullTime
	var dur sql.NullInt64
	var parent sql.NullString
	err := row.Scan(
		&d.ID, &d.FunctionID, &d.Version, &d.Status, &d.Phase, &d.ErrorMessage,
		&d.SubmittedAt, &started, &finished, &dur,
		&d.CodeHash, &d.Source, &parent,
	)
	if err != nil {
		return nil, err
	}
	if started.Valid {
		d.StartedAt = &started.Time
	}
	if finished.Valid {
		d.FinishedAt = &finished.Time
	}
	if dur.Valid {
		v := dur.Int64
		d.DurationMS = &v
	}
	if parent.Valid && parent.String != "" {
		v := parent.String
		d.ParentDeploymentID = &v
	}
	return &d, nil
}

func scanDeploymentRows(rows *sql.Rows) (*Deployment, error) {
	var d Deployment
	var started, finished sql.NullTime
	var dur sql.NullInt64
	var parent sql.NullString
	err := rows.Scan(
		&d.ID, &d.FunctionID, &d.Version, &d.Status, &d.Phase, &d.ErrorMessage,
		&d.SubmittedAt, &started, &finished, &dur,
		&d.CodeHash, &d.Source, &parent,
	)
	if err != nil {
		return nil, err
	}
	if started.Valid {
		d.StartedAt = &started.Time
	}
	if finished.Valid {
		d.FinishedAt = &finished.Time
	}
	if dur.Valid {
		v := dur.Int64
		d.DurationMS = &v
	}
	if parent.Valid && parent.String != "" {
		v := parent.String
		d.ParentDeploymentID = &v
	}
	return &d, nil
}
