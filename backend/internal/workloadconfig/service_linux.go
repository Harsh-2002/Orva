//go:build linux

// Package workloadconfig is an explicit, in-process configuration publication
// service. Normal boot, routes and legacy writers do not construct it.
package workloadconfig

import (
	"context"
	"errors"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
)

var ErrUnavailable = errors.New("configuration publication unavailable")

// Binding is trusted host provisioning of the unchanged concrete code/runtime.
// No request can supply stores, paths, generations or artifact approval.
type Binding struct {
	Code, Runtime                     *artifactfs.Store
	CodeGeneration, RuntimeGeneration string
	codeIdentity, runtimeIdentity     artifactfs.Identity
}

type Service struct {
	db        *database.Database
	reg       *registry.Registry
	authority *workloadiam.Authority
	pool      *pool.Manager
	secrets   *secrets.Manager
	bindings  map[string]Binding
}

func New(db *database.Database, reg *registry.Registry, authority *workloadiam.Authority, manager *pool.Manager, sm *secrets.Manager, bindings map[string]Binding) (*Service, error) {
	if db == nil || reg == nil || authority == nil || manager == nil || !manager.ManagedBy(authority) || sm == nil || len(bindings) == 0 || len(bindings) > 256 {
		return nil, ErrUnavailable
	}
	owned := make(map[string]Binding, len(bindings))
	for owner, b := range bindings {
		if b.Code == nil || b.Runtime == nil || b.CodeGeneration == "" || b.RuntimeGeneration == "" {
			return nil, ErrUnavailable
		}
		var current *workloadspawn.Factory
		if err := authority.ChangeHostState(func() { current = manager.CurrentFactory(owner) }); err != nil || current == nil {
			return nil, ErrUnavailable
		}
		b.codeIdentity, b.runtimeIdentity = current.Identities()
		if b.CodeGeneration != b.codeIdentity.Generation || b.RuntimeGeneration != b.runtimeIdentity.Generation {
			return nil, ErrUnavailable
		}
		owned[owner] = b
	}
	return &Service{db: db, reg: reg, authority: authority, pool: manager, secrets: sm, bindings: owned}, nil
}

func (s *Service) prepare(ctx context.Context, owner string, expected database.WorkloadConfigurationExpected, env map[string]string) (*database.WorkloadConfigurationPlan, *secrets.PreparedArtifactSnapshot, error) {
	if s == nil || ctx == nil {
		return nil, nil, ErrUnavailable
	}
	if _, ok := s.bindings[owner]; !ok {
		return nil, nil, ErrUnavailable
	}
	plan, err := s.db.PrepareWorkloadConfiguration(ctx, owner, expected, env)
	if err != nil {
		return nil, nil, err
	}
	input, err := s.snapshot(plan)
	if err != nil {
		return nil, nil, err
	}
	return plan, input, nil
}

func (s *Service) snapshot(plan *database.WorkloadConfigurationPlan) (*secrets.PreparedArtifactSnapshot, error) {
	raw := plan.Snapshot()
	owner := raw.Artifact.OwnerID
	plain, err := s.secrets.SnapshotForIAM(owner, raw.Secrets, secrets.SecretSnapshotLimits{MaxSecrets: secrets.MaxIAMSecretSnapshotCount, MaxTotalBytes: secrets.MaxIAMSecretSnapshotBytes})
	if err != nil {
		return nil, ErrUnavailable
	}
	return &secrets.PreparedArtifactSnapshot{Artifact: raw.Artifact, Function: raw.Function, Secrets: plain}, nil
}

func (s *Service) factory(ctx context.Context, owner string, input *secrets.PreparedArtifactSnapshot) (*workloadspawn.Factory, error) {
	b := s.bindings[owner]
	f, err := workloadspawn.NewFactory(ctx, input, b.Code, b.CodeGeneration, b.Runtime, b.RuntimeGeneration,
		workloadspawn.FactoryLimits{MaxPreparedPlans: 16, MaxPreparedBytes: 64 << 20, MaxSnapshotBytes: 64 << 20})
	if err != nil {
		return nil, ErrUnavailable
	}
	code, runtime := f.Identities()
	if code != b.codeIdentity || runtime != b.runtimeIdentity {
		f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}

// Environment commits deny-state first. Explicit Install followed by the existing
// source-aware policy Review is required before any new protected admission.
func (s *Service) Environment(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected database.WorkloadConfigurationExpected, currentArtifact string, env map[string]string) (*database.WorkloadEnvironmentPublication, error) {
	plan, input, err := s.prepare(ctx, owner, expected, env)
	if err != nil {
		return nil, err
	}
	return s.publish(ctx, ref, owner, expected, currentArtifact, plan, input, nil)
}

func (s *Service) Settings(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected database.WorkloadConfigurationExpected, currentArtifact string, settings database.WorkloadExecutionSettings, env map[string]string) (*database.WorkloadEnvironmentPublication, error) {
	if s == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	if _, ok := s.bindings[owner]; !ok {
		return nil, ErrUnavailable
	}
	plan, err := s.db.PrepareWorkloadSettings(ctx, owner, expected, settings, env)
	if err != nil {
		return nil, err
	}
	input, err := s.snapshot(plan)
	if err != nil {
		return nil, err
	}
	return s.publish(ctx, ref, owner, expected, currentArtifact, plan, input, nil)
}

func (s *Service) Secret(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected database.WorkloadConfigurationExpected, currentArtifact, key, value string, remove bool) (*database.WorkloadEnvironmentPublication, error) {
	if s == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	if _, ok := s.bindings[owner]; !ok {
		return nil, ErrUnavailable
	}
	if len(value) > MaxRequestBytes || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return nil, database.ErrWorkloadArtifactInvalid
	}
	encrypted := ""
	if !remove {
		var err error
		encrypted, err = s.secrets.EncryptValue(value)
		if err != nil {
			return nil, ErrUnavailable
		}
	}
	plan, err := s.db.PrepareWorkloadSecret(ctx, owner, expected, key, encrypted, remove)
	if err != nil {
		return nil, err
	}
	input, err := s.snapshot(plan)
	if err != nil {
		return nil, err
	}
	afterSecrets := maps.Clone(input.Secrets)
	if remove {
		delete(afterSecrets, key)
	} else {
		afterSecrets[key] = value
	}
	return s.publish(ctx, ref, owner, expected, currentArtifact, plan, input, afterSecrets)
}

func (s *Service) publish(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected database.WorkloadConfigurationExpected, currentArtifact string, plan *database.WorkloadConfigurationPlan, input *secrets.PreparedArtifactSnapshot, afterSecrets map[string]string) (*database.WorkloadEnvironmentPublication, error) {
	var current *workloadspawn.Factory
	err := s.authority.ChangeHostState(func() { current = s.pool.CurrentFactory(owner) })
	if err != nil || current == nil || current.ArtifactRevision() != currentArtifact || !current.MatchesSnapshot(input) {
		return nil, ErrUnavailable
	}
	// Validate the intended configuration and leases before taking either lock.
	input.Function = plan.CandidateFunction()
	if afterSecrets != nil {
		input.Secrets = afterSecrets
	}
	candidate, err := s.factory(ctx, owner, input)
	if err != nil {
		return nil, err
	}
	defer candidate.Close()
	after := plan.CandidatePolicy()
	evaluator, err := after.AdmissionEvaluator()
	if err != nil {
		return nil, err
	}
	lock := s.reg.MutationLock(owner)
	lock.Lock()
	var out *database.WorkloadEnvironmentPublication
	var retire func()
	cancel, err := s.authority.PublishBatchDeferred([]workloadiam.Publication{{Expected: expected.PolicyRevision, Candidate: evaluator}}, func() error {
		if s.pool.CurrentFactory(owner) != current {
			return ErrUnavailable
		}
		var err error
		out, err = s.db.CommitWorkloadConfiguration(ctx, ref, plan)
		if err != nil {
			return err
		}
		retire = s.pool.InvalidateFactoryUnderGate(current)
		s.reg.CacheCommitted(out.Function)
		return nil
	})
	lock.Unlock()
	if errors.Is(err, workloadiam.ErrConflict) {
		return nil, database.ErrIAMConflict
	}
	if err != nil {
		return nil, err
	}
	cancel()
	retire()
	if s.reg.PublishEvent != nil {
		s.reg.PublishEvent("function", map[string]string{"action": "updated", "id": owner})
	}
	return out, nil
}

// Install uses a fresh strict snapshot and new concrete leases, never the old
// observer's artifact. It installs no grant and does not clear quarantine.
func (s *Service) Install(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected database.WorkloadConfigurationExpected) (string, error) {
	plan, input, err := s.prepare(ctx, owner, expected, map[string]string{})
	if err != nil {
		return "", err
	}
	factory, err := s.factory(ctx, owner, input)
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			factory.Close()
		}
	}()
	lock := s.reg.MutationLock(owner)
	lock.Lock()
	var checked error
	err = s.authority.ChangeHostState(func() {
		checked = s.db.CheckWorkloadConfigurationInstallation(ctx, ref, plan)
	})
	if err != nil {
		lock.Unlock()
		return "", err
	}
	if checked != nil {
		lock.Unlock()
		return "", checked
	}
	// All coordinated writers and reviewers share the owner lock across this handoff.
	cleanup, err := s.pool.InstallFactoryDeferred(ctx, factory)
	lock.Unlock()
	if err != nil {
		return "", ErrUnavailable
	}
	keep = true
	cleanup()
	return factory.ArtifactRevision(), nil
}
