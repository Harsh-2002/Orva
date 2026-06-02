package database

import (
	"database/sql"
	"errors"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// This file holds all persistence for the in-product AI chat agent:
// conversations, messages, tool calls, provider credentials, and settings.
// It follows the same convention as the rest of the database package — CRUD
// as methods on *Database, time.Time over DATETIME columns, UUIDv7 ids — so
// the AI feature's storage reads like every other resource here.

// ErrAINotFound is returned when an AI row lookup misses. Callers can use
// errors.Is to disambiguate from real database errors.
var ErrAINotFound = errors.New("ai: not found")

// ─── types ──────────────────────────────────────────────────────────────

// AIConversation is one chat thread. provider/model snapshot what was
// active when the thread began; the live settings can drift independently.
type AIConversation struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id,omitempty"`
	Title     string    `json:"title"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AIMessage is one turn in a conversation. content is the flattened text
// for fast render/search; parts is the JSON-encoded structured payload
// (text / code / thinking / tool_call blocks). Raw-JSON columns are kept
// as strings so the database layer stays decoupled from the agent's part
// types — higher layers marshal/unmarshal.
type AIMessage struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"` // user | assistant | tool | system
	Content        string    `json:"content"`
	Parts          string    `json:"parts"`                 // JSON array
	TokenUsage     string    `json:"token_usage,omitempty"` // JSON object
	Seq            int       `json:"seq"`
	CreatedAt      time.Time `json:"created_at"`
}

// AIToolCall records one tool invocation requested by the agent, its
// approval state, and its result. status drives the loop and the UI card.
type AIToolCall struct {
	ID               string     `json:"id"`
	ConversationID   string     `json:"conversation_id"`
	MessageID        string     `json:"message_id,omitempty"`
	CallID           string     `json:"call_id,omitempty"` // provider tool_call id
	ToolName         string     `json:"tool_name"`
	ToolGroup        string     `json:"tool_group,omitempty"`
	Args             string     `json:"args"`             // JSON object
	Result           string     `json:"result,omitempty"` // JSON output or error
	Status           string     `json:"status"`           // pending_approval|approved|rejected|running|succeeded|failed
	RequiresApproval bool       `json:"requires_approval"`
	Destructive      bool       `json:"destructive"`
	ApprovedBy       string     `json:"approved_by,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	DurationMS       *int64     `json:"duration_ms,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// AIProviderConfig is a configured LLM provider + its (encrypted) API key.
// APIKeyEncrypted holds base64(nonce||ciphertext); it is never serialised
// to API clients (handlers strip it and report has_key instead).
type AIProviderConfig struct {
	ID              string    `json:"id"`
	Provider        string    `json:"provider"`
	Label           string    `json:"label"`
	APIKeyEncrypted string    `json:"-"`
	BaseURL         string    `json:"base_url,omitempty"`
	ExtraConfig     string    `json:"extra_config,omitempty"` // JSON
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// AISettings is the singleton (or per-user) agent configuration row.
type AISettings struct {
	ID                string    `json:"id"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	ThinkingLevel     string    `json:"thinking_level"`  // off | standard | deep
	SystemPrompt      string    `json:"system_prompt"`   // empty → built-in default
	ApprovalPolicy    string    `json:"approval_policy"` // all_writes | destructive_only | auto
	MaxToolIterations int       `json:"max_tool_iterations"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ─── conversations ────────────────────────────────────────────────────────

func (db *Database) CreateConversation(c *AIConversation) error {
	if c.ID == "" {
		c.ID = ids.New()
	}
	if c.Title == "" {
		c.Title = "New conversation"
	}
	now := time.Now().UTC()
	c.CreatedAt = now
	c.UpdatedAt = now
	_, err := db.write.Exec(`
		INSERT INTO ai_conversations (id, user_id, title, provider, model, archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, nullStr(c.UserID), c.Title, nullStr(c.Provider), nullStr(c.Model),
		boolToInt(c.Archived), c.CreatedAt, c.UpdatedAt,
	)
	return err
}

func (db *Database) GetConversation(id string) (*AIConversation, error) {
	row := db.read.QueryRow(`
		SELECT id, user_id, title, provider, model, archived, created_at, updated_at
		FROM ai_conversations WHERE id = ?`, id)
	c, err := scanConversation(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAINotFound
	}
	return c, err
}

// ListConversations returns threads ordered most-recently-updated first.
// userID="" returns all (single-user / admin view); a non-empty userID
// scopes to that principal.
func (db *Database) ListConversations(userID string, includeArchived bool, limit, offset int) ([]*AIConversation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, user_id, title, provider, model, archived, created_at, updated_at
	      FROM ai_conversations WHERE 1=1`
	args := []any{}
	if userID != "" {
		q += " AND user_id = ?"
		args = append(args, userID)
	}
	if !includeArchived {
		q += " AND archived = 0"
	}
	q += " ORDER BY updated_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := db.read.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AIConversation, 0)
	for rows.Next() {
		c, err := scanConversation(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateConversation patches title and/or archived. nil pointers are left
// untouched. updated_at is always bumped.
func (db *Database) UpdateConversation(id string, title *string, archived *bool) (*AIConversation, error) {
	set := "updated_at = ?"
	args := []any{time.Now().UTC()}
	if title != nil {
		set += ", title = ?"
		args = append(args, *title)
	}
	if archived != nil {
		set += ", archived = ?"
		args = append(args, boolToInt(*archived))
	}
	args = append(args, id)
	res, err := db.write.Exec("UPDATE ai_conversations SET "+set+" WHERE id = ?", args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrAINotFound
	}
	return db.GetConversation(id)
}

// TouchConversation bumps updated_at — called when a new message lands so
// the thread floats to the top of the list.
func (db *Database) TouchConversation(id string) error {
	_, err := db.write.Exec(`UPDATE ai_conversations SET updated_at = ? WHERE id = ?`,
		time.Now().UTC(), id)
	return err
}

// DeleteConversation removes a thread; messages + tool_calls cascade via FK.
// Idempotent — no error when the row never existed.
func (db *Database) DeleteConversation(id string) error {
	_, err := db.write.Exec(`DELETE FROM ai_conversations WHERE id = ?`, id)
	return err
}

func scanConversation(scan func(...any) error) (*AIConversation, error) {
	var (
		c        AIConversation
		userID   sql.NullString
		provider sql.NullString
		model    sql.NullString
		archived int
	)
	if err := scan(&c.ID, &userID, &c.Title, &provider, &model, &archived, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.UserID = userID.String
	c.Provider = provider.String
	c.Model = model.String
	c.Archived = archived != 0
	return &c, nil
}

// ─── messages ─────────────────────────────────────────────────────────────

// InsertMessage appends a message, assigning id, the next per-conversation
// seq, and created_at. The write pool is single-connection so the
// MAX(seq)+1 read-then-insert is race-free.
func (db *Database) InsertMessage(m *AIMessage) error {
	if m.ID == "" {
		m.ID = ids.New()
	}
	if m.Parts == "" {
		m.Parts = "[]"
	}
	if m.Seq == 0 {
		var maxSeq sql.NullInt64
		if err := db.write.QueryRow(
			`SELECT MAX(seq) FROM ai_messages WHERE conversation_id = ?`, m.ConversationID,
		).Scan(&maxSeq); err != nil {
			return err
		}
		m.Seq = int(maxSeq.Int64) + 1
	}
	m.CreatedAt = time.Now().UTC()
	_, err := db.write.Exec(`
		INSERT INTO ai_messages (id, conversation_id, role, content, parts, token_usage, seq, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ConversationID, m.Role, m.Content, m.Parts, nullStr(m.TokenUsage), m.Seq, m.CreatedAt,
	)
	return err
}

// UpdateMessage rewrites the content/parts/token_usage of an existing
// message — used to finalise an assistant message after streaming.
func (db *Database) UpdateMessage(id, content, parts, tokenUsage string) error {
	if parts == "" {
		parts = "[]"
	}
	_, err := db.write.Exec(
		`UPDATE ai_messages SET content = ?, parts = ?, token_usage = ? WHERE id = ?`,
		content, parts, nullStr(tokenUsage), id)
	return err
}

// ListMessages returns a conversation's messages in order. sinceSeq > 0
// returns only messages after that seq (incremental fetch).
func (db *Database) ListMessages(conversationID string, sinceSeq int) ([]*AIMessage, error) {
	rows, err := db.read.Query(`
		SELECT id, conversation_id, role, content, parts, token_usage, seq, created_at
		FROM ai_messages
		WHERE conversation_id = ? AND seq > ?
		ORDER BY seq ASC`, conversationID, sinceSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AIMessage, 0)
	for rows.Next() {
		var (
			m          AIMessage
			tokenUsage sql.NullString
		)
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Parts, &tokenUsage, &m.Seq, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.TokenUsage = tokenUsage.String
		out = append(out, &m)
	}
	return out, rows.Err()
}

// GetMessage returns a single message by id.
func (db *Database) GetMessage(id string) (*AIMessage, error) {
	var (
		m          AIMessage
		tokenUsage sql.NullString
	)
	err := db.read.QueryRow(`
		SELECT id, conversation_id, role, content, parts, token_usage, seq, created_at
		FROM ai_messages WHERE id = ?`, id).Scan(
		&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Parts, &tokenUsage, &m.Seq, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	m.TokenUsage = tokenUsage.String
	return &m, nil
}

// DeleteMessagesFromSeq removes a conversation's messages with seq >= fromSeq
// (and the tool calls those messages own), truncating the conversation. Used by
// regenerate, edit-and-resend, and delete-from-here. Runs in one transaction so
// history never ends up half-truncated.
func (db *Database) DeleteMessagesFromSeq(conversationID string, fromSeq int) error {
	tx, err := db.write.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		DELETE FROM ai_tool_calls
		WHERE conversation_id = ? AND message_id IN (
			SELECT id FROM ai_messages WHERE conversation_id = ? AND seq >= ?
		)`, conversationID, conversationID, fromSeq); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`DELETE FROM ai_messages WHERE conversation_id = ? AND seq >= ?`,
		conversationID, fromSeq); err != nil {
		return err
	}
	return tx.Commit()
}

// ─── tool calls ───────────────────────────────────────────────────────────

func (db *Database) InsertToolCall(t *AIToolCall) error {
	if t.ID == "" {
		t.ID = ids.New()
	}
	if t.Args == "" {
		t.Args = "{}"
	}
	if t.Status == "" {
		t.Status = "pending_approval"
	}
	t.CreatedAt = time.Now().UTC()
	_, err := db.write.Exec(`
		INSERT INTO ai_tool_calls
			(id, conversation_id, message_id, call_id, tool_name, tool_group, args, result,
			 status, requires_approval, destructive, approved_by, started_at, finished_at, duration_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.ConversationID, nullStr(t.MessageID), nullStr(t.CallID), t.ToolName, t.ToolGroup,
		t.Args, nullStr(t.Result), t.Status, boolToInt(t.RequiresApproval), boolToInt(t.Destructive),
		nullStr(t.ApprovedBy), t.StartedAt, t.FinishedAt, t.DurationMS, t.CreatedAt,
	)
	return err
}

func (db *Database) GetToolCall(id string) (*AIToolCall, error) {
	row := db.read.QueryRow(`
		SELECT id, conversation_id, message_id, call_id, tool_name, tool_group, args, result,
		       status, requires_approval, destructive, approved_by, started_at, finished_at, duration_ms, created_at
		FROM ai_tool_calls WHERE id = ?`, id)
	t, err := scanToolCall(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAINotFound
	}
	return t, err
}

// UpdateToolCall persists status/result/approval/timing transitions. Only
// the supplied fields move; pass the current values for the rest (the loop
// holds the row in memory between transitions).
func (db *Database) UpdateToolCall(t *AIToolCall) error {
	res, err := db.write.Exec(`
		UPDATE ai_tool_calls
		SET status = ?, result = ?, approved_by = ?, started_at = ?, finished_at = ?, duration_ms = ?
		WHERE id = ?`,
		t.Status, nullStr(t.Result), nullStr(t.ApprovedBy), t.StartedAt, t.FinishedAt, t.DurationMS, t.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAINotFound
	}
	return nil
}

// ListToolCalls returns all tool calls for a conversation (rehydration).
func (db *Database) ListToolCalls(conversationID string) ([]*AIToolCall, error) {
	rows, err := db.read.Query(`
		SELECT id, conversation_id, message_id, call_id, tool_name, tool_group, args, result,
		       status, requires_approval, destructive, approved_by, started_at, finished_at, duration_ms, created_at
		FROM ai_tool_calls WHERE conversation_id = ? ORDER BY created_at ASC`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AIToolCall, 0)
	for rows.Next() {
		t, err := scanToolCall(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanToolCall(scan func(...any) error) (*AIToolCall, error) {
	var (
		t                          AIToolCall
		messageID, callID          sql.NullString
		result, approvedBy         sql.NullString
		requiresApproval, destruct int
		started, finished          sql.NullTime
		durationMS                 sql.NullInt64
	)
	if err := scan(&t.ID, &t.ConversationID, &messageID, &callID, &t.ToolName, &t.ToolGroup,
		&t.Args, &result, &t.Status, &requiresApproval, &destruct, &approvedBy,
		&started, &finished, &durationMS, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.MessageID = messageID.String
	t.CallID = callID.String
	t.Result = result.String
	t.ApprovedBy = approvedBy.String
	t.RequiresApproval = requiresApproval != 0
	t.Destructive = destruct != 0
	if started.Valid {
		t.StartedAt = &started.Time
	}
	if finished.Valid {
		t.FinishedAt = &finished.Time
	}
	if durationMS.Valid {
		v := durationMS.Int64
		t.DurationMS = &v
	}
	return &t, nil
}

// ─── provider configs ─────────────────────────────────────────────────────

// UpsertProviderConfig inserts or updates a provider config keyed by
// (provider, label). The encrypted key is only overwritten when non-empty,
// so a PATCH that omits the key rotates nothing.
func (db *Database) UpsertProviderConfig(c *AIProviderConfig) (*AIProviderConfig, error) {
	if c.ID == "" {
		c.ID = ids.New()
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	_, err := db.write.Exec(`
		INSERT INTO ai_provider_configs
			(id, provider, label, api_key_encrypted, base_url, extra_config, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, label) DO UPDATE SET
			api_key_encrypted = CASE WHEN excluded.api_key_encrypted != '' THEN excluded.api_key_encrypted ELSE ai_provider_configs.api_key_encrypted END,
			base_url     = excluded.base_url,
			extra_config = excluded.extra_config,
			enabled      = excluded.enabled,
			updated_at   = excluded.updated_at`,
		c.ID, c.Provider, c.Label, c.APIKeyEncrypted, nullStr(c.BaseURL), nullStr(c.ExtraConfig),
		boolToInt(c.Enabled), c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return db.GetProviderConfigByKey(c.Provider, c.Label)
}

func (db *Database) GetProviderConfig(id string) (*AIProviderConfig, error) {
	row := db.read.QueryRow(providerSelect+` WHERE id = ?`, id)
	c, err := scanProviderConfig(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAINotFound
	}
	return c, err
}

func (db *Database) GetProviderConfigByKey(provider, label string) (*AIProviderConfig, error) {
	row := db.read.QueryRow(providerSelect+` WHERE provider = ? AND label = ?`, provider, label)
	c, err := scanProviderConfig(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAINotFound
	}
	return c, err
}

// GetEnabledProviderConfig returns the first enabled config for a provider,
// preferring the most recently updated. Used by the LLM account adapter to
// resolve the key for an inbound chat request.
func (db *Database) GetEnabledProviderConfig(provider string) (*AIProviderConfig, error) {
	row := db.read.QueryRow(providerSelect+
		` WHERE provider = ? AND enabled = 1 ORDER BY updated_at DESC LIMIT 1`, provider)
	c, err := scanProviderConfig(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAINotFound
	}
	return c, err
}

func (db *Database) ListProviderConfigs() ([]*AIProviderConfig, error) {
	rows, err := db.read.Query(providerSelect + ` ORDER BY provider ASC, label ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*AIProviderConfig, 0)
	for rows.Next() {
		c, err := scanProviderConfig(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *Database) DeleteProviderConfig(id string) error {
	_, err := db.write.Exec(`DELETE FROM ai_provider_configs WHERE id = ?`, id)
	return err
}

const providerSelect = `SELECT id, provider, label, api_key_encrypted, base_url, extra_config, enabled, created_at, updated_at FROM ai_provider_configs`

func scanProviderConfig(scan func(...any) error) (*AIProviderConfig, error) {
	var (
		c                    AIProviderConfig
		apiKey               sql.NullString
		baseURL, extraConfig sql.NullString
		enabled              int
	)
	if err := scan(&c.ID, &c.Provider, &c.Label, &apiKey, &baseURL, &extraConfig, &enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.APIKeyEncrypted = apiKey.String
	c.BaseURL = baseURL.String
	c.ExtraConfig = extraConfig.String
	c.Enabled = enabled != 0
	return &c, nil
}

// ─── settings ─────────────────────────────────────────────────────────────

// GetSettings returns the row for id (use "default" for the instance-wide
// row). When the row is missing it returns a zero-value AISettings with the
// id set and ok=false, so callers can apply built-in defaults without a
// separate not-found dance.
func (db *Database) GetSettings(id string) (*AISettings, bool, error) {
	row := db.read.QueryRow(`
		SELECT id, provider, model, thinking_level, system_prompt, approval_policy, max_tool_iterations, updated_at
		FROM ai_settings WHERE id = ?`, id)
	var (
		s            AISettings
		systemPrompt sql.NullString
	)
	err := row.Scan(&s.ID, &s.Provider, &s.Model, &s.ThinkingLevel, &systemPrompt, &s.ApprovalPolicy, &s.MaxToolIterations, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &AISettings{ID: id}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	s.SystemPrompt = systemPrompt.String
	return &s, true, nil
}

// UpsertSettings writes the settings row by id.
func (db *Database) UpsertSettings(s *AISettings) error {
	if s.ID == "" {
		s.ID = "default"
	}
	if s.MaxToolIterations <= 0 {
		s.MaxToolIterations = 25
	}
	s.UpdatedAt = time.Now().UTC()
	_, err := db.write.Exec(`
		INSERT INTO ai_settings
			(id, provider, model, thinking_level, system_prompt, approval_policy, max_tool_iterations, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider            = excluded.provider,
			model               = excluded.model,
			thinking_level      = excluded.thinking_level,
			system_prompt       = excluded.system_prompt,
			approval_policy     = excluded.approval_policy,
			max_tool_iterations = excluded.max_tool_iterations,
			updated_at          = excluded.updated_at`,
		s.ID, s.Provider, s.Model, s.ThinkingLevel, nullStr(s.SystemPrompt), s.ApprovalPolicy,
		s.MaxToolIterations, s.UpdatedAt,
	)
	return err
}

// ─── small helpers ────────────────────────────────────────────────────────

// nullStr maps "" → NULL so empty optional columns store as NULL rather
// than empty string (keeps scans symmetric with sql.NullString).
// (boolToInt is shared from blocklist.go.)
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
