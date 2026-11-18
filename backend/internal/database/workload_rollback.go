package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/safepath"
)

type rollbackAssociation struct {
	generation, provenance, raw string
	output                      WorkloadBuildOutput
	runtime, authored           string
	version                     int64
}

func (a rollbackAssociation) digest() string {
	raw, _ := json.Marshal(struct {
		Version int64
		Fields  [3][]byte
	}{a.version, [3][]byte{[]byte(a.generation), []byte(a.provenance), []byte(a.raw)}})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// WorkloadRollbackPlan holds only bounded host metadata and CAS digests.
type WorkloadRollbackPlan struct {
	owner, target, sources, snapshot string
	current, historical              rollbackAssociation
	operator                         OperatorCredentialReference
	policy                           IAMPolicyRecord
	pauseMode                        bool
}

func (p *WorkloadRollbackPlan) Output() WorkloadBuildOutput { return p.historical.output }
func (p *WorkloadRollbackPlan) DeploymentVersion() int64    { return p.historical.version }
func (p *WorkloadRollbackPlan) Runtime() string             { return p.historical.runtime }
func (p *WorkloadRollbackPlan) AuthoredEntrypoint() string  { return p.historical.authored }
func (p *WorkloadRollbackPlan) PolicyRevision() uint64      { return p.policy.Revision }

func (p *WorkloadRollbackPlan) DenyPolicy() *IAMPolicyRecord {
	r := p.policy
	r.Revision++
	r.AuthorityEpoch++
	r.ReviewRequired = true
	return &r
}

func readRollbackAssociationTx(ctx context.Context, tx *sql.Tx, owner, id string) (rollbackAssociation, error) {
	var a rollbackAssociation
	var hash string
	err := tx.QueryRowContext(ctx, `SELECT c.published_generation,c.provenance,c.output,d.code_hash,d.version
		FROM workload_deployment_capture c JOIN deployments d ON d.id=c.deployment_id
		WHERE d.id=? AND d.function_id=? AND c.function_id=d.function_id AND d.status='succeeded'
		AND length(CAST(c.published_generation AS BLOB))=36 AND length(CAST(d.code_hash AS BLOB))=64
		AND length(CAST(c.provenance AS BLOB))<=16384 AND length(CAST(c.output AS BLOB))<=16384`,
		id, owner).Scan(&a.generation, &a.provenance, &a.raw, &hash, &a.version)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrWorkloadArtifactConflict
	}
	if err != nil {
		return a, workloadArtifactStorageError(ctx, "rollback association")
	}
	var p deploymentProvenance
	if a.version <= 0 || !validIAMUUID(a.generation) || json.Unmarshal([]byte(a.provenance), &p) != nil ||
		p.Owner != owner || p.Artifact.OwnerID != owner || (p.Runtime != "node" && p.Runtime != "python") ||
		json.Unmarshal([]byte(a.raw), &a.output) != nil || !a.output.valid() || a.output.CodeHash != hash ||
		safepath.Validate(p.Entrypoint) != nil || safepath.Validate(a.output.Entrypoint) != nil {
		return a, ErrWorkloadArtifactInvalid
	}
	a.runtime, a.authored = p.Runtime, p.Entrypoint
	return a, nil
}

func readDeploymentPromotionTx(ctx context.Context, tx *sql.Tx, owner, id, generation, digest string) (bool, error) {
	var installed bool
	err := tx.QueryRowContext(ctx, `SELECT installed FROM workload_deployment_promotion
		WHERE function_id=? AND deployment_id=? AND generation=? AND association_digest=?`, owner, id, generation, digest).Scan(&installed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrWorkloadArtifactConflict
	}
	if err != nil {
		return false, workloadArtifactStorageError(ctx, "read deployment promotion")
	}
	return installed, nil
}

func readRollbackTx(ctx context.Context, tx *sql.Tx, ref OperatorCredentialReference, owner, target string) (*WorkloadRollbackPlan, *WorkloadArtifactSnapshot, IAMActorReference, string, time.Time, error) {
	p := &WorkloadRollbackPlan{owner: owner, target: target, operator: ref}
	sources, err := readSources(ctx, tx, owner)
	if err != nil {
		return nil, nil, IAMActorReference{}, "", time.Time{}, workloadPublicationReadError(ctx, err)
	}
	required, err := deploymentPermission(sources)
	if err != nil {
		return nil, nil, IAMActorReference{}, "", time.Time{}, err
	}
	actor, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, required)
	if err != nil {
		return nil, nil, IAMActorReference{}, "", time.Time{}, err
	}
	snapshot, err := p.capture(ctx, tx, sources)
	return p, snapshot, actor, required, expiry, err
}

func (p *WorkloadRollbackPlan) capture(ctx context.Context, tx *sql.Tx, sources IAMSourceSnapshot) (*WorkloadArtifactSnapshot, error) {
	var err error
	p.sources, err = deploymentSourceDigest(sources)
	if err != nil {
		return nil, err
	}
	p.policy = *sources.Record
	p.pauseMode, err = workloadPauseModeTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if p.pauseMode {
		if err := workloadRequireUnpausedTx(ctx, tx, p.owner); err != nil {
			return nil, err
		}
	}
	s, err := readDeploymentSnapshotTx(ctx, tx, p.owner)
	if err != nil {
		return nil, err
	}
	f := s.Function
	if !validIAMUUID(f.ActiveDeploymentID) || f.ActiveDeploymentID == p.target {
		return nil, ErrWorkloadArtifactConflict
	}
	if f.Status != "active" {
		if f.Status != "inactive" || !p.policy.ReviewRequired {
			return nil, ErrWorkloadArtifactConflict
		}
		pending, err := workloadPendingReviewTx(ctx, tx, p.owner, f, s.Artifact.Generation)
		if err != nil {
			return nil, err
		}
		if !pending {
			return nil, ErrWorkloadArtifactConflict
		}
	}
	if f.Version <= 0 || f.Version == math.MaxInt || p.policy.Revision >= math.MaxInt64 ||
		p.policy.AuthorityEpoch >= math.MaxInt64 || s.Artifact.Revision >= math.MaxInt64 || s.Artifact.Epoch >= math.MaxInt64 {
		return nil, ErrWorkloadArtifactExhausted
	}
	p.current, err = readRollbackAssociationTx(ctx, tx, p.owner, f.ActiveDeploymentID)
	if err != nil {
		return nil, err
	}
	p.historical, err = readRollbackAssociationTx(ctx, tx, p.owner, p.target)
	if err != nil {
		return nil, err
	}
	if p.historical.authored != f.Entrypoint || p.historical.runtime != f.Runtime ||
		p.current.authored != f.Entrypoint || p.current.runtime != f.Runtime ||
		p.current.output.CodeHash != f.CodeHash || p.current.output.Entrypoint != executedEntrypoint(f) {
		return nil, ErrWorkloadArtifactConflict
	}
	for _, row := range s.Secrets {
		if !utf8.ValidString(row.ValueEncrypted) {
			return nil, ErrWorkloadArtifactInvalid
		}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	sum := sha256.Sum256(raw)
	p.snapshot = hex.EncodeToString(sum[:])
	return s, nil
}

func executedEntrypoint(f *Function) string {
	if f.RunEntrypoint != "" {
		return f.RunEntrypoint
	}
	return f.Entrypoint
}

func (db *Database) PrepareWorkloadRollback(ctx context.Context, ref OperatorCredentialReference, owner, target string) (*WorkloadRollbackPlan, error) {
	if !validIAMUUID(owner) || !validIAMUUID(target) {
		return nil, ErrWorkloadArtifactInvalid
	}
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "rollback prepare")
	}
	defer tx.Rollback()
	p, _, _, _, _, err := readRollbackTx(ctx, tx, ref, owner, target)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// CommitWorkloadRollback requires the caller to hold verified historical leases
// and the ordered build, owner and IAM gates. Output is host evidence, not input.
func (db *Database) CommitWorkloadRollback(ctx context.Context, ref OperatorCredentialReference,
	p *WorkloadRollbackPlan, verified WorkloadBuildOutput) (*WorkloadEnvironmentPublication, error) {
	if p == nil || verified != p.Output() || !verified.valid() {
		return nil, ErrWorkloadArtifactInvalid
	}
	if p.operator != ref {
		return nil, ErrOperatorDenied
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "rollback begin")
	}
	defer tx.Rollback()
	current, s, actor, required, expiry, err := readRollbackTx(ctx, tx, ref, p.owner, p.target)
	if err != nil {
		return nil, err
	}
	if current.sources != p.sources {
		return nil, ErrIAMConflict
	}
	if current.snapshot != p.snapshot || current.historical != p.historical || current.current != p.current ||
		current.pauseMode != p.pauseMode {
		return nil, ErrWorkloadArtifactConflict
	}
	a, r := s.Artifact, current.policy
	c := WorkloadEnvironmentChange{WorkloadArtifactChange: WorkloadArtifactChange{
		OwnerID: p.owner, ExpectedRevision: a.Revision, ExpectedEpoch: a.Epoch, ExpectedGeneration: a.Generation, Actor: actor},
		ExpectedFunctionVersion: s.Function.Version, ExpectedPolicyRevision: r.Revision,
		ExpectedApprovedArtifact: r.ApprovedArtifact, Env: s.Function.EnvVars}
	env, err := prepareWorkloadEnvironmentChange(c)
	if err != nil {
		return nil, err
	}
	out, err := publishWorkloadEnvironmentTx(ctx, tx, c, env, expiry)
	if err != nil {
		return nil, err
	}
	run := verified.Entrypoint
	if run == s.Function.Entrypoint {
		run = ""
	}
	res, err := tx.ExecContext(ctx, `UPDATE functions SET code_hash=?,image='',image_size=0,run_entrypoint=?,
		active_deployment_id=?,status='inactive' WHERE id=? AND version=? AND active_deployment_id=?`,
		verified.CodeHash, run, p.target, p.owner, s.Function.Version+1, s.Function.ActiveDeploymentID)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "rollback function")
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return nil, ErrWorkloadArtifactConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workload_deployment_promotion(function_id,deployment_id,generation,association_digest,installed)
		VALUES(?,?,?,?,0) ON CONFLICT(function_id) DO UPDATE
		SET deployment_id=excluded.deployment_id,generation=excluded.generation,association_digest=excluded.association_digest,installed=0`,
		p.owner, p.target, out.Artifact.Generation, p.historical.digest())
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "rollback promotion")
	}
	out.Function, err = readWorkloadArtifactFunctionTx(ctx, tx, p.owner)
	if err != nil {
		return nil, err
	}
	if _, _, err := authorizeOperatorPermissionTx(ctx, tx, ref, required); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "rollback commit")
	}
	return out, nil
}
