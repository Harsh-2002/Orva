package database

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

var ErrWorkloadResumeAssociation = errors.New("paused workload has no exact current sealed deployment association")

// WorkloadResumePlan is metadata, not permission to install a sealed pair.
type WorkloadResumePlan struct {
	request        WorkloadPauseRequest
	operator       OperatorCredentialReference
	pause          WorkloadOperatorPause
	artifact       WorkloadArtifactRecord
	policy         IAMPolicyRecord
	sourceDigest   string
	snapshotDigest string
	deploymentID   string
	association    rollbackAssociation
	installed      bool
}

func (p *WorkloadResumePlan) Output() WorkloadBuildOutput { return p.association.output }
func (p *WorkloadResumePlan) Runtime() string             { return p.association.runtime }
func (p *WorkloadResumePlan) AuthoredEntrypoint() string  { return p.association.authored }
func (p *WorkloadResumePlan) ExpectedAuthority() workloadiam.Metadata {
	return p.policy.Metadata
}

func (p *WorkloadResumePlan) DenyPolicy() *IAMPolicyRecord {
	r := p.policy
	r.Revision++
	r.AuthorityEpoch++
	r.ReviewRequired = true
	return &r
}

func (db *Database) PrepareWorkloadResume(ctx context.Context, ref OperatorCredentialReference,
	req WorkloadPauseRequest) (*WorkloadResumePlan, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "operator resume plan begin")
	}
	defer tx.Rollback()
	p, _, err := prepareWorkloadResumeTx(ctx, tx, ref, req)
	return p, err
}

func prepareWorkloadResumeTx(ctx context.Context, tx *sql.Tx, ref OperatorCredentialReference,
	req WorkloadPauseRequest) (*WorkloadResumePlan, *WorkloadArtifactSnapshot, error) {
	if !validIAMUUID(req.OwnerID) || !validIAMUUID(req.ExpectedGeneration) ||
		req.ExpectedPolicyRevision == 0 || req.ExpectedPolicyRevision > math.MaxInt64 ||
		req.ExpectedAuthorityEpoch == 0 || req.ExpectedAuthorityEpoch > math.MaxInt64 {
		return nil, nil, ErrWorkloadArtifactInvalid
	}
	_, expiry, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return nil, nil, err
	}
	enabled, err := workloadPauseModeTx(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return nil, nil, ErrWorkloadPauseUnavailable
	}
	pause, present, err := workloadPauseRecordTx(ctx, tx, req.OwnerID)
	if err != nil {
		return nil, nil, err
	}
	if !present {
		return nil, nil, ErrWorkloadArtifactConflict
	}
	snapshot, err := readDeploymentSnapshotTx(ctx, tx, req.OwnerID)
	if err != nil {
		return nil, nil, err
	}
	sources, err := readSources(ctx, tx, req.OwnerID)
	if err != nil {
		return nil, nil, workloadPublicationReadError(ctx, err)
	}
	fn, policy, a := snapshot.Function, sources.Record, snapshot.Artifact
	if fn.Status != "inactive" || a.Generation != req.ExpectedGeneration ||
		policy.Revision != req.ExpectedPolicyRevision || policy.AuthorityEpoch != req.ExpectedAuthorityEpoch ||
		pause.ArtifactGeneration != a.Generation || pause.PolicyRevision > policy.Revision || pause.AuthorityEpoch > policy.AuthorityEpoch {
		return nil, nil, ErrWorkloadArtifactConflict
	}
	if fn.Version <= 0 || fn.Version == math.MaxInt || policy.Revision >= math.MaxInt64 ||
		policy.AuthorityEpoch >= math.MaxInt64 || a.Revision >= math.MaxInt64 || a.Epoch >= math.MaxInt64 {
		return nil, nil, ErrWorkloadArtifactExhausted
	}
	for _, row := range snapshot.Secrets {
		if !utf8.ValidString(row.ValueEncrypted) {
			return nil, nil, ErrWorkloadArtifactInvalid
		}
	}
	digest, err := deploymentSourceDigest(sources)
	if err != nil {
		return nil, nil, err
	}
	association, installed, err := readResumeAssociationTx(ctx, tx, fn, a.Generation)
	if err != nil {
		return nil, nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, nil, operatorDenied(ctx)
	}
	return &WorkloadResumePlan{request: req, operator: ref, pause: *pause, artifact: a, policy: *policy,
		sourceDigest: digest, snapshotDigest: deploymentSnapshotDigest(snapshot),
		deploymentID: fn.ActiveDeploymentID, association: association, installed: installed}, snapshot, nil
}

func readResumeAssociationTx(ctx context.Context, tx *sql.Tx, fn *Function, generation string) (rollbackAssociation, bool, error) {
	if !validIAMUUID(fn.ActiveDeploymentID) {
		return rollbackAssociation{}, false, ErrWorkloadResumeAssociation
	}
	a, err := readRollbackAssociationTx(ctx, tx, fn.ID, fn.ActiveDeploymentID)
	if errors.Is(err, ErrWorkloadArtifactConflict) || errors.Is(err, ErrWorkloadArtifactInvalid) {
		return a, false, ErrWorkloadResumeAssociation
	}
	if err != nil {
		return a, false, err
	}
	if a.runtime != fn.Runtime || a.authored != fn.Entrypoint ||
		a.output.CodeHash != fn.CodeHash || a.output.Entrypoint != executedEntrypoint(fn) {
		return a, false, ErrWorkloadResumeAssociation
	}
	if a.generation == generation {
		var installed bool
		err := tx.QueryRowContext(ctx, `SELECT phase='installed' FROM deployments
			WHERE id=? AND phase IN ('review_required','installed')`, fn.ActiveDeploymentID).Scan(&installed)
		if errors.Is(err, sql.ErrNoRows) {
			return a, false, ErrWorkloadResumeAssociation
		}
		if err != nil {
			return a, false, workloadArtifactStorageError(ctx, "operator resume original phase")
		}
		return a, installed, nil
	}
	installed, err := readDeploymentPromotionTx(ctx, tx, fn.ID, fn.ActiveDeploymentID, generation, a.digest())
	if errors.Is(err, ErrWorkloadArtifactConflict) {
		return a, false, ErrWorkloadResumeAssociation
	}
	return a, installed, err
}

// CommitWorkloadResume requires verified pair leases and the build, owner and
// IAM gates. It consumes pause intent into a new inactive pending installation.
func (db *Database) CommitWorkloadResume(ctx context.Context, ref OperatorCredentialReference,
	p *WorkloadResumePlan, verified WorkloadBuildOutput) (*WorkloadEnvironmentPublication, error) {
	if p == nil || !verified.valid() || verified != p.Output() {
		return nil, ErrWorkloadArtifactInvalid
	}
	if p.operator != ref {
		return nil, ErrOperatorDenied
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "operator resume begin")
	}
	defer tx.Rollback()
	current, snapshot, err := prepareWorkloadResumeTx(ctx, tx, ref, p.request)
	if err != nil {
		return nil, err
	}
	if current.sourceDigest != p.sourceDigest {
		return nil, ErrIAMConflict
	}
	if *current != *p {
		return nil, ErrWorkloadArtifactConflict
	}
	actor, expiry, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	a, r, owner := current.artifact, current.policy, p.request.OwnerID
	c := WorkloadEnvironmentChange{WorkloadArtifactChange: WorkloadArtifactChange{
		OwnerID: owner, ExpectedRevision: a.Revision, ExpectedEpoch: a.Epoch, ExpectedGeneration: a.Generation, Actor: actor},
		ExpectedFunctionVersion: snapshot.Function.Version, ExpectedPolicyRevision: r.Revision,
		ExpectedApprovedArtifact: r.ApprovedArtifact, Env: snapshot.Function.EnvVars}
	env, err := prepareWorkloadEnvironmentChange(c)
	if err != nil {
		return nil, err
	}
	out, err := publishWorkloadEnvironmentTx(ctx, tx, c, env, expiry)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workload_deployment_promotion(function_id,deployment_id,generation,association_digest,installed)
		VALUES(?,?,?,?,0) ON CONFLICT(function_id) DO UPDATE
		SET deployment_id=excluded.deployment_id,generation=excluded.generation,association_digest=excluded.association_digest,installed=0`,
		owner, p.deploymentID, out.Artifact.Generation, p.association.digest())
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "operator resume promotion")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM workload_operator_pause WHERE function_id=?`, owner)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "operator resume pause")
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return nil, ErrWorkloadArtifactConflict
	}
	if _, _, err := authorizeOperatorTx(ctx, tx, ref); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "operator resume commit")
	}
	return out, nil
}
