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

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const workloadDeploymentSchema = `CREATE TABLE IF NOT EXISTS workload_deployment_capture (
		deployment_id TEXT PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
		function_id TEXT NOT NULL REFERENCES functions(id) ON DELETE CASCADE,
		provenance TEXT NOT NULL CHECK(length(CAST(provenance AS BLOB)) BETWEEN 1 AND 16384),
		output TEXT NOT NULL DEFAULT '' CHECK(length(CAST(output AS BLOB)) <= 16384),
		published_generation TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS workload_deployment_promotion (
		function_id TEXT PRIMARY KEY REFERENCES functions(id) ON DELETE CASCADE,
		deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
		generation TEXT NOT NULL,
		association_digest TEXT NOT NULL CHECK(length(association_digest)=64),
		installed INTEGER NOT NULL CHECK(installed IN (0,1))
	);`

// InitWorkloadDeploymentSchema is explicit managed provisioning, never legacy boot.
func (db *Database) InitWorkloadDeploymentSchema(ctx context.Context) error {
	_, err := db.write.ExecContext(ctx, workloadDeploymentSchema)
	if err != nil {
		return workloadArtifactStorageError(ctx, "deployment schema")
	}
	return nil
}

type deploymentProvenance struct {
	Operator        OperatorCredentialReference
	Owner           string
	FunctionVersion int
	Runtime         string
	Entrypoint      string
	SourceDigest    string
	SnapshotDigest  string
	PolicyRevision  uint64
	AuthorityEpoch  uint64
	Artifact        WorkloadArtifactRecord
}

// WorkloadDeploymentPlan contains no source payload, environment or secret snapshot.
type WorkloadDeploymentPlan struct {
	ID         string
	provenance deploymentProvenance
	before     IAMSourceSnapshot
	function   *Function
}

func (p *WorkloadDeploymentPlan) Function() *Function    { return p.function.Clone() }
func (p *WorkloadDeploymentPlan) PolicyRevision() uint64 { return p.provenance.PolicyRevision }
func (p *WorkloadDeploymentPlan) DenyPolicy() *IAMPolicyRecord {
	r := *p.before.Record
	r.Revision++
	r.AuthorityEpoch++
	r.ReviewRequired = true
	return &r
}

// SourcePrivilege includes disabled/retained attached grants, not just the effective union.
func SourcePrivilege(s IAMSourceSnapshot) (bool, error) {
	if s.Record == nil {
		return false, ErrIAMInvalidPolicy
	}
	privileged := s.Record.ReviewRequired || s.Record.Quarantined
	bodies := []string{s.Record.PolicyJSON, s.InlineJSON}
	for _, a := range s.Attachments {
		bodies = append(bodies, a.PolicyJSON)
	}
	for _, body := range bodies {
		p, err := workloadiam.Decode([]byte(body))
		if err != nil {
			return false, ErrIAMInvalidPolicy
		}
		privileged = privileged || len(p.Allow) != 0 || len(p.Inbound) != 0
	}
	return privileged, nil
}

func deploymentSourceDigest(s IAMSourceSnapshot) (string, error) {
	projected, err := projectSources(s.InlineJSON, s.Attachments)
	if err != nil || s.Record == nil || projected != s.Record.PolicyJSON {
		return "", ErrIAMConflict
	}
	if _, err := s.Record.AdmissionEvaluator(); err != nil {
		return "", err
	}
	// IAMAttachment's public JSON intentionally omits its body.
	bodies := make([]string, 0, len(s.Attachments))
	for _, a := range s.Attachments {
		bodies = append(bodies, a.PolicyJSON)
	}
	data, err := json.Marshal(struct {
		Snapshot IAMSourceSnapshot
		Bodies   []string
	}{s, bodies})
	if err != nil || len(data) > 2<<20 {
		return "", ErrIAMInvalidPolicy
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func deploymentPermission(s IAMSourceSnapshot) (string, error) {
	privileged, err := SourcePrivilege(s)
	if err != nil {
		return "", err
	}
	if privileged {
		return "admin", nil
	}
	return "write", nil
}

// CaptureWorkloadDeployment records provenance and the queued deployment in one TX.
// Its caller holds the owner mutation lock and IAM gate; request metadata is not an input.
func (db *Database) CaptureWorkloadDeployment(ctx context.Context, ref OperatorCredentialReference, owner string) (*WorkloadDeploymentPlan, error) {
	if !validIAMUUID(owner) {
		return nil, ErrWorkloadArtifactInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment capture begin")
	}
	defer tx.Rollback()
	sources, err := readSources(ctx, tx, owner)
	if err != nil {
		return nil, workloadPublicationReadError(ctx, err)
	}
	digest, err := deploymentSourceDigest(sources)
	if err != nil {
		return nil, err
	}
	required, err := deploymentPermission(sources)
	if err != nil {
		return nil, err
	}
	_, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, required)
	if err != nil {
		return nil, err
	}
	snapshot, err := readDeploymentSnapshotTx(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	fn, artifact := snapshot.Function, &snapshot.Artifact
	pauseMode, err := workloadPauseModeTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if pauseMode {
		if err := workloadRequireUnpausedTx(ctx, tx, owner); err != nil {
			return nil, err
		}
	}
	if fn.Status == "inactive" {
		if !pauseMode || !sources.Record.ReviewRequired {
			return nil, ErrWorkloadArtifactConflict
		}
		staged, err := workloadPendingReviewTx(ctx, tx, owner, fn, artifact.Generation)
		if err != nil {
			return nil, err
		}
		if !staged {
			return nil, ErrWorkloadArtifactConflict
		}
	}
	if fn.Version <= 0 || fn.Version == math.MaxInt || sources.Record.Revision >= math.MaxInt64 ||
		sources.Record.AuthorityEpoch >= math.MaxInt64 {
		return nil, ErrWorkloadArtifactExhausted
	}
	var pending int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workload_deployment_capture c
		JOIN deployments d ON d.id=c.deployment_id
		WHERE d.status IN ('queued','building') OR (c.output!='' AND c.published_generation='')`).Scan(&pending)
	if err != nil || pending >= 64 {
		return nil, ErrWorkloadArtifactConflict
	}
	p := &WorkloadDeploymentPlan{ID: ids.New(), before: sources, function: fn,
		provenance: deploymentProvenance{Operator: ref, Owner: owner, FunctionVersion: fn.Version,
			Runtime: fn.Runtime, Entrypoint: fn.Entrypoint, SourceDigest: digest,
			SnapshotDigest: deploymentSnapshotDigest(snapshot),
			PolicyRevision: sources.Record.Revision, AuthorityEpoch: sources.Record.AuthorityEpoch, Artifact: *artifact}}
	raw, err := json.Marshal(p.provenance)
	if err != nil || len(raw) > 16384 {
		return nil, ErrWorkloadArtifactInvalid
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO deployments(id,function_id,version,status,phase,submitted_at)
		SELECT ?,?,COALESCE(MAX(version),0)+1,'queued','queued',? FROM deployments WHERE function_id=?`,
		p.ID, owner, now, owner)
	if err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO workload_deployment_capture(deployment_id,function_id,provenance) VALUES(?,?,?)`,
			p.ID, owner, string(raw))
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment capture")
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment capture commit")
	}
	return p, nil
}

func readDeploymentProvenance(ctx context.Context, tx *sql.Tx, id string) (deploymentProvenance, error) {
	var p deploymentProvenance
	var raw, owner, status string
	err := tx.QueryRowContext(ctx, `SELECT c.provenance,c.function_id,d.status
		FROM workload_deployment_capture c JOIN deployments d ON d.id=c.deployment_id
		WHERE c.deployment_id=? AND c.function_id=d.function_id`, id).Scan(&raw, &owner, &status)
	if err != nil || len(raw) > 16384 || json.Unmarshal([]byte(raw), &p) != nil ||
		p.Owner != owner || !validIAMUUID(owner) || (status != "queued" && status != "building") {
		return p, ErrWorkloadArtifactConflict
	}
	return p, nil
}

func checkDeploymentTx(ctx context.Context, tx *sql.Tx, id string) (*WorkloadDeploymentPlan, time.Time, error) {
	p, err := readDeploymentProvenance(ctx, tx, id)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err := workloadCheckPauseTx(ctx, tx, p.Owner); err != nil {
		return nil, time.Time{}, err
	}
	s, err := readSources(ctx, tx, p.Owner)
	if err != nil {
		return nil, time.Time{}, workloadPublicationReadError(ctx, err)
	}
	required, err := deploymentPermission(s)
	if err != nil {
		return nil, time.Time{}, err
	}
	_, expiry, err := authorizeOperatorPermissionTx(ctx, tx, p.Operator, required)
	if err != nil {
		return nil, expiry, err
	}
	digest, err := deploymentSourceDigest(s)
	if err != nil || digest != p.SourceDigest || s.Record.Revision != p.PolicyRevision ||
		s.Record.AuthorityEpoch != p.AuthorityEpoch {
		return nil, expiry, ErrIAMConflict
	}
	snapshot, err := readDeploymentSnapshotTx(ctx, tx, p.Owner)
	if err != nil {
		return nil, expiry, err
	}
	fn, a := snapshot.Function, &snapshot.Artifact
	if fn.Version != p.FunctionVersion || fn.Runtime != p.Runtime || fn.Entrypoint != p.Entrypoint ||
		*a != p.Artifact || deploymentSnapshotDigest(snapshot) != p.SnapshotDigest {
		return nil, expiry, ErrWorkloadArtifactConflict
	}
	return &WorkloadDeploymentPlan{ID: id, provenance: p, before: s, function: fn}, expiry, nil
}

func deploymentSnapshotDigest(s *WorkloadArtifactSnapshot) string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readDeploymentSnapshotTx(ctx context.Context, tx *sql.Tx, owner string) (*WorkloadArtifactSnapshot, error) {
	var size int64
	err := tx.QueryRowContext(ctx, `SELECT
		COALESCE(length(CAST(name AS BLOB)),0)+COALESCE(length(CAST(description AS BLOB)),0)+
		COALESCE(length(CAST(runtime AS BLOB)),0)+COALESCE(length(CAST(entrypoint AS BLOB)),0)+
		COALESCE(length(CAST(run_entrypoint AS BLOB)),0)+COALESCE(length(CAST(image AS BLOB)),0)+
		COALESCE(length(CAST(code_hash AS BLOB)),0)+COALESCE(length(CAST(active_deployment_id AS BLOB)),0)+
		COALESCE(length(CAST(status AS BLOB)),0) FROM functions WHERE id=?`, owner).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIAMFunctionNotFound
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment metadata bounds")
	}
	if size > 64<<10 {
		return nil, ErrWorkloadArtifactInvalid
	}
	s, err := readWorkloadArtifactSnapshotTx(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	f := s.Function
	for _, value := range []string{f.Name, f.Description, f.Runtime, f.Entrypoint, f.RunEntrypoint, f.Image, f.CodeHash, f.ActiveDeploymentID, f.Status} {
		if !utf8.ValidString(value) {
			return nil, ErrWorkloadArtifactInvalid
		}
	}
	return s, nil
}

func (db *Database) CheckWorkloadDeployment(ctx context.Context, id string) (*WorkloadDeploymentPlan, error) {
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment check")
	}
	defer tx.Rollback()
	p, _, err := checkDeploymentTx(ctx, tx, id)
	return p, err
}

// WorkloadBuildOutput describes an association already verified through concrete
// sealed store leases by the host. Hash strings alone never authorize installation.
type WorkloadBuildOutput struct {
	CodeGeneration, CodeDigest, RuntimeGeneration, RuntimeDigest string
	CodeHash, Entrypoint                                         string
	SourceArchiveSHA256                                          string `json:",omitempty"`
}

func (o WorkloadBuildOutput) valid() bool {
	if !validIAMUUID(o.CodeGeneration) || !validIAMUUID(o.RuntimeGeneration) ||
		len(o.Entrypoint) == 0 || len(o.Entrypoint) > 4096 {
		return false
	}
	for _, d := range []string{o.CodeDigest, o.RuntimeDigest, o.CodeHash} {
		if len(d) != 64 {
			return false
		}
		if _, err := hex.DecodeString(d); err != nil {
			return false
		}
	}
	return o.SourceArchiveSHA256 == "" || enrollmentSHA(o.SourceArchiveSHA256)
}

// RecordWorkloadBuildOutput retains recoverable sealed output before publication.
// A rejected/failed publication does not delete or automatically reclaim it.
func (db *Database) RecordWorkloadBuildOutput(ctx context.Context, id string, output WorkloadBuildOutput) error {
	if !output.valid() {
		return ErrWorkloadArtifactInvalid
	}
	raw, _ := json.Marshal(output)
	result, err := db.write.ExecContext(ctx, `UPDATE workload_deployment_capture SET output=?
		WHERE deployment_id=? AND output='' AND published_generation=''
		AND EXISTS(SELECT 1 FROM deployments WHERE id=? AND status IN ('queued','building'))`,
		string(raw), id, id)
	if err != nil {
		return workloadArtifactStorageError(ctx, "deployment output")
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrWorkloadArtifactConflict
	}
	return nil
}

func (db *Database) PublishWorkloadDeployment(ctx context.Context, id string, output WorkloadBuildOutput) (*WorkloadEnvironmentPublication, error) {
	if !output.valid() {
		return nil, ErrWorkloadArtifactInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment publication begin")
	}
	defer tx.Rollback()
	p, expiry, err := checkDeploymentTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(output)
	var saved string
	if err := tx.QueryRowContext(ctx, `SELECT output FROM workload_deployment_capture WHERE deployment_id=?`, id).Scan(&saved); err != nil || saved != string(raw) {
		return nil, ErrWorkloadArtifactConflict
	}
	actor, ok := operatorReferenceActor(p.provenance.Operator)
	if !ok {
		return nil, ErrOperatorDenied
	}
	a, r := p.provenance.Artifact, p.before.Record
	c := WorkloadEnvironmentChange{WorkloadArtifactChange: WorkloadArtifactChange{
		OwnerID: a.OwnerID, ExpectedRevision: a.Revision, ExpectedEpoch: a.Epoch, ExpectedGeneration: a.Generation, Actor: actor},
		ExpectedFunctionVersion: p.function.Version, ExpectedPolicyRevision: r.Revision,
		ExpectedApprovedArtifact: r.ApprovedArtifact, Env: p.function.EnvVars}
	envJSON, err := prepareWorkloadEnvironmentChange(c)
	if err != nil {
		return nil, err
	}
	out, err := publishWorkloadEnvironmentTx(ctx, tx, c, envJSON, expiry)
	if err != nil {
		return nil, err
	}
	run := ""
	if output.Entrypoint != out.Function.Entrypoint {
		run = output.Entrypoint
	}
	_, err = tx.ExecContext(ctx, `UPDATE functions SET code_hash=?,image='',image_size=0,run_entrypoint=?,active_deployment_id=?,status=? WHERE id=?`,
		output.CodeHash, run, id, "inactive", a.OwnerID)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment function")
	}
	out.Function, err = readWorkloadArtifactFunctionTx(ctx, tx, a.OwnerID)
	if err != nil {
		return nil, err
	}
	snapshot, err := json.Marshal(SnapshotFromFunction(out.Function))
	if err != nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	_, err = tx.ExecContext(ctx, `UPDATE deployments SET status='succeeded',phase='review_required',code_hash=?,snapshot=?,finished_at=? WHERE id=?`,
		output.CodeHash, string(snapshot), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE workload_deployment_capture SET published_generation=? WHERE deployment_id=?`,
			out.Artifact.Generation, id)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM workload_deployment_promotion WHERE function_id=?`, a.OwnerID)
	}
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment completion")
	}
	required, _ := deploymentPermission(p.before)
	if _, _, err := authorizeOperatorPermissionTx(ctx, tx, p.provenance.Operator, required); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment publication commit")
	}
	return out, nil
}

// AbandonWorkloadDeployments never resumes author work or changes current authority.
func (db *Database) AbandonWorkloadDeployments(ctx context.Context) error {
	_, err := db.write.ExecContext(ctx, `UPDATE deployments SET status='failed',phase='abandoned',
		error_message='process restarted; fresh submission required',finished_at=?
		WHERE status IN ('queued','building') AND id IN (SELECT deployment_id FROM workload_deployment_capture)`,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil && !errors.Is(err, context.Canceled) {
		return workloadArtifactStorageError(ctx, "deployment abandonment")
	}
	return err
}

type WorkloadDeploymentInstallation struct {
	snapshot  *WorkloadArtifactSnapshot
	policy    IAMPolicyRecord
	sources   string
	id        string
	output    WorkloadBuildOutput
	pauseMode bool
	promotion bool
}

func (p *WorkloadDeploymentInstallation) Snapshot() *WorkloadArtifactSnapshot {
	return p.snapshot.Clone()
}
func (p *WorkloadDeploymentInstallation) Output() WorkloadBuildOutput { return p.output }
func (p *WorkloadDeploymentInstallation) Policy() IAMPolicyRecord     { return p.policy }

func prepareDeploymentInstallationTx(ctx context.Context, tx *sql.Tx, id string) (*WorkloadDeploymentInstallation, error) {
	var owner, generation, raw string
	err := tx.QueryRowContext(ctx, `SELECT c.function_id,c.published_generation,c.output
		FROM workload_deployment_capture c JOIN deployments d ON d.id=c.deployment_id
		WHERE c.deployment_id=? AND d.function_id=c.function_id AND d.status='succeeded'`, id).Scan(&owner, &generation, &raw)
	if err != nil || !validIAMUUID(generation) || len(raw) > 16384 {
		return nil, ErrWorkloadArtifactConflict
	}
	var output WorkloadBuildOutput
	if json.Unmarshal([]byte(raw), &output) != nil || !output.valid() {
		return nil, ErrWorkloadArtifactInvalid
	}
	pauseMode, err := workloadPauseModeTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if pauseMode {
		if err := workloadRequireUnpausedTx(ctx, tx, owner); err != nil {
			return nil, err
		}
	}
	snapshot, err := readDeploymentSnapshotTx(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	if snapshot.Function.ActiveDeploymentID != id {
		return nil, ErrWorkloadArtifactConflict
	}
	promotion := snapshot.Artifact.Generation != generation
	if promotion {
		association, err := readRollbackAssociationTx(ctx, tx, owner, id)
		if err != nil {
			return nil, err
		}
		f := snapshot.Function
		if association.runtime != f.Runtime || association.authored != f.Entrypoint ||
			output.CodeHash != f.CodeHash || output.Entrypoint != executedEntrypoint(f) {
			return nil, ErrWorkloadArtifactConflict
		}
		installed, err := readDeploymentPromotionTx(ctx, tx, owner, id, snapshot.Artifact.Generation, association.digest())
		if err != nil {
			return nil, err
		}
		if (installed && snapshot.Function.Status != "active") ||
			(!installed && snapshot.Function.Status != "inactive") {
			return nil, ErrWorkloadArtifactConflict
		}
	}
	sources, err := readSources(ctx, tx, owner)
	if err != nil {
		return nil, workloadPublicationReadError(ctx, err)
	}
	if (pauseMode || promotion) && snapshot.Function.Status != "active" {
		if snapshot.Function.Status != "inactive" || !sources.Record.ReviewRequired {
			return nil, ErrWorkloadArtifactConflict
		}
		staged, err := workloadPendingReviewTx(ctx, tx, owner, snapshot.Function, snapshot.Artifact.Generation)
		if err != nil {
			return nil, err
		}
		if !staged {
			return nil, ErrWorkloadArtifactConflict
		}
	}
	digest, err := deploymentSourceDigest(sources)
	if err != nil {
		return nil, err
	}
	return &WorkloadDeploymentInstallation{snapshot: snapshot, policy: *sources.Record, sources: digest, id: id, output: output, pauseMode: pauseMode, promotion: promotion}, nil
}

func (db *Database) PrepareWorkloadDeploymentInstallation(ctx context.Context, id string) (*WorkloadDeploymentInstallation, error) {
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment installation prepare")
	}
	defer tx.Rollback()
	return prepareDeploymentInstallationTx(ctx, tx, id)
}

// CheckWorkloadDeploymentInstallation is deny-only and requires live ADMIN.
func (db *Database) CheckWorkloadDeploymentInstallation(ctx context.Context, ref OperatorCredentialReference, p *WorkloadDeploymentInstallation) error {
	if p == nil {
		return ErrWorkloadArtifactInvalid
	}
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		return workloadArtifactStorageError(ctx, "deployment installation check")
	}
	defer tx.Rollback()
	if _, _, err := authorizeOperatorTx(ctx, tx, ref); err != nil {
		return err
	}
	current, err := prepareDeploymentInstallationTx(ctx, tx, p.id)
	if err != nil {
		return err
	}
	if current.pauseMode != p.pauseMode || current.promotion != p.promotion || current.sources != p.sources || current.output != p.output ||
		deploymentSnapshotDigest(current.snapshot) != deploymentSnapshotDigest(p.snapshot) {
		return ErrWorkloadArtifactConflict
	}
	return nil
}

// ActivateWorkloadDeploymentInstallation commits only the checked managed mapping.
// A crash or failed in-memory install still has no eligible Factory or grant.
func (db *Database) ActivateWorkloadDeploymentInstallation(ctx context.Context, ref OperatorCredentialReference, p *WorkloadDeploymentInstallation) (*Function, error) {
	if p == nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment installation begin")
	}
	defer tx.Rollback()
	_, expiry, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	current, err := prepareDeploymentInstallationTx(ctx, tx, p.id)
	if err != nil {
		return nil, err
	}
	if current.pauseMode != p.pauseMode || current.promotion != p.promotion || current.sources != p.sources || current.output != p.output ||
		deploymentSnapshotDigest(current.snapshot) != deploymentSnapshotDigest(p.snapshot) {
		return nil, ErrWorkloadArtifactConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE functions SET status='active' WHERE id=? AND status IN ('inactive','active')`, p.snapshot.Artifact.OwnerID); err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment installation activate")
	}
	if current.promotion {
		if _, err := tx.ExecContext(ctx, `UPDATE workload_deployment_promotion SET installed=1
			WHERE function_id=? AND deployment_id=? AND generation=?`,
			p.snapshot.Artifact.OwnerID, p.id, p.snapshot.Artifact.Generation); err != nil {
			return nil, workloadArtifactStorageError(ctx, "deployment promotion install")
		}
	} else if current.pauseMode {
		if _, err := tx.ExecContext(ctx, `UPDATE deployments SET phase='installed' WHERE id=? AND phase='review_required'`, p.id); err != nil {
			return nil, workloadArtifactStorageError(ctx, "deployment installation phase")
		}
	}
	fn, err := readWorkloadArtifactFunctionTx(ctx, tx, p.snapshot.Artifact.OwnerID)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "deployment installation commit")
	}
	return fn, nil
}
