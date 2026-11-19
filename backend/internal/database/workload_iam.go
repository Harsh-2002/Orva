package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/google/uuid"
)

const (
	// IAMUnboundArtifact is not a worker artifact; it can bind only an empty policy.
	IAMUnboundArtifact = "iam:unbound"
	// IAMRestoredUnapprovedArtifact permits retained policy text, never a worker artifact.
	IAMRestoredUnapprovedArtifact = "iam:restored-unapproved"
	IAMAuditRetention             = 32
)

var (
	ErrIAMConflict          = errors.New("IAM policy revision or stored approval conflict")
	ErrIAMPolicyNotFound    = errors.New("IAM policy not found")
	ErrIAMFunctionNotFound  = errors.New("IAM function not found")
	ErrIAMInvalidPolicy     = errors.New("invalid IAM policy or metadata")
	ErrIAMRevisionExhausted = errors.New("IAM revision or authority epoch exhausted")
)

// IAMActorReference is provenance only, never proof of authentication/permission.
// ID is an immutable principal/credential UUID reference, never a token or label.
type IAMActorReference struct {
	Kind string
	ID   string
}

type IAMPolicyRecord struct {
	workloadiam.Metadata
	AuthorityEpoch uint64
	ReviewRequired bool
	Quarantined    bool
	PolicyJSON     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AdmissionEvaluator retains the current revision while withholding unapproved
// authority. Hosts must still coordinate live publication and worker admission.
func (r *IAMPolicyRecord) AdmissionEvaluator() (*workloadiam.Evaluator, error) {
	if r == nil || r.AuthorityEpoch == 0 || r.AuthorityEpoch > math.MaxInt64 {
		return nil, ErrIAMInvalidPolicy
	}
	canonical, err := canonicalIAMPolicy(r.Metadata, []byte(r.PolicyJSON))
	if err != nil || canonical != r.PolicyJSON {
		return nil, ErrIAMInvalidPolicy
	}
	if r.ReviewRequired || r.Quarantined || r.ApprovedArtifact == IAMRestoredUnapprovedArtifact {
		return workloadiam.CompileDeny(r.Metadata)
	}
	p, err := workloadiam.Decode([]byte(canonical))
	if err != nil {
		return nil, ErrIAMInvalidPolicy
	}
	return workloadiam.Compile(r.Metadata, p)
}

type IAMPolicyChange struct {
	OwnerID                  string
	ExpectedRevision         uint64
	ExpectedApprovedArtifact string
	ApprovedArtifact         string
	PolicyJSON               []byte
	ReviewRequired           bool
	Quarantined              bool
	Actor                    IAMActorReference
}

type IAMPolicyAudit struct {
	Revision       uint64
	AuthorityEpoch uint64
	Digest         string
	Actor          IAMActorReference
	ChangedAt      time.Time
}

func validIAMUUID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id && u.Version() >= 1 && u.Version() <= 8 && u.Variant() == uuid.RFC4122
}

func validateIAMActor(a IAMActorReference) error {
	if !validIAMUUID(a.ID) {
		return ErrIAMInvalidPolicy
	}
	switch a.Kind {
	case "api_key", "session", "oauth", "system":
		return nil
	default:
		return ErrIAMInvalidPolicy
	}
}

func canonicalIAMPolicy(m workloadiam.Metadata, data []byte) (string, error) {
	if m.Revision == 0 || m.Revision > math.MaxInt64 {
		return "", ErrIAMInvalidPolicy
	}
	p, err := workloadiam.Decode(data)
	if err != nil {
		return "", ErrIAMInvalidPolicy
	}
	if m.ApprovedArtifact == IAMUnboundArtifact && (len(p.Allow) != 0 || len(p.Inbound) != 0) {
		return "", ErrIAMInvalidPolicy
	}
	if _, err := workloadiam.Compile(m, p); err != nil {
		return "", ErrIAMInvalidPolicy
	}
	canonical, err := json.Marshal(p)
	if err != nil {
		return "", ErrIAMInvalidPolicy
	}
	return string(canonical), nil
}

// NewIAMPolicy creates only empty authority; SDK compatibility is not translated.
func (db *Database) NewIAMPolicy(ctx context.Context, ownerID string, reviewRequired bool, actor IAMActorReference) (*IAMPolicyRecord, error) {
	r := &IAMPolicyRecord{Metadata: workloadiam.Metadata{OwnerID: ownerID, Revision: 1, ApprovedArtifact: IAMUnboundArtifact}, AuthorityEpoch: 1, ReviewRequired: reviewRequired}
	data, _ := json.Marshal(workloadiam.Policy{SchemaVersion: workloadiam.SchemaVersion, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}})
	var err error
	if r.PolicyJSON, err = canonicalIAMPolicy(r.Metadata, data); err != nil {
		return nil, err
	}
	if err := validateIAMActor(actor); err != nil {
		return nil, err
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create IAM policy: %w", err)
	}
	defer tx.Rollback()
	if err := iamFunctionExists(ctx, tx, ownerID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	r.CreatedAt, r.UpdatedAt = now, now
	result, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_policies
	 (function_id,revision,authority_epoch,approved_artifact,review_required,quarantined,policy_json,created_at,updated_at)
	 VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(function_id) DO NOTHING`,
		ownerID, int64(r.Revision), int64(r.AuthorityEpoch), r.ApprovedArtifact, r.ReviewRequired, r.Quarantined, r.PolicyJSON, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("create IAM policy: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, ErrIAMConflict
	}
	if err := appendIAMAudit(ctx, tx, r, actor); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create IAM policy: %w", err)
	}
	return r, nil
}

func iamFunctionExists(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) error {
	var status string
	err := q.QueryRowContext(ctx, `SELECT status FROM functions WHERE id=?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIAMFunctionNotFound
	}
	if err != nil {
		return fmt.Errorf("IAM function lookup: %w", err)
	}
	if status == FunctionStatusDeleting {
		return ErrFunctionDeleting
	}
	return nil
}

const iamPolicySelect = `SELECT function_id,revision,authority_epoch,approved_artifact,review_required,quarantined,policy_json,created_at,updated_at FROM workload_iam_policies WHERE function_id=?`

func scanIAMPolicy(row *sql.Row) (*IAMPolicyRecord, error) {
	r := &IAMPolicyRecord{}
	var revision, epoch int64
	var created, updated string
	err := row.Scan(&r.OwnerID, &revision, &epoch, &r.ApprovedArtifact, &r.ReviewRequired, &r.Quarantined, &r.PolicyJSON, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIAMPolicyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read IAM policy: %w", err)
	}
	if revision <= 0 || epoch <= 0 {
		return nil, ErrIAMInvalidPolicy
	}
	r.Revision, r.AuthorityEpoch = uint64(revision), uint64(epoch)
	canonical, err := canonicalIAMPolicy(r.Metadata, []byte(r.PolicyJSON))
	if err != nil || canonical != r.PolicyJSON {
		return nil, ErrIAMInvalidPolicy
	}
	if r.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, ErrIAMInvalidPolicy
	}
	if r.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return nil, ErrIAMInvalidPolicy
	}
	return r, nil
}

func (db *Database) GetIAMPolicy(ctx context.Context, ownerID string) (*IAMPolicyRecord, error) {
	if !validIAMUUID(ownerID) {
		return nil, ErrIAMInvalidPolicy
	}
	if err := iamFunctionExists(ctx, db.read, ownerID); err != nil {
		return nil, err
	}
	return scanIAMPolicy(db.read.QueryRowContext(ctx, iamPolicySelect, ownerID))
}

// SaveIAMPolicy is synchronous persistence only. A future service must check
// live reviewed artifact/config and admin authority under the ordered host gates.
func (db *Database) SaveIAMPolicy(ctx context.Context, c IAMPolicyChange) (*IAMPolicyRecord, error) {
	canonical, err := prepareIAMPolicyChange(c)
	if err != nil {
		return nil, err
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("save IAM policy: %w", err)
	}
	defer tx.Rollback()
	r, err := saveIAMPolicyTx(ctx, tx, c, canonical)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("save IAM policy: %w", err)
	}
	return r, nil
}

func prepareIAMPolicyChange(c IAMPolicyChange) (string, error) {
	if c.ExpectedRevision == 0 || c.ExpectedRevision > math.MaxInt64 {
		return "", ErrIAMInvalidPolicy
	}
	if c.ExpectedRevision == math.MaxInt64 {
		return "", ErrIAMRevisionExhausted
	}
	// Validate the caller's complete document/metadata before reserving SQLite.
	canonical, err := canonicalIAMPolicy(workloadiam.Metadata{OwnerID: c.OwnerID, Revision: c.ExpectedRevision + 1, ApprovedArtifact: c.ApprovedArtifact}, c.PolicyJSON)
	if err != nil {
		return "", err
	}
	if err := validateIAMActor(c.Actor); err != nil {
		return "", err
	}
	return canonical, nil
}

func saveIAMPolicyTx(ctx context.Context, tx *sql.Tx, c IAMPolicyChange, canonical string) (*IAMPolicyRecord, error) {
	if err := iamFunctionExists(ctx, tx, c.OwnerID); err != nil {
		return nil, err
	}
	r, err := scanIAMPolicy(tx.QueryRowContext(ctx, iamPolicySelect, c.OwnerID))
	if err != nil {
		return nil, err
	}
	if r.Revision != c.ExpectedRevision || r.ApprovedArtifact != c.ExpectedApprovedArtifact {
		return nil, ErrIAMConflict
	}
	if r.AuthorityEpoch == math.MaxInt64 {
		return nil, ErrIAMRevisionExhausted
	}
	r.Revision++
	r.AuthorityEpoch++
	r.ApprovedArtifact, r.PolicyJSON = c.ApprovedArtifact, canonical
	r.ReviewRequired, r.Quarantined = c.ReviewRequired, c.Quarantined
	r.UpdatedAt = time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE workload_iam_policies SET revision=?,authority_epoch=?,approved_artifact=?,review_required=?,quarantined=?,policy_json=?,updated_at=? WHERE function_id=? AND revision=? AND approved_artifact=?`,
		int64(r.Revision), int64(r.AuthorityEpoch), r.ApprovedArtifact, r.ReviewRequired, r.Quarantined, r.PolicyJSON, r.UpdatedAt.Format(time.RFC3339Nano), r.OwnerID, int64(c.ExpectedRevision), c.ExpectedApprovedArtifact)
	if err != nil {
		return nil, fmt.Errorf("save IAM policy: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, ErrIAMConflict
	}
	if err := appendIAMAudit(ctx, tx, r, c.Actor); err != nil {
		return nil, err
	}
	return r, nil
}

func appendIAMAudit(ctx context.Context, tx *sql.Tx, r *IAMPolicyRecord, actor IAMActorReference) error {
	// Hash the full approved state; retain neither raw selectors nor artifacts.
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("IAM audit digest: %w", err)
	}
	digest := sha256.Sum256(data)
	_, err = tx.ExecContext(ctx, `INSERT INTO workload_iam_audit (function_id,revision,authority_epoch,digest,actor_kind,actor_id,changed_at) VALUES (?,?,?,?,?,?,?)`,
		r.OwnerID, int64(r.Revision), int64(r.AuthorityEpoch), hex.EncodeToString(digest[:]), actor.Kind, actor.ID, r.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("IAM audit insert: %w", err)
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM workload_iam_audit WHERE function_id=? AND revision NOT IN (SELECT revision FROM workload_iam_audit WHERE function_id=? ORDER BY revision DESC LIMIT ?)`, r.OwnerID, r.OwnerID, IAMAuditRetention)
	if err != nil {
		return fmt.Errorf("IAM audit retention: %w", err)
	}
	return nil
}

func (db *Database) ListIAMPolicyAudit(ctx context.Context, ownerID string) ([]IAMPolicyAudit, error) {
	if !validIAMUUID(ownerID) {
		return nil, ErrIAMInvalidPolicy
	}
	rows, err := db.read.QueryContext(ctx, `SELECT revision,authority_epoch,digest,actor_kind,actor_id,changed_at FROM workload_iam_audit WHERE function_id=? ORDER BY revision DESC LIMIT ?`, ownerID, IAMAuditRetention)
	if err != nil {
		return nil, fmt.Errorf("IAM audit read: %w", err)
	}
	defer rows.Close()
	result := []IAMPolicyAudit{}
	for rows.Next() {
		var a IAMPolicyAudit
		var revision, epoch int64
		var changed string
		if err := rows.Scan(&revision, &epoch, &a.Digest, &a.Actor.Kind, &a.Actor.ID, &changed); err != nil {
			return nil, err
		}
		if revision <= 0 || epoch <= 0 {
			return nil, ErrIAMInvalidPolicy
		}
		a.Revision, a.AuthorityEpoch = uint64(revision), uint64(epoch)
		if a.ChangedAt, err = time.Parse(time.RFC3339Nano, changed); err != nil {
			return nil, ErrIAMInvalidPolicy
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
