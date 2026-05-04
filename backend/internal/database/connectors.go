package database

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// AgentConnector is a named bundle of N functions plus a static bearer
// token. When the token is presented at /mcp, the agent sees one MCP
// tool per bundled function (invoke-only) and nothing else — no Orva
// management surface.
//
// The token plaintext (orva_aco_<32 hex>) is only ever in the response
// to Create/Rotate; we store the SHA-256 hash plus a 16-char prefix
// for UI identification.
type AgentConnector struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	Instructions string     `json:"instructions,omitempty"` // per-connector system prompt; empty → built default
	TokenHash    string     `json:"-"`                      // never serialised
	TokenPrefix  string     `json:"prefix"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	RevokedAt    *time.Time `json:"-"`

	// Populated by GetConnector / GetConnectorByTokenHash via JOIN.
	// Zero-length on List* (callers can fetch per-row).
	FunctionIDs []string `json:"function_ids,omitempty"`
}

// IsActive reports whether the connector token is currently usable.
// Callers should reject non-active connectors with 401 at the auth
// gate. Mirrors APIKey expiry semantics.
func (c *AgentConnector) IsActive(now time.Time) bool {
	if c.RevokedAt != nil {
		return false
	}
	if c.ExpiresAt != nil && c.ExpiresAt.Before(now) {
		return false
	}
	return true
}

// ConnectorFunction is one row in the M:M junction. Description is
// the per-connector override of the function's tool description; NULL
// means "fall back to functions.description".
type ConnectorFunction struct {
	ConnectorID    string    `json:"connector_id"`
	FunctionID     string    `json:"function_id"`
	Description    string    `json:"description,omitempty"`
	AddedByActorID string    `json:"added_by_actor_id,omitempty"`
	AddedAt        time.Time `json:"added_at"`
}

// ── connectors CRUD ──────────────────────────────────────────────────

// InsertConnector creates a new connector row. Caller is responsible
// for hashing the token and setting TokenHash/TokenPrefix; this
// function just persists.
func (db *Database) InsertConnector(c *AgentConnector) error {
	_, err := db.write.Exec(`
		INSERT INTO agent_connectors (
			id, name, description, instructions, token_hash, token_prefix, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, nullableString(c.Description), nullableString(c.Instructions),
		c.TokenHash, c.TokenPrefix, nullableTime(c.ExpiresAt),
	)
	if err != nil {
		return err
	}
	// Read back the timestamps the DB stamped for us.
	return db.read.QueryRow(
		`SELECT created_at, updated_at FROM agent_connectors WHERE id = ?`, c.ID,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
}

// GetConnectorByID returns the connector + its function-id list.
func (db *Database) GetConnectorByID(id string) (*AgentConnector, error) {
	c, err := db.scanConnector(db.read.QueryRow(`
		SELECT id, name, description, instructions, token_hash, token_prefix,
		       expires_at, created_at, updated_at, last_used_at, revoked_at
		FROM agent_connectors WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	c.FunctionIDs, err = db.listConnectorFunctionIDs(id)
	return c, err
}

// GetConnectorByTokenHash is the hot-path lookup for /mcp auth. Hash
// is SHA-256 hex of the bearer plaintext.
func (db *Database) GetConnectorByTokenHash(hash string) (*AgentConnector, error) {
	c, err := db.scanConnector(db.read.QueryRow(`
		SELECT id, name, description, instructions, token_hash, token_prefix,
		       expires_at, created_at, updated_at, last_used_at, revoked_at
		FROM agent_connectors WHERE token_hash = ? AND revoked_at IS NULL`, hash))
	if err != nil {
		return nil, err
	}
	c.FunctionIDs, err = db.listConnectorFunctionIDs(c.ID)
	return c, err
}

// ListConnectors returns every non-revoked connector. The function
// list per row is empty — callers that need it should fetch per-row.
// Returns rows newest-first.
func (db *Database) ListConnectors() ([]*AgentConnector, error) {
	rows, err := db.read.Query(`
		SELECT id, name, description, instructions, token_hash, token_prefix,
		       expires_at, created_at, updated_at, last_used_at, revoked_at
		FROM agent_connectors
		WHERE revoked_at IS NULL
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AgentConnector
	for rows.Next() {
		c, err := db.scanConnectorRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateConnectorMetadata patches name/description/expires_at.
// FunctionIDs are not touched; use SetConnectorFunctions for that.
func (db *Database) UpdateConnectorMetadata(id, name, description string, expiresAt *time.Time) error {
	_, err := db.write.Exec(`
		UPDATE agent_connectors
		SET name = ?, description = ?, expires_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND revoked_at IS NULL`,
		name, nullableString(description), nullableTime(expiresAt), id,
	)
	return err
}

// RotateConnectorToken replaces the token_hash + token_prefix on an
// existing connector. The old hash is overwritten — clients holding
// the previous plaintext 401 immediately on next /mcp call.
func (db *Database) RotateConnectorToken(id, newHash, newPrefix string) error {
	res, err := db.write.Exec(`
		UPDATE agent_connectors
		SET token_hash = ?, token_prefix = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND revoked_at IS NULL`,
		newHash, newPrefix, id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteConnector drops the row + cascades the junction. Used when
// the operator clicks Delete; revocation is just a soft variant of
// this and isn't exposed as a separate API in v1.
func (db *Database) DeleteConnector(id string) error {
	_, err := db.write.Exec(`DELETE FROM agent_connectors WHERE id = ?`, id)
	return err
}

// TouchConnectorLastUsed bumps last_used_at. Called async from /mcp.
func (db *Database) TouchConnectorLastUsed(id string) error {
	_, err := db.write.Exec(`
		UPDATE agent_connectors
		SET last_used_at = CURRENT_TIMESTAMP
		WHERE id = ?`, id)
	return err
}

// ── connector_functions M:M ──────────────────────────────────────────

// SetConnectorFunctions replaces the connector's function set in a
// single transaction. `descriptions` may carry per-function tool-
// description overrides; missing keys mean "use functions.description"
// (NULL in the junction row).
//
// addedByActorID is stamped on every NEW row; existing rows preserve
// their original audit value.
func (db *Database) SetConnectorFunctions(
	connectorID string, functionIDs []string,
	descriptions map[string]string, addedByActorID string,
) error {
	tx, err := db.write.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Snapshot existing rows so we don't lose audit info on overlap.
	existing := make(map[string]ConnectorFunction)
	rows, err := tx.Query(
		`SELECT function_id, description, added_by_actor_id, added_at
		 FROM connector_functions WHERE connector_id = ?`, connectorID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cf ConnectorFunction
		var desc, actor sql.NullString
		if err := rows.Scan(&cf.FunctionID, &desc, &actor, &cf.AddedAt); err != nil {
			rows.Close()
			return err
		}
		cf.Description = desc.String
		cf.AddedByActorID = actor.String
		existing[cf.FunctionID] = cf
	}
	rows.Close()

	if _, err := tx.Exec(
		`DELETE FROM connector_functions WHERE connector_id = ?`, connectorID,
	); err != nil {
		return err
	}

	for _, fnID := range functionIDs {
		desc := descriptions[fnID]
		actor := addedByActorID
		if prev, ok := existing[fnID]; ok && actor == "" {
			actor = prev.AddedByActorID
		}
		if _, err := tx.Exec(`
			INSERT INTO connector_functions (
				connector_id, function_id, description, added_by_actor_id
			) VALUES (?, ?, ?, ?)`,
			connectorID, fnID, nullableString(desc), nullableString(actor),
		); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(
		`UPDATE agent_connectors SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		connectorID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// listConnectorFunctionIDs returns the function-id set for a connector,
// in the order they were added. Used by GetConnector* to populate the
// FunctionIDs slice.
func (db *Database) listConnectorFunctionIDs(connectorID string) ([]string, error) {
	rows, err := db.read.Query(`
		SELECT function_id FROM connector_functions
		WHERE connector_id = ?
		ORDER BY added_at ASC`, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListConnectorFunctionRecords returns the full junction rows for a
// connector — used by registerConnectorTools so MCP can render the
// per-connector description override.
func (db *Database) ListConnectorFunctionRecords(connectorID string) ([]*ConnectorFunction, error) {
	rows, err := db.read.Query(`
		SELECT connector_id, function_id, description, added_by_actor_id, added_at
		FROM connector_functions
		WHERE connector_id = ?
		ORDER BY added_at ASC`, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ConnectorFunction
	for rows.Next() {
		var cf ConnectorFunction
		var desc, actor sql.NullString
		if err := rows.Scan(&cf.ConnectorID, &cf.FunctionID, &desc, &actor, &cf.AddedAt); err != nil {
			return nil, err
		}
		cf.Description = desc.String
		cf.AddedByActorID = actor.String
		out = append(out, &cf)
	}
	return out, rows.Err()
}

// CountConnectorFunctions is a cheap aggregate for the dashboard list
// view. Avoids round-tripping the full id slice when only the number
// matters.
func (db *Database) CountConnectorFunctions(connectorID string) (int, error) {
	var n int
	err := db.read.QueryRow(
		`SELECT COUNT(*) FROM connector_functions WHERE connector_id = ?`,
		connectorID,
	).Scan(&n)
	return n, err
}

// ── tool-name collision check ────────────────────────────────────────

// ErrToolNameCollision is returned by CheckConnectorToolNameCollision
// when two functions in a proposed set map to the same MCP tool name.
// The caller (REST handler) returns 400 with the message verbatim.
var ErrToolNameCollision = errors.New("tool name collision")

// CheckConnectorToolNameCollision verifies that no two function names
// in `functionIDs` collide on the MCP tool-name conversion (dashes →
// underscores). Returns ErrToolNameCollision wrapped with a clear
// human-readable description on the first collision found.
func (db *Database) CheckConnectorToolNameCollision(functionIDs []string) error {
	if len(functionIDs) < 2 {
		return nil
	}
	seen := make(map[string]string, len(functionIDs)) // toolName → fnName
	for _, fnID := range functionIDs {
		fn, err := db.GetFunction(fnID)
		if err != nil {
			return err
		}
		toolName := connectorToolName(fn.Name)
		if other, dup := seen[toolName]; dup && other != fn.Name {
			return errors.Join(
				ErrToolNameCollision,
				errors.New("functions "+other+" and "+fn.Name+
					" both map to MCP tool "+toolName+" — rename one"),
			)
		}
		seen[toolName] = fn.Name
	}
	return nil
}

// connectorToolName converts a function name (dash-separated lowercase)
// to an MCP-friendly tool name (snake_case). Deliberately simple —
// just dashes → underscores. If we ever need to handle uppercase or
// non-ASCII, it'll be a single change here.
func connectorToolName(fnName string) string {
	return strings.ReplaceAll(fnName, "-", "_")
}

// ── scan helpers ─────────────────────────────────────────────────────

func (db *Database) scanConnector(row *sql.Row) (*AgentConnector, error) {
	var c AgentConnector
	var desc, instr sql.NullString
	var expires, lastUsed, revoked sql.NullTime
	err := row.Scan(
		&c.ID, &c.Name, &desc, &instr, &c.TokenHash, &c.TokenPrefix,
		&expires, &c.CreatedAt, &c.UpdatedAt, &lastUsed, &revoked,
	)
	if err != nil {
		return nil, err
	}
	c.Description = desc.String
	c.Instructions = instr.String
	if expires.Valid {
		c.ExpiresAt = &expires.Time
	}
	if lastUsed.Valid {
		c.LastUsedAt = &lastUsed.Time
	}
	if revoked.Valid {
		c.RevokedAt = &revoked.Time
	}
	return &c, nil
}

func (db *Database) scanConnectorRows(rows *sql.Rows) (*AgentConnector, error) {
	var c AgentConnector
	var desc, instr sql.NullString
	var expires, lastUsed, revoked sql.NullTime
	err := rows.Scan(
		&c.ID, &c.Name, &desc, &instr, &c.TokenHash, &c.TokenPrefix,
		&expires, &c.CreatedAt, &c.UpdatedAt, &lastUsed, &revoked,
	)
	if err != nil {
		return nil, err
	}
	c.Description = desc.String
	c.Instructions = instr.String
	if expires.Valid {
		c.ExpiresAt = &expires.Time
	}
	if lastUsed.Valid {
		c.LastUsedAt = &lastUsed.Time
	}
	if revoked.Valid {
		c.RevokedAt = &revoked.Time
	}
	return &c, nil
}
