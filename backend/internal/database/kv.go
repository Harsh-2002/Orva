package database

import (
	"database/sql"
	"errors"
	"time"
)

// ErrKVNotFound is returned by KVGet when the key is missing OR expired.
// Callers can use errors.Is to disambiguate from real database errors.
var ErrKVNotFound = errors.New("kv: key not found")

// KVEntry is the row shape returned by KVGet / KVList.
type KVEntry struct {
	FunctionID string     `json:"function_id"`
	Key        string     `json:"key"`
	Value      []byte     `json:"value"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// KVPut upserts a value. ttlSeconds=0 means no expiry; positive values
// set expires_at to now+ttl. Negative values are clamped to 0 (no expiry)
// since negative TTLs would expire immediately and just create churn.
func (db *Database) KVPut(functionID, key string, value []byte, ttlSeconds int) error {
	now := time.Now().UTC()
	var expires any
	if ttlSeconds > 0 {
		expires = now.Add(time.Duration(ttlSeconds) * time.Second)
	}
	_, err := db.write.Exec(`
		INSERT INTO kv_store (function_id, key, value, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(function_id, key) DO UPDATE SET
			value      = excluded.value,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at`,
		functionID, key, value, expires, now, now)
	return err
}

// KVGet returns the entry for (function_id, key) or ErrKVNotFound if the
// key is missing or expired. Expired rows are not auto-deleted here — the
// scheduler's TTL sweep handles bulk cleanup.
func (db *Database) KVGet(functionID, key string) (*KVEntry, error) {
	var e KVEntry
	var expires sql.NullTime
	err := db.read.QueryRow(`
		SELECT function_id, key, value, expires_at, created_at, updated_at
		FROM kv_store
		WHERE function_id = ? AND key = ?`, functionID, key,
	).Scan(&e.FunctionID, &e.Key, &e.Value, &expires, &e.CreatedAt, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrKVNotFound
	}
	if err != nil {
		return nil, err
	}
	if expires.Valid {
		if expires.Time.Before(time.Now().UTC()) {
			return nil, ErrKVNotFound
		}
		t := expires.Time
		e.ExpiresAt = &t
	}
	return &e, nil
}

// KVDelete removes (function_id, key). No error if the row doesn't exist.
func (db *Database) KVDelete(functionID, key string) error {
	_, err := db.write.Exec(
		`DELETE FROM kv_store WHERE function_id = ? AND key = ?`,
		functionID, key)
	return err
}

// KVList returns up to `limit` keys for a function, optionally filtered by
// prefix. Skips expired entries. Used by the dashboard / introspection.
func (db *Database) KVList(functionID, prefix string, limit int) ([]*KVEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
		now  = time.Now().UTC()
	)
	if prefix != "" {
		rows, err = db.read.Query(`
			SELECT function_id, key, value, expires_at, created_at, updated_at
			FROM kv_store
			WHERE function_id = ? AND key LIKE ? AND (expires_at IS NULL OR expires_at > ?)
			ORDER BY key ASC LIMIT ?`,
			functionID, prefix+"%", now, limit)
	} else {
		rows, err = db.read.Query(`
			SELECT function_id, key, value, expires_at, created_at, updated_at
			FROM kv_store
			WHERE function_id = ? AND (expires_at IS NULL OR expires_at > ?)
			ORDER BY key ASC LIMIT ?`,
			functionID, now, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*KVEntry
	for rows.Next() {
		var e KVEntry
		var expires sql.NullTime
		if err := rows.Scan(&e.FunctionID, &e.Key, &e.Value, &expires, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		if expires.Valid {
			t := expires.Time
			e.ExpiresAt = &t
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// KVCount returns the number of live (non-expired) keys for a function.
// Used by the dashboard "X keys" badge so the operator sees the namespace
// size at a glance without having to page through the full list.
func (db *Database) KVCount(functionID string) (int, error) {
	var n int
	err := db.read.QueryRow(`
		SELECT COUNT(*) FROM kv_store
		WHERE function_id = ? AND (expires_at IS NULL OR expires_at > ?)`,
		functionID, time.Now().UTC(),
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// KVSweepExpired deletes all rows whose expires_at is in the past.
// Returns the number of rows removed. Called periodically by the
// scheduler.
func (db *Database) KVSweepExpired() (int64, error) {
	res, err := db.write.Exec(
		`DELETE FROM kv_store WHERE expires_at IS NOT NULL AND expires_at <= ?`,
		time.Now().UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
