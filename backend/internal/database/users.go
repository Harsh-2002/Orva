package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	LastLogin    *time.Time `json:"last_login"`
}

type Session struct {
	Token     string    `json:"token"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CountUsers returns the number of registered users.
func (db *Database) CountUsers() (int, error) {
	var count int
	err := db.read.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

// CreateUser creates a new user with a bcrypt password hash.
func (db *Database) CreateUser(username, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	result, err := db.write.Exec(
		"INSERT INTO users (username, password_hash) VALUES (?, ?)",
		username, string(hash),
	)
	if err != nil {
		return nil, err
	}

	id, _ := result.LastInsertId()
	return &User{ID: id, Username: username, CreatedAt: time.Now()}, nil
}

// AuthenticateUser checks username/password and returns the user if valid.
func (db *Database) AuthenticateUser(username, password string) (*User, error) {
	var user User
	err := db.read.QueryRow(
		"SELECT id, username, password_hash, created_at FROM users WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt)
	if err != nil {
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, err
	}

	// Update last login.
	db.write.Exec("UPDATE users SET last_login = CURRENT_TIMESTAMP WHERE id = ?", user.ID)

	return &user, nil
}

// CreateSession creates a session token for a user. ttl controls expiry;
// pass 0 to use the default of 7 days. The HTTP cookie MaxAge in
// handlers/auth.go is derived from the same configured value.
func (db *Database) CreateSession(userID int64, ttl time.Duration) (*Session, error) {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes)

	session := &Session{
		Token:     token,
		UserID:    userID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}

	_, err := db.write.Exec(
		"INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)",
		session.Token, session.UserID, session.CreatedAt, session.ExpiresAt,
	)
	return session, err
}

// GetSessionUser returns the user for a valid session token.
func (db *Database) GetSessionUser(token string) (*User, error) {
	var user User
	err := db.read.QueryRow(`
		SELECT u.id, u.username, u.created_at
		FROM sessions s JOIN users u ON s.user_id = u.id
		WHERE s.token = ? AND s.expires_at > CURRENT_TIMESTAMP`,
		token,
	).Scan(&user.ID, &user.Username, &user.CreatedAt)
	return &user, err
}

// GetSession returns the raw session row (token, user_id, created_at,
// expires_at) for a valid session. Used by /auth/me + /auth/refresh so
// the UI can show "session expires in X" without a second roundtrip.
func (db *Database) GetSession(token string) (*Session, error) {
	var s Session
	err := db.read.QueryRow(`
		SELECT token, user_id, created_at, expires_at
		FROM sessions
		WHERE token = ? AND expires_at > CURRENT_TIMESTAMP`,
		token,
	).Scan(&s.Token, &s.UserID, &s.CreatedAt, &s.ExpiresAt)
	return &s, err
}

// ListSessionsForUser returns every active session for a user, newest
// first. Used by Settings → Active sessions to show "this device + N
// other browsers signed in." Expired rows are filtered server-side so
// the UI doesn't have to.
func (db *Database) ListSessionsForUser(userID int64) ([]*Session, error) {
	rows, err := db.read.Query(`
		SELECT token, user_id, created_at, expires_at
		FROM sessions
		WHERE user_id = ? AND expires_at > CURRENT_TIMESTAMP
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.Token, &s.UserID, &s.CreatedAt, &s.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// DeleteSessionByPrefix matches the first 16 hex chars of a session
// token and deletes the unique row that hash-prefixes to it. The
// dashboard never sees the full token (only its prefix), so this is
// the safe identifier to expose in URLs without re-creating the
// session-fixation vulnerability the prefix is designed to avoid.
//
// We also accept user_id for the ownership guard — even a leaked
// prefix only lets the legitimate owner act on it.
//
// Returns sql.ErrNoRows if the prefix doesn't match (or matches a
// session belonging to a different user). Returns ErrAmbiguousPrefix
// if more than one row matches — should never happen with 16 hex
// chars (~64 bits of entropy) but the loop is cheap insurance.
func (db *Database) DeleteSessionByPrefix(prefix string, userID int64) error {
	rows, err := db.read.Query(`
		SELECT token FROM sessions
		WHERE user_id = ? AND token LIKE ? || '%'`, userID, prefix)
	if err != nil {
		return err
	}
	var matches []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		matches = append(matches, t)
	}
	rows.Close()
	switch len(matches) {
	case 0:
		return sql.ErrNoRows
	case 1:
		_, err := db.write.Exec("DELETE FROM sessions WHERE token = ?", matches[0])
		return err
	default:
		return ErrAmbiguousSessionPrefix
	}
}

// ErrAmbiguousSessionPrefix is the (unreachable in practice) signal
// that more than one session shares the supplied prefix.
var ErrAmbiguousSessionPrefix = errors.New("session prefix matches multiple rows")

// DeleteSession removes a session.
func (db *Database) DeleteSession(token string) error {
	_, err := db.write.Exec("DELETE FROM sessions WHERE token = ?", token)
	return err
}

// ErrWrongPassword is returned by UpdateUserPassword when the supplied
// current password does not match the stored hash.
var ErrWrongPassword = errors.New("wrong password")

// UpdateUserPassword verifies oldPassword against the stored bcrypt hash,
// then replaces it with a new hash derived from newPassword.
func (db *Database) UpdateUserPassword(userID int64, oldPassword, newPassword string) error {
	var hash string
	err := db.read.QueryRow("SELECT password_hash FROM users WHERE id = ?", userID).Scan(&hash)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPassword)); err != nil {
		return ErrWrongPassword
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.write.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(newHash), userID)
	return err
}
