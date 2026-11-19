package workloadpolicy

import (
	"context"
	"errors"
	"sync"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

// NewManaged is internal opt-in. It requires explicitly initialized source
// storage and authority loaded from that same effective projection.
func NewManaged(db *database.Database, reg *registry.Registry, authority *workloadiam.Authority, observe ObserveArtifact) (*Service, error) {
	s, err := New(db, reg, authority, observe)
	if err != nil {
		return nil, err
	}
	s.managed = true
	return s, nil
}

// NewManagedServing requires the admission-gated, current Factory observer.
// Discovery labels serving composition, not the approval of any policy.
func NewManagedServing(db *database.Database, reg *registry.Registry, authority *workloadiam.Authority, observe ObserveArtifact) (*Service, error) {
	if observe == nil {
		return nil, ErrUnavailable
	}
	s, err := NewManaged(db, reg, authority, observe)
	if err != nil {
		return nil, err
	}
	s.serving = true
	return s, nil
}

type ManagedResult struct {
	Policy *database.IAMManagedPolicy
	Owners []database.IAMPolicyRecord
}

// MutateManaged is the only managed publication path. Preparation owns source
// proofs outside admission; sorted owner locks precede one gate and one DB commit.
func (s *Service) MutateManaged(ctx context.Context, ref database.OperatorCredentialReference, request database.IAMManagedMutation) (*ManagedResult, error) {
	if s == nil || !s.managed || ctx == nil {
		return nil, database.ErrIAMInvalidPolicy
	}
	plan, err := s.db.PrepareIAMManagedMutation(ctx, ref, request)
	if err != nil {
		return nil, err
	}
	changes := plan.Publications()
	batch := make([]workloadiam.Publication, 0, len(changes))
	for _, c := range changes {
		expected := c.Expected
		if (expected.CurrentArtifact != "" && !digest(expected.CurrentArtifact)) ||
			(expected.ApprovedArtifact != database.IAMUnboundArtifact && !digest(expected.ApprovedArtifact)) {
			return nil, database.ErrIAMInvalidPolicy
		}
		if !c.Reduction && !digest(expected.CurrentArtifact) {
			return nil, ErrUnavailable
		}
		e, err := c.After.AdmissionEvaluator()
		if err != nil {
			return nil, err
		}
		batch = append(batch, workloadiam.Publication{Expected: expected.Revision, Candidate: e})
	}
	locks := make([]*sync.Mutex, 0, len(changes))
	for _, c := range changes {
		lock := s.registry.MutationLock(c.Expected.OwnerID)
		lock.Lock()
		locks = append(locks, lock)
	}
	if request.PolicyID != "" {
		lock := s.registry.PolicyMutationLock(request.PolicyID)
		lock.Lock()
		locks = append(locks, lock)
	}
	unlock := func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
		locks = nil
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(database.ErrOperatorDenied, err)
	}
	cancel, err := s.authority.PublishBatchDeferred(batch, func() error {
		for _, c := range changes {
			var state ArtifactState
			if s.observe != nil {
				state = s.observe(c.Expected.OwnerID)
			}
			if state.Known {
				if state.OwnerID != c.Expected.OwnerID || !digest(state.Artifact) {
					return ErrUnavailable
				}
				if state.Artifact != c.Expected.CurrentArtifact {
					return database.ErrIAMConflict
				}
			} else {
				if state.Artifact != "" || (state.OwnerID != "" && state.OwnerID != c.Expected.OwnerID) {
					return ErrUnavailable
				}
				if !c.Reduction || c.Expected.CurrentArtifact != "" {
					return ErrUnavailable
				}
			}
		}
		return s.db.CommitIAMManagedMutation(ctx, ref, plan)
	})
	if errors.Is(err, workloadiam.ErrConflict) {
		return nil, database.ErrIAMConflict
	}
	if err != nil {
		return nil, err
	}
	unlock()
	cancel()
	out := &ManagedResult{Policy: plan.Policy(), Owners: make([]database.IAMPolicyRecord, 0, len(changes))}
	for _, c := range changes {
		out.Owners = append(out.Owners, c.After)
	}
	return out, nil
}

func (s *Service) replaceManaged(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected Expected, body []byte, review bool) (*database.IAMPolicyRecord, error) {
	op := "inline"
	if review {
		op = "review"
	}
	result, err := s.MutateManaged(ctx, ref, database.IAMManagedMutation{
		Operation: op, PolicyJSON: body,
		Owners: []database.IAMOwnerExpectation{{OwnerID: owner, Revision: expected.Revision, ApprovedArtifact: expected.ApprovedArtifact, CurrentArtifact: expected.CurrentArtifact}},
	})
	if err != nil {
		return nil, err
	}
	return &result.Owners[0], nil
}
