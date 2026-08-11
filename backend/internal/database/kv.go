package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
	"unicode/utf8"
)

// ErrKVNotFound is returned by KVGet when the key is missing OR expired.
// Callers can use errors.Is to disambiguate from real database errors.
var ErrKVNotFound = errors.New("kv: key not found")

const (
	KVMaxKeyRunes   = 256
	KVMaxValueBytes = 64 * 1024
)

func ValidateKVKey(key string) error {
	if key == "" {
		return errors.New("kv: key is required")
	}
	if !utf8.ValidString(key) {
		return errors.New("kv: key must be valid UTF-8")
	}
	if utf8.RuneCountInString(key) > KVMaxKeyRunes {
		return errors.New("kv: key exceeds 256 characters")
	}
	return nil
}

func ValidateKVValue(value []byte) error {
	if len(value) == 0 || !json.Valid(value) {
		return errors.New("kv: value must be valid JSON")
	}
	if len(value) > KVMaxValueBytes {
		return errors.New("kv: value exceeds 64 KB")
	}
	return nil
}

func ValidateKVTTL(ttlSeconds *int) error {
	if ttlSeconds == nil {
		return nil
	}
	if *ttlSeconds < 0 {
		return errors.New("kv: ttl_seconds must be >= 0")
	}
	if int64(*ttlSeconds) > math.MaxInt64/int64(time.Second) {
		return errors.New("kv: ttl_seconds is too large")
	}
	return nil
}

// KVEntry is the row shape returned by KVGet / KVList.
type KVEntry struct {
	FunctionID string     `json:"function_id"`
	Key        string     `json:"key"`
	Value      []byte     `json:"value"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// KVPut upserts a value. A nil TTL preserves an existing expiry (and makes
// new keys persistent), zero clears expiry, and a positive value sets it.
func (db *Database) KVPut(functionID, key string, value []byte, ttlSeconds *int) error {
	return db.KVPutContext(context.Background(), functionID, key, value, ttlSeconds)
}

func (db *Database) KVPutContext(ctx context.Context, functionID, key string, value []byte, ttlSeconds *int) (err error) {
	started := time.Now()
	defer func() { db.observeKV("put", started, err) }()
	if err := ValidateKVKey(key); err != nil {
		return err
	}
	if err := ValidateKVValue(value); err != nil {
		return err
	}
	now := time.Now().UTC()
	var expires any
	specified := 0
	if err := ValidateKVTTL(ttlSeconds); err != nil {
		return err
	}
	if ttlSeconds != nil {
		specified = 1
		if *ttlSeconds > 0 {
			expires = now.Add(time.Duration(*ttlSeconds) * time.Second)
		}
	}
	_, err = db.write.ExecContext(ctx, `
			INSERT INTO kv_store (function_id, key, value, expires_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(function_id, key) DO UPDATE SET
				value      = excluded.value,
				expires_at = CASE
					WHEN ? = 1 THEN excluded.expires_at
					WHEN kv_store.expires_at IS NOT NULL AND kv_store.expires_at <= ? THEN NULL
					ELSE kv_store.expires_at
				END,
				updated_at = excluded.updated_at`,
		functionID, key, value, expires, now, now, specified, now)
	return err
}

// KVGet returns the entry for (function_id, key) or ErrKVNotFound if the
// key is missing or expired. Expired rows are not auto-deleted here — the
// scheduler's TTL sweep handles bulk cleanup.
func (db *Database) KVGet(functionID, key string) (*KVEntry, error) {
	return db.KVGetContext(context.Background(), functionID, key)
}

func (db *Database) KVGetContext(ctx context.Context, functionID, key string) (entry *KVEntry, err error) {
	started := time.Now()
	defer func() { db.observeKV("get", started, err) }()
	if err := ValidateKVKey(key); err != nil {
		return nil, err
	}
	var e KVEntry
	var expires sql.NullTime
	err = db.read.QueryRowContext(ctx, `
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
	return db.KVDeleteContext(context.Background(), functionID, key)
}

func (db *Database) KVDeleteContext(ctx context.Context, functionID, key string) (err error) {
	started := time.Now()
	defer func() { db.observeKV("delete", started, err) }()
	if err := ValidateKVKey(key); err != nil {
		return err
	}
	_, err = db.write.ExecContext(ctx,
		`DELETE FROM kv_store WHERE function_id = ? AND key = ?`,
		functionID, key)
	return err
}

// KVList returns up to `limit` keys for a function, optionally filtered by
// prefix. Skips expired entries. Used by the dashboard / introspection.
func (db *Database) KVList(functionID, prefix string, limit int) ([]*KVEntry, error) {
	return db.KVListContext(context.Background(), functionID, prefix, limit)
}

func (db *Database) KVListContext(ctx context.Context, functionID, prefix string, limit int) (entries []*KVEntry, err error) {
	started := time.Now()
	defer func() { db.observeKV("list", started, err) }()
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var (
		rows *sql.Rows
		now  = time.Now().UTC()
	)
	if prefix != "" {
		rows, err = db.read.QueryContext(ctx, `
			SELECT function_id, key, value, expires_at, created_at, updated_at
			FROM kv_store
			WHERE function_id = ? AND key LIKE ? AND (expires_at IS NULL OR expires_at > ?)
			ORDER BY key ASC LIMIT ?`,
			functionID, prefix+"%", now, limit)
	} else {
		rows, err = db.read.QueryContext(ctx, `
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
	return db.KVCountContext(context.Background(), functionID)
}

func (db *Database) KVCountContext(ctx context.Context, functionID string) (int, error) {
	var n int
	err := db.read.QueryRowContext(ctx, `
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

// KVListPage is the result shape for cursor-paginated KV list. NextCursor
// is empty when there are no more rows after this page; otherwise it is
// the last key returned and callers should pass it back as the next
// request's `cursor` to continue paging.
type KVListPage struct {
	Entries    []*KVEntry
	NextCursor string
}

// KVListWithCursor returns up to `limit` entries with `key > cursor`,
// optionally filtered by prefix. Pass an empty cursor for the first page.
// The implementation queries `limit+1` rows: if all are returned the last
// is dropped from the result and its key becomes the cursor for the next
// page. This keeps the cursor a stable, exclusive `key > cursor` boundary
// (no offset arithmetic, safe under concurrent inserts).
func (db *Database) KVListWithCursor(functionID, prefix, cursor string, limit int) (*KVListPage, error) {
	return db.KVListWithCursorContext(context.Background(), functionID, prefix, cursor, limit)
}

func (db *Database) KVListWithCursorContext(ctx context.Context, functionID, prefix, cursor string, limit int) (pageResult *KVListPage, err error) {
	started := time.Now()
	defer func() { db.observeKV("list", started, err) }()
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	now := time.Now().UTC()
	probe := limit + 1

	var (
		rows *sql.Rows
	)
	switch {
	case prefix != "" && cursor != "":
		rows, err = db.read.QueryContext(ctx, `
			SELECT function_id, key, value, expires_at, created_at, updated_at
			FROM kv_store
			WHERE function_id = ? AND key LIKE ? AND key > ?
			  AND (expires_at IS NULL OR expires_at > ?)
			ORDER BY key ASC LIMIT ?`,
			functionID, prefix+"%", cursor, now, probe)
	case prefix != "":
		rows, err = db.read.QueryContext(ctx, `
			SELECT function_id, key, value, expires_at, created_at, updated_at
			FROM kv_store
			WHERE function_id = ? AND key LIKE ?
			  AND (expires_at IS NULL OR expires_at > ?)
			ORDER BY key ASC LIMIT ?`,
			functionID, prefix+"%", now, probe)
	case cursor != "":
		rows, err = db.read.QueryContext(ctx, `
			SELECT function_id, key, value, expires_at, created_at, updated_at
			FROM kv_store
			WHERE function_id = ? AND key > ?
			  AND (expires_at IS NULL OR expires_at > ?)
			ORDER BY key ASC LIMIT ?`,
			functionID, cursor, now, probe)
	default:
		rows, err = db.read.QueryContext(ctx, `
			SELECT function_id, key, value, expires_at, created_at, updated_at
			FROM kv_store
			WHERE function_id = ?
			  AND (expires_at IS NULL OR expires_at > ?)
			ORDER BY key ASC LIMIT ?`,
			functionID, now, probe)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*KVEntry, 0, limit)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}

	page := &KVListPage{Entries: out}
	if len(out) > limit {
		page.Entries = out[:limit]
		page.NextCursor = out[limit-1].Key
	}
	return page, nil
}

// KVIncr atomically reads the integer value at (function_id, key),
// increments by delta (which may be negative), writes it back, and returns
// the new value. The whole operation runs inside a single SQLite write
// transaction — SQLite serialises writers so concurrent callers always
// see consistent counters. Missing keys are treated as 0. A value that
// is not a JSON-integer returns an error.
//
// A nil TTL preserves an existing expiry (and makes new keys persistent),
// zero clears expiry, and a positive value sets or refreshes it.
func (db *Database) KVIncr(functionID, key string, delta int64, ttlSeconds *int) (int64, error) {
	return db.KVIncrContext(context.Background(), functionID, key, delta, ttlSeconds)
}

func (db *Database) KVIncrContext(ctx context.Context, functionID, key string, delta int64, ttlSeconds *int) (value int64, err error) {
	started := time.Now()
	defer func() { db.observeKV("incr", started, err) }()
	if err := ValidateKVKey(key); err != nil {
		return 0, err
	}
	if err := ValidateKVTTL(ttlSeconds); err != nil {
		return 0, err
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		raw     []byte
		expires sql.NullTime
	)
	err = tx.QueryRowContext(ctx,
		`SELECT value, expires_at FROM kv_store WHERE function_id = ? AND key = ?`,
		functionID, key,
	).Scan(&raw, &expires)

	var current int64 = 0
	now := time.Now().UTC()
	exists := err == nil
	expired := exists && expires.Valid && expires.Time.Before(now)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if exists && !expired {
		// The KV wire format is JSON; the stored bytes are typically
		// `"42"` (a JSON string holding the digits) or `42` (a bare
		// number). Accept either to keep the on-wire schema flexible.
		s := string(raw)
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		}
		if _, err := fmtInt(&current, s); err != nil {
			return 0, errors.New("kv.incr: existing value is not an integer")
		}
	}

	next := current + delta

	var expiresVal any
	switch {
	case ttlSeconds != nil && *ttlSeconds > 0:
		expiresVal = now.Add(time.Duration(*ttlSeconds) * time.Second)
	case ttlSeconds == nil && exists && !expired && expires.Valid:
		// Preserve any existing TTL.
		expiresVal = expires.Time
	}

	newBytes := []byte(fmtIntStr(next))
	if exists {
		_, err = tx.ExecContext(ctx, `
			UPDATE kv_store SET value = ?, expires_at = ?, updated_at = ?
			WHERE function_id = ? AND key = ?`,
			newBytes, expiresVal, now, functionID, key)
	} else {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO kv_store (function_id, key, value, expires_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			functionID, key, newBytes, expiresVal, now, now)
	}
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

// KVCAS atomically swaps the value at (function_id, key) from `expected`
// to `newValue` only if the current bytes equal `expected`. Returns
// (true, nil) on success, or (false, currentValue) when the precondition
// failed (currentValue may be nil if the row doesn't exist). The whole
// operation runs inside a single SQLite write transaction.
//
// A nil `expected` means "the key must not currently exist" (a guarded
// insert). A successful CAS with positive ttlSeconds sets/refreshes the
// expiry; nil preserves an existing TTL and zero clears it.
func (db *Database) KVCAS(functionID, key string, expected, newValue []byte, ttlSeconds *int) (bool, []byte, error) {
	return db.KVCASContext(context.Background(), functionID, key, expected, newValue, ttlSeconds)
}

func (db *Database) KVCASContext(ctx context.Context, functionID, key string, expected, newValue []byte, ttlSeconds *int) (swapped bool, currentValue []byte, err error) {
	started := time.Now()
	defer func() { db.observeKV("cas", started, err) }()
	if err := ValidateKVKey(key); err != nil {
		return false, nil, err
	}
	if err := ValidateKVValue(newValue); err != nil {
		return false, nil, err
	}
	if expected != nil {
		if err := ValidateKVValue(expected); err != nil {
			return false, nil, errors.New("kv: expected value is invalid: " + err.Error())
		}
	}
	if err := ValidateKVTTL(ttlSeconds); err != nil {
		return false, nil, err
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		raw     []byte
		expires sql.NullTime
	)
	err = tx.QueryRowContext(ctx,
		`SELECT value, expires_at FROM kv_store WHERE function_id = ? AND key = ?`,
		functionID, key,
	).Scan(&raw, &expires)

	now := time.Now().UTC()
	exists := err == nil
	expired := exists && expires.Valid && expires.Time.Before(now)
	if err != nil && err != sql.ErrNoRows {
		return false, nil, err
	}

	// "Currently present" for CAS purposes excludes expired rows — they
	// are functionally gone, even though the row is still on disk.
	currentlyPresent := exists && !expired

	switch {
	case expected == nil && !currentlyPresent:
		// Insert-if-absent path.
	case expected != nil && currentlyPresent && bytesEq(raw, expected):
		// Update-if-matching path.
	default:
		// Pre-condition failed. Return current value (or nil if absent
		// / expired) so callers can decide whether to retry.
		if !currentlyPresent {
			return false, nil, nil
		}
		return false, raw, nil
	}

	var expiresVal any
	switch {
	case ttlSeconds != nil && *ttlSeconds > 0:
		expiresVal = now.Add(time.Duration(*ttlSeconds) * time.Second)
	case ttlSeconds == nil && currentlyPresent && expires.Valid:
		expiresVal = expires.Time
	}

	if exists {
		_, err = tx.ExecContext(ctx, `
			UPDATE kv_store SET value = ?, expires_at = ?, updated_at = ?
			WHERE function_id = ? AND key = ?`,
			newValue, expiresVal, now, functionID, key)
	} else {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO kv_store (function_id, key, value, expires_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			functionID, key, newValue, expiresVal, now, now)
	}
	if err != nil {
		return false, nil, err
	}
	if err := tx.Commit(); err != nil {
		return false, nil, err
	}
	return true, nil, nil
}

// KVBatchOp is one operation inside a batch request. Op is "get", "put",
// or "delete". For "put", Value and TTLSeconds apply; the others ignore
// them.
type KVBatchOp struct {
	Op         string `json:"op"`
	Key        string `json:"key"`
	Value      []byte `json:"value,omitempty"`
	TTLSeconds *int   `json:"ttl_seconds,omitempty"`
}

// KVBatchResult is the per-op outcome. For "get": Value and ExpiresAt are
// populated when the key existed. For "put"/"delete", Found reports whether
// the write affected a key. Validation and database failures abort the batch.
type KVBatchResult struct {
	Op        string     `json:"op"`
	Key       string     `json:"key"`
	Found     bool       `json:"found"`
	Value     []byte     `json:"value,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Err       string     `json:"error,omitempty"`
}

// KVBatch executes the given ops atomically in a single write transaction.
// Every operation is validated before the transaction starts and any database
// failure rolls back all writes. Result order matches operation order.
func (db *Database) KVBatch(functionID string, ops []KVBatchOp) ([]KVBatchResult, error) {
	return db.KVBatchContext(context.Background(), functionID, ops)
}

func (db *Database) KVBatchContext(ctx context.Context, functionID string, ops []KVBatchOp) (batchResults []KVBatchResult, err error) {
	started := time.Now()
	txStarted := false
	defer func() {
		db.observeKV("batch", started, err)
		if txStarted && err != nil {
			db.kv.rollbacks.Add(1)
		}
	}()
	results := make([]KVBatchResult, len(ops))
	if len(ops) == 0 {
		return results, nil
	}
	if len(ops) > 100 {
		return nil, errors.New("kv batch: capped at 100 operations")
	}
	for _, op := range ops {
		if err := ValidateKVKey(op.Key); err != nil {
			return nil, err
		}
		switch op.Op {
		case "get", "delete":
		case "put":
			if err := ValidateKVValue(op.Value); err != nil {
				return nil, err
			}
			if err := ValidateKVTTL(op.TTLSeconds); err != nil {
				return nil, err
			}
		default:
			return nil, errors.New("kv batch: unknown op: " + op.Op)
		}
	}

	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	txStarted = true
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	for i, op := range ops {
		r := KVBatchResult{Op: op.Op, Key: op.Key}
		switch op.Op {
		case "get":
			var raw []byte
			var expires sql.NullTime
			err := tx.QueryRowContext(ctx,
				`SELECT value, expires_at FROM kv_store WHERE function_id = ? AND key = ?`,
				functionID, op.Key,
			).Scan(&raw, &expires)
			if err == sql.ErrNoRows {
				// Found = false, no value.
			} else if err != nil {
				return nil, err
			} else {
				if expires.Valid && expires.Time.Before(now) {
					// Expired — treat as not found.
				} else {
					r.Found = true
					r.Value = raw
					if expires.Valid {
						t := expires.Time
						r.ExpiresAt = &t
					}
				}
			}
		case "put":
			var expiresVal any
			specified := 0
			if op.TTLSeconds != nil {
				specified = 1
				if *op.TTLSeconds > 0 {
					expiresVal = now.Add(time.Duration(*op.TTLSeconds) * time.Second)
				}
			}
			_, err := tx.ExecContext(ctx, `
				INSERT INTO kv_store (function_id, key, value, expires_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(function_id, key) DO UPDATE SET
					value      = excluded.value,
						expires_at = CASE
							WHEN ? = 1 THEN excluded.expires_at
							WHEN kv_store.expires_at IS NOT NULL AND kv_store.expires_at <= ? THEN NULL
							ELSE kv_store.expires_at
						END,
						updated_at = excluded.updated_at`,
				functionID, op.Key, op.Value, expiresVal, now, now, specified, now)
			if err != nil {
				return nil, err
			} else {
				r.Found = true
			}
		case "delete":
			res, err := tx.ExecContext(ctx,
				`DELETE FROM kv_store WHERE function_id = ? AND key = ?`,
				functionID, op.Key)
			if err != nil {
				return nil, err
			} else if n, _ := res.RowsAffected(); n > 0 {
				r.Found = true
			}
		default:
			return nil, errors.New("kv batch: unknown op: " + op.Op)
		}
		results[i] = r
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

// bytesEq is a small constant-time-friendly equality check for short
// values. CAS guards never compare secret data so we use the plain
// version; if that changes, switch to subtle.ConstantTimeCompare.
func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fmtInt parses a base-10 integer into *dst and returns it. Tiny helper
// kept here (instead of strconv) so the KV file's parser is centralised
// and any future bigint relaxation has one call site.
func fmtInt(dst *int64, s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	var n int64
	neg := false
	i := 0
	if s[0] == '-' {
		neg = true
		i = 1
	} else if s[0] == '+' {
		i = 1
	}
	if i == len(s) {
		return 0, errors.New("no digits")
	}
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("non-digit")
		}
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	*dst = n
	return n, nil
}

// fmtIntStr formats an int64 as decimal bytes. Avoids strconv import here.
func fmtIntStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
