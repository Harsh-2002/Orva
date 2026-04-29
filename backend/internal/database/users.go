package database

import (
	"crypto/rand"
	"encoding/hex"
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

// DeleteSession removes a session.
func (db *Database) DeleteSession(token string) error {
	_, err := db.write.Exec("DELETE FROM sessions WHERE token = ?", token)
	return err
}
