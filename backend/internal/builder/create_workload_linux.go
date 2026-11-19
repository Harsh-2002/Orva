//go:build linux

package builder

import (
	"context"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func (s *PublicationQueue) CreateWorkload(ctx context.Context, in database.WorkloadCreateRequest) (*database.Function, error) {
	if s == nil || ctx == nil {
		return nil, ErrPublicationUnavailable
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return nil, database.ErrOperatorDenied
	}
	plan, err := database.PrepareWorkloadCreation(in)
	if err != nil {
		return nil, err
	}
	policy, err := plan.AdmissionEvaluator()
	if err != nil {
		return nil, err
	}
	lock := s.queue.reg.MutationLock(plan.OwnerID())
	lock.Lock()
	var fn *database.Function
	cancel, err := s.authority.PublishBatchDeferred([]workloadiam.Publication{{Candidate: policy}}, func() error {
		var err error
		fn, err = s.queue.db.CreateWorkloadAsOperator(ctx, ref, plan)
		if err == nil {
			s.queue.reg.CacheCommitted(fn)
		}
		return err
	})
	lock.Unlock()
	if err != nil {
		return nil, err
	}
	cancel()
	return fn, nil
}
