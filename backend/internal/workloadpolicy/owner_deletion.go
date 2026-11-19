package workloadpolicy

import (
	"context"
	"sync"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

// RetireOwnerWithHistory requires the caller's owner mutation lock. Surviving
// owner sources are unchanged: only that owner and its sorted definitions lock.
func (s *Service) RetireOwnerWithHistory(ctx context.Context, ref database.OperatorCredentialReference, owner string, db *database.Database, reg *registry.Registry, a *workloadiam.Authority) (func(), error) {
	return s.RetireOwnerWithHistoryAndFactory(ctx, ref, owner, db, reg, a, nil)
}

// RetireOwnerWithHistoryAndFactory invalidates the current Factory at the same
// gate as history reservation. Only the returned cleanup may do process I/O.
func (s *Service) RetireOwnerWithHistoryAndFactory(ctx context.Context, ref database.OperatorCredentialReference, owner string, db *database.Database, reg *registry.Registry, a *workloadiam.Authority, invalidate func(string) func()) (func(), error) {
	if s == nil || !s.managed || s.db != db || s.registry != reg || s.authority != a {
		return nil, ErrUnavailable
	}
	plan, err := db.PrepareIAMOwnerDeletion(ctx, ref, owner)
	if err != nil {
		return nil, err
	}
	locks := []*sync.Mutex{}
	for _, id := range plan.PolicyIDs() {
		lock := reg.PolicyMutationLock(id)
		lock.Lock()
		locks = append(locks, lock)
	}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}()
	var cleanup func()
	cancel, err := a.RetireOwnerDeferred(owner, func() error {
		if err := db.ReserveHistoryDeletionWithManaged(ctx, ref, owner, plan); err != nil {
			return err
		}
		if invalidate != nil {
			cleanup = invalidate(owner)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return func() {
		cancel()
		if cleanup != nil {
			cleanup()
		}
	}, nil
}
