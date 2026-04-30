package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"
)

// EventSubscription is an operator-managed webhook target. One row says
// "send the listed events to this URL, signed with this secret."
// Subscriptions are global (not scoped to a function) — they receive
// system-level signals regardless of which function emitted them.
type EventSubscription struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	URL            string     `json:"url"`
	Secret         string     `json:"-"`              // never serialized after create
	SecretPreview  string     `json:"secret_preview"` // first 8 chars for display
	Events         []string   `json:"events"`         // ["*"] or ["deployment.failed", ...]
	Enabled        bool       `json:"enabled"`
	LastDeliveryAt *time.Time `json:"last_delivery_at,omitempty"`
	LastStatus     string     `json:"last_status,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// NewSubscriptionID returns a fresh sub_<12-hex>.
func NewSubscriptionID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "sub_" + hex.EncodeToString(b[:])
}

// NewWebhookSecret returns a 32-byte (64-hex) random secret, the value
// the operator copies once and configures on the receiver to verify
// HMAC signatures with.
func NewWebhookSecret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// InsertEventSubscription persists a fresh subscription. Caller is
// responsible for filling Secret (use NewWebhookSecret) — the API
// handler does this so the plaintext is generated server-side.
func (db *Database) InsertEventSubscription(s *EventSubscription) error {
	if s.ID == "" {
		s.ID = NewSubscriptionID()
	}
	if len(s.Events) == 0 {
		s.Events = []string{"*"}
	}
	now := time.Now().UTC()
	s.CreatedAt = now
	s.UpdatedAt = now

	_, err := db.write.Exec(`
		INSERT INTO event_subscriptions
			(id, name, url, secret, events, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Name, s.URL, s.Secret, encodeEventArray(s.Events),
		boolToInt(s.Enabled), s.CreatedAt, s.UpdatedAt)
	return err
}

func (db *Database) GetEventSubscription(id string) (*EventSubscription, error) {
	var s EventSubscription
	var enabled int
	var eventsRaw string
	var lastDelivery sql.NullTime
	var lastStatus, lastErr sql.NullString
	err := db.read.QueryRow(`
		SELECT id, name, url, secret, events, enabled, last_delivery_at,
		       last_status, last_error, created_at, updated_at
		FROM event_subscriptions WHERE id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.URL, &s.Secret, &eventsRaw, &enabled,
		&lastDelivery, &lastStatus, &lastErr, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	s.Events = decodeEventArray(eventsRaw)
	s.Enabled = enabled == 1
	if lastDelivery.Valid {
		t := lastDelivery.Time
		s.LastDeliveryAt = &t
	}
	s.LastStatus = lastStatus.String
	s.LastError = lastErr.String
	if len(s.Secret) >= 8 {
		s.SecretPreview = s.Secret[:8] + "…"
	}
	return &s, nil
}

func (db *Database) ListEventSubscriptions() ([]*EventSubscription, error) {
	rows, err := db.read.Query(`
		SELECT id, name, url, secret, events, enabled, last_delivery_at,
		       last_status, last_error, created_at, updated_at
		FROM event_subscriptions
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubscriptionRows(rows)
}

// MatchingSubscriptions returns enabled subscriptions whose `events`
// JSON array contains either "*" or the given event name. The LIKE
// match is fine here because the table is operator-scale (rarely > 50
// rows) and the JSON shape is fixed by InsertEventSubscription.
func (db *Database) MatchingSubscriptions(eventName string) ([]*EventSubscription, error) {
	rows, err := db.read.Query(`
		SELECT id, name, url, secret, events, enabled, last_delivery_at,
		       last_status, last_error, created_at, updated_at
		FROM event_subscriptions
		WHERE enabled = 1
		  AND (events LIKE '%"*"%' OR events LIKE ?)
		ORDER BY created_at ASC`,
		`%"`+eventName+`"%`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubscriptionRows(rows)
}

// UpdateEventSubscription applies the editable fields. Secret cannot be
// rotated through this path — use a fresh subscription for that, same
// as the api_keys flow.
func (db *Database) UpdateEventSubscription(s *EventSubscription) error {
	s.UpdatedAt = time.Now().UTC()
	_, err := db.write.Exec(`
		UPDATE event_subscriptions
		SET name = ?, url = ?, events = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		s.Name, s.URL, encodeEventArray(s.Events), boolToInt(s.Enabled),
		s.UpdatedAt, s.ID)
	return err
}

// MarkSubscriptionResult stamps the last_delivery_at / last_status /
// last_error columns. Called by the delivery worker after each attempt
// (success or terminal failure) so the operator can see at-a-glance
// health on the Webhooks dashboard without drilling into deliveries.
func (db *Database) MarkSubscriptionResult(id, status, errMsg string) error {
	_, err := db.write.Exec(`
		UPDATE event_subscriptions
		SET last_delivery_at = ?, last_status = ?, last_error = ?, updated_at = ?
		WHERE id = ?`,
		time.Now().UTC(), status, errMsg, time.Now().UTC(), id)
	return err
}

func (db *Database) DeleteEventSubscription(id string) error {
	_, err := db.write.Exec(`DELETE FROM event_subscriptions WHERE id = ?`, id)
	return err
}

// ── webhook_deliveries ─────────────────────────────────────────────

type WebhookDelivery struct {
	ID             string     `json:"id"`
	SubscriptionID string     `json:"subscription_id"`
	EventName      string     `json:"event_name"`
	Payload        []byte     `json:"-"` // JSON envelope; surfaced in Get only
	Status         string     `json:"status"` // pending|running|succeeded|failed
	ScheduledAt    time.Time  `json:"scheduled_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	ResponseStatus int        `json:"response_status,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// NewDeliveryID returns a fresh whd_<12-hex>.
func NewDeliveryID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "whd_" + hex.EncodeToString(b[:])
}

func (db *Database) InsertDelivery(d *WebhookDelivery) error {
	if d.ID == "" {
		d.ID = NewDeliveryID()
	}
	if d.Status == "" {
		d.Status = "pending"
	}
	if d.MaxAttempts <= 0 {
		d.MaxAttempts = 5
	}
	if d.ScheduledAt.IsZero() {
		d.ScheduledAt = time.Now().UTC()
	}
	d.CreatedAt = time.Now().UTC()
	_, err := db.write.Exec(`
		INSERT INTO webhook_deliveries
			(id, subscription_id, event_name, payload, status, scheduled_at,
			 attempts, max_attempts, last_error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.SubscriptionID, d.EventName, d.Payload, d.Status,
		d.ScheduledAt, d.Attempts, d.MaxAttempts, d.LastError, d.CreatedAt)
	return err
}

func (db *Database) GetDelivery(id string) (*WebhookDelivery, error) {
	var d WebhookDelivery
	var started, finished sql.NullTime
	var lastErr sql.NullString
	var respStatus sql.NullInt64
	err := db.read.QueryRow(`
		SELECT id, subscription_id, event_name, payload, status, scheduled_at,
		       started_at, finished_at, attempts, max_attempts,
		       response_status, last_error, created_at
		FROM webhook_deliveries WHERE id = ?`, id,
	).Scan(&d.ID, &d.SubscriptionID, &d.EventName, &d.Payload, &d.Status,
		&d.ScheduledAt, &started, &finished, &d.Attempts, &d.MaxAttempts,
		&respStatus, &lastErr, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	if started.Valid {
		t := started.Time
		d.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time
		d.FinishedAt = &t
	}
	d.LastError = lastErr.String
	if respStatus.Valid {
		d.ResponseStatus = int(respStatus.Int64)
	}
	return &d, nil
}

// ListDeliveriesForSubscription returns recent deliveries for a single
// subscription, newest first. limit defaults to 100 if 0.
func (db *Database) ListDeliveriesForSubscription(subID string, limit int) ([]*WebhookDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.read.Query(`
		SELECT id, subscription_id, event_name, payload, status, scheduled_at,
		       started_at, finished_at, attempts, max_attempts,
		       response_status, last_error, created_at
		FROM webhook_deliveries
		WHERE subscription_id = ?
		ORDER BY created_at DESC LIMIT ?`, subID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeliveryRows(rows)
}

// ClaimDueDeliveries atomically marks up to limit pending+due rows as
// running and returns them. Mirrors jobs.ClaimDueJobs so concurrent
// scheduler ticks can't double-deliver.
func (db *Database) ClaimDueDeliveries(now time.Time, limit int) ([]*WebhookDelivery, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := db.write.Query(`
		UPDATE webhook_deliveries
		SET status = 'running',
		    started_at = ?,
		    attempts = attempts + 1
		WHERE id IN (
			SELECT id FROM webhook_deliveries
			WHERE status = 'pending' AND scheduled_at <= ?
			ORDER BY scheduled_at ASC LIMIT ?
		)
		RETURNING id, subscription_id, event_name, payload, status, scheduled_at,
		          started_at, finished_at, attempts, max_attempts,
		          response_status, COALESCE(last_error, ''), created_at`,
		now.UTC(), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeliveryRows(rows)
}

func (db *Database) MarkDeliverySuccess(id string, respStatus int) error {
	_, err := db.write.Exec(`
		UPDATE webhook_deliveries
		SET status = 'succeeded', finished_at = ?, response_status = ?, last_error = NULL
		WHERE id = ?`, time.Now().UTC(), respStatus, id)
	return err
}

// MarkDeliveryFailure either retries with exp backoff (status=pending,
// scheduled_at advanced) or marks the delivery permanently failed.
// Backoff curve mirrors jobs.MarkJobFailure: 2 << attempts seconds,
// capped at 1h. respStatus is 0 on transport errors.
func (db *Database) MarkDeliveryFailure(id, errMsg string, attempts, maxAttempts, respStatus int) error {
	now := time.Now().UTC()
	if attempts >= maxAttempts {
		_, err := db.write.Exec(`
			UPDATE webhook_deliveries
			SET status = 'failed', finished_at = ?, last_error = ?, response_status = ?
			WHERE id = ?`, now, errMsg, respStatus, id)
		return err
	}
	delaySec := 1 << attempts
	if delaySec > 3600 {
		delaySec = 3600
	}
	next := now.Add(time.Duration(delaySec) * time.Second)
	_, err := db.write.Exec(`
		UPDATE webhook_deliveries
		SET status = 'pending', started_at = NULL, scheduled_at = ?,
		    last_error = ?, response_status = ?
		WHERE id = ?`, next, errMsg, respStatus, id)
	return err
}

// RetryDelivery resets a terminal delivery back to pending with the
// attempt counter cleared. Operator-triggered from the Deliveries
// drawer.
func (db *Database) RetryDelivery(id string) error {
	_, err := db.write.Exec(`
		UPDATE webhook_deliveries
		SET status = 'pending', scheduled_at = ?, attempts = 0,
		    started_at = NULL, finished_at = NULL, last_error = NULL,
		    response_status = NULL
		WHERE id = ?`, time.Now().UTC(), id)
	return err
}

// ── helpers ────────────────────────────────────────────────────────

func encodeEventArray(events []string) string {
	if len(events) == 0 {
		return `["*"]`
	}
	// JSON-shaped string built by hand — no need for a marshal call,
	// and the LIKE match in MatchingSubscriptions depends on the exact
	// "<name>" formatting being present.
	var b strings.Builder
	b.WriteByte('[')
	for i, e := range events {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		// Escape embedded quotes / backslashes defensively. Event names
		// are operator-controlled but the API may eventually accept
		// custom names so we don't trust them here.
		for _, r := range e {
			if r == '"' || r == '\\' {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return b.String()
}

func decodeEventArray(s string) []string {
	// Only used to display + for matching equivalence; strict parsing
	// isn't needed since we control what writes the column.
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, `"`)
		p = strings.TrimSuffix(p, `"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func scanSubscriptionRows(rows *sql.Rows) ([]*EventSubscription, error) {
	var out []*EventSubscription
	for rows.Next() {
		var s EventSubscription
		var enabled int
		var eventsRaw string
		var lastDelivery sql.NullTime
		var lastStatus, lastErr sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.Secret, &eventsRaw, &enabled,
			&lastDelivery, &lastStatus, &lastErr, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Events = decodeEventArray(eventsRaw)
		s.Enabled = enabled == 1
		if lastDelivery.Valid {
			t := lastDelivery.Time
			s.LastDeliveryAt = &t
		}
		s.LastStatus = lastStatus.String
		s.LastError = lastErr.String
		if len(s.Secret) >= 8 {
			s.SecretPreview = s.Secret[:8] + "…"
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func scanDeliveryRows(rows *sql.Rows) ([]*WebhookDelivery, error) {
	var out []*WebhookDelivery
	for rows.Next() {
		var d WebhookDelivery
		var started, finished sql.NullTime
		var lastErr sql.NullString
		var respStatus sql.NullInt64
		if err := rows.Scan(&d.ID, &d.SubscriptionID, &d.EventName, &d.Payload, &d.Status,
			&d.ScheduledAt, &started, &finished, &d.Attempts, &d.MaxAttempts,
			&respStatus, &lastErr, &d.CreatedAt); err != nil {
			return nil, err
		}
		if started.Valid {
			t := started.Time
			d.StartedAt = &t
		}
		if finished.Valid {
			t := finished.Time
			d.FinishedAt = &t
		}
		d.LastError = lastErr.String
		if respStatus.Valid {
			d.ResponseStatus = int(respStatus.Int64)
		}
		out = append(out, &d)
	}
	return out, rows.Err()
}
