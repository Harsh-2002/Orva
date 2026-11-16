package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrSessionIdentityUnavailable deliberately hides credentials and SQL diagnostics.
var ErrSessionIdentityUnavailable = errors.New("session identity unavailable")

// SessionIdentity is provenance, not an authentication or administrator grant.
// IDs are independent of cookies; future services must require trusted principals.
type SessionIdentity struct {
	ID        string
	UserID    int64
	Username  string
	ExpiresAt time.Time
}

func sessionIdentityError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionIdentityUnavailable, err)
	}
	return ErrSessionIdentityUnavailable
}

// ResolveSessionIdentityByToken reads a live session without returning its bearer.
// Uninitialized schema and legacy-created rows without IDs fail closed.
func (db *Database) ResolveSessionIdentityByToken(ctx context.Context, token string) (*SessionIdentity, error) {
	if token == "" {
		return nil, sessionIdentityError(ctx)
	}
	return db.resolveSessionIdentity(ctx, "s.token", token)
}

// ResolveSessionIdentityByID rechecks the current row, user and expiry, not a cache.
// Knowing an ID alone is never authentication; this is a future commit-time read.
func (db *Database) ResolveSessionIdentityByID(ctx context.Context, id string) (*SessionIdentity, error) {
	if !validIAMUUID(id) {
		return nil, sessionIdentityError(ctx)
	}
	return db.resolveSessionIdentity(ctx, "s.id", id)
}

func (db *Database) resolveSessionIdentity(ctx context.Context, column, value string) (*SessionIdentity, error) {
	var s SessionIdentity
	err := db.read.QueryRowContext(ctx, `
		SELECT s.id, u.id, u.username, s.expires_at
		FROM sessions s JOIN users u ON s.user_id = u.id
		WHERE `+column+` = ? AND s.expires_at > CURRENT_TIMESTAMP
		AND (SELECT COUNT(*) FROM sessions other WHERE other.id = s.id) = 1`,
		value,
	).Scan(&s.ID, &s.UserID, &s.Username, &s.ExpiresAt)
	if err != nil || !validIAMUUID(s.ID) || !s.ExpiresAt.After(time.Now()) {
		return nil, sessionIdentityError(ctx)
	}
	s.ExpiresAt = s.ExpiresAt.UTC()
	return &s, nil
}

// Keep sql errors private: driver and trigger diagnostics may contain bearer data.
func sessionIdentityColumn(ctx context.Context, tx *sql.Tx) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'id'`).Scan(&count)
	return count == 1, err
}
