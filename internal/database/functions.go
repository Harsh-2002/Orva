package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type Function struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Runtime     string            `json:"runtime"`
	Entrypoint  string            `json:"entrypoint"`
	Image       string            `json:"image"`
	TimeoutMS   int64             `json:"timeout_ms"`
	MemoryMB    int64             `json:"memory_mb"`
	CPUs        float64           `json:"cpus"`
	EnvVars     map[string]string `json:"env_vars"`
	NetworkMode string            `json:"network_mode"`
	Version     int               `json:"version"`
	Status      string            `json:"status"`
	CodeHash    string            `json:"code_hash"`
	ImageSize   int64             `json:"image_size"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (db *Database) InsertFunction(fn *Function) error {
	envJSON, err := json.Marshal(fn.EnvVars)
	if err != nil {
		return fmt.Errorf("marshal env vars: %w", err)
	}

	err = db.write.QueryRow(`
		INSERT INTO functions (id, name, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, version, status, code_hash, image_size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING created_at, updated_at`,
		fn.ID, fn.Name, fn.Runtime, fn.Entrypoint, fn.Image,
		fn.TimeoutMS, fn.MemoryMB, fn.CPUs, string(envJSON),
		fn.NetworkMode, fn.Version, fn.Status, fn.CodeHash, fn.ImageSize,
	).Scan(&fn.CreatedAt, &fn.UpdatedAt)
	return err
}

func (db *Database) GetFunction(id string) (*Function, error) {
	return scanFunction(db.read.QueryRow(`SELECT id, name, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, version, status, code_hash, image_size, created_at, updated_at FROM functions WHERE id = ?`, id))
}

func (db *Database) GetFunctionByName(name string) (*Function, error) {
	return scanFunction(db.read.QueryRow(`SELECT id, name, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, version, status, code_hash, image_size, created_at, updated_at FROM functions WHERE name = ?`, name))
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

	query := "SELECT id, name, runtime, entrypoint, image, timeout_ms, memory_mb, cpus, env_vars, network_mode, version, status, code_hash, image_size, created_at, updated_at FROM functions WHERE 1=1"
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
			network_mode = ?, version = ?, status = ?, code_hash = ?,
			image_size = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
		RETURNING created_at, updated_at`,
		fn.Name, fn.Runtime, fn.Entrypoint, fn.Image,
		fn.TimeoutMS, fn.MemoryMB, fn.CPUs, string(envJSON),
		fn.NetworkMode, fn.Version, fn.Status, fn.CodeHash,
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
		&fn.NetworkMode, &fn.Version, &fn.Status, &codeHash,
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
		&fn.NetworkMode, &fn.Version, &fn.Status, &codeHash,
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
