// Package workloadpolicy joins policy persistence and in-process admission.
// No serving caller or public management adapter is registered yet.
package workloadpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

var ErrUnavailable = errors.New("current workload artifact unavailable")

type Expected struct {
	Revision         uint64
	CurrentArtifact  string
	ApprovedArtifact string
}

// ArtifactState must describe pinned current code/config/secrets/runtime, not stored approval.
type ArtifactState struct {
	OwnerID  string
	Artifact string
	Known    bool
}

// ObserveArtifact runs under admission: bounded atomic reads, no I/O or earlier locks.
// Updates to these facts must use this service's Authority.ChangeHostState boundary.
type ObserveArtifact func(string) ArtifactState

type Service struct {
	db        *database.Database
	registry  *registry.Registry
	authority *workloadiam.Authority
	observe   ObserveArtifact
	managed   bool
}

func New(db *database.Database, reg *registry.Registry, authority *workloadiam.Authority, observe ObserveArtifact) (*Service, error) {
	if db == nil || reg == nil || authority == nil {
		return nil, workloadiam.ErrInvalid
	}
	return &Service{db: db, registry: reg, authority: authority, observe: observe}, nil
}

// Replace preserves quarantine/review flags; changing stored policy is not a review.
// ref is captured by trusted authentication, never read from authorable requests.
func (s *Service) Replace(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected Expected, body []byte) (*database.IAMPolicyRecord, error) {
	if s != nil && s.managed {
		return s.replaceManaged(ctx, ref, owner, expected, body, false)
	}
	p, err := workloadiam.Decode(body)
	if err != nil {
		return nil, database.ErrIAMInvalidPolicy
	}
	return s.publish(ctx, ref, owner, expected, &p, false)
}

// Review explicitly approves the unchanged stored policy against the reviewed current artifact.
func (s *Service) Review(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected Expected) (*database.IAMPolicyRecord, error) {
	if s != nil && s.managed {
		return s.replaceManaged(ctx, ref, owner, expected, nil, true)
	}
	return s.publish(ctx, ref, owner, expected, nil, true)
}

func digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Service) publish(ctx context.Context, ref database.OperatorCredentialReference, owner string, expected Expected, policy *workloadiam.Policy, review bool) (*database.IAMPolicyRecord, error) {
	if s == nil || s.db == nil || s.registry == nil || s.authority == nil || ctx == nil ||
		expected.Revision == 0 || expected.Revision > math.MaxInt64 ||
		(expected.CurrentArtifact != "" && !digest(expected.CurrentArtifact)) {
		return nil, database.ErrIAMInvalidPolicy
	}
	if expected.Revision == math.MaxInt64 {
		return nil, database.ErrIAMRevisionExhausted
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(database.ErrOperatorDenied, err)
	}
	old, err := s.db.GetIAMPolicy(ctx, owner)
	if err != nil {
		return nil, err
	}
	lock := s.registry.MutationLock(owner)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(database.ErrOperatorDenied, err)
	}
	if old.Revision != expected.Revision || old.ApprovedArtifact != expected.ApprovedArtifact {
		return nil, database.ErrIAMConflict
	}
	if old.AuthorityEpoch == math.MaxInt64 {
		return nil, database.ErrIAMRevisionExhausted
	}
	if review {
		p, err := workloadiam.Decode([]byte(old.PolicyJSON))
		if err != nil {
			return nil, database.ErrIAMInvalidPolicy
		}
		policy = &p
	}
	if policy == nil {
		return nil, database.ErrIAMInvalidPolicy
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return nil, database.ErrIAMInvalidPolicy
	}
	revoke := !review && len(policy.Allow) == 0 && len(policy.Inbound) == 0
	approved := expected.CurrentArtifact
	if revoke {
		approved = old.ApprovedArtifact
	} else if !digest(approved) {
		return nil, ErrUnavailable
	}
	candidate := *old
	candidate.Revision++
	candidate.AuthorityEpoch++
	candidate.ApprovedArtifact = approved
	candidate.PolicyJSON = string(canonical)
	if review {
		candidate.ReviewRequired, candidate.Quarantined = false, false
	}
	evaluator, err := candidate.AdmissionEvaluator()
	if err != nil {
		return nil, err
	}
	var saved *database.IAMPolicyRecord
	err = s.authority.Publish(expected.Revision, evaluator, func() error {
		state := ArtifactState{}
		if s.observe != nil {
			state = s.observe(owner)
		}
		if state.Known {
			if state.OwnerID != owner || !digest(state.Artifact) {
				return ErrUnavailable
			}
			if state.Artifact != expected.CurrentArtifact {
				return database.ErrIAMConflict
			}
		} else {
			if state.Artifact != "" || (state.OwnerID != "" && state.OwnerID != owner) {
				return ErrUnavailable
			}
			if !revoke || expected.CurrentArtifact != "" {
				return ErrUnavailable
			}
		}
		var err error
		saved, err = s.db.SaveIAMPolicyAsOperator(ctx, ref, database.IAMPolicyChange{
			OwnerID: owner, ExpectedRevision: expected.Revision,
			ExpectedApprovedArtifact: expected.ApprovedArtifact, ApprovedArtifact: approved,
			PolicyJSON: canonical, ReviewRequired: candidate.ReviewRequired, Quarantined: candidate.Quarantined,
		})
		return err
	})
	if errors.Is(err, workloadiam.ErrConflict) {
		return nil, database.ErrIAMConflict
	}
	if err != nil {
		return nil, err
	}
	return saved, nil
}
