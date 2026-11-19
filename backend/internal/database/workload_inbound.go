package database

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// InboundSecretDigest binds a publication to the exact stored signature secret
// without persisting a second copy of it.
func InboundSecretDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// MatchManagedInbound resolves one published inbound trigger. The mapping must
// carry the current exposure version, owner, format, header, active flag and
// secret digest from the managed mutation that published it. Rows written by
// legacy paths, or preserved by a restore, never match.
func (db *Database) MatchManagedInbound(ctx context.Context, id string) (*InboundWebhook, error) {
	if !ids.IsUUID(id) {
		return nil, nil
	}
	var w InboundWebhook
	var created int64
	var digest string
	err := db.read.QueryRowContext(ctx, `SELECT w.id,w.function_id,w.name,w.secret,w.signature_header,w.signature_format,w.active,w.created_at,p.secret_digest
 FROM inbound_webhooks AS w
 JOIN workload_exposure_versions AS v ON v.kind='inbound' AND v.key=w.id
 JOIN workload_inbound_publications AS p ON p.id=w.id AND p.version=v.version AND p.function_id=w.function_id
  AND p.signature_format=w.signature_format AND p.signature_header=w.signature_header AND p.active=w.active
 WHERE w.id=?`, id).Scan(&w.ID, &w.FunctionID, &w.Name, &w.Secret, &w.SignatureHeader, &w.SignatureFormat, &w.Active, &created, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrExposureUnavailable
	}
	if subtle.ConstantTimeCompare([]byte(digest), []byte(InboundSecretDigest(w.Secret))) != 1 {
		return nil, nil
	}
	w.CreatedAt = time.UnixMilli(created).UTC()
	return &w, nil
}

// quarantineManagedInboundTx preserves mappings and version high-water marks but
// removes the public grant, exactly as it does for custom routes.
func quarantineManagedInboundTx(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema
	 WHERE type='table' AND name='workload_inbound_publications')`).Scan(&exists)
	if err != nil || !exists {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM workload_inbound_publications`)
	return err
}

const maxExposureList = 256

// RouteExposureRow is an inspection row; Published means dispatchable.
type RouteExposureRow struct {
	Route
	Version   uint64 `json:"version,string"`
	Published bool   `json:"published"`
}

// InboundExposureRow never carries the signature secret.
type InboundExposureRow struct {
	InboundWebhook
	Version   uint64 `json:"version,string"`
	Published bool   `json:"published"`
}

// ListRouteExposures returns a bounded, read-authorized view of every mapping.
func (db *Database) ListRouteExposures(ctx context.Context, ref OperatorCredentialReference) ([]RouteExposureRow, bool, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, ErrExposureUnavailable
	}
	defer tx.Rollback()
	_, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, "read")
	if err != nil {
		return nil, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.path,r.function_id,r.methods,r.created_at,COALESCE(v.version,0),
 EXISTS(SELECT 1 FROM workload_route_publications AS p WHERE p.path=r.path AND p.version=v.version
  AND p.function_id=r.function_id AND p.methods=r.methods)
 FROM routes AS r LEFT JOIN workload_exposure_versions AS v ON v.kind='route' AND v.key=r.path
 WHERE length(CAST(r.path AS BLOB))<=4096 AND length(CAST(r.methods AS BLOB))<=256 ORDER BY r.path LIMIT ?`, maxExposureList+1)
	if err != nil {
		return nil, false, ErrExposureUnavailable
	}
	defer rows.Close()
	out := []RouteExposureRow{}
	for rows.Next() {
		var row RouteExposureRow
		if rows.Scan(&row.Path, &row.FunctionID, &row.Methods, &row.CreatedAt, &row.Version, &row.Published) != nil {
			return nil, false, ErrExposureUnavailable
		}
		out = append(out, row)
	}
	if rows.Err() != nil {
		return nil, false, ErrExposureUnavailable
	}
	truncated := len(out) > maxExposureList
	if truncated {
		out = out[:maxExposureList]
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, false, operatorDenied(ctx)
	}
	return out, truncated, nil
}

// ListInboundExposures lists one owner's triggers with the owner's current CAS
// expectations, so an operator can mutate without a second inspection.
func (db *Database) ListInboundExposures(ctx context.Context, ref OperatorCredentialReference, owner string) ([]InboundExposureRow, map[string]ExposureOwnerExpected, bool, error) {
	if !validIAMUUID(owner) {
		return nil, nil, false, ErrExposureInvalid
	}
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, false, ErrExposureUnavailable
	}
	defer tx.Rollback()
	_, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, "read")
	if err != nil {
		return nil, nil, false, err
	}
	o, err := readExposureOwner(ctx, tx, owner)
	if err != nil {
		return nil, nil, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT w.id,w.function_id,
 CASE WHEN length(CAST(w.name AS BLOB))<=256 THEN w.name END,
 CASE WHEN length(CAST(w.secret AS BLOB))<=4096 THEN w.secret END,
 CASE WHEN length(CAST(w.signature_header AS BLOB))<=256 THEN w.signature_header END,
 CASE WHEN length(CAST(w.signature_format AS BLOB))<=64 THEN w.signature_format END,w.active,w.created_at,
 COALESCE(v.version,0),
 (SELECT p.secret_digest FROM workload_inbound_publications AS p WHERE p.id=w.id AND p.version=v.version
  AND p.function_id=w.function_id AND p.signature_format=w.signature_format
  AND p.signature_header=w.signature_header AND p.active=w.active)
 FROM inbound_webhooks AS w LEFT JOIN workload_exposure_versions AS v ON v.kind='inbound' AND v.key=w.id
 WHERE w.function_id=? ORDER BY w.created_at DESC,w.id LIMIT ?`, owner, maxExposureList+1)
	if err != nil {
		return nil, nil, false, ErrExposureUnavailable
	}
	defer rows.Close()
	out := []InboundExposureRow{}
	for rows.Next() {
		var row InboundExposureRow
		var created int64
		var digest sql.NullString
		if rows.Scan(&row.ID, &row.FunctionID, &row.Name, &row.Secret, &row.SignatureHeader, &row.SignatureFormat,
			&row.Active, &created, &row.Version, &digest) != nil {
			return nil, nil, false, ErrExposureUnavailable
		}
		row.CreatedAt = time.UnixMilli(created).UTC()
		if len(row.Secret) >= 8 {
			row.SecretPreview = row.Secret[:8] + "…"
		}
		row.Published = digest.Valid && digest.String == InboundSecretDigest(row.Secret)
		row.Secret = ""
		out = append(out, row)
	}
	if rows.Err() != nil {
		return nil, nil, false, ErrExposureUnavailable
	}
	truncated := len(out) > maxExposureList
	if truncated {
		out = out[:maxExposureList]
	}
	r := o.sources.Record
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, nil, false, operatorDenied(ctx)
	}
	return out, map[string]ExposureOwnerExpected{owner: {PolicyRevision: r.Revision, AuthorityEpoch: r.AuthorityEpoch}}, truncated, nil
}
