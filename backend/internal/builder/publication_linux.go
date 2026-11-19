//go:build linux

package builder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
)

var ErrPublicationUnavailable = errors.New("managed deployment publication unavailable")

// PublicationQueue is host-only opt-in; normal NewQueue and boot remain unchanged.
type PublicationQueue struct {
	queue         *Queue
	authority     *workloadiam.Authority
	manager       *pool.Manager
	secrets       *secrets.Manager
	code, runtime *artifactfs.Store
	submitMu      sync.Mutex
	pendingMu     sync.Mutex
	pending       map[string]*artifactfs.PreparedPaths
}

func NewPublicationQueue(b *Builder, db *database.Database, reg *registry.Registry, a *workloadiam.Authority,
	manager *pool.Manager, sm *secrets.Manager, code, runtime *artifactfs.Store) (*PublicationQueue, error) {
	if b == nil || db == nil || reg == nil || a == nil || !manager.ManagedBy(a) || sm == nil ||
		code == nil || runtime == nil || !b.managedOutputEnabled || !b.runtimeBindingEnabled ||
		b.outputOwner == nil || b.outputOwner.s == nil || b.outputOwner.s.cfg.CodeStore != code ||
		b.outputOwner.s.cfg.ParentDomain == nil || b.BuildFunc != nil || b.checkSyntaxHook != nil {
		return nil, ErrPublicationUnavailable
	}
	owned := *b
	q := NewQueue(&owned, db, reg)
	s := &PublicationQueue{queue: q, authority: a, manager: manager, secrets: sm,
		code: code, runtime: runtime, pending: make(map[string]*artifactfs.PreparedPaths)}
	q.publication, q.FnLock = s, manager.FunctionLock
	return s, nil
}

func (s *PublicationQueue) Start()     { s.queue.Start() }
func (s *PublicationQueue) Depth() int { return s.queue.QueuedDepth() }

// Submit consumes the staged archive on all paths. Once captured, work belongs
// to the background queue, not the request cancellation lifetime.
func (s *PublicationQueue) Submit(ctx context.Context, owner, archive string) (string, error) {
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(archive)
		}
	}()
	if s == nil || ctx == nil {
		return "", ErrPublicationUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return "", database.ErrOperatorDenied
	}
	if err := s.managedNamespace(owner); err != nil {
		return "", err
	}
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	if s.queue.stopping.Load() {
		return "", ErrQueueStopping
	}
	s.pendingMu.Lock()
	full := len(s.pending) >= 64
	s.pendingMu.Unlock()
	if full {
		return "", ErrOutputCapacity
	}
	lock := s.queue.reg.MutationLock(owner)
	lock.Lock()
	var capture *database.WorkloadDeploymentPlan
	var checked error
	err := s.authority.ChangeHostState(func() {
		if s.authority.PublicationRevisionUnderGate(owner) == 0 {
			checked = ErrPublicationUnavailable
			return
		}
		capture, checked = s.queue.db.CaptureWorkloadDeployment(ctx, ref, owner)
		if checked == nil && s.authority.PublicationRevisionUnderGate(owner) != capture.PolicyRevision() {
			checked = database.ErrIAMConflict
		}
	})
	lock.Unlock()
	if err != nil {
		return "", err
	}
	if checked != nil {
		if capture != nil {
			_ = s.queue.db.FinishDeployment(capture.ID, "failed", "authority snapshot changed; fresh submission required", 0)
			return capture.ID, checked
		}
		return "", checked
	}
	job := BuildJob{DeploymentID: capture.ID, FunctionID: owner, TarballPath: archive, SubmittedAt: time.Now()}
	if err := s.queue.enqueue(job); err != nil {
		_ = s.queue.db.FinishDeployment(job.DeploymentID, "failed", err.Error(), 0)
		return capture.ID, err
	}
	keep = true
	return capture.ID, nil
}

func (s *PublicationQueue) Shutdown(ctx context.Context) {
	s.submitMu.Lock()
	s.queue.stopping.Store(true)
	s.submitMu.Unlock()
	s.queue.Shutdown(ctx)
	// Jobs that never started remain explicit failed work, not abandoned tempfiles.
	for {
		select {
		case job := <-s.queue.jobs:
			_ = os.Remove(job.TarballPath)
			_ = s.queue.db.FinishDeployment(job.DeploymentID, "failed", "build queue stopped before start", 0)
		default:
			return
		}
	}
}

func (s *PublicationQueue) run(job BuildJob) {
	started := time.Now()
	err := s.buildAndPublish(job)
	_ = os.Remove(job.TarballPath)
	if err != nil {
		s.queue.fail(job, "publication", err.Error(), started)
		return
	}
	s.queue.publishStatusChange(job.DeploymentID, job.FunctionID, "succeeded", "review_required")
}

func (s *PublicationQueue) buildAndPublish(job BuildJob) error {
	lk := s.manager.FunctionLock(job.FunctionID)
	lk.Lock()
	var afterUnlock func()
	defer func() {
		lk.Unlock()
		if afterUnlock != nil {
			afterUnlock()
		}
	}()
	ctx := context.Background()
	if err := s.managedNamespace(job.FunctionID); err != nil {
		return err
	}
	plan, err := s.queue.db.CheckWorkloadDeployment(ctx, job.DeploymentID)
	if err != nil {
		return err
	}
	fn := plan.Function()
	if fn.ID != job.FunctionID {
		return database.ErrWorkloadArtifactConflict
	}
	if err := s.queue.db.UpdateDeploymentPhase(job.DeploymentID, "deps"); err != nil {
		return err
	}
	b := *s.queue.bld
	b.Logger = newLogWriter(s.queue.db, job.DeploymentID)
	result, err := b.Build(ctx, fn, job.TarballPath)
	if err != nil {
		return err
	}
	paths, err := artifactfs.PreparePaths(s.code, result.CodeArtifact.Generation, s.runtime, result.RuntimeArtifact.Generation)
	if err != nil {
		return ErrPublicationUnavailable
	}
	code, runtime := paths.Identities()
	if code != result.CodeArtifact || runtime != result.RuntimeArtifact {
		paths.Release()
		return ErrPublicationUnavailable
	}
	// Keep successful-but-unpublished output pinned and charged until inspection.
	s.pendingMu.Lock()
	s.pending[job.DeploymentID] = paths
	s.pendingMu.Unlock()
	output := database.WorkloadBuildOutput{CodeGeneration: code.Generation, CodeDigest: code.Digest,
		RuntimeGeneration: runtime.Generation, RuntimeDigest: runtime.Digest,
		CodeHash: result.CodeHash, Entrypoint: result.Entrypoint}
	if err := s.queue.db.RecordWorkloadBuildOutput(ctx, job.DeploymentID, output); err != nil {
		return err
	}
	if err := s.managedNamespace(fn.ID); err != nil {
		return err
	}
	// Compile the immutable deny snapshot outside owner/IAM locks.
	candidate, err := plan.DenyPolicy().AdmissionEvaluator()
	if err != nil {
		return err
	}
	owner := s.queue.reg.MutationLock(fn.ID)
	owner.Lock()
	var retirement func()
	cancel, err := s.authority.PublishBatchDeferred([]workloadiam.Publication{{
		Expected: plan.PolicyRevision(), Candidate: candidate}}, func() error {
		out, err := s.queue.db.PublishWorkloadDeployment(ctx, job.DeploymentID, output)
		if err != nil {
			return err
		}
		retirement = s.manager.InvalidateOwnerFactoryUnderGate(fn.ID)
		s.queue.reg.CacheCommitted(out.Function)
		return nil
	})
	owner.Unlock()
	if err != nil {
		return err
	}
	afterUnlock = func() {
		cancel()
		retirement()
		s.pendingMu.Lock()
		delete(s.pending, job.DeploymentID)
		s.pendingMu.Unlock()
		paths.Release()
	}
	return nil
}

// Pause records operator intent without installing, approving or resuming code.
func (s *PublicationQueue) Pause(ctx context.Context, req database.WorkloadPauseRequest) error {
	if s == nil || ctx == nil || !s.manager.ManagedBy(s.authority) {
		return ErrPublicationUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return database.ErrOperatorDenied
	}
	if err := s.managedNamespace(req.OwnerID); err != nil {
		return err
	}
	lock := s.queue.reg.MutationLock(req.OwnerID)
	lock.Lock()
	var retire func()
	cancel, err := s.authority.DeactivateOwnerDeferred(req.OwnerID, req.ExpectedPolicyRevision, func() error {
		_, fn, err := s.queue.db.PauseWorkloadFunctionAsOperator(ctx, ref, req)
		if err != nil {
			return err
		}
		retire = s.manager.InvalidateOwnerFactoryUnderGate(req.OwnerID)
		s.queue.reg.CacheCommitted(fn)
		return nil
	})
	lock.Unlock()
	if errors.Is(err, workloadiam.ErrConflict) {
		return database.ErrIAMConflict
	}
	if err != nil {
		return err
	}
	cancel()
	retire()
	if s.queue.reg.PublishEvent != nil {
		s.queue.reg.PublishEvent("function", map[string]string{"action": "updated", "id": req.OwnerID})
	}
	return nil
}

// Install is a separate fresh-lease operation. It grants no permissions; source-aware
// Review must bind the resulting actual current Factory afterward.
func (s *PublicationQueue) Install(ctx context.Context, deployment string) (string, error) {
	if s == nil || ctx == nil {
		return "", ErrPublicationUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return "", database.ErrOperatorDenied
	}
	plan, err := s.queue.db.PrepareWorkloadDeploymentInstallation(ctx, deployment)
	if err != nil {
		return "", err
	}
	raw, output := plan.Snapshot(), plan.Output()
	buildLock := s.manager.FunctionLock(raw.Artifact.OwnerID)
	buildLock.Lock()
	var afterUnlock func()
	defer func() {
		buildLock.Unlock()
		if afterUnlock != nil {
			afterUnlock()
		}
	}()
	if err := s.managedNamespace(raw.Artifact.OwnerID); err != nil {
		return "", err
	}
	plain, err := s.secrets.SnapshotForIAM(raw.Artifact.OwnerID, raw.Secrets,
		secrets.SecretSnapshotLimits{MaxSecrets: secrets.MaxIAMSecretSnapshotCount, MaxTotalBytes: secrets.MaxIAMSecretSnapshotBytes})
	if err != nil {
		return "", ErrPublicationUnavailable
	}
	input := &secrets.PreparedArtifactSnapshot{Artifact: raw.Artifact, Function: raw.Function, Secrets: plain}
	input.Function.Status = "active"
	factory, err := workloadspawn.NewFactory(ctx, input, s.code, output.CodeGeneration, s.runtime, output.RuntimeGeneration,
		workloadspawn.FactoryLimits{MaxPreparedPlans: 16, MaxPreparedBytes: 64 << 20, MaxSnapshotBytes: 64 << 20})
	if err != nil {
		return "", ErrPublicationUnavailable
	}
	keep := false
	defer func() {
		if !keep {
			factory.Close()
		}
	}()
	code, runtime := factory.Identities()
	if code.Digest != output.CodeDigest || runtime.Digest != output.RuntimeDigest {
		return "", ErrPublicationUnavailable
	}
	lock := s.queue.reg.MutationLock(raw.Artifact.OwnerID)
	lock.Lock()
	cleanup, err := s.manager.InstallFactoryChecked(ctx, factory, func() error {
		committed, err := s.queue.db.ActivateWorkloadDeploymentInstallation(ctx, ref, plan)
		if err != nil {
			return err
		}
		s.queue.reg.CacheCommitted(committed)
		return nil
	})
	lock.Unlock()
	if err != nil {
		return "", err
	}
	keep = true
	afterUnlock = cleanup
	return factory.ArtifactRevision(), nil
}

// Managed outputs never enter the legacy mutable directory/symlink namespace.
func (s *PublicationQueue) managedNamespace(owner string) error {
	id, err := uuid.Parse(owner)
	if s.queue.bld.DataDir == "" || err != nil || id.String() != owner {
		return ErrPublicationUnavailable
	}
	// Join the re-encoded canonical form, never the caller's string.
	_, err = os.Lstat(filepath.Join(s.queue.bld.DataDir, "functions", id.String()))
	if !os.IsNotExist(err) {
		return ErrPublicationUnavailable
	}
	return nil
}
