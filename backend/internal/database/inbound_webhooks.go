// Package database — inbound webhook triggers (v0.4 C2a).
//
// Each row represents a "POST /webhook/<id> with this signature scheme
// fires this function" mapping. Authentication is the HMAC signature
// itself; the trigger path is intentionally outside /api/v1 so external
// callers (GitHub, Stripe, Slack, your own service) don't need an Orva
// API key. The signature_format column is a small string enum that
// picks the verifier the trigger handler runs.
package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// InboundWebhook is a configured inbound trigger pointing at a function.
// Secret is 32 random bytes hex-encoded (64 chars). It's returned in the
// API response exactly once on Create so the operator can capture it.
type InboundWebhook struct {
	ID              string    `json:"id"`
	FunctionID      string    `json:"function_id"`
	Name            string    `json:"name"`
	Secret          string    `json:"-"`               // never serialized after create
	SecretPreview   string    `json:"secret_preview"`  // first 8 chars + ellipsis
	SignatureHeader string    `json:"signature_header"`
	SignatureFormat string    `json:"signature_format"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at"`

	// FunctionName is filled in by callers that JOIN against functions.
	FunctionName string `json:"function_name,omitempty"`
}

// AllowedInboundFormats is the closed set of signature formats the
// trigger handler knows how to verify. Adding a new format means:
//   - adding the string here
//   - extending verifyInboundSignature() in the trigger handler
var AllowedInboundFormats = map[string]struct{}{
	"hmac_sha256_hex":    {},
	"hmac_sha256_base64": {},
	"github":             {},
	"stripe":             {},
	"slack":              {},
}

// DefaultSignatureHeader returns the canonical header name a given
// format expects. Operators can override on update; this is the value
// stamped on Create when the caller doesn't supply one.
func DefaultSignatureHeader(format string) string {
	switch format {
	case "github":
		return "X-Hub-Signature-256"
	case "stripe":
		return "Stripe-Signature"
	case "slack":
		return "X-Slack-Signature"
	default:
		return "X-Orva-Signature"
	}
}

// NewInboundWebhookID returns a fresh UUIDv7. Replaces the legacy
// iwh_<hex> form. The webhook URL the operator configures upstream
// is /webhook/<uuid>.
func NewInboundWebhookID() string { return ids.New() }

// NewInboundWebhookSecret returns a 32-byte hex-encoded random secret.
// This is the HMAC key the operator copies once and configures on the
// upstream service.
func NewInboundWebhookSecret() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// InsertInboundWebhook persists a fresh row. Caller is responsible for
// filling Secret + SignatureFormat (handlers do this so the plaintext
// secret is generated server-side).
func (db *Database) InsertInboundWebhook(w *InboundWebhook) error {
	if w.ID == "" {
		w.ID = NewInboundWebhookID()
	}
	if w.SignatureFormat == "" {
		w.SignatureFormat = "hmac_sha256_hex"
	}
	if _, ok := AllowedInboundFormats[w.SignatureFormat]; !ok {
		return errors.New("unknown signature_format")
	}
	if w.SignatureHeader == "" {
		w.SignatureHeader = DefaultSignatureHeader(w.SignatureFormat)
	}
	w.CreatedAt = time.Now().UTC()
	_, err := db.write.Exec(`
		INSERT INTO inbound_webhooks
			(id, function_id, name, secret, signature_header,
			 signature_format, active, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.FunctionID, w.Name, w.Secret, w.SignatureHeader,
		w.SignatureFormat, boolToInt(w.Active), w.CreatedAt.UnixMilli())
	return err
}

// GetInboundWebhook returns a single row by id. The Secret is hydrated
// (the trigger handler needs it for HMAC verification).
func (db *Database) GetInboundWebhook(id string) (*InboundWebhook, error) {
	var w InboundWebhook
	var active int
	var createdMS int64
	err := db.read.QueryRow(`
		SELECT id, function_id, name, secret, signature_header,
		       signature_format, active, created_at
		FROM inbound_webhooks WHERE id = ?`, id,
	).Scan(&w.ID, &w.FunctionID, &w.Name, &w.Secret, &w.SignatureHeader,
		&w.SignatureFormat, &active, &createdMS)
	if err != nil {
		return nil, err
	}
	w.Active = active == 1
	w.CreatedAt = time.UnixMilli(createdMS).UTC()
	if len(w.Secret) >= 8 {
		w.SecretPreview = w.Secret[:8] + "…"
	}
	return &w, nil
}

// ListInboundWebhooksForFunction returns rows for a single function,
// newest first. The Secret is intentionally NOT cleared here — callers
// that render to JSON rely on the `json:"-"` tag and SecretPreview.
func (db *Database) ListInboundWebhooksForFunction(fnID string) ([]*InboundWebhook, error) {
	rows, err := db.read.Query(`
		SELECT id, function_id, name, secret, signature_header,
		       signature_format, active, created_at
		FROM inbound_webhooks WHERE function_id = ?
		ORDER BY created_at DESC`, fnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInboundWebhookRows(rows)
}

// UpdateInboundWebhook applies the editable fields. Secret cannot be
// rotated through this path — operators delete + recreate to rotate, in
// line with the outbound-webhooks model.
func (db *Database) UpdateInboundWebhook(w *InboundWebhook) error {
	if _, ok := AllowedInboundFormats[w.SignatureFormat]; !ok {
		return errors.New("unknown signature_format")
	}
	_, err := db.write.Exec(`
		UPDATE inbound_webhooks
		SET name = ?, signature_header = ?, signature_format = ?, active = ?
		WHERE id = ?`,
		w.Name, w.SignatureHeader, w.SignatureFormat,
		boolToInt(w.Active), w.ID)
	return err
}

// DeleteInboundWebhook removes a single row. FK cascade from functions
// keeps things tidy when the owning function is deleted; this method
// is for operator-driven removals from the dashboard / CLI / MCP.
func (db *Database) DeleteInboundWebhook(id string) error {
	_, err := db.write.Exec(`DELETE FROM inbound_webhooks WHERE id = ?`, id)
	return err
}

func scanInboundWebhookRows(rows *sql.Rows) ([]*InboundWebhook, error) {
	var out []*InboundWebhook
	for rows.Next() {
		var w InboundWebhook
		var active int
		var createdMS int64
		if err := rows.Scan(&w.ID, &w.FunctionID, &w.Name, &w.Secret,
			&w.SignatureHeader, &w.SignatureFormat, &active, &createdMS); err != nil {
			return nil, err
		}
		w.Active = active == 1
		w.CreatedAt = time.UnixMilli(createdMS).UTC()
		if len(w.Secret) >= 8 {
			w.SecretPreview = w.Secret[:8] + "…"
		}
		out = append(out, &w)
	}
	return out, rows.Err()
}
