// Package workloadasync composes opt-in durable work with current IAM authority.
package workloadasync

import (
	"context"
	"errors"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

type Service struct {
	DB        *database.Database
	Authority *workloadiam.Authority
	// CurrentArtifact is a gate-held atomic managed-pool observer, never DB/I/O.
	CurrentArtifact func(string) string
}

func (s *Service) standing(o workloadiam.AsyncOrigin) workloadiam.StandingState {
	if s == nil || s.CurrentArtifact == nil {
		return workloadiam.StandingState{}
	}
	return workloadiam.StandingState{CreatorArtifact: s.CurrentArtifact(o.CreatorID), TargetArtifact: s.CurrentArtifact(o.TargetID)}
}

func (s *Service) EnqueueFunction(ctx context.Context, observe func() workloadiam.Context, j *database.AsyncJob) error {
	if s == nil || s.DB == nil || s.Authority == nil || observe == nil || j == nil {
		return workloadiam.ErrDenied
	}
	r := workloadiam.Request{Operation: workloadiam.OpJobsEnqueue, FunctionID: j.CreatorID,
		Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: j.FunctionID}}}
	return s.Authority.DoAsyncCreateObserved(func() workloadiam.Context {
		c := observe()
		if s.CurrentArtifact == nil || c.Principal.FunctionID != j.CreatorID ||
			s.CurrentArtifact(j.CreatorID) != c.Principal.ArtifactRevision || s.CurrentArtifact(j.FunctionID) == "" || ctx.Err() != nil {
			return workloadiam.Context{}
		}
		return c
	}, func() string { return s.CurrentArtifact(j.FunctionID) }, r, func(revs workloadiam.AsyncRevisions) error {
		j.OriginKind = "function"
		j.OperatorID = ""
		j.OperatorKind = ""
		j.CreationCallerRevision = revs.Creator
		j.CreationTargetRevision = revs.Target
		return s.DB.EnqueueAsyncJob(ctx, j, nil)
	})
}

func (s *Service) EnqueueOperator(ctx context.Context, ref database.OperatorCredentialReference, j *database.AsyncJob) error {
	if s == nil || s.Authority == nil || s.DB == nil || j == nil {
		return workloadiam.ErrDenied
	}
	o := workloadiam.AsyncOrigin{Kind: "operator", OperatorKind: ref.Kind, OperatorID: ref.ID, TargetID: j.FunctionID, Trigger: workloadiam.Job}
	return s.Authority.DoStandingObserved(o, func() workloadiam.StandingState { return s.standing(o) }, func(revs workloadiam.AsyncRevisions) error {
		j.CreationTargetRevision = revs.Target
		return s.DB.EnqueueAsyncJob(ctx, j, &ref)
	})
}

func (s *Service) BeginJob(ctx context.Context, j *database.AsyncJob) (*workloadiam.AsyncAdmission, error) {
	if s == nil || s.Authority == nil || s.DB == nil || j == nil || j.AuthorityState != "active" || j.Status != "pending" || j.AdmissionTargetRevision == 0 || (j.OriginKind == "function" && j.AdmissionCallerRevision == 0) {
		return nil, workloadiam.ErrDenied
	}
	o := j.Origin()
	return s.Authority.BeginAsyncObserved(ctx, o, func() workloadiam.StandingState { return s.standing(o) })
}

func (s *Service) ClaimJob(ctx context.Context, j *database.AsyncJob, now time.Time) error {
	if s == nil || s.Authority == nil || s.DB == nil || j == nil {
		return workloadiam.ErrDenied
	}
	o := j.Origin()
	return s.Authority.DoStandingObserved(o, func() workloadiam.StandingState { return s.standing(o) }, func(workloadiam.AsyncRevisions) error {
		return s.DB.ClaimAsyncJob(ctx, j, now)
	})
}

func (s *Service) AdmitJob(ctx context.Context, j *database.AsyncJob, call *workloadiam.AsyncAdmission, observe func() workloadiam.TargetState) error {
	if s == nil || observe == nil || j == nil {
		return workloadiam.ErrDenied
	}
	return call.AdmitObserved(func() (workloadiam.StandingState, workloadiam.TargetState) {
		return s.standing(j.Origin()), observe()
	}, func() error { return s.DB.StartAsyncJob(ctx, j) })
}

// MutateJob never substitutes the administrator for a function's original origin.
func (s *Service) MutateJob(ctx context.Context, ref database.OperatorCredentialReference, id, action string) error {
	if s == nil || s.Authority == nil || s.DB == nil {
		return workloadiam.ErrDenied
	}
	j, err := s.DB.GetAsyncJob(ctx, id)
	if err != nil {
		return err
	}
	if action == "delete" {
		return s.DB.MutateAsyncJobAsOperator(ctx, ref, j, action, workloadiam.AsyncRevisions{})
	}
	o := j.Origin()
	o.ExpectedCreatorRevision = 0
	o.ExpectedTargetRevision = 0
	return s.Authority.DoStandingObserved(o, func() workloadiam.StandingState { return s.standing(o) }, func(revs workloadiam.AsyncRevisions) error {
		return s.DB.MutateAsyncJobAsOperator(ctx, ref, j, action, revs)
	})
}

func IsDenied(err error) bool { return errors.Is(err, workloadiam.ErrDenied) }
