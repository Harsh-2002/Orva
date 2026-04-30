// Package scheduler runs background time-based work for Orva: cron
// triggers (Phase 1), KV TTL sweeps (Phase 3), and queued background
// jobs (Phase 5). Each concern is a tick on the same goroutine so we
// don't grow goroutine sprawl as the feature set expands.
package scheduler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/server/events"
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

	// hub is the in-process events broker. Optional — tests can pass
	// nil and the scheduler stays usable; webhook fanout is the only
	// consumer that cares.
	hub *events.Hub

	// Tick intervals for each loop. Cron fires due rows; KV sweeps
	// expired entries; jobs claims due jobs and dispatches them;
	// webhooks delivers queued event payloads. All have sane defaults
	// but are exported via setters for tests.
	cronInterval     time.Duration
	kvInterval       time.Duration
	jobsInterval     time.Duration
	webhookInterval  time.Duration

	// Concurrency cap on background jobs so a queue spike can't starve
	// HTTP traffic. Default min(8, sandbox.max_concurrent / 4).
	jobsConcurrency    int
	webhookConcurrency int

	// Inflight prevents the same cron row from being fired twice if a
	// previous tick's invocation overruns the next tick (a 1-minute
	// schedule that takes 90s to invoke). Map of schedule_id → struct{}.
	inflight   sync.Map
	inflightWG sync.WaitGroup

	// jobsSem / webhookSem cap their respective tick concurrency.
	jobsSem    chan struct{}
	webhookSem chan struct{}

	// httpClient delivers webhook POSTs. 10s timeout — receivers
	// should ack quickly or fail; long blocking would tie up workers.
	httpClient *http.Client

	// stop signals the loop to exit. Closed by Stop().
	stop chan struct{}
}

// New constructs a Scheduler. Wire by passing the running database +
// pool manager from server.New. hub may be nil — the scheduler still
// works, only the webhook fanout consumer misses out on cron/job
// signals (which is fine for tests).
func New(db *database.Database, pm *pool.Manager, dataDir string, hub *events.Hub) *Scheduler {
	jobsConc := 8
	webhookConc := 4
	return &Scheduler{
		db:                 db,
		pool:               pm,
		dataDir:            dataDir,
		hub:                hub,
		cronInterval:       30 * time.Second,
		kvInterval:         5 * time.Minute,
		jobsInterval:       5 * time.Second,
		webhookInterval:    5 * time.Second,
		jobsConcurrency:    jobsConc,
		webhookConcurrency: webhookConc,
		jobsSem:            make(chan struct{}, jobsConc),
		webhookSem:         make(chan struct{}, webhookConc),
		httpClient:         &http.Client{Timeout: 10 * time.Second},
		stop:               make(chan struct{}),
	}
}

// publishCron emits a cron-related Hub event when a hub is wired. Used
// by the webhook fanout to translate cron failures into user-visible
// notifications. Safe to call with hub=nil (tests).
func (s *Scheduler) publishCron(status string, row *database.CronSchedule, fnName, errMsg string) {
	if s.hub == nil {
		return
	}
	s.hub.Publish("cron", map[string]any{
		"status":        status,
		"schedule_id":   row.ID,
		"function_id":   row.FunctionID,
		"function_name": fnName,
		"cron_expr":     row.CronExpr,
		"error_message": errMsg,
	})
}

// publishJob emits a job-related Hub event. Only called for terminal
// outcomes (success or final failure) — intermediate retries don't
// fire so receivers don't get spammed during backoff.
func (s *Scheduler) publishJob(status string, j *database.Job, fnName, errMsg string, durationMS int64) {
	if s.hub == nil {
		return
	}
	s.hub.Publish("job", map[string]any{
		"status":        status,
		"job_id":        j.ID,
		"function_id":   j.FunctionID,
		"function_name": fnName,
		"attempts":      j.Attempts,
		"max_attempts":  j.MaxAttempts,
		"duration_ms":   durationMS,
		"last_error":    errMsg,
	})
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
	go s.webhookLoop(ctx)
	slog.Info("scheduler started",
		"cron_interval_s", int(s.cronInterval.Seconds()),
		"kv_sweep_interval_s", int(s.kvInterval.Seconds()),
		"jobs_interval_s", int(s.jobsInterval.Seconds()),
		"jobs_concurrency", s.jobsConcurrency,
		"webhook_interval_s", int(s.webhookInterval.Seconds()),
		"webhook_concurrency", s.webhookConcurrency)
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
		errMsg := "invalid cron_expr: " + err.Error()
		s.persistResult(row.ID, ranAt, nextAt, "failed", errMsg)
		s.publishCron("failed", row, "", errMsg)
		return
	}

	// Look up the function — confirms it still exists (cron rows are FK'd
	// with ON DELETE CASCADE so this should always succeed) and gives us
	// the timeout for the dispatch context.
	fn, err := s.db.GetFunction(row.FunctionID)
	if err != nil {
		errMsg := "function lookup: " + err.Error()
		s.persistResult(row.ID, ranAt, nextAt, "failed", errMsg)
		s.publishCron("failed", row, "", errMsg)
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
		errMsg := "pool acquire: " + err.Error()
		s.persistResult(row.ID, ranAt, nextAt, "failed", errMsg)
		s.publishCron("failed", row, fn.Name, errMsg)
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
		s.publishCron("failed", row, fn.Name, errMsg)
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
		errMsg := "function returned " + http3xxLabel(statusCode)
		s.recordExecution(execID, fn.ID, "error", statusCode, ranAt, stderr, errMsg)
		s.persistResult(row.ID, ranAt, nextAt, "failed", errMsg)
		s.publishCron("failed", row, fn.Name, errMsg)
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
	startedAt := time.Now()

	// finalize handles the dual concerns of (a) persisting the
	// outcome to the jobs table (which decides whether to retry or
	// terminate) and (b) firing a Hub event ONLY when the outcome is
	// terminal — so cross-process subscribers (the webhook fanout)
	// see one event per job, not one per attempt.
	finalize := func(fnName, errMsg string, success bool) {
		durationMS := time.Since(startedAt).Milliseconds()
		if success {
			_ = s.db.MarkJobSuccess(j.ID)
			s.publishJob("succeeded", j, fnName, "", durationMS)
			return
		}
		_ = s.db.MarkJobFailure(j.ID, errMsg, j.Attempts, j.MaxAttempts)
		// MarkJobFailure transitions to "failed" only when attempts
		// have run out; otherwise it leaves the row in pending for
		// the next tick. Mirror that decision here so we don't fire
		// during retries.
		if j.Attempts >= j.MaxAttempts {
			s.publishJob("failed", j, fnName, errMsg, durationMS)
		}
	}

	fn, err := s.db.GetFunction(j.FunctionID)
	if err != nil {
		// No fn name to report (the row's gone) — webhook receivers
		// still get the function_id.
		finalize("", "function lookup: "+err.Error(), false)
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
		finalize(fn.Name, "pool acquire: "+err.Error(), false)
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
		finalize(fn.Name, err.Error(), false)
		return
	}

	// 5xx counts as a failure for retry purposes.
	var resp struct {
		StatusCode int `json:"statusCode"`
	}
	_ = json.Unmarshal(respJSON, &resp)
	if resp.StatusCode >= 500 {
		finalize(fn.Name, "function returned 5xx", false)
		return
	}
	finalize(fn.Name, "", true)
}

// ── Webhook delivery worker ──────────────────────────────────────────

func (s *Scheduler) webhookLoop(ctx context.Context) {
	t := time.NewTicker(s.webhookInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			s.webhookTick(ctx)
		}
	}
}

func (s *Scheduler) webhookTick(parent context.Context) {
	free := cap(s.webhookSem) - len(s.webhookSem)
	if free <= 0 {
		return
	}
	deliveries, err := s.db.ClaimDueDeliveries(time.Now().UTC(), free)
	if err != nil {
		slog.Warn("webhook: claim due deliveries failed", "err", err)
		return
	}
	for _, d := range deliveries {
		select {
		case s.webhookSem <- struct{}{}:
		default:
			continue
		}
		s.inflightWG.Add(1)
		go func(d *database.WebhookDelivery) {
			defer s.inflightWG.Done()
			defer func() { <-s.webhookSem }()
			s.deliverWebhook(parent, d)
		}(d)
	}
}

// deliverWebhook signs the payload, POSTs to the subscriber, and
// records the outcome. Mirrors the Stripe-style signature verifiable
// by the receiver with HMAC-SHA256 over "<ts>.<body>".
func (s *Scheduler) deliverWebhook(parent context.Context, d *database.WebhookDelivery) {
	sub, err := s.db.GetEventSubscription(d.SubscriptionID)
	if err != nil {
		// Subscription was deleted between claim and delivery (the
		// CASCADE delete should have removed the row, but the FK
		// race-with-claim is possible). Mark permanently failed so
		// we don't retry into thin air.
		_ = s.db.MarkDeliveryFailure(d.ID, "subscription deleted: "+err.Error(),
			d.MaxAttempts, d.MaxAttempts, 0)
		return
	}

	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(sub.Secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(d.Payload)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(d.Payload))
	if err != nil {
		s.handleDeliveryFailure(d, sub, "build request: "+err.Error(), 0)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Orva-Webhook/1.0")
	req.Header.Set("X-Orva-Event", d.EventName)
	req.Header.Set("X-Orva-Delivery-Id", d.ID)
	req.Header.Set("X-Orva-Timestamp", ts)
	req.Header.Set("X-Orva-Signature", signature)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.handleDeliveryFailure(d, sub, "transport: "+err.Error(), 0)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain so connection can be reused
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_ = s.db.MarkDeliverySuccess(d.ID, resp.StatusCode)
		_ = s.db.MarkSubscriptionResult(sub.ID, "ok", "")
		return
	}
	s.handleDeliveryFailure(d, sub,
		fmt.Sprintf("HTTP %d from receiver", resp.StatusCode), resp.StatusCode)
}

// handleDeliveryFailure records the failure on both the delivery row
// (which decides retry vs terminate) and the subscription row (so the
// dashboard's "last status" lights up red on persistent failures).
func (s *Scheduler) handleDeliveryFailure(d *database.WebhookDelivery, sub *database.EventSubscription, errMsg string, respStatus int) {
	_ = s.db.MarkDeliveryFailure(d.ID, errMsg, d.Attempts, d.MaxAttempts, respStatus)
	if d.Attempts >= d.MaxAttempts {
		_ = s.db.MarkSubscriptionResult(sub.ID, "failed", errMsg)
	}
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
