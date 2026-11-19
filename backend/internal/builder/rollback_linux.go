//go:build linux

package builder

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

// Rollback promotes an existing sealed deployment to inactive, review-required
// state. It neither installs a Factory nor grants approval.
func (s *PublicationQueue) Rollback(ctx context.Context, owner, target string) error {
	_, err := s.PromoteRollback(ctx, owner, target)
	return err
}

type RollbackPromotion struct {
	ID             string `json:"id"`
	DeploymentID   string `json:"deployment_id"`
	FunctionID     string `json:"function_id"`
	Version        int64  `json:"version"`
	CodeHash       string `json:"code_hash"`
	Status         string `json:"status"`
	Phase          string `json:"phase"`
	FunctionStatus string `json:"function_status"`
}

func (s *PublicationQueue) PromoteRollback(ctx context.Context, owner, target string) (out RollbackPromotion, err error) {
	if s == nil || ctx == nil || !s.manager.ManagedBy(s.authority) || s.code == nil || s.runtime == nil {
		return out, ErrPublicationUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return out, database.ErrOperatorDenied
	}
	plan, err := s.queue.db.PrepareWorkloadRollback(ctx, ref, owner, target)
	if err != nil {
		return out, err
	}

	build := s.manager.FunctionLock(owner)
	build.Lock()
	var paths *artifactfs.PreparedPaths
	var afterUnlock func()
	defer func() {
		build.Unlock()
		paths.Release()
		if afterUnlock != nil {
			afterUnlock()
		}
	}()
	if err := s.managedNamespace(owner); err != nil {
		return out, err
	}
	output := plan.Output()
	paths, err = artifactfs.PreparePaths(s.code, output.CodeGeneration, s.runtime, output.RuntimeGeneration)
	if err != nil {
		return out, ErrPublicationUnavailable
	}
	code, runtime := paths.Identities()
	if code.Owner != owner || runtime.Owner != plan.Runtime() ||
		code.Digest != output.CodeDigest || runtime.Digest != output.RuntimeDigest {
		return out, ErrPublicationUnavailable
	}
	codePath, _ := paths.Paths()
	if !verifiedRollbackEntrypoints(codePath, plan.AuthoredEntrypoint(), output.Entrypoint) {
		return out, ErrPublicationUnavailable
	}
	candidate, err := plan.DenyPolicy().AdmissionEvaluator()
	if err != nil {
		return out, err
	}
	lock := s.queue.reg.MutationLock(owner)
	lock.Lock()
	var retire func()
	cancel, err := s.authority.PublishBatchDeferred([]workloadiam.Publication{{
		Expected: plan.PolicyRevision(), Candidate: candidate,
	}}, func() error {
		out, err := s.queue.db.CommitWorkloadRollback(ctx, ref, plan, output)
		if err != nil {
			return err
		}
		retire = s.manager.InvalidateOwnerFactoryUnderGate(owner)
		s.queue.reg.CacheCommitted(out.Function)
		return nil
	})
	lock.Unlock()
	if errors.Is(err, workloadiam.ErrConflict) {
		return out, database.ErrIAMConflict
	}
	if err != nil {
		return out, err
	}
	afterUnlock = func() {
		cancel()
		retire()
		if s.queue.reg.PublishEvent != nil {
			s.queue.reg.PublishEvent("function", map[string]string{"action": "updated", "id": owner})
		}
	}
	return RollbackPromotion{ID: target, DeploymentID: target, FunctionID: owner,
		Version: plan.DeploymentVersion(), CodeHash: output.CodeHash, Status: "succeeded",
		Phase: "review_required", FunctionStatus: "inactive"}, nil
}

func verifiedRollbackEntrypoints(tree, authored, executed string) bool {
	root, err := os.OpenRoot(tree)
	if err != nil {
		return false
	}
	defer root.Close()
	for _, name := range []string{authored, executed} {
		if !filepath.IsLocal(name) || filepath.Clean(name) != name {
			return false
		}
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}
