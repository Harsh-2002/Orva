package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type Function struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Runtime            string            `json:"runtime"`
	Entrypoint         string            `json:"entrypoint"`
	Image              string            `json:"image"`
	TimeoutMS          int64             `json:"timeout_ms"`
	MemoryMB           int64             `json:"memory_mb"`
	CPUs               float64           `json:"cpus"`
	EnvVars            map[string]string `json:"env_vars"`
	NetworkMode        string            `json:"network_mode"`
	MaxConcurrency     int               `json:"max_concurrency"`     // 0 = unlimited
	ConcurrencyPolicy  string            `json:"concurrency_policy"`  // "queue" | "reject"
	AuthMode           string            `json:"auth_mode"`           // "none" | "platform_key" | "signed"
	RateLimitPerMin    int               `json:"rate_limit_per_min"`  // per-IP, 0 = unlimited
	Version            int               `json:"version"`
	Status             string            `json:"status"`
	CodeHash           string            `json:"code_hash"`
	ImageSize          int64             `json:"image_size"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
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
	AuthModeNone         = "none"
	AuthModePlatformKey  = "platform_key"
	AuthModeSigned       = "signed"
	SigningSecretKey     = "ORVA_SIGNING_SECRET"
	SignatureHeader      = "X-Orva-Signature"
	SignatureTimestamp   = "X-Orva-Timestamp"
)

func ValidAuthMode(s string) bool {
	switch s {
	case "", AuthModeNone, AuthModePlatformKey, AuthModeSigned:
		return true
	}
	return false
}

func (db *Database) InsertFunction(fn *Function) error {
	envJSON, err := json.Marshal(fn.EnvVars)
	if err != nil {
		return fmt.Errorf("marshal env vars: %w", err)
	}

	err = db.write.QueryRow(`
		INSERT INTO functions (id, name, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, max_concurrency, concurrency_policy, auth_mode, rate_limit_per_min, version, status, code_hash, image_size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING created_at, updated_at`,
		fn.ID, fn.Name, fn.Runtime, fn.Entrypoint, fn.Image,
		fn.TimeoutMS, fn.MemoryMB, fn.CPUs, string(envJSON),
		fn.NetworkMode, fn.MaxConcurrency, fn.ConcurrencyPolicy,
		fn.AuthMode, fn.RateLimitPerMin,
		fn.Version, fn.Status, fn.CodeHash, fn.ImageSize,
	).Scan(&fn.CreatedAt, &fn.UpdatedAt)
	return err
}

func (db *Database) GetFunction(id string) (*Function, error) {
	return scanFunction(db.read.QueryRow(`SELECT id, name, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, max_concurrency, concurrency_policy, auth_mode, rate_limit_per_min, version, status, code_hash, image_size, created_at, updated_at FROM functions WHERE id = ?`, id))
}

func (db *Database) GetFunctionByName(name string) (*Function, error) {
	return scanFunction(db.read.QueryRow(`SELECT id, name, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, max_concurrency, concurrency_policy, auth_mode, rate_limit_per_min, version, status, code_hash, image_size, created_at, updated_at FROM functions WHERE name = ?`, name))
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

	query := "SELECT id, name, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, max_concurrency, concurrency_policy, auth_mode, rate_limit_per_min, version, status, code_hash, image_size, created_at, updated_at FROM functions WHERE 1=1"
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
			name = ?, runtime = ?, entrypoint = ?, image = ?,
			timeout_ms = ?, memory_mb = ?, cpus = ?, env_vars = ?,
			network_mode = ?, max_concurrency = ?, concurrency_policy = ?,
			auth_mode = ?, rate_limit_per_min = ?,
			version = ?, status = ?, code_hash = ?,
			image_size = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING created_at, updated_at`,
		fn.Name, fn.Runtime, fn.Entrypoint, fn.Image,
		fn.TimeoutMS, fn.MemoryMB, fn.CPUs, string(envJSON),
		fn.NetworkMode, fn.MaxConcurrency, fn.ConcurrencyPolicy,
		fn.AuthMode, fn.RateLimitPerMin,
		fn.Version, fn.Status, fn.CodeHash,
		fn.ImageSize, fn.ID,
	).Scan(&fn.CreatedAt, &fn.UpdatedAt)
	return err
}

func (db *Database) DeleteFunction(id string) error {
	_, err := db.write.Exec("DELETE FROM functions WHERE id = ?", id)
	return err
}

func scanFunction(row *sql.Row) (*Function, error) {
	var fn Function
	var envJSON string
	var image, codeHash sql.NullString

	err := row.Scan(
		&fn.ID, &fn.Name, &fn.Runtime, &fn.Entrypoint, &image,
		&fn.TimeoutMS, &fn.MemoryMB, &fn.CPUs, &envJSON,
		&fn.NetworkMode, &fn.MaxConcurrency, &fn.ConcurrencyPolicy,
		&fn.AuthMode, &fn.RateLimitPerMin,
		&fn.Version, &fn.Status, &codeHash,
		&fn.ImageSize, &fn.CreatedAt, &fn.UpdatedAt,
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
		&fn.ID, &fn.Name, &fn.Runtime, &fn.Entrypoint, &image,
		&fn.TimeoutMS, &fn.MemoryMB, &fn.CPUs, &envJSON,
		&fn.NetworkMode, &fn.MaxConcurrency, &fn.ConcurrencyPolicy,
		&fn.AuthMode, &fn.RateLimitPerMin,
		&fn.Version, &fn.Status, &codeHash,
		&fn.ImageSize, &fn.CreatedAt, &fn.UpdatedAt,
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
