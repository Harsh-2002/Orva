package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/safepath"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

var (
	ErrWorkloadCreateInvalid = errors.New("invalid managed function name, runtime or entrypoint")
	ErrWorkloadNameConflict  = errors.New("function name already exists")
	workloadName             = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type WorkloadCreateRequest struct {
	Name       string `json:"name"`
	Runtime    string `json:"runtime"`
	Entrypoint string `json:"entrypoint,omitempty"`
}

// WorkloadCreation binds the host-allocated owner to the candidate used by the IAM gate.
type WorkloadCreation struct {
	request WorkloadCreateRequest
	policy  IAMPolicyRecord
}

func PrepareWorkloadCreation(in WorkloadCreateRequest) (*WorkloadCreation, error) {
	if len(in.Name) > 128 || !workloadName.MatchString(in.Name) {
		return nil, ErrWorkloadCreateInvalid
	}
	switch in.Runtime {
	case "node":
		if in.Entrypoint == "" {
			in.Entrypoint = "handler.js"
		}
	case "python":
		if in.Entrypoint == "" {
			in.Entrypoint = "handler.py"
		}
	default:
		return nil, ErrWorkloadCreateInvalid
	}
	if len(in.Entrypoint) > 4096 || !utf8.ValidString(in.Entrypoint) ||
		strings.IndexByte(in.Entrypoint, 0) >= 0 || safepath.Validate(in.Entrypoint) != nil {
		return nil, ErrWorkloadCreateInvalid
	}
	p := IAMPolicyRecord{Metadata: workloadiam.Metadata{
		OwnerID: ids.New(), Revision: 1, ApprovedArtifact: IAMUnboundArtifact,
	}, AuthorityEpoch: 1}
	body, err := json.Marshal(workloadiam.Policy{SchemaVersion: workloadiam.SchemaVersion, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}})
	if err != nil {
		return nil, err
	}
	p.PolicyJSON = string(body)
	return &WorkloadCreation{request: in, policy: p}, nil
}

func (c *WorkloadCreation) OwnerID() string { return c.policy.OwnerID }

func (c *WorkloadCreation) AdmissionEvaluator() (*workloadiam.Evaluator, error) {
	return c.policy.AdmissionEvaluator()
}

// CreateWorkloadAsOperator must run under the host IAM gate; it never publishes live authority.
func (db *Database) CreateWorkloadAsOperator(ctx context.Context, ref OperatorCredentialReference, c *WorkloadCreation) (*Function, error) {
	if c == nil || !validIAMUUID(c.OwnerID()) {
		return nil, ErrWorkloadCreateInvalid
	}
	db.lifecycleMu.Lock()
	defer db.lifecycleMu.Unlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "managed creation begin")
	}
	defer tx.Rollback()
	actor, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, "write")
	if err != nil {
		return nil, err
	}

	owner := c.OwnerID()
	// The channel carries no grants. It is a host requirement, not an author setting.
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM functions LIMIT 256)`).Scan(&count); err != nil {
		return nil, workloadArtifactStorageError(ctx, "managed function capacity")
	}
	if count >= 256 {
		return nil, ErrWorkloadArtifactExhausted
	}
	var inserted string
	err = tx.QueryRowContext(ctx, `INSERT INTO functions(id,name,runtime,entrypoint,version,status,sdk,memory_mb)
		VALUES(?,?,?,?,1,'created',1,256) ON CONFLICT(name) DO NOTHING RETURNING id`,
		owner, c.request.Name, c.request.Runtime, c.request.Entrypoint).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWorkloadNameConflict
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "managed function insert")
	}

	p := c.policy
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	stamp := now.Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_policies
		(function_id,revision,authority_epoch,approved_artifact,review_required,quarantined,policy_json,created_at,updated_at)
		VALUES(?,?,?,?,0,0,?,?,?)`, owner, p.Revision, p.AuthorityEpoch, p.ApprovedArtifact, p.PolicyJSON, stamp, stamp); err != nil {
		return nil, workloadArtifactStorageError(ctx, "managed policy insert")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_inline(function_id,policy_json) VALUES(?,?)`,
		owner, p.PolicyJSON); err != nil {
		return nil, workloadArtifactStorageError(ctx, "managed inline insert")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_artifact_generations
		(function_id,revision,epoch,generation,actor_kind,actor_id,change_kind,created_at,updated_at)
		VALUES(?,1,1,?,?,?,'initialize',?,?)`, owner, ids.New(), actor.Kind, actor.ID, stamp, stamp); err != nil {
		return nil, workloadArtifactStorageError(ctx, "managed generation insert")
	}
	if err := appendIAMAudit(ctx, tx, &p, actor); err != nil {
		return nil, workloadArtifactStorageError(ctx, "managed creation audit")
	}
	fn, err := readWorkloadArtifactFunctionTx(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	if _, _, err := authorizeOperatorPermissionTx(ctx, tx, ref, "write"); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "managed creation commit")
	}
	db.liveFunctions.Store(owner, struct{}{})
	return fn, nil
}
