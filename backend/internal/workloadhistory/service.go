// Package workloadhistory is an internal opt-in deletion/history boundary.
package workloadhistory

import (
	"context"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
)

type Service struct {
	DB              *database.Database
	Authority       *workloadiam.Authority
	CurrentArtifact func(string) string
	// Managed and Registry must share this exact DB and Authority binding.
	Managed                    *workloadpolicy.Service
	Registry                   *registry.Registry
	InvalidateFactoryUnderGate func(string) func()
}

// Prepare is called only under Registry's function mutation lock. No mutation
// occurs on failed capacity/credential checks; no legacy fallback is permitted.
func (s *Service) Prepare(ctx context.Context, id string) (func(), error) {
	if s == nil || s.DB == nil || s.Authority == nil {
		return nil, workloadiam.ErrDenied
	}
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok {
		return nil, database.ErrOperatorDenied
	}
	if s.Managed != nil {
		return s.Managed.RetireOwnerWithHistoryAndFactory(ctx, ref, id, s.DB, s.Registry, s.Authority, s.InvalidateFactoryUnderGate)
	}
	if s.InvalidateFactoryUnderGate != nil {
		return nil, workloadiam.ErrDenied
	}
	return s.Authority.RetireOwnerDeferred(id, func() error { return s.DB.ReserveHistoryDeletion(ctx, ref, id) })
}
func (s *Service) Drain(ctx context.Context, id string) error {
	if s == nil || s.DB == nil {
		return workloadiam.ErrDenied
	}
	return s.DB.DrainHistoryDeletion(ctx, id)
}
func (s *Service) List(ctx context.Context, target string, after database.HistorySelector, limit int) ([]database.HistoryEntry, error) {
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok || s == nil || s.DB == nil {
		return nil, database.ErrOperatorDenied
	}
	return s.DB.ListWorkHistory(ctx, ref, target, after, limit)
}
func (s *Service) Get(ctx context.Context, selected database.HistorySelector) (*database.HistoryEntry, error) {
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok || s == nil || s.DB == nil {
		return nil, database.ErrOperatorDenied
	}
	return s.DB.GetWorkHistory(ctx, ref, selected)
}
func (s *Service) Purge(ctx context.Context, selected []database.HistorySelector, confirm bool) error {
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok || s == nil || s.DB == nil {
		return database.ErrOperatorDenied
	}
	return s.DB.PurgeWorkHistory(ctx, ref, selected, confirm)
}
func (s *Service) Reissue(ctx context.Context, r database.HistoryReissue) (string, error) {
	ref, ok := auth.OperatorFromContext(ctx)
	if !ok || s == nil || s.DB == nil || s.Authority == nil || s.CurrentArtifact == nil {
		return "", database.ErrOperatorDenied
	}
	if !r.Confirm || !r.Reviewed {
		return "", database.ErrHistoryInput
	}
	var next *time.Time
	trigger := workloadiam.Job
	if r.Source.Kind == "cron" {
		trigger = workloadiam.Cron
		n, err := workloadasync.NextCron(&database.AsyncCron{CronSchedule: database.CronSchedule{CronExpr: r.Expression, Timezone: r.Timezone}}, time.Now().UTC())
		if err != nil {
			return "", err
		}
		if r.Enabled {
			next = &n
		}
	}
	o := workloadiam.AsyncOrigin{Kind: "operator", OperatorKind: ref.Kind, OperatorID: ref.ID, TargetID: r.TargetID, Trigger: trigger}
	var id string
	err := s.Authority.DoStandingObserved(o, func() workloadiam.StandingState {
		return workloadiam.StandingState{TargetArtifact: s.CurrentArtifact(r.TargetID)}
	}, func(revs workloadiam.AsyncRevisions) error {
		var err error
		id, err = s.DB.ReissueWorkHistory(ctx, ref, r, revs, next)
		return err
	})
	return id, err
}
