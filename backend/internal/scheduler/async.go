package scheduler

import (
	"context"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
)

type Option func(*Scheduler)

// WithAsyncIAM is fixture-only. Explicit nil denies work; omission is legacy.
func WithAsyncIAM(service *workloadasync.Service) Option {
	return func(s *Scheduler) { s.asyncEnabled = true; s.asyncIAM = service }
}

func (s *Scheduler) asyncJobsTick(ctx context.Context) {
	if s.asyncIAM == nil || s.asyncIAM.DB != s.db || s.pool == nil {
		return
	}
	free := cap(s.jobsSem) - len(s.jobsSem)
	if free <= 0 {
		return
	}
	now := time.Now().UTC()
	jobs, err := s.db.ListAsyncJobs(ctx, "pending", "", free, &now)
	if err != nil {
		return
	}
	for _, j := range jobs {
		call, err := s.asyncIAM.BeginJob(ctx, j)
		if err != nil {
			if workloadasync.IsDenied(err) {
				_ = s.db.SuspendAsyncJob(ctx, j, "policy_denied")
			}
			continue
		}
		reserve, cancel := context.WithTimeout(call.Context(), 50*time.Millisecond)
		lease, err := s.db.ReserveExecution(reserve)
		cancel()
		if err != nil {
			call.Finish()
			continue
		}
		if err := s.asyncIAM.ClaimJob(call.Context(), j, now); err != nil {
			if workloadasync.IsDenied(err) || (call.Context().Err() != nil && ctx.Err() == nil) {
				_ = s.db.SuspendAsyncJob(ctx, j, "policy_denied")
			}
			lease.Cancel()
			call.Finish()
			continue
		}
		s.jobsSem <- struct{}{}
		s.inflightWG.Add(1)
		go func(j *database.AsyncJob) {
			defer s.inflightWG.Done()
			defer func() { <-s.jobsSem }()
			defer call.Finish()
			s.runJobMode(call.Context(), &j.Job, lease, j, call)
		}(j)
	}
}
