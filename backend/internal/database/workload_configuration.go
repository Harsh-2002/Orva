package database

import (
	"context"
	"database/sql"
	"maps"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

type WorkloadConfigurationExpected struct {
	FunctionVersion  int    `json:"function_version,string"`
	ArtifactRevision uint64 `json:"artifact_revision,string"`
	ArtifactEpoch    uint64 `json:"artifact_epoch,string"`
	Generation       string `json:"generation"`
	PolicyRevision   uint64 `json:"policy_revision,string"`
	AuthorityEpoch   uint64 `json:"authority_epoch,string"`
	ApprovedArtifact string `json:"approved_artifact"`
}

type WorkloadConfigurationInspection struct {
	Expected           WorkloadConfigurationExpected `json:"expected"`
	ReviewRequired     bool                          `json:"review_required"`
	Quarantined        bool                          `json:"quarantined"`
	EnvironmentEntries int                           `json:"environment_entries"`
	SecretEntries      int                           `json:"secret_entries"`
}

// InspectWorkloadConfiguration returns metadata only, under current READ authority.
func (db *Database) InspectWorkloadConfiguration(ctx context.Context, ref OperatorCredentialReference, owner string) (*WorkloadConfigurationInspection, error) {
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration inspect")
	}
	defer tx.Rollback()
	_, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, "read")
	if err != nil {
		return nil, err
	}
	raw, err := readWorkloadArtifactSnapshotTx(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	sources, err := readSources(ctx, tx, owner)
	if err != nil {
		return nil, workloadPublicationReadError(ctx, err)
	}
	p, a := sources.Record, raw.Artifact
	out := &WorkloadConfigurationInspection{
		Expected:       WorkloadConfigurationExpected{raw.Function.Version, a.Revision, a.Epoch, a.Generation, p.Revision, p.AuthorityEpoch, p.ApprovedArtifact},
		ReviewRequired: p.ReviewRequired, Quarantined: p.Quarantined, EnvironmentEntries: len(raw.Function.EnvVars), SecretEntries: len(raw.Secrets),
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration inspect commit")
	}
	return out, nil
}

// WorkloadConfigurationPlan retains exact source/ciphertext proof, never plaintext.
type WorkloadConfigurationPlan struct {
	before                           *WorkloadArtifactSnapshot
	sources                          IAMSourceSnapshot
	change                           WorkloadEnvironmentChange
	envJSON                          string
	privileged                       bool
	secretKind, secretKey, encrypted string
	settings                         *WorkloadExecutionSettings
}

type WorkloadExecutionSettings struct {
	TimeoutMS         int64   `json:"timeout_ms"`
	MemoryMB          int64   `json:"memory_mb"`
	CPUs              float64 `json:"cpus"`
	NetworkMode       string  `json:"network_mode"`
	SDK               bool    `json:"sdk"`
	MaxConcurrency    int     `json:"max_concurrency"`
	ConcurrencyPolicy string  `json:"concurrency_policy"`
	AuthMode          string  `json:"auth_mode"`
	RateLimitPerMin   int     `json:"rate_limit_per_min"`
}

func (s WorkloadExecutionSettings) Apply(fn *Function) {
	fn.TimeoutMS, fn.MemoryMB, fn.CPUs = s.TimeoutMS, s.MemoryMB, s.CPUs
	fn.NetworkMode, fn.SDK = s.NetworkMode, s.SDK
	fn.MaxConcurrency, fn.ConcurrencyPolicy = s.MaxConcurrency, s.ConcurrencyPolicy
	fn.AuthMode, fn.RateLimitPerMin = s.AuthMode, s.RateLimitPerMin
}

func (p *WorkloadConfigurationPlan) Snapshot() *WorkloadArtifactSnapshot { return p.before.Clone() }
func (p *WorkloadConfigurationPlan) Policy() IAMPolicyRecord             { return *p.sources.Record }
func (p *WorkloadConfigurationPlan) CandidatePolicy() IAMPolicyRecord {
	r := *p.sources.Record
	r.Revision++
	r.AuthorityEpoch++
	r.ReviewRequired = true
	return r
}

func (p *WorkloadConfigurationPlan) CandidateFunction() *Function {
	fn := p.before.Function.Clone()
	fn.EnvVars, _ = decodeArtifactEnvironment([]byte(p.envJSON))
	if p.settings != nil {
		p.settings.Apply(fn)
	}
	return fn
}

func validConfigurationSecretKey(key string) bool {
	return key != "" && len(key) <= 4096 && utf8.ValidString(key) && !strings.ContainsAny(key, "=\x00")
}

func (db *Database) PrepareWorkloadSecret(ctx context.Context, owner string, expected WorkloadConfigurationExpected, key, encrypted string, remove bool) (*WorkloadConfigurationPlan, error) {
	if !validConfigurationSecretKey(key) || (!remove && (encrypted == "" || len(encrypted) > MaxWorkloadArtifactCiphertextBytes)) {
		return nil, ErrWorkloadArtifactInvalid
	}
	p, err := db.PrepareWorkloadConfiguration(ctx, owner, expected, map[string]string{})
	if err != nil {
		return nil, err
	}
	p.envJSON, err = prepareWorkloadEnvironment(p.before.Function.EnvVars)
	if err != nil {
		return nil, err
	}
	p.secretKind, p.secretKey, p.encrypted = "secret_upsert", key, encrypted
	if remove {
		p.secretKind, p.encrypted = "secret_delete", ""
	}
	return p, nil
}

func (db *Database) PrepareWorkloadSettings(ctx context.Context, owner string, expected WorkloadConfigurationExpected, settings WorkloadExecutionSettings, env map[string]string) (*WorkloadConfigurationPlan, error) {
	if settings.TimeoutMS < 1 || settings.TimeoutMS > 86400000 || settings.MemoryMB < 16 || settings.MemoryMB > 1<<20 ||
		math.IsNaN(settings.CPUs) || math.IsInf(settings.CPUs, 0) || settings.CPUs < 0 || settings.CPUs > 1024 ||
		!ValidNetworkMode(settings.NetworkMode) || !ValidAuthMode(settings.AuthMode) || !ValidConcurrencyPolicy(settings.ConcurrencyPolicy) ||
		settings.MaxConcurrency < 0 || settings.RateLimitPerMin < 0 {
		return nil, ErrWorkloadArtifactInvalid
	}
	p, err := db.PrepareWorkloadConfiguration(ctx, owner, expected, maps.Clone(env))
	if err != nil {
		return nil, err
	}
	p.settings = &settings
	return p, nil
}

func configurationPrivileged(s IAMSourceSnapshot) (bool, error) {
	privileged := s.Record.ReviewRequired || s.Record.Quarantined
	for _, body := range append([]string{s.Record.PolicyJSON, s.InlineJSON}, attachmentPolicies(s.Attachments)...) {
		p, err := workloadiam.Decode([]byte(body))
		if err != nil {
			return false, ErrIAMInvalidPolicy
		}
		privileged = privileged || len(p.Allow) != 0 || len(p.Inbound) != 0
	}
	return privileged, nil
}

func attachmentPolicies(attachments []IAMAttachment) []string {
	out := make([]string, 0, len(attachments))
	for _, a := range attachments {
		out = append(out, a.PolicyJSON)
	}
	return out
}

// PrepareWorkloadConfiguration requires explicitly initialized managed sources.
func (db *Database) PrepareWorkloadConfiguration(ctx context.Context, owner string, expected WorkloadConfigurationExpected, env map[string]string) (*WorkloadConfigurationPlan, error) {
	envJSON, err := prepareWorkloadEnvironment(env)
	if err != nil {
		return nil, err
	}
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration prepare")
	}
	defer tx.Rollback()
	before, err := readWorkloadArtifactSnapshotTx(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	s, err := readSources(ctx, tx, owner)
	if err != nil {
		return nil, workloadPublicationReadError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration prepare commit")
	}
	r, a := s.Record, before.Artifact
	if before.Function.Version <= 0 || before.Function.Version == math.MaxInt ||
		a.Revision == math.MaxInt64 || a.Epoch == math.MaxInt64 {
		return nil, ErrWorkloadArtifactExhausted
	}
	if before.Function.Version != expected.FunctionVersion || a.Revision != expected.ArtifactRevision ||
		a.Epoch != expected.ArtifactEpoch || a.Generation != expected.Generation {
		return nil, ErrWorkloadArtifactConflict
	}
	if r.Revision != expected.PolicyRevision || r.AuthorityEpoch != expected.AuthorityEpoch ||
		r.ApprovedArtifact != expected.ApprovedArtifact {
		return nil, ErrIAMConflict
	}
	if r.Revision == math.MaxInt64 || r.AuthorityEpoch == math.MaxInt64 {
		return nil, ErrIAMRevisionExhausted
	}
	projected, err := projectSources(s.InlineJSON, s.Attachments)
	if err != nil || projected != r.PolicyJSON {
		return nil, ErrIAMConflict
	}
	privileged, err := configurationPrivileged(s)
	if err != nil {
		return nil, err
	}
	c := WorkloadEnvironmentChange{
		WorkloadArtifactChange:  WorkloadArtifactChange{OwnerID: owner, ExpectedRevision: a.Revision, ExpectedEpoch: a.Epoch, ExpectedGeneration: a.Generation},
		ExpectedFunctionVersion: before.Function.Version, ExpectedPolicyRevision: r.Revision, ExpectedApprovedArtifact: r.ApprovedArtifact,
	}
	return &WorkloadConfigurationPlan{before: before, sources: s, change: c, envJSON: envJSON, privileged: privileged}, nil
}

func (db *Database) checkWorkloadConfigurationTx(ctx context.Context, tx *sql.Tx, ref OperatorCredentialReference, p *WorkloadConfigurationPlan, requireAdmin bool) (IAMActorReference, time.Time, error) {
	current, err := readSources(ctx, tx, p.before.Artifact.OwnerID)
	if err != nil {
		return IAMActorReference{}, time.Time{}, workloadPublicationReadError(ctx, err)
	}
	required := "write"
	// Changed sources cannot reuse a prepared ordinary writer's authority.
	if requireAdmin || p.privileged || !sameSourceState(current, p.sources) {
		required = "admin"
	}
	actor, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, required)
	if err != nil {
		return actor, expiry, err
	}
	if !sameSourceState(current, p.sources) {
		return actor, expiry, ErrIAMConflict
	}
	snapshot, err := readWorkloadArtifactSnapshotTx(ctx, tx, p.before.Artifact.OwnerID)
	if err != nil {
		return actor, expiry, err
	}
	if !reflect.DeepEqual(snapshot, p.before) {
		return actor, expiry, ErrWorkloadArtifactConflict
	}
	return actor, expiry, nil
}

// CommitWorkloadConfiguration is persistence-only; its caller holds owner then IAM.
func (db *Database) CommitWorkloadConfiguration(ctx context.Context, ref OperatorCredentialReference, p *WorkloadConfigurationPlan) (*WorkloadEnvironmentPublication, error) {
	if p == nil || p.before == nil {
		return nil, ErrWorkloadArtifactInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration begin")
	}
	defer tx.Rollback()
	actor, expiry, err := db.checkWorkloadConfigurationTx(ctx, tx, ref, p, false)
	if err != nil {
		return nil, err
	}
	c := p.change
	c.Actor = actor
	out, err := publishWorkloadConfigurationTx(ctx, tx, c, p.envJSON, expiry, p)
	if err != nil {
		return nil, err
	}
	if _, err := readWorkloadArtifactSnapshotTx(ctx, tx, c.OwnerID); err != nil {
		return nil, err
	}
	required := "write"
	if p.privileged {
		required = "admin"
	}
	if _, _, err := authorizeOperatorPermissionTx(ctx, tx, ref, required); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return nil, workloadArtifactStorageError(ctx, "configuration commit")
	}
	return out, nil
}

// CheckWorkloadConfigurationInstallation rechecks the complete prepared snapshot.
// Installation remains deny-only until the separate current-artifact policy review.
func (db *Database) CheckWorkloadConfigurationInstallation(ctx context.Context, ref OperatorCredentialReference, p *WorkloadConfigurationPlan) error {
	if p == nil || p.before == nil {
		return ErrWorkloadArtifactInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return workloadArtifactStorageError(ctx, "configuration installation")
	}
	defer tx.Rollback()
	_, expiry, err := db.checkWorkloadConfigurationTx(ctx, tx, ref, p, true)
	if err != nil {
		return err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return operatorDenied(ctx)
	}
	if err := tx.Commit(); err != nil {
		return workloadArtifactStorageError(ctx, "configuration installation commit")
	}
	return nil
}
