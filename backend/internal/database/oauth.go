package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ── oauth_clients ────────────────────────────────────────────────────

// OAuthClient represents an application registered via RFC 7591 DCR or
// pre-registration. Public clients (token_endpoint_auth_method="none")
// have a NULL ClientSecretHash and rely on PKCE; confidential clients
// authenticate at /oauth/token with their secret.
type OAuthClient struct {
	ID                       string     `json:"-"`
	ClientID                 string     `json:"client_id"`
	ClientSecretHash         string     `json:"-"`
	ClientName               string     `json:"client_name"`
	ClientURI                string     `json:"client_uri,omitempty"`
	RedirectURIs             string     `json:"-"` // JSON array on disk; use RedirectURIList()
	GrantTypes               string     `json:"-"`
	ResponseTypes            string     `json:"-"`
	TokenEndpointAuthMethod  string     `json:"token_endpoint_auth_method"`
	Scope                    string     `json:"scope"`
	CreatedAt                time.Time  `json:"created_at"`
	RevokedAt                *time.Time `json:"revoked_at,omitempty"`
}

// RedirectURIList parses the JSON-encoded redirect_uris column. Returns
// empty slice on malformed JSON so callers never see raw storage.
func (c *OAuthClient) RedirectURIList() []string {
	return decodeJSONStrings(c.RedirectURIs)
}

func (c *OAuthClient) GrantTypesList() []string    { return decodeJSONStrings(c.GrantTypes) }
func (c *OAuthClient) ResponseTypesList() []string { return decodeJSONStrings(c.ResponseTypes) }

// IsPublic returns true when the client uses PKCE-only authentication
// (no client_secret). Browser-based MCP clients (Claude Code, ChatGPT,
// claude.ai) all register as public clients.
func (c *OAuthClient) IsPublic() bool {
	return c.TokenEndpointAuthMethod == "none" || c.ClientSecretHash == ""
}

func decodeJSONStrings(s string) []string {
	if s == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []string{}
	}
	return out
}

// EncodeStringsAsJSON is a small helper for callers that need to write a
// []string into one of the JSON-encoded TEXT columns.
func EncodeStringsAsJSON(in []string) string {
	if in == nil {
		in = []string{}
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func (db *Database) InsertOAuthClient(c *OAuthClient) error {
	_, err := db.write.Exec(`
		INSERT INTO oauth_clients (
			id, client_id, client_secret_hash, client_name, client_uri,
			redirect_uris, grant_types, response_types,
			token_endpoint_auth_method, scope
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ClientID, nullableString(c.ClientSecretHash),
		c.ClientName, nullableString(c.ClientURI),
		c.RedirectURIs, c.GrantTypes, c.ResponseTypes,
		c.TokenEndpointAuthMethod, c.Scope,
	)
	return err
}

func (db *Database) GetOAuthClientByID(clientID string) (*OAuthClient, error) {
	var c OAuthClient
	var secretHash, clientURI sql.NullString
	var revokedAt sql.NullTime
	err := db.read.QueryRow(`
		SELECT id, client_id, client_secret_hash, client_name, client_uri,
		       redirect_uris, grant_types, response_types,
		       token_endpoint_auth_method, scope, created_at, revoked_at
		FROM oauth_clients WHERE client_id = ?`, clientID,
	).Scan(&c.ID, &c.ClientID, &secretHash, &c.ClientName, &clientURI,
		&c.RedirectURIs, &c.GrantTypes, &c.ResponseTypes,
		&c.TokenEndpointAuthMethod, &c.Scope, &c.CreatedAt, &revokedAt)
	if err != nil {
		return nil, err
	}
	c.ClientSecretHash = secretHash.String
	c.ClientURI = clientURI.String
	if revokedAt.Valid {
		c.RevokedAt = &revokedAt.Time
	}
	return &c, nil
}

// ── oauth_authorization_codes ────────────────────────────────────────

// OAuthAuthorizationCode is a one-shot token bridging /oauth/authorize
// (consent → mint code) and /oauth/token (verify PKCE → mint access token).
// We store the SHA256 hash of the plaintext code, not the code itself,
// to mirror api_keys's at-rest hashing posture.
type OAuthAuthorizationCode struct {
	CodeHash            string     // SHA256 of plaintext
	ClientID            string
	UserID              int64
	RedirectURI         string
	Scope               string
	Resource            string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
	UsedAt              *time.Time
	CreatedAt           time.Time
}

func (db *Database) InsertOAuthAuthorizationCode(c *OAuthAuthorizationCode) error {
	_, err := db.write.Exec(`
		INSERT INTO oauth_authorization_codes (
			code, client_id, user_id, redirect_uri, scope, resource,
			code_challenge, code_challenge_method, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.CodeHash, c.ClientID, c.UserID, c.RedirectURI,
		c.Scope, nullableString(c.Resource),
		c.CodeChallenge, c.CodeChallengeMethod, c.ExpiresAt.UTC(),
	)
	return err
}

// ErrAuthCodeAlreadyUsed is the sentinel returned by RedeemOAuthAuthorizationCode
// when the code is already burned. RFC 6749 §10.5: a single code MUST be
// usable at most once.
var ErrAuthCodeAlreadyUsed = errors.New("authorization code already used")

// RedeemOAuthAuthorizationCode atomically marks an unused, unexpired code
// as used and returns its row. Single SQL statement to prevent the
// well-known code-replay TOCTOU race when a DCR client retries.
func (db *Database) RedeemOAuthAuthorizationCode(codeHash string) (*OAuthAuthorizationCode, error) {
	var c OAuthAuthorizationCode
	var resource sql.NullString
	var usedAt sql.NullTime
	err := db.write.QueryRow(`
		UPDATE oauth_authorization_codes
		SET used_at = CURRENT_TIMESTAMP
		WHERE code = ? AND used_at IS NULL AND expires_at > CURRENT_TIMESTAMP
		RETURNING code, client_id, user_id, redirect_uri, scope, resource,
		          code_challenge, code_challenge_method, expires_at, used_at, created_at`,
		codeHash,
	).Scan(&c.CodeHash, &c.ClientID, &c.UserID, &c.RedirectURI,
		&c.Scope, &resource, &c.CodeChallenge, &c.CodeChallengeMethod,
		&c.ExpiresAt, &usedAt, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrAuthCodeAlreadyUsed
	}
	if err != nil {
		return nil, err
	}
	c.Resource = resource.String
	if usedAt.Valid {
		c.UsedAt = &usedAt.Time
	}
	return &c, nil
}

// SweepExpiredOAuthCodes deletes codes whose expiry passed >1h ago. Called
// from the scheduler every 5 min. Same pattern as the KV sweeper.
func (db *Database) SweepExpiredOAuthCodes() (int64, error) {
	res, err := db.write.Exec(`
		DELETE FROM oauth_authorization_codes
		WHERE expires_at < datetime('now', '-1 hour')`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── oauth_access_tokens ──────────────────────────────────────────────

// OAuthAccessToken wraps an API-key-equivalent permission set bound to a
// (client, user) pair. Access token is used on /mcp; refresh token is
// used on /oauth/token to mint a fresh pair (and rotate the refresh per
// OAuth 2.1 §4.3.1).
type OAuthAccessToken struct {
	ID                string
	AccessTokenHash   string
	RefreshTokenHash  string
	ClientID          string
	UserID            int64
	Scope             string
	Resource          string
	IssuedAt          time.Time
	AccessExpiresAt   time.Time
	RefreshExpiresAt  *time.Time
	RevokedAt         *time.Time
}

// ScopesList parses the space-separated scope string per RFC 6749 §3.3.
// Trims empty entries so accidental double spaces don't create "" scopes.
func (t *OAuthAccessToken) ScopesList() []string {
	if t.Scope == "" {
		return nil
	}
	parts := strings.Fields(t.Scope)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// IsExpired honors revocation AND access-token expiry. Refresh checks
// happen separately at the /oauth/token grant_type=refresh_token path.
func (t *OAuthAccessToken) IsExpired(now time.Time) bool {
	if t.RevokedAt != nil {
		return true
	}
	return now.After(t.AccessExpiresAt)
}

func (db *Database) InsertOAuthAccessToken(t *OAuthAccessToken) error {
	_, err := db.write.Exec(`
		INSERT INTO oauth_access_tokens (
			id, access_token_hash, refresh_token_hash, client_id, user_id,
			scope, resource, access_expires_at, refresh_expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.AccessTokenHash, nullableString(t.RefreshTokenHash),
		t.ClientID, t.UserID, t.Scope, nullableString(t.Resource),
		t.AccessExpiresAt.UTC(),
		nullableTime(t.RefreshExpiresAt),
	)
	return err
}

// GetOAuthAccessTokenByAccessHash is the hot-path lookup the MCP middleware
// calls on every authenticated request that doesn't match an api_key.
func (db *Database) GetOAuthAccessTokenByAccessHash(hash string) (*OAuthAccessToken, error) {
	return db.getOAuthAccessTokenBy("access_token_hash", hash)
}

// GetOAuthAccessTokenByRefreshHash powers the refresh_token grant.
func (db *Database) GetOAuthAccessTokenByRefreshHash(hash string) (*OAuthAccessToken, error) {
	return db.getOAuthAccessTokenBy("refresh_token_hash", hash)
}

func (db *Database) getOAuthAccessTokenBy(column, hash string) (*OAuthAccessToken, error) {
	var t OAuthAccessToken
	var refreshHash, resource sql.NullString
	var refreshExpires sql.NullTime
	var revokedAt sql.NullTime
	q := `
		SELECT id, access_token_hash, refresh_token_hash, client_id, user_id,
		       scope, resource, issued_at, access_expires_at, refresh_expires_at, revoked_at
		FROM oauth_access_tokens WHERE ` + column + ` = ?`
	err := db.read.QueryRow(q, hash).Scan(
		&t.ID, &t.AccessTokenHash, &refreshHash, &t.ClientID, &t.UserID,
		&t.Scope, &resource, &t.IssuedAt, &t.AccessExpiresAt,
		&refreshExpires, &revokedAt,
	)
	if err != nil {
		return nil, err
	}
	t.RefreshTokenHash = refreshHash.String
	t.Resource = resource.String
	if refreshExpires.Valid {
		t.RefreshExpiresAt = &refreshExpires.Time
	}
	if revokedAt.Valid {
		t.RevokedAt = &revokedAt.Time
	}
	return &t, nil
}

// RotateOAuthRefreshToken atomically nulls the OLD refresh hash on a row
// AND inserts a new row carrying the freshly-minted access+refresh pair.
// Must run in one transaction so a concurrent refresh attempt sees either
// the pre- or post-rotation state, never both.
func (db *Database) RotateOAuthRefreshToken(oldRefreshHash string, replacement *OAuthAccessToken) error {
	tx, err := db.write.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`
		UPDATE oauth_access_tokens
		SET refresh_token_hash = NULL, revoked_at = COALESCE(revoked_at, CURRENT_TIMESTAMP)
		WHERE refresh_token_hash = ?`, oldRefreshHash)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrRefreshTokenAlreadyRotated
	}

	if _, err := tx.Exec(`
		INSERT INTO oauth_access_tokens (
			id, access_token_hash, refresh_token_hash, client_id, user_id,
			scope, resource, access_expires_at, refresh_expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		replacement.ID, replacement.AccessTokenHash, nullableString(replacement.RefreshTokenHash),
		replacement.ClientID, replacement.UserID, replacement.Scope, nullableString(replacement.Resource),
		replacement.AccessExpiresAt.UTC(), nullableTime(replacement.RefreshExpiresAt),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrRefreshTokenAlreadyRotated is the sentinel for the refresh-token
// rotation race: the rotation lost — caller should treat as
// invalid_grant per OAuth 2.1 §4.3.1.
var ErrRefreshTokenAlreadyRotated = errors.New("refresh token already rotated or revoked")

// RevokeOAuthAccessToken sets revoked_at on the row identified by either
// hash. Used by /oauth/revoke and the dashboard's "Revoke connector"
// button. Idempotent: returning silently when the token wasn't found
// matches RFC 7009 §2.2 ("the authorization server responds with HTTP
// status code 200 if the token has been revoked successfully or if the
// client submitted an invalid token").
func (db *Database) RevokeOAuthAccessToken(hash string) error {
	_, err := db.write.Exec(`
		UPDATE oauth_access_tokens
		SET revoked_at = COALESCE(revoked_at, CURRENT_TIMESTAMP)
		WHERE access_token_hash = ? OR refresh_token_hash = ?`,
		hash, hash,
	)
	return err
}

// ListActiveOAuthAccessTokens returns the rows that the dashboard's
// Settings → Connected apps card renders. Joined with oauth_clients for
// the friendly name. Filters out revoked + expired rows.
type ConnectedApp struct {
	ID              string    `json:"id"`
	ClientID        string    `json:"client_id"`
	ClientName      string    `json:"client_name"`
	Scope           string    `json:"scope"`
	IssuedAt        time.Time `json:"issued_at"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
}

func (db *Database) ListActiveOAuthAccessTokens(userID int64) ([]*ConnectedApp, error) {
	rows, err := db.read.Query(`
		SELECT t.id, t.client_id, COALESCE(c.client_name, t.client_id),
		       t.scope, t.issued_at, t.access_expires_at
		FROM oauth_access_tokens t
		LEFT JOIN oauth_clients c ON c.client_id = t.client_id
		WHERE t.user_id = ?
		  AND t.revoked_at IS NULL
		  AND t.access_expires_at > CURRENT_TIMESTAMP
		ORDER BY t.issued_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ConnectedApp
	for rows.Next() {
		var a ConnectedApp
		if err := rows.Scan(&a.ID, &a.ClientID, &a.ClientName,
			&a.Scope, &a.IssuedAt, &a.AccessExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// SweepExpiredOAuthTokens removes access-token rows whose access_expires_at
// passed >24 h ago AND whose refresh_token (if any) is also expired or
// nulled. Keeps recently-expired rows around briefly so /oauth/revoke
// idempotency works for clients that retry after a stale token.
func (db *Database) SweepExpiredOAuthTokens() (int64, error) {
	res, err := db.write.Exec(`
		DELETE FROM oauth_access_tokens
		WHERE access_expires_at < datetime('now', '-1 day')
		  AND (refresh_expires_at IS NULL OR refresh_expires_at < datetime('now', '-1 day'))`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── small shared helpers ─────────────────────────────────────────────

// nullableTime is the *time.Time mirror of nullableString already defined
// in executions.go. Returns nil for the zero/nil value so SQLite stores
// NULL instead of an empty timestamp.
func nullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC()
}
