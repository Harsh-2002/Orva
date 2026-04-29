package database

import (
	"database/sql"
	"encoding/json"
	"time"
)

// DeploymentSnapshot captures everything except code/secrets that defines
// the function's behaviour at deploy time. Stored as JSON in
// deployments.snapshot. Code is already versioned via the
// versions/<hash>/ tree and dependencies live inside that tree, so they
// don't need to be in here. Secrets are intentionally excluded — they
// rotate independently of code (key rotation, password changes) and a
// rollback should not silently revert to a stale credential.
type DeploymentSnapshot struct {
	EnvVars           map[string]string `json:"env_vars"`
	MemoryMB          int64             `json:"memory_mb"`
	CPUs              float64           `json:"cpus"`
	TimeoutMS         int64             `json:"timeout_ms"`
	NetworkMode       string            `json:"network_mode"`
	AuthMode          string            `json:"auth_mode"`
	RateLimitPerMin   int               `json:"rate_limit_per_min"`
	MaxConcurrency    int               `json:"max_concurrency"`
	ConcurrencyPolicy string            `json:"concurrency_policy"`
}

// SnapshotFromFunction copies the snapshot-relevant fields off a Function
// record. Used by the deploy path right before FinishDeployment so the row
// captures the state the function settled on after this build.
func SnapshotFromFunction(fn *Function) *DeploymentSnapshot {
	env := map[string]string{}
	for k, v := range fn.EnvVars {
		env[k] = v
	}
	return &DeploymentSnapshot{
		EnvVars:           env,
		MemoryMB:          fn.MemoryMB,
		CPUs:              fn.CPUs,
		TimeoutMS:         fn.TimeoutMS,
		NetworkMode:       fn.NetworkMode,
		AuthMode:          fn.AuthMode,
		RateLimitPerMin:   fn.RateLimitPerMin,
		MaxConcurrency:    fn.MaxConcurrency,
		ConcurrencyPolicy: fn.ConcurrencyPolicy,
	}
}

// Deployment is one build attempt for a function — queued, building, or
// terminal (succeeded/failed). Replaces the synchronous build-on-request
// behaviour of the old deploy endpoint.
type Deployment struct {
	ID                 string              `json:"id"`
	FunctionID         string              `json:"function_id"`
	Version            int64               `json:"version"`
	Status             string              `json:"status"`
	Phase              string              `json:"phase"`
	ErrorMessage       string              `json:"error_message,omitempty"`
	SubmittedAt        time.Time           `json:"submitted_at"`
	StartedAt          *time.Time          `json:"started_at,omitempty"`
	FinishedAt         *time.Time          `json:"finished_at,omitempty"`
	DurationMS         *int64              `json:"duration_ms,omitempty"`
	CodeHash           string              `json:"code_hash,omitempty"`
	Source             string              `json:"source,omitempty"`               // "deploy" | "rollback"
	ParentDeploymentID *string             `json:"parent_deployment_id,omitempty"` // set when Source=="rollback"
	Snapshot           *DeploymentSnapshot `json:"snapshot,omitempty"`             // env + spawn config at deploy time
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

// FindLatestSucceededByHash returns the most-recent succeeded deployment
// for fnID matching codeHash. Used by Rollback when the caller targets
// by code_hash (no deployment_id) so we can still recover the snapshot
// that was active when that version last shipped. Returns sql.ErrNoRows
// when nothing matches.
func (db *Database) FindLatestSucceededByHash(fnID, codeHash string) (*Deployment, error) {
	row := db.read.QueryRow(
		`SELECT id, function_id, version, status, COALESCE(phase, ''),
		        COALESCE(error_message, ''), submitted_at, started_at,
		        finished_at, duration_ms,
		        COALESCE(code_hash, ''), COALESCE(source, 'deploy'), parent_deployment_id,
		        COALESCE(snapshot, '')
		 FROM deployments
		 WHERE function_id = ? AND code_hash = ? AND status = 'succeeded'
		 ORDER BY submitted_at DESC LIMIT 1`,
		fnID, codeHash,
	)
	return scanDeployment(row)
}

// SetDeploymentSnapshot persists the function's mutable state at the
// moment the deployment succeeded. Called right before FinishDeployment
// in the deploy path. JSON-encodes the snapshot — passing nil clears the
// column. Errors are returned but the call site usually best-efforts
// these, since failing to record the snapshot doesn't invalidate a
// successful deploy.
func (db *Database) SetDeploymentSnapshot(id string, snap *DeploymentSnapshot) error {
	if snap == nil {
		_, err := db.write.Exec(`UPDATE deployments SET snapshot = '' WHERE id = ?`, id)
		return err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = db.write.Exec(
		`UPDATE deployments SET snapshot = ? WHERE id = ?`,
		string(raw), id,
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
		        COALESCE(code_hash, ''), COALESCE(source, 'deploy'), parent_deployment_id,
		        COALESCE(snapshot, '')
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
		        COALESCE(code_hash, ''), COALESCE(source, 'deploy'), parent_deployment_id,
		        COALESCE(snapshot, '')
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
	var snapshot string
	err := row.Scan(
		&d.ID, &d.FunctionID, &d.Version, &d.Status, &d.Phase, &d.ErrorMessage,
		&d.SubmittedAt, &started, &finished, &dur,
		&d.CodeHash, &d.Source, &parent, &snapshot,
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
	if snapshot != "" {
		var snap DeploymentSnapshot
		if err := json.Unmarshal([]byte(snapshot), &snap); err == nil {
			d.Snapshot = &snap
		}
	}
	return &d, nil
}

func scanDeploymentRows(rows *sql.Rows) (*Deployment, error) {
	var d Deployment
	var started, finished sql.NullTime
	var dur sql.NullInt64
	var parent sql.NullString
	var snapshot string
	err := rows.Scan(
		&d.ID, &d.FunctionID, &d.Version, &d.Status, &d.Phase, &d.ErrorMessage,
		&d.SubmittedAt, &started, &finished, &dur,
		&d.CodeHash, &d.Source, &parent, &snapshot,
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
	if snapshot != "" {
		var snap DeploymentSnapshot
		if err := json.Unmarshal([]byte(snapshot), &snap); err == nil {
			d.Snapshot = &snap
		}
	}
	return &d, nil
}
