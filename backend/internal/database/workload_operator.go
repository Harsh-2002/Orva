package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrOperatorDenied = errors.New("operator publication denied")

// OperatorCredentialReference is host-resolved submission provenance, not authentication by ID.
// Never construct it from function-author input; capture it only after trusted authentication.
type OperatorCredentialReference struct {
	Kind string
	ID   string
	// ExpectedResource is the host-validated initial OAuth audience, not a request-supplied override.
	ExpectedResource string
}

const maxOperatorMetadataBytes = 4096

func operatorDenied(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrOperatorDenied, err)
	}
	return ErrOperatorDenied
}

func operatorReferenceActor(ref OperatorCredentialReference) (IAMActorReference, bool) {
	if !validIAMUUID(ref.ID) {
		return IAMActorReference{}, false
	}
	switch ref.Kind {
	case "api_key", "session":
		if ref.ExpectedResource != "" {
			return IAMActorReference{}, false
		}
	case "oauth":
		if !validOperatorMetadata(ref.ExpectedResource) || strings.IndexByte(ref.ExpectedResource, 0) >= 0 {
			return IAMActorReference{}, false
		}
	default:
		return IAMActorReference{}, false
	}
	return IAMActorReference{Kind: ref.Kind, ID: ref.ID}, true
}

func validOperatorMetadata(s string) bool {
	return len(s) > 0 && len(s) <= maxOperatorMetadataBytes && utf8.ValidString(s)
}

func operatorAdminPermissions(s string, oauth bool) bool {
	if !validOperatorMetadata(s) {
		return false
	}
	var permissions []string
	if oauth {
		permissions = strings.Split(s, " ")
	} else {
		var values []json.RawMessage
		if json.Unmarshal([]byte(s), &values) != nil || values == nil {
			return false
		}
		for _, value := range values {
			var permission string
			if string(value) == "null" || json.Unmarshal(value, &permission) != nil {
				return false
			}
			permissions = append(permissions, permission)
		}
	}
	seen := make(map[string]bool, 7)
	for _, permission := range permissions {
		switch permission {
		case "read", "invoke", "write", "admin":
		case "openid", "email", "profile":
			if !oauth {
				return false
			}
		default:
			return false
		}
		if seen[permission] {
			return false
		}
		seen[permission] = true
	}
	return seen["admin"]
}

func authorizeOperatorTx(ctx context.Context, tx *sql.Tx, ref OperatorCredentialReference) (IAMActorReference, time.Time, error) {
	actor, ok := operatorReferenceActor(ref)
	if !ok {
		return IAMActorReference{}, time.Time{}, operatorDenied(ctx)
	}
	var expiry sql.NullTime
	var err error
	switch ref.Kind {
	case "api_key":
		var permissions string
		err = tx.QueryRowContext(ctx, `SELECT
			CASE WHEN typeof(permissions)='text' AND length(CAST(permissions AS BLOB)) BETWEEN 1 AND 4096
				THEN permissions END, expires_at FROM api_keys WHERE id=?`, ref.ID).Scan(&permissions, &expiry)
		if err == nil && !operatorAdminPermissions(permissions, false) {
			err = ErrOperatorDenied
		}
	case "session":
		var id string
		var userID int64
		err = tx.QueryRowContext(ctx, `SELECT s.id,u.id,s.expires_at
			FROM sessions s JOIN users u ON u.id=s.user_id
			WHERE s.id=? AND u.id>0
			AND (SELECT COUNT(*) FROM sessions other WHERE other.id=s.id)=1`, ref.ID).
			Scan(&id, &userID, &expiry)
		if err == nil && (id != ref.ID || !validIAMUUID(id) || userID <= 0 || !expiry.Valid) {
			err = ErrOperatorDenied
		}
	case "oauth":
		var scope, clientScope, resource string
		err = tx.QueryRowContext(ctx, `SELECT
			CASE WHEN typeof(t.scope)='text' AND length(CAST(t.scope AS BLOB)) BETWEEN 1 AND 4096 THEN t.scope END,
			t.access_expires_at,
			CASE WHEN typeof(t.resource)='text' AND length(CAST(t.resource AS BLOB)) BETWEEN 1 AND 4096 THEN t.resource END,
			CASE WHEN typeof(c.scope)='text' AND length(CAST(c.scope AS BLOB)) BETWEEN 1 AND 4096 THEN c.scope END
			FROM oauth_access_tokens t JOIN oauth_clients c ON c.client_id=t.client_id
			JOIN users u ON u.id=t.user_id
			WHERE t.id=? AND t.revoked_at IS NULL AND c.revoked_at IS NULL AND u.id>0`, ref.ID).
			Scan(&scope, &expiry, &resource, &clientScope)
		if err == nil && (!expiry.Valid || resource != ref.ExpectedResource ||
			!operatorAdminPermissions(scope, true) || !operatorAdminPermissions(clientScope, true)) {
			err = ErrOperatorDenied
		}
	}
	if err != nil || (expiry.Valid && !expiry.Time.After(time.Now().UTC())) {
		return IAMActorReference{}, time.Time{}, operatorDenied(ctx)
	}
	return actor, expiry.Time.UTC(), nil
}

// PublishWorkloadEnvironmentAsOperator rechecks current admin authority in the publication transaction.
// Trusted submission capture and ordered live IAM/pool publication remain service obligations; no serving caller uses this seam.
func (db *Database) PublishWorkloadEnvironmentAsOperator(ctx context.Context, ref OperatorCredentialReference, c WorkloadEnvironmentChange) (*WorkloadEnvironmentPublication, error) {
	actor, ok := operatorReferenceActor(ref)
	if !ok || (c.Actor != (IAMActorReference{}) && c.Actor != actor) {
		return nil, operatorDenied(ctx)
	}
	c.Actor = actor
	envJSON, err := prepareWorkloadEnvironmentChange(c)
	if err != nil {
		return nil, err
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, operatorDenied(ctx)
	}
	defer tx.Rollback()
	actor, expiry, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	c.Actor = actor
	publication, err := publishWorkloadEnvironmentTx(ctx, tx, c, envJSON, expiry)
	if err != nil {
		return nil, err
	}
	// Final expiry check linearizes before commit; time passing during commit cannot be excluded.
	if (!expiry.IsZero() && !expiry.After(time.Now().UTC())) || ctx.Err() != nil {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "publication commit")
	}
	return publication, nil
}
