package database

import (
	"database/sql"
	"errors"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// ErrFixtureNotFound is returned when a fixture lookup misses. Callers
// can use errors.Is to disambiguate from real database errors.
var ErrFixtureNotFound = errors.New("fixture: not found")

// Fixture is one saved request preset for a function. body is the raw
// bytes the editor / MCP tool will resend; HeadersJSON is a JSON-encoded
// object (e.g. {"X-Foo":"bar"}) so the storage stays opaque to header
// ordering quirks.
type Fixture struct {
	ID          string    `json:"id"`
	FunctionID  string    `json:"function_id"`
	Name        string    `json:"name"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	HeadersJSON string    `json:"headers_json"`
	Body        []byte    `json:"body,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NewFixtureID returns a fresh UUIDv7. Replaces the legacy fix_<hex> form.
func NewFixtureID() string { return ids.New() }

// InsertFixture inserts a new fixture. Fails on UNIQUE(function_id,name)
// conflict — callers that want upsert semantics should use UpsertFixture.
func (db *Database) InsertFixture(f *Fixture) error {
	if f.ID == "" {
		f.ID = NewFixtureID()
	}
	if f.HeadersJSON == "" {
		f.HeadersJSON = "{}"
	}
	now := time.Now().UTC()
	f.CreatedAt = now
	f.UpdatedAt = now
	_, err := db.write.Exec(`
		INSERT INTO fixtures
			(id, function_id, name, method, path, headers_json, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.FunctionID, f.Name, f.Method, f.Path, f.HeadersJSON, f.Body,
		f.CreatedAt.UnixMilli(), f.UpdatedAt.UnixMilli(),
	)
	return err
}

// UpsertFixture inserts or replaces a fixture identified by (function_id,
// name). On conflict the id, created_at, method, path, headers, and body
// are all updated; updated_at is bumped. Returns the canonical row.
func (db *Database) UpsertFixture(f *Fixture) (*Fixture, error) {
	if f.ID == "" {
		f.ID = NewFixtureID()
	}
	if f.HeadersJSON == "" {
		f.HeadersJSON = "{}"
	}
	now := time.Now().UTC()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	f.UpdatedAt = now
	_, err := db.write.Exec(`
		INSERT INTO fixtures
			(id, function_id, name, method, path, headers_json, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(function_id, name) DO UPDATE SET
			method       = excluded.method,
			path         = excluded.path,
			headers_json = excluded.headers_json,
			body         = excluded.body,
			updated_at   = excluded.updated_at`,
		f.ID, f.FunctionID, f.Name, f.Method, f.Path, f.HeadersJSON, f.Body,
		f.CreatedAt.UnixMilli(), f.UpdatedAt.UnixMilli(),
	)
	if err != nil {
		return nil, err
	}
	// Re-read so callers see the canonical (id, created_at) — these stay
	// intact when the row already existed and the upsert fell through to
	// UPDATE. Keeps the API response stable across insert vs. update.
	return db.GetFixtureByName(f.FunctionID, f.Name)
}

// UpdateFixture replaces an existing fixture by id. Used for full PUT
// semantics where the caller already knows the id (e.g. dashboards).
// Returns ErrFixtureNotFound if no row matches.
func (db *Database) UpdateFixture(f *Fixture) error {
	if f.ID == "" {
		return errors.New("fixture id is required")
	}
	if f.HeadersJSON == "" {
		f.HeadersJSON = "{}"
	}
	f.UpdatedAt = time.Now().UTC()
	res, err := db.write.Exec(`
		UPDATE fixtures
		SET name = ?, method = ?, path = ?, headers_json = ?, body = ?, updated_at = ?
		WHERE id = ?`,
		f.Name, f.Method, f.Path, f.HeadersJSON, f.Body, f.UpdatedAt.UnixMilli(), f.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrFixtureNotFound
	}
	return nil
}

// DeleteFixture removes a fixture by (function_id, name). Idempotent —
// no error if the row never existed (matches KV / route semantics).
func (db *Database) DeleteFixture(functionID, name string) error {
	_, err := db.write.Exec(
		`DELETE FROM fixtures WHERE function_id = ? AND name = ?`,
		functionID, name,
	)
	return err
}

// ListFixtures returns all fixtures for a function ordered by name.
// Bodies are included — they're typically tiny (≤1 KB) and surfacing them
// in the list keeps the editor's "click → preview" interaction zero
// extra round-trips.
func (db *Database) ListFixtures(functionID string) ([]*Fixture, error) {
	rows, err := db.read.Query(`
		SELECT id, function_id, name, method, path, headers_json, body, created_at, updated_at
		FROM fixtures
		WHERE function_id = ?
		ORDER BY name ASC`, functionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*Fixture, 0)
	for rows.Next() {
		f, err := scanFixture(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFixtureByName looks up one fixture by (function_id, name). Returns
// ErrFixtureNotFound when the row is missing.
func (db *Database) GetFixtureByName(functionID, name string) (*Fixture, error) {
	row := db.read.QueryRow(`
		SELECT id, function_id, name, method, path, headers_json, body, created_at, updated_at
		FROM fixtures WHERE function_id = ? AND name = ?`,
		functionID, name)
	f, err := scanFixture(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFixtureNotFound
	}
	return f, err
}

// scanFixture is the shared row-scan body for List/Get. Accepts a Scan
// fn so the same code works against *sql.Row and *sql.Rows.
func scanFixture(scan func(...any) error) (*Fixture, error) {
	var (
		f                 Fixture
		body              []byte
		createdMS, updMS  int64
	)
	if err := scan(&f.ID, &f.FunctionID, &f.Name, &f.Method, &f.Path, &f.HeadersJSON, &body, &createdMS, &updMS); err != nil {
		return nil, err
	}
	f.Body = body
	f.CreatedAt = time.UnixMilli(createdMS).UTC()
	f.UpdatedAt = time.UnixMilli(updMS).UTC()
	return &f, nil
}
