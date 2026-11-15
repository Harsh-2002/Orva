package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"
)

type Function struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	Runtime           string            `json:"runtime"`
	Entrypoint        string            `json:"entrypoint"`
	Image             string            `json:"image"`
	TimeoutMS         int64             `json:"timeout_ms"`
	MemoryMB          int64             `json:"memory_mb"`
	CPUs              float64           `json:"cpus"`
	EnvVars           map[string]string `json:"env_vars"`
	NetworkMode       string            `json:"network_mode"`
	SDK               bool              `json:"sdk"`
	MaxConcurrency    int               `json:"max_concurrency"`    // 0 = unlimited
	ConcurrencyPolicy string            `json:"concurrency_policy"` // "queue" | "reject"
	AuthMode          string            `json:"auth_mode"`          // "none" | "platform_key" | "signed"
	RateLimitPerMin   int               `json:"rate_limit_per_min"` // per-IP, 0 = unlimited
	Version           int               `json:"version"`
	Status            string            `json:"status"`
	CodeHash          string            `json:"code_hash"`
	// RunEntrypoint is the file the sandbox executes when it differs from the
	// file the operator authored, which happens only for TypeScript (tsc emits
	// dist/handler.js from handler.ts). Empty means "same as Entrypoint".
	RunEntrypoint string `json:"run_entrypoint"`
	// ActiveDeploymentID names the deployment currently serving. Rollback
	// moves this pointer rather than appending a new deployment, so the
	// history stays "the versions you deployed" and exactly one is live.
	ActiveDeploymentID string    `json:"active_deployment_id"`
	ImageSize          int64     `json:"image_size"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Clone returns a deep copy safe to hand out and mutate independently.
//
// EnvVars is the only reference-typed field, so it is the only one that
// needs work; nil-ness is preserved because "no env" and "empty env" are
// distinguishable at the call sites that build spawn environments.
// time.Time carries a *Location pointer that must NOT be deep-copied —
// locations are immutable and shared by design.
//
// TestFunctionCloneCoversEveryReferenceField pins this against a future
// field being added without updating it.
func (f *Function) Clone() *Function {
	if f == nil {
		return nil
	}
	cp := *f
	if f.EnvVars != nil {
		cp.EnvVars = maps.Clone(f.EnvVars)
	}
	return &cp
}

// ConcurrencyPolicy values.
const (
	ConcurrencyPolicyQueue  = "queue"
	ConcurrencyPolicyReject = "reject"
)

// ValidConcurrencyPolicy reports whether the value is acceptable.
func ValidConcurrencyPolicy(s string) bool {
	switch s {
	case "", ConcurrencyPolicyQueue, ConcurrencyPolicyReject:
		return true
	}
	return false
}

// NetworkMode values. "none" is the default; the sandbox runs in a
// new net namespace with only loopback. "egress" enables nsjail's
// --use_pasta integration, giving the function a userspace TCP/UDP
// stack that NATs out via the host. The empty string is treated as
// "none" so older rows / pre-toggle clients keep their isolation.
const (
	NetworkModeNone   = "none"
	NetworkModeEgress = "egress"
)

// ValidNetworkMode reports whether the value is one we accept on the
// API. Empty is allowed and means "default to none" — applied at the
// handler layer.
func ValidNetworkMode(s string) bool {
	switch s {
	case "", NetworkModeNone, NetworkModeEgress:
		return true
	}
	return false
}

// AuthMode values control how the invoke path validates incoming requests.
//
//	"none"          — public, no platform-side auth (default; matches
//	                  Cloudflare Workers / Vercel Functions / Lambda Function
//	                  URLs with AuthorizationType=NONE). The function code is
//	                  expected to verify a JWT, signed cookie, HMAC, etc.
//	                  itself if it needs identity.
//	"platform_key"  — require an Orva API key (X-Orva-API-Key header) or a
//	                  valid session cookie. Useful for "internal-only"
//	                  functions called by your own server / CI / cron.
//	"signed"        — require an HMAC-SHA256 signature in X-Orva-Signature.
//	                  Secret lives in the function's secret store under the
//	                  key ORVA_SIGNING_SECRET.
const (
	AuthModeNone        = "none"
	AuthModePlatformKey = "platform_key"
	AuthModeSigned      = "signed"
	SigningSecretKey    = "ORVA_SIGNING_SECRET"
	SignatureHeader     = "X-Orva-Signature"
	SignatureTimestamp  = "X-Orva-Timestamp"
)

func ValidAuthMode(s string) bool {
	switch s {
	case "", AuthModeNone, AuthModePlatformKey, AuthModeSigned:
		return true
	}
	return false
}

func (db *Database) InsertFunction(fn *Function) error {
	db.lifecycleMu.Lock()
	defer db.lifecycleMu.Unlock()
	envJSON, err := json.Marshal(fn.EnvVars)
	if err != nil {
		return fmt.Errorf("marshal env vars: %w", err)
	}

	err = db.write.QueryRow(`
		INSERT INTO functions (id, name, description, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, sdk, max_concurrency, concurrency_policy, auth_mode, rate_limit_per_min, version, status, code_hash, image_size, active_deployment_id, run_entrypoint)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING created_at, updated_at`,
		fn.ID, fn.Name, fn.Description, fn.Runtime, fn.Entrypoint, fn.Image,
		fn.TimeoutMS, fn.MemoryMB, fn.CPUs, string(envJSON),
		fn.NetworkMode, fn.SDK, fn.MaxConcurrency, fn.ConcurrencyPolicy,
		fn.AuthMode, fn.RateLimitPerMin,
		fn.Version, fn.Status, fn.CodeHash, fn.ImageSize, fn.ActiveDeploymentID, fn.RunEntrypoint,
	).Scan(&fn.CreatedAt, &fn.UpdatedAt)
	if err == nil {
		db.liveFunctions.Store(fn.ID, struct{}{})
	}
	return err
}

func (db *Database) GetFunction(id string) (*Function, error) {
	return scanFunction(db.read.QueryRow(`SELECT id, name, description, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, sdk, max_concurrency, concurrency_policy, auth_mode, rate_limit_per_min, version, status, code_hash, image_size, active_deployment_id, run_entrypoint, created_at, updated_at FROM functions WHERE id = ?`, id))
}

func (db *Database) GetFunctionByName(name string) (*Function, error) {
	return scanFunction(db.read.QueryRow(`SELECT id, name, description, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, sdk, max_concurrency, concurrency_policy, auth_mode, rate_limit_per_min, version, status, code_hash, image_size, active_deployment_id, run_entrypoint, created_at, updated_at FROM functions WHERE name = ?`, name))
}

type ListFunctionsParams struct {
	Status  string
	Runtime string
	Limit   int
	Offset  int
}

type ListFunctionsResult struct {
	Functions []*Function `json:"functions"`
	Total     int         `json:"total"`
}

func (db *Database) ListFunctions(params ListFunctionsParams) (*ListFunctionsResult, error) {
	if params.Limit <= 0 {
		params.Limit = 20
	}
	if params.Limit > 1000 {
		params.Limit = 1000
	}

	query := "SELECT id, name, description, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, sdk, max_concurrency, concurrency_policy, auth_mode, rate_limit_per_min, version, status, code_hash, image_size, active_deployment_id, run_entrypoint, created_at, updated_at FROM functions WHERE 1=1"
	countQuery := "SELECT COUNT(*) FROM functions WHERE 1=1"
	var args []any

	if params.Status != "" {
		query += " AND status = ?"
		countQuery += " AND status = ?"
		args = append(args, params.Status)
	}
	if params.Runtime != "" {
		query += " AND runtime = ?"
		countQuery += " AND runtime = ?"
		args = append(args, params.Runtime)
	}

	var total int
	if err := db.read.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, err
	}

	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, params.Limit, params.Offset)

	rows, err := db.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fns []*Function
	for rows.Next() {
		fn, err := scanFunctionRow(rows)
		if err != nil {
			return nil, err
		}
		fns = append(fns, fn)
	}

	return &ListFunctionsResult{Functions: fns, Total: total}, nil
}

func (db *Database) UpdateFunction(fn *Function) error {
	envJSON, err := json.Marshal(fn.EnvVars)
	if err != nil {
		return fmt.Errorf("marshal env vars: %w", err)
	}

	err = db.write.QueryRow(`
		UPDATE functions SET
			name = ?, description = ?, runtime = ?, entrypoint = ?, image = ?,
			timeout_ms = ?, memory_mb = ?, cpus = ?, env_vars = ?,
			network_mode = ?, sdk = ?, max_concurrency = ?, concurrency_policy = ?,
			auth_mode = ?, rate_limit_per_min = ?,
			version = ?, status = ?, code_hash = ?, active_deployment_id = ?, run_entrypoint = ?,
			image_size = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status != 'deleting'
		RETURNING created_at, updated_at`,
		fn.Name, fn.Description, fn.Runtime, fn.Entrypoint, fn.Image,
		fn.TimeoutMS, fn.MemoryMB, fn.CPUs, string(envJSON),
		fn.NetworkMode, fn.SDK, fn.MaxConcurrency, fn.ConcurrencyPolicy,
		fn.AuthMode, fn.RateLimitPerMin,
		fn.Version, fn.Status, fn.CodeHash, fn.ActiveDeploymentID, fn.RunEntrypoint,
		fn.ImageSize, fn.ID,
	).Scan(&fn.CreatedAt, &fn.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		var status string
		if db.write.QueryRow("SELECT status FROM functions WHERE id = ?", fn.ID).Scan(&status) == nil &&
			status == FunctionStatusDeleting {
			return ErrFunctionDeleting
		}
	}
	return err
}

// FunctionStatusDeleting marks a function whose delete is in progress. It is
// persisted so a restart resumes the delete (ResumeFunctionDeletes).
const FunctionStatusDeleting = "deleting"

// ErrFunctionDeleting is returned by UpdateFunction for a function whose
// delete has started, so a late build, rollback or PUT cannot revive it.
var ErrFunctionDeleting = errors.New("function is being deleted")

// DeleteFunction removes a function in bounded batches: db.write is the single
// connection the async writer commits through, so one transaction deleting
// millions of executions starved every other function (STORAGE_BACKPRESSURE).
// It flips the row to 'deleting', drains the large tables in batches without
// lifecycleMu, then removes the stragglers and the row in one short transaction.
// Idempotent; a cancelled ctx leaves the function 'deleting' and a later call
// finishes it.
func (db *Database) DeleteFunction(ctx context.Context, id string) error {
	// lifecycleMu is held only for the flip: a writer waiting on an RWMutex
	// blocks new RLock calls, which would stall the async commit pass.
	db.lifecycleMu.Lock()
	_, err := db.write.ExecContext(ctx,
		"UPDATE functions SET status = ? WHERE id = ?", FunctionStatusDeleting, id)
	if err == nil {
		db.liveFunctions.Delete(id)
	}
	db.lifecycleMu.Unlock()
	if err != nil {
		return fmt.Errorf("delete %s: mark deleting: %w", id, err)
	}

	for {
		n, err := db.deleteExecutionBatch(ctx, "function_id = ?", id)
		if err != nil {
			return fmt.Errorf("delete %s: executions: %w", id, err)
		}
		if n < purgeChunk {
			break
		}
		if err := sleepCtx(ctx, purgePause); err != nil {
			return fmt.Errorf("delete %s: %w", id, err)
		}
	}

	for _, t := range executionOwnedChildTables {
		if _, err := db.deleteRowsChunked(ctx, t, "function_id = ?", id); err != nil {
			return fmt.Errorf("delete %s: %s: %w", id, t, err)
		}
	}
	// The other cascade tables that grow with use. deployments, routes, crons,
	// secrets, fixtures, inbound webhooks, pool_config and channel_functions are
	// operator-authored and small, so the final cascade removes them.
	for _, c := range []struct{ table, cond string }{
		{"kv_store", "function_id = ?"},
		{"jobs", "function_id = ?"},
		{"build_logs", "deployment_id IN (SELECT id FROM deployments WHERE function_id = ?)"},
	} {
		if _, err := db.deleteRowsChunked(ctx, c.table, c.cond, id); err != nil {
			return fmt.Errorf("delete %s: %s: %w", id, c.table, err)
		}
	}
	return db.finishFunctionDelete(ctx, id)
}

// finishFunctionDelete sweeps whatever the batches missed (rows an in-flight
// async batch committed meanwhile) and deletes the row, atomically.
func (db *Database) finishFunctionDelete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	db.lifecycleMu.Lock()
	defer db.lifecycleMu.Unlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, t := range executionChildTables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t+" WHERE execution_id IN ("+
			"SELECT id FROM executions WHERE function_id = ?)", id); err != nil {
			return fmt.Errorf("delete %s: %s: %w", id, t, err)
		}
	}
	for _, t := range executionOwnedChildTables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t+" WHERE function_id = ?", id); err != nil {
			return fmt.Errorf("delete %s: orphan %s: %w", id, t, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM executions WHERE function_id = ?", id); err != nil {
		return fmt.Errorf("delete %s: executions: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM functions WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete %s: function: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete %s: %w", id, err)
	}
	db.liveFunctions.Delete(id)
	return nil
}

// ResumeFunctionDeletes finishes deletes interrupted by a crash or a cancelled
// context, returning how many it completed.
func (db *Database) ResumeFunctionDeletes(ctx context.Context) (int, error) {
	rows, err := db.read.QueryContext(ctx, "SELECT id FROM functions WHERE status = ?", FunctionStatusDeleting)
	if err != nil {
		return 0, fmt.Errorf("list deleting functions: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		if err := db.DeleteFunction(ctx, id); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

func scanFunction(row *sql.Row) (*Function, error) {
	var fn Function
	var envJSON string
	var image, codeHash sql.NullString

	err := row.Scan(
		&fn.ID, &fn.Name, &fn.Description, &fn.Runtime, &fn.Entrypoint, &image,
		&fn.TimeoutMS, &fn.MemoryMB, &fn.CPUs, &envJSON,
		&fn.NetworkMode, &fn.SDK, &fn.MaxConcurrency, &fn.ConcurrencyPolicy,
		&fn.AuthMode, &fn.RateLimitPerMin,
		&fn.Version, &fn.Status, &codeHash,
		&fn.ImageSize, &fn.ActiveDeploymentID, &fn.RunEntrypoint, &fn.CreatedAt, &fn.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	fn.Image = image.String
	fn.CodeHash = codeHash.String
	fn.EnvVars = make(map[string]string)
	json.Unmarshal([]byte(envJSON), &fn.EnvVars)
	return &fn, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFunctionRow(rows *sql.Rows) (*Function, error) {
	var fn Function
	var envJSON string
	var image, codeHash sql.NullString

	err := rows.Scan(
		&fn.ID, &fn.Name, &fn.Description, &fn.Runtime, &fn.Entrypoint, &image,
		&fn.TimeoutMS, &fn.MemoryMB, &fn.CPUs, &envJSON,
		&fn.NetworkMode, &fn.SDK, &fn.MaxConcurrency, &fn.ConcurrencyPolicy,
		&fn.AuthMode, &fn.RateLimitPerMin,
		&fn.Version, &fn.Status, &codeHash,
		&fn.ImageSize, &fn.ActiveDeploymentID, &fn.RunEntrypoint, &fn.CreatedAt, &fn.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	fn.Image = image.String
	fn.CodeHash = codeHash.String
	fn.EnvVars = make(map[string]string)
	json.Unmarshal([]byte(envJSON), &fn.EnvVars)
	return &fn, nil
}
