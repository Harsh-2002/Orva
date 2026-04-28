package database

import (
	"database/sql"
	"encoding/json"
	"time"
)

type APIKey struct {
	ID          string     `json:"id"`
	KeyHash     string     `json:"-"`
	Prefix      string     `json:"prefix"` // first ~12 chars of plaintext, captured at create time so the UI can identify keys after the secret is gone.
	Name        string     `json:"name"`
	Permissions string     `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// PermissionsList returns Permissions parsed as a string slice. It gracefully
// degrades to an empty slice on malformed JSON so callers never see the raw
// storage form.
func (k *APIKey) PermissionsList() []string {
	if k.Permissions == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(k.Permissions), &out); err != nil {
		return []string{}
	}
	return out
}

// MarshalJSON emits Permissions as a proper JSON array rather than the stored
// JSON-text string — fixes the double-encoding bug on GET /api/v1/keys.
func (k *APIKey) MarshalJSON() ([]byte, error) {
	type alias APIKey
	return json.Marshal(&struct {
		*alias
		Permissions []string `json:"permissions"`
	}{
		alias:       (*alias)(k),
		Permissions: k.PermissionsList(),
	})
}

func (db *Database) InsertAPIKey(key *APIKey) error {
	_, err := db.write.Exec(`
		INSERT INTO api_keys (id, key_hash, key_prefix, name, permissions, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key.ID, key.KeyHash, key.Prefix, key.Name, key.Permissions, key.ExpiresAt,
	)
	return err
}

func (db *Database) GetAPIKeyByHash(hash string) (*APIKey, error) {
	var key APIKey
	var lastUsed, expires sql.NullTime

	err := db.read.QueryRow(`
		SELECT id, key_hash, COALESCE(key_prefix, ''), name, permissions, created_at, last_used_at, expires_at
		FROM api_keys WHERE key_hash = ?`, hash,
	).Scan(&key.ID, &key.KeyHash, &key.Prefix, &key.Name, &key.Permissions, &key.CreatedAt, &lastUsed, &expires)
	if err != nil {
		return nil, err
	}

	if lastUsed.Valid {
		key.LastUsedAt = &lastUsed.Time
	}
	if expires.Valid {
		key.ExpiresAt = &expires.Time
	}
	return &key, nil
}

func (db *Database) ListAPIKeys() ([]*APIKey, error) {
	rows, err := db.read.Query(`
		SELECT id, key_hash, COALESCE(key_prefix, ''), name, permissions, created_at, last_used_at, expires_at
		FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*APIKey
	for rows.Next() {
		var key APIKey
		var lastUsed, expires sql.NullTime
		err := rows.Scan(&key.ID, &key.KeyHash, &key.Prefix, &key.Name, &key.Permissions, &key.CreatedAt, &lastUsed, &expires)
		if err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			key.LastUsedAt = &lastUsed.Time
		}
		if expires.Valid {
			key.ExpiresAt = &expires.Time
		}
		keys = append(keys, &key)
	}
	return keys, nil
}

func (db *Database) DeleteAPIKey(id string) error {
	_, err := db.write.Exec("DELETE FROM api_keys WHERE id = ?", id)
	return err
}

func (db *Database) UpdateAPIKeyLastUsed(hash string) error {
	_, err := db.write.Exec(
		"UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE key_hash = ?",
		hash,
	)
	return err
}

func (db *Database) CountAPIKeys() (int, error) {
	var count int
	err := db.read.QueryRow("SELECT COUNT(*) FROM api_keys").Scan(&count)
	return count, err
}

// PoolConfig represents a per-function pool configuration. The autoscaler
// reads this at pool creation time; runtime changes require pool recreation
// (restart Orva, or wait for the pool to be torn down).
type PoolConfig struct {
	FunctionID        string `json:"function_id"`
	MinWarm           int    `json:"min_warm"`
	MaxWarm           int    `json:"max_warm"`
	IdleTTLS          int    `json:"idle_ttl_seconds"`
	TargetConcurrency int    `json:"target_concurrency"` // req/worker before scale-up considered (Knative-style)
	ScaleToZero       bool   `json:"scale_to_zero"`      // if true, scale down to 0 when idle (cold-start on next req)
}

func (db *Database) UpsertPoolConfig(cfg *PoolConfig) error {
	sc := 0
	if cfg.ScaleToZero {
		sc = 1
	}
	_, err := db.write.Exec(`
		INSERT INTO pool_config (function_id, min_warm, max_warm, idle_ttl_s, target_concurrency, scale_to_zero)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(function_id) DO UPDATE SET
			min_warm = excluded.min_warm,
			max_warm = excluded.max_warm,
			idle_ttl_s = excluded.idle_ttl_s,
			target_concurrency = excluded.target_concurrency,
			scale_to_zero = excluded.scale_to_zero`,
		cfg.FunctionID, cfg.MinWarm, cfg.MaxWarm, cfg.IdleTTLS, cfg.TargetConcurrency, sc,
	)
	return err
}

func (db *Database) GetPoolConfig(functionID string) (*PoolConfig, error) {
	var cfg PoolConfig
	var sc int
	err := db.read.QueryRow(`
		SELECT function_id, min_warm, max_warm, idle_ttl_s,
		       COALESCE(target_concurrency, 10), COALESCE(scale_to_zero, 0)
		FROM pool_config WHERE function_id = ?`, functionID,
	).Scan(&cfg.FunctionID, &cfg.MinWarm, &cfg.MaxWarm, &cfg.IdleTTLS, &cfg.TargetConcurrency, &sc)
	if err != nil {
		return nil, err
	}
	cfg.ScaleToZero = sc != 0
	return &cfg, nil
}
