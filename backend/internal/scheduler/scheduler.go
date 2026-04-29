// Package scheduler runs background time-based work for Orva: cron
// triggers (Phase 1), KV TTL sweeps (Phase 3), and queued background
// jobs (Phase 5). Each concern is a tick on the same goroutine so we
// don't grow goroutine sprawl as the feature set expands.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/pool"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/robfig/cron/v3"
)

// cronParser supports the standard 5-field expression with all the usual
// shorthands (@hourly, @daily, @weekly, @monthly, @yearly) and ranges.
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ParseCronExpr returns a Schedule the scheduler can use to compute the
// next fire time. Exposed so handlers can validate user input before
// persisting the row.
func ParseCronExpr(expr string) (cron.Schedule, error) {
	return cronParser.Parse(expr)
}

// Scheduler owns the timer goroutine. Constructed once at server boot
// and started immediately after the HTTP server is listening so cron
// fires don't compete with the prewarm path on a cold container.
type Scheduler struct {
	db      *database.Database
	pool    *pool.Manager
	dataDir string

	// Tick intervals for each loop. Cron fires due rows; KV sweeps
	// expired entries; jobs claims due jobs and dispatches them. All
	// have sane defaults but are exported via setters for tests.
	cronInterval time.Duration
	kvInterval   time.Duration
	jobsInterval time.Duration

	// Concurrency cap on background jobs so a queue spike can't starve
	// HTTP traffic. Default min(8, sandbox.max_concurrent / 4).
	jobsConcurrency int

	// Inflight prevents the same cron row from being fired twice if a
	// previous tick's invocation overruns the next tick (a 1-minute
	// schedule that takes 90s to invoke). Map of schedule_id → struct{}.
	inflight   sync.Map
	inflightWG sync.WaitGroup

	// jobsSem caps jobs concurrency.
	jobsSem chan struct{}

	// stop signals the loop to exit. Closed by Stop().
	stop chan struct{}
}

// New constructs a Scheduler. Wire by passing the running database +
// pool manager from server.New.
func New(db *database.Database, pm *pool.Manager, dataDir string) *Scheduler {
	jobsConc := 8
	return &Scheduler{
		db:              db,
		pool:            pm,
		dataDir:         dataDir,
		cronInterval:    30 * time.Second,
		kvInterval:      5 * time.Minute,
		jobsInterval:    5 * time.Second,
		jobsConcurrency: jobsConc,
		jobsSem:         make(chan struct{}, jobsConc),
		stop:            make(chan struct{}),
	}
}

// Start kicks off the timer loops. Returns immediately. ctx cancellation
// drains in-flight invocations before the goroutine exits.
func (s *Scheduler) Start(ctx context.Context) {
	// Recompute next_run_at on boot so a long downtime doesn't leave
	// thousands of "missed" rows pretending they're due. Best-effort —
	// any errors are logged and the row simply won't fire until its
	// recomputed time.
	s.recomputeNextRunOnBoot()

	go s.cronLoop(ctx)
	go s.kvSweepLoop(ctx)
	go s.jobsLoop(ctx)
	slog.Info("scheduler started",
		"cron_interval_s", int(s.cronInterval.Seconds()),
		"kv_sweep_interval_s", int(s.kvInterval.Seconds()),
		"jobs_interval_s", int(s.jobsInterval.Seconds()),
		"jobs_concurrency", s.jobsConcurrency)
}

// Stop drains in-flight invocations and signals the loop to exit. Safe
// to call multiple times.
func (s *Scheduler) Stop(timeout time.Duration) {
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
	done := make(chan struct{})
	go func() { s.inflightWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		slog.Warn("scheduler shutdown timeout — some cron invocations may still be running")
	}
}

func (s *Scheduler) recomputeNextRunOnBoot() {
	rows, err := s.db.ListAllCronSchedules()
	if err != nil {
		slog.Warn("scheduler: list schedules at boot failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		sched, err := ParseCronExpr(r.CronExpr)
		if err != nil {
			slog.Warn("scheduler: bad cron expr at boot", "id", r.ID, "expr", r.CronExpr, "err", err)
			continue
		}
		next := sched.Next(now)
		// Only update when the row had no next_run_at OR it's in the past.
		if r.NextRunAt == nil || r.NextRunAt.Before(now) {
			r.NextRunAt = &next
			if err := s.db.UpdateCronSchedule(r); err != nil {
				slog.Warn("scheduler: persist next_run_at on boot failed", "id", r.ID, "err", err)
			}
		}
	}
}

func (s *Scheduler) cronLoop(ctx context.Context) {
	t := time.NewTicker(s.cronInterval)
	defer t.Stop()

	// Fire once on boot too so a freshly-deployed schedule with
	// next_run_at in the past doesn't wait a full interval.
	s.cronTick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			s.cronTick(ctx)
		}
	}
}

func (s *Scheduler) cronTick(ctx context.Context) {
	due, err := s.db.DueCronSchedules(time.Now().UTC(), 50)
	if err != nil {
		slog.Warn("scheduler: query due schedules failed", "err", err)
		return
	}
	for _, row := range due {
		// Skip if a previous tick is still firing this exact row.
		if _, busy := s.inflight.LoadOrStore(row.ID, struct{}{}); busy {
			continue
		}
		s.inflightWG.Add(1)
		go func(r *database.CronSchedule) {
			defer s.inflightWG.Done()
			defer s.inflight.Delete(r.ID)
			s.fireCron(ctx, r)
		}(row)
	}
}

// fireCron dispatches a single cron row's payload to its function and
// records the result. The whole call uses an isolated context so the
// caller's context (which may be the long-lived server context) doesn't
// hold the worker pinned beyond the function's own timeout.
func (s *Scheduler) fireCron(parent context.Context, row *database.CronSchedule) {
	ranAt := time.Now().UTC()

	// Compute next_run_at first so an Acquire failure still moves the row
	// forward. Without this, a permanently-broken function would keep
	// matching the "due" query every tick and saturate the goroutine.
	var nextAt time.Time
	if sched, err := ParseCronExpr(row.CronExpr); err == nil {
		nextAt = sched.Next(ranAt)
	} else {
		// Bad expression — back off an hour and surface the error.
		nextAt = ranAt.Add(time.Hour)
		s.persistResult(row.ID, ranAt, nextAt, "failed", "invalid cron_expr: "+err.Error())
		return
	}

	// Look up the function — confirms it still exists (cron rows are FK'd
	// with ON DELETE CASCADE so this should always succeed) and gives us
	// the timeout for the dispatch context.
	fn, err := s.db.GetFunction(row.FunctionID)
	if err != nil {
		s.persistResult(row.ID, ranAt, nextAt, "failed", "function lookup: "+err.Error())
		return
	}
	timeout := time.Duration(fn.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	acq, err := s.pool.Acquire(ctx, row.FunctionID)
	if err != nil {
		s.persistResult(row.ID, ranAt, nextAt, "failed", "pool acquire: "+err.Error())
		return
	}
	var reqErr error
	defer func() { s.pool.Release(row.FunctionID, acq.Worker, reqErr) }()

	// Build the synthetic event. Cron payloads land at POST / so the
	// handler signature is identical to a public invocation; we add a
	// header so user code can branch on origin.
	execID, _ := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 12)
	execID = "exec_" + execID
	body := row.Payload
	if body == "" {
		body = "{}"
	}
	event := map[string]any{
		"method": "POST",
		"path":   "/",
		"headers": map[string]string{
			"content-type":          "application/json",
			"x-orva-trigger":        "cron",
			"x-orva-cron-id":        row.ID,
			"x-orva-execution-id":   execID,
			"x-orva-function-id":    fn.ID,
		},
		"body": body,
	}
	eventJSON, _ := json.Marshal(event)

	respJSON, stderr, err := acq.Worker.Dispatch(ctx, eventJSON)
	if err != nil {
		reqErr = err
		errMsg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg = "function timed out"
		}
		s.recordExecution(execID, fn.ID, "error", 0, ranAt, stderr, errMsg)
		s.persistResult(row.ID, ranAt, nextAt, "failed", errMsg)
		return
	}

	// Inspect the response status so a 5xx returned by user code is
	// recorded as a cron failure (matching HTTP invoke semantics).
	var resp struct {
		StatusCode int `json:"statusCode"`
	}
	_ = json.Unmarshal(respJSON, &resp)
	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = 200
	}

	if statusCode >= 500 {
		s.recordExecution(execID, fn.ID, "error", statusCode, ranAt, stderr, "function returned "+http3xxLabel(statusCode))
		s.persistResult(row.ID, ranAt, nextAt, "failed", "function returned "+http3xxLabel(statusCode))
		return
	}

	s.recordExecution(execID, fn.ID, "success", statusCode, ranAt, stderr, "")
	s.persistResult(row.ID, ranAt, nextAt, "ok", "")
}

func (s *Scheduler) persistResult(id string, ranAt, nextAt time.Time, status, errMsg string) {
	if err := s.db.UpdateCronAfterRun(id, ranAt, nextAt, status, errMsg); err != nil {
		slog.Warn("scheduler: update after run failed", "id", id, "err", err)
	}
}

// recordExecution mirrors what handlers/invoke.go does for HTTP-triggered
// runs. The Activity tab + Dashboard recent-invocations list both read
// from the executions table so cron-fired runs need to land there too.
func (s *Scheduler) recordExecution(execID, fnID, status string, statusCode int, startedAt time.Time, stderr []byte, errMsg string) {
	durationMS := time.Since(startedAt).Milliseconds()
	exec := &database.Execution{
		ID:         execID,
		FunctionID: fnID,
		Status:     status,
		ColdStart:  false, // best-effort; cron ignores cold-start signal
	}
	s.db.AsyncInsertExecutionFinal(exec, durationMS, statusCode, errMsg, 0)
	if len(stderr) > 0 {
		s.db.AsyncInsertExecutionLog(&database.ExecutionLog{
			ExecutionID: execID,
			Stderr:      string(stderr),
		})
	}
}

// ── KV TTL sweep ─────────────────────────────────────────────────────

func (s *Scheduler) kvSweepLoop(ctx context.Context) {
	t := time.NewTicker(s.kvInterval)
	defer t.Stop()

	// One sweep on boot so a recently-restarted server doesn't keep
	// hours-old expired rows around for the full first interval.
	s.kvSweepOnce()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			s.kvSweepOnce()
		}
	}
}

func (s *Scheduler) kvSweepOnce() {
	deleted, err := s.db.KVSweepExpired()
	if err != nil {
		slog.Warn("kv: sweep failed", "err", err)
		return
	}
	if deleted > 0 {
		slog.Debug("kv: sweep removed expired keys", "deleted", deleted)
	}
}

// ── Jobs queue ───────────────────────────────────────────────────────

func (s *Scheduler) jobsLoop(ctx context.Context) {
	t := time.NewTicker(s.jobsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			s.jobsTick(ctx)
		}
	}
}

func (s *Scheduler) jobsTick(parent context.Context) {
	// Don't claim more than the semaphore can run at once. Otherwise
	// claimed-but-blocked jobs sit at status='running' while their
	// goroutines wait, which inflates the in-flight metric and makes
	// retries harder to reason about.
	free := cap(s.jobsSem) - len(s.jobsSem)
	if free <= 0 {
		return
	}
	jobs, err := s.db.ClaimDueJobs(time.Now().UTC(), free)
	if err != nil {
		slog.Warn("jobs: claim failed", "err", err)
		return
	}
	for _, job := range jobs {
		select {
		case s.jobsSem <- struct{}{}:
		default:
			// Shouldn't happen since we sized to `free`, but be safe.
			continue
		}
		s.inflightWG.Add(1)
		go func(j *database.Job) {
			defer s.inflightWG.Done()
			defer func() { <-s.jobsSem }()
			s.runJob(parent, j)
		}(job)
	}
}

func (s *Scheduler) runJob(parent context.Context, j *database.Job) {
	fn, err := s.db.GetFunction(j.FunctionID)
	if err != nil {
		_ = s.db.MarkJobFailure(j.ID, "function lookup: "+err.Error(), j.Attempts, j.MaxAttempts)
		return
	}
	timeout := time.Duration(fn.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	acq, err := s.pool.Acquire(ctx, j.FunctionID)
	if err != nil {
		_ = s.db.MarkJobFailure(j.ID, "pool acquire: "+err.Error(), j.Attempts, j.MaxAttempts)
		return
	}
	var reqErr error
	defer func() { s.pool.Release(j.FunctionID, acq.Worker, reqErr) }()

	body := string(j.Payload)
	if body == "" {
		body = "{}"
	}
	event := map[string]any{
		"method": "POST",
		"path":   "/",
		"headers": map[string]string{
			"content-type":          "application/json",
			"x-orva-trigger":        "job",
			"x-orva-job-id":         j.ID,
			"x-orva-function-id":    fn.ID,
			"x-orva-attempt":        strconv.Itoa(j.Attempts),
		},
		"body": body,
	}
	eventJSON, _ := json.Marshal(event)

	respJSON, _, err := acq.Worker.Dispatch(ctx, eventJSON)
	if err != nil {
		reqErr = err
		_ = s.db.MarkJobFailure(j.ID, err.Error(), j.Attempts, j.MaxAttempts)
		return
	}

	// 5xx counts as a failure for retry purposes.
	var resp struct {
		StatusCode int `json:"statusCode"`
	}
	_ = json.Unmarshal(respJSON, &resp)
	if resp.StatusCode >= 500 {
		_ = s.db.MarkJobFailure(j.ID, "function returned 5xx", j.Attempts, j.MaxAttempts)
		return
	}
	_ = s.db.MarkJobSuccess(j.ID)
}

// http3xxLabel renders an HTTP status as a short string for log lines.
// (Misnamed historically; covers any code.)
func http3xxLabel(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	default:
		return "ok"
	}
}
