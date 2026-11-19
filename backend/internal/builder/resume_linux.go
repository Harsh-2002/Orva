//go:build linux

package builder

import (
	"context"
	"errors"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

var ErrResumePairUnavailable = errors.New("paused workload's sealed code/runtime pair is unavailable")

// Resume publishes an inactive successor. Install and source-aware review are
// separate operations; this method never replaces pause with serving authority.
func (s *PublicationQueue) Resume(ctx context.Context, req database.WorkloadPauseRequest) error {
	if s == nil || ctx == nil || !s.manager.ManagedBy(s.authority) || s.code == nil || s.runtime == nil {
		return ErrPublicationUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return database.ErrOperatorDenied
	}
	plan, err := s.queue.db.PrepareWorkloadResume(ctx, ref, req)
	if err != nil {
		return err
	}
	candidate, err := plan.DenyPolicy().AdmissionEvaluator()
	if err != nil {
		return err
	}
	owner := req.OwnerID
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
		return err
	}
	output := plan.Output()
	paths, err = artifactfs.PreparePaths(s.code, output.CodeGeneration, s.runtime, output.RuntimeGeneration)
	if err != nil {
		return ErrResumePairUnavailable
	}
	code, runtime := paths.Identities()
	codePath, _ := paths.Paths()
	if code.Owner != owner || runtime.Owner != plan.Runtime() ||
		code.Digest != output.CodeDigest || runtime.Digest != output.RuntimeDigest ||
		!verifiedRollbackEntrypoints(codePath, plan.AuthoredEntrypoint(), output.Entrypoint) {
		return ErrResumePairUnavailable
	}
	lock := s.queue.reg.MutationLock(owner)
	lock.Lock()
	var retire func()
	cancel, err := s.authority.ResumeOwnerDeferred(plan.ExpectedAuthority(), candidate, func() error {
		out, err := s.queue.db.CommitWorkloadResume(ctx, ref, plan, output)
		if err != nil {
			return err
		}
		retire = s.manager.InvalidateOwnerFactoryUnderGate(owner)
		s.queue.reg.CacheCommitted(out.Function)
		return nil
	})
	lock.Unlock()
	if errors.Is(err, workloadiam.ErrConflict) {
		return database.ErrIAMConflict
	}
	if err != nil {
		return err
	}
	afterUnlock = func() {
		cancel()
		retire()
		if s.queue.reg.PublishEvent != nil {
			s.queue.reg.PublishEvent("function", map[string]string{"action": "updated", "id": owner})
		}
	}
	return nil
}
