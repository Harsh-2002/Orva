package scheduler

import (
	"context"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
)

// WithCronIAM is explicit and independent from the frozen jobs-only option.
func WithCronIAM(service *workloadasync.Service) Option {
	return func(s *Scheduler) { s.cronEnabled = true; s.cronIAM = service }
}

// WithCronPollIntervalForFixture bounds live qualification latency, not cron grammar.
func WithCronPollIntervalForFixture(interval time.Duration) Option {
	return func(s *Scheduler) {
		if interval >= 10*time.Millisecond && interval <= 30*time.Second {
			s.cronInterval = interval
		}
	}
}
func (s *Scheduler) asyncCronTick(ctx context.Context) {
	if s.cronIAM == nil || s.cronIAM.DB != s.db || s.pool == nil {
		return
	}
	now := time.Now().UTC()
	rows, err := s.db.ListAsyncCrons(ctx, "", &now)
	if err != nil {
		return
	}
	for _, row := range rows {
		if _, busy := s.inflight.LoadOrStore(row.ID, struct{}{}); busy {
			continue
		}
		call, err := s.cronIAM.BeginCron(ctx, row)
		if err != nil {
			if workloadasync.IsDenied(err) {
				_ = s.db.SuspendAsyncCron(ctx, row, "policy_denied")
			}
			s.inflight.Delete(row.ID)
			continue
		}
		if err := s.cronIAM.ClaimCron(call.Context(), row, now); err != nil {
			if workloadasync.IsDenied(err) {
				_ = s.db.SuspendAsyncCron(ctx, row, "policy_denied")
			}
			call.Finish()
			s.inflight.Delete(row.ID)
			continue
		}
		s.inflightWG.Add(1)
		go func(c *database.AsyncCron) {
			defer s.inflightWG.Done()
			defer s.inflight.Delete(c.ID)
			defer call.Finish()
			s.fireCronManaged(call.Context(), &c.CronSchedule, c, call)
		}(row)
	}
}
