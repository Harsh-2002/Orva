package scheduler

import (
	"context"
	"errors"
	"time"
)

var ErrManagedUnavailable = errors.New("managed scheduler requires a shared job, cron, and pool authority")

// RunManaged blocks until both admission loops have exited and their in-flight
// work has drained. Legacy claims, requeues and webhook delivery are excluded.
// Cancel ctx to stop it; the caller must wait for return before closing the DB.
func (s *Scheduler) RunManaged(ctx context.Context) error {
	return s.RunManagedReady(ctx, nil)
}

// RunManagedReady closes ready after successful validation and ownership of
// the loops, before either loop can dispatch.
func (s *Scheduler) RunManagedReady(ctx context.Context, ready chan<- struct{}) error {
	if s == nil || ctx == nil || !s.asyncEnabled || !s.cronEnabled || s.db == nil || s.pool == nil ||
		s.asyncIAM == nil || s.asyncIAM != s.cronIAM || s.asyncIAM.DB != s.db ||
		s.asyncIAM.CurrentArtifact == nil || !s.pool.ManagedBy(s.asyncIAM.Authority) {
		return ErrManagedUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.started.CompareAndSwap(false, true) {
		return ErrManagedUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.inflightWG.Wait()
	}()
	cron, jobs := time.NewTicker(s.cronInterval), time.NewTicker(s.jobsInterval)
	defer cron.Stop()
	defer jobs.Stop()
	if ready != nil {
		close(ready)
	}
	s.asyncCronTick(ctx)
	s.asyncJobsTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.stop:
			return nil
		case <-cron.C:
			s.asyncCronTick(ctx)
		case <-jobs.C:
			s.asyncJobsTick(ctx)
		}
	}
}
