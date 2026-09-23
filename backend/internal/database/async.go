package database

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// writeJob is a single INSERT/UPDATE the batched writer will apply.
type writeJob struct {
	sql  string
	args []any
	// bytes is the approximate heap this job retains. Queues are bounded by
	// bytes as well as count because a job can carry a captured request body
	// (replay_capture_max_bytes, 1 MiB by default) -- 1024 slots of those is
	// ~1 GiB held while the writer is stalled.
	bytes int
	// attempts counts commit passes this job has survived. A statement that
	// fails deterministically would otherwise be retried forever.
	attempts int
}

// jobBytes approximates what a job retains: the SQL text plus the payload
// args. Only string and []byte args are worth counting; the rest are
// word-sized.
func jobBytes(sql string, args []any) int {
	n := len(sql) + 64
	for _, a := range args {
		switch v := a.(type) {
		case string:
			n += len(v)
		case []byte:
			n += len(v)
		default:
			n += 16
		}
	}
	return n
}

// Queue byte ceilings. Generous enough that ordinary bursts never touch
// them, small enough that a stalled writer cannot exhaust the host.
const (
	maxCriticalQueueBytes  = 64 << 20 // 64 MiB
	maxTelemetryQueueBytes = 32 << 20 // 32 MiB
)

// Reserve before publishing to a channel: the consumer can receive a job
// immediately, so incrementing after send can make the counter negative and
// concurrent load-then-add checks can exceed the queue's memory budget.
func reserveQueueBytes(counter *atomic.Int64, n, limit int64) bool {
	if n < 0 || n > limit {
		return false
	}
	for {
		current := counter.Load()
		if current < 0 || current > limit-n {
			return false
		}
		if counter.CompareAndSwap(current, current+n) {
			return true
		}
	}
}

// retainRetryBatch copies before clearing because commit may return batch or
// a sub-slice of it. Failure is rare, so allocating here is preferable to
// holding references to completed jobs or silently zeroing retry payloads.
func retainRetryBatch(batch, retry []writeJob) []writeJob {
	next := append(make([]writeJob, 0, len(retry)), retry...)
	clear(batch)
	return next
}

// Commit budgets.
//
// The old code gave the whole BeginTx+Exec+Commit 5s and dropped the batch
// when it expired. Two things were wrong with that. It undercut the DSN's
// busy_timeout of 10s, so SQLite's own contention handling never got to
// finish; and the write pool has MaxOpenConns(1), so when VACUUM holds that
// connection the failure is Go's pool handing out nothing, not SQLite being
// busy -- BeginTx blocks and the batch is discarded outright, for the entire
// multi-minute duration of the VACUUM.
//
// The fix is a single budget ABOVE busy_timeout, plus retention: expiry now
// means "hold this batch and try again", not "throw it away". A separate,
// shorter budget for acquiring the connection was tried first and is wrong
// -- the context handed to BeginTx governs the whole transaction, so
// cancelling it after acquisition makes database/sql roll the transaction
// back underneath you ("transaction has already been committed or rolled
// back" at commit time). Blocking the writer for txBudget while a VACUUM
// holds the connection is fine: nothing can be written during it anyway,
// and producers feel backpressure through the channel.
const (
	txBudget         = 15 * time.Second
	maxCommitBackoff = 5 * time.Second
	maxJobAttempts   = 3
)

// asyncWriter runs a single goroutine that consumes writeJobs from a
// buffered channel and commits them in small transactions. This replaces
// the goroutine-per-call pattern which, at sustained 500+ req/s, churns a
// goroutine and a separate SQLite transaction per invoke.
//
// The writer batches up to batchMax jobs or flushes every flushEvery
// interval — whichever comes first. That gives bounded per-job latency
// while amortizing fsync cost across dozens of rows.
type asyncWriter struct {
	db        *Database
	critical  chan writeJob
	telemetry chan writeJob
	done      chan struct{}

	// stopRequested wakes producers waiting to enqueue. enqueueMu fences
	// their final channel sends before quit tells the consumer to drain.
	// The job channels stay open: closing them could panic a late producer.
	quit          chan struct{}
	stopRequested chan struct{}
	enqueueMu     sync.RWMutex
	closeOnce     sync.Once

	batchMax   int
	flushEvery time.Duration

	criticalBytes  atomic.Int64
	telemetryBytes atomic.Int64

	dropped  atomic.Uint64
	timeouts atomic.Uint64
	failed   atomic.Uint64
	shed     atomic.Uint64
	retried  atomic.Uint64
}

func newAsyncWriter(db *Database) *asyncWriter {
	return &asyncWriter{
		db:            db,
		critical:      make(chan writeJob, 1024),
		telemetry:     make(chan writeJob, 1024),
		done:          make(chan struct{}),
		quit:          make(chan struct{}),
		stopRequested: make(chan struct{}),
		batchMax:      50,
		flushEvery:    50 * time.Millisecond,
	}
}

// AsyncExec queues a critical write with bounded backpressure.
// Callers that can propagate cancellation should use AsyncExecCritical.
func (db *Database) AsyncExec(sql string, args ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.AsyncExecCritical(ctx, sql, args...); err != nil {
		slog.Warn("critical async write failed", "err", err)
		return err
	}
	return nil
}

// AsyncExecCritical applies bounded backpressure and reports queue or deadline
// failures. Once enqueued, the writer owns the job. It never creates an
// overflow goroutine or makes request latency wait for SQLite fsync.
func (db *Database) AsyncExecCritical(ctx context.Context, statement string, args ...any) error {
	if db.writer == nil {
		_, err := db.write.ExecContext(ctx, statement, args...)
		return err
	}
	a := db.writer
	// A reader keeps publication ahead of the final drain. Shutdown wakes
	// blocked senders first, then takes the exclusive lock before closing quit.
	a.enqueueMu.RLock()
	select {
	case <-a.stopRequested:
		a.enqueueMu.RUnlock()
		_, err := db.write.ExecContext(ctx, statement, args...)
		return err
	default:
	}
	j := writeJob{sql: statement, args: args, bytes: jobBytes(statement, args)}
	if !reserveQueueBytes(&a.criticalBytes, int64(j.bytes), maxCriticalQueueBytes) {
		a.enqueueMu.RUnlock()
		a.timeouts.Add(1)
		return errors.New("critical write queue is over its byte budget")
	}
	select {
	case <-a.stopRequested:
		a.criticalBytes.Add(int64(-j.bytes))
		a.enqueueMu.RUnlock()
		// Shutting down. Fall back to a direct write so work already in
		// flight still lands, rather than panicking on a closed channel.
		_, err := db.write.ExecContext(ctx, statement, args...)
		return err
	case a.critical <- j:
		a.enqueueMu.RUnlock()
		return nil
	case <-ctx.Done():
		a.criticalBytes.Add(int64(-j.bytes))
		a.enqueueMu.RUnlock()
		a.timeouts.Add(1)
		return ctx.Err()
	}
}

// AsyncExecTelemetry queues best-effort activity, log, or span data. A full
// queue drops the record and increments an observable counter.
func (db *Database) AsyncExecTelemetry(statement string, args ...any) {
	if db.writer == nil {
		if _, err := db.write.Exec(statement, args...); err != nil {
			slog.Warn("direct telemetry write failed", "err", err)
		}
		return
	}
	a := db.writer
	a.enqueueMu.RLock()
	select {
	case <-a.stopRequested:
		a.enqueueMu.RUnlock()
		a.dropped.Add(1)
		return
	default:
	}
	j := writeJob{sql: statement, args: args, bytes: jobBytes(statement, args)}
	if !reserveQueueBytes(&a.telemetryBytes, int64(j.bytes), maxTelemetryQueueBytes) {
		a.enqueueMu.RUnlock()
		a.dropped.Add(1)
		return
	}
	select {
	case <-a.stopRequested:
		a.telemetryBytes.Add(int64(-j.bytes))
		a.enqueueMu.RUnlock()
		a.dropped.Add(1)
	case a.telemetry <- j:
		a.enqueueMu.RUnlock()
	default:
		a.telemetryBytes.Add(int64(-j.bytes))
		a.enqueueMu.RUnlock()
		a.dropped.Add(1)
	}
}

type WriterStats struct {
	CriticalDepth     int
	TelemetryDepth    int
	CriticalCap       int
	TelemetryCap      int
	CriticalBytes     int64
	TelemetryBytes    int64
	CriticalCapBytes  int64
	TelemetryCapBytes int64
	CriticalTimeouts  uint64
	CriticalFailures  uint64
	DroppedTelemetry  uint64
	ShedWrites        uint64
}

func (db *Database) WriterStats() WriterStats {
	if db == nil || db.writer == nil {
		return WriterStats{}
	}
	return WriterStats{
		CriticalDepth: len(db.writer.critical), TelemetryDepth: len(db.writer.telemetry),
		CriticalCap: cap(db.writer.critical), TelemetryCap: cap(db.writer.telemetry),
		CriticalBytes: db.writer.criticalBytes.Load(), TelemetryBytes: db.writer.telemetryBytes.Load(),
		CriticalCapBytes: maxCriticalQueueBytes, TelemetryCapBytes: maxTelemetryQueueBytes,
		CriticalTimeouts: db.writer.timeouts.Load(), CriticalFailures: db.writer.failed.Load(),
		DroppedTelemetry: db.writer.dropped.Load(), ShedWrites: db.writer.shed.Load(),
	}
}

// start launches the consumer goroutine. Idempotent — called at most once
// per Database instance.
// start launches the consumer goroutine.
//
// Deliberately NOT registered in db.asyncWG. It used to be, and that forced
// Close() to signal the writer before waiting for producers -- the writer
// was itself one of the things being waited on, so waiting first would
// deadlock. Signalling first is what made "send on closed channel" reachable
// from any producer still running. Keeping the writer out of the group lets
// Close wait for producers and only then stop the writer, which is the
// order that is actually correct.
func (a *asyncWriter) start() {
	go a.run()
}

// run is the consumer loop. Drains the channels into batched transactions.
//
// A batch that fails transiently is RETAINED and retried with backoff
// instead of being discarded, and while a queue is backing off its channel
// case is disabled so the buffer fills and producers feel real backpressure.
// That is the difference between "a VACUUM ran" and "every execution record
// written during the VACUUM is gone".
func (a *asyncWriter) run() {
	ticker := time.NewTicker(a.flushEvery)
	defer ticker.Stop()
	critical := a.critical
	telemetry := a.telemetry

	criticalBatch := make([]writeJob, 0, a.batchMax)
	telemetryBatch := make([]writeJob, 0, a.batchMax)

	var criticalBackoff, telemetryBackoff time.Duration
	var criticalUntil, telemetryUntil time.Time

	// release zeroes the slice before reslicing. batch[:0] alone keeps the
	// backing array alive with every job's args still referenced, which for
	// captured request bodies is up to batchMax MiB pinned after every flush.
	release := func(b []writeJob) []writeJob {
		clear(b)
		return b[:0]
	}
	batchBytes := func(b []writeJob) int64 {
		var n int64
		for _, job := range b {
			n += int64(job.bytes)
		}
		return n
	}

	flushCritical := func() {
		if len(criticalBatch) == 0 {
			return
		}
		retry := a.commit(criticalBatch, false)
		a.criticalBytes.Add(batchBytes(retry) - batchBytes(criticalBatch))
		if len(retry) == 0 {
			criticalBatch = release(criticalBatch)
			criticalBackoff, criticalUntil = 0, time.Time{}
			critical = a.critical
			return
		}
		criticalBatch = retainRetryBatch(criticalBatch, retry)
		criticalBackoff = nextBackoff(criticalBackoff)
		criticalUntil = time.Now().Add(criticalBackoff)
		critical = nil // stop draining: let the channel fill and push back
		slog.Warn("async writer retrying critical batch",
			"jobs", len(retry), "backoff", criticalBackoff)
	}
	flushTelemetry := func() {
		if len(telemetryBatch) == 0 {
			return
		}
		retry := a.commit(telemetryBatch, true)
		a.telemetryBytes.Add(batchBytes(retry) - batchBytes(telemetryBatch))
		if len(retry) == 0 {
			telemetryBatch = release(telemetryBatch)
			telemetryBackoff, telemetryUntil = 0, time.Time{}
			telemetry = a.telemetry
			return
		}
		telemetryBatch = retainRetryBatch(telemetryBatch, retry)
		telemetryBackoff = nextBackoff(telemetryBackoff)
		telemetryUntil = time.Now().Add(telemetryBackoff)
		telemetry = nil
	}

	// drainAndFinish is the shutdown path: take whatever is already queued,
	// flush it, and force the last attempt regardless of backoff.
	drainAndFinish := func() {
		for {
			select {
			case job := <-a.critical:
				criticalBatch = append(criticalBatch, job)
				continue
			case job := <-a.telemetry:
				telemetryBatch = append(telemetryBatch, job)
				continue
			default:
			}
			break
		}
		criticalUntil, telemetryUntil = time.Time{}, time.Time{}
		if len(criticalBatch) > 0 {
			if retry := a.commit(criticalBatch, false); len(retry) > 0 {
				a.failed.Add(uint64(len(retry)))
				slog.Warn("async writer shutting down with unwritten critical jobs",
					"jobs", len(retry))
			}
		}
		if len(telemetryBatch) > 0 {
			if retry := a.commit(telemetryBatch, true); len(retry) > 0 {
				a.dropped.Add(uint64(len(retry)))
			}
		}
		// No producer can publish after quit closes. Every retained job has
		// either been committed or explicitly counted as failed/dropped.
		a.criticalBytes.Store(0)
		a.telemetryBytes.Store(0)
		close(a.done)
	}

	for {
		select {
		case job, ok := <-critical:
			if !ok {
				critical = nil
				continue
			}
			criticalBatch = append(criticalBatch, job)
			if len(criticalBatch) >= a.batchMax {
				flushCritical()
			}
		case job, ok := <-telemetry:
			if !ok {
				telemetry = nil
				continue
			}
			telemetryBatch = append(telemetryBatch, job)
			if len(telemetryBatch) >= a.batchMax {
				flushTelemetry()
			}
		case <-ticker.C:
			now := time.Now()
			if now.After(criticalUntil) {
				flushCritical()
			}
			if now.After(telemetryUntil) {
				flushTelemetry()
			}
		case <-a.quit:
			drainAndFinish()
			return
		}
	}
}

// nextBackoff doubles up to the cap, starting at the flush interval.
func nextBackoff(cur time.Duration) time.Duration {
	if cur == 0 {
		return 100 * time.Millisecond
	}
	next := cur * 2
	if next > maxCommitBackoff {
		return maxCommitBackoff
	}
	return next
}

// commit applies a batch. It returns the jobs that should be RETRIED --
// empty on success, and on a transient failure the whole batch, so the
// caller can hold them in memory and try again rather than discarding them.
//
// Previously any error rolled the transaction back and only incremented a
// counter. batchMax is 50, so one statement failing took up to 49 unrelated
// rows with it -- and the failure that triggers this in practice is an FK
// violation from a function deleted mid-invocation, whose executions row
// and execution_logs row are queued together with an FK between them, so
// killing batch N also killed batch N+1.
func (a *asyncWriter) commit(batch []writeJob, telemetry bool) []writeJob {
	ctx, cancel := context.WithTimeout(context.Background(), txBudget)
	defer cancel()

	tx, err := a.db.write.BeginTx(ctx, nil)
	if err != nil {
		// Could not get the single write connection -- almost always because
		// a VACUUM or a backup is holding it. Nothing is wrong with the work,
		// so hand it back to be retried rather than dropping it.
		return batch
	}
	stmtCtx := ctx
	prepared := make(map[string]*sql.Stmt)
	closePrepared := func() {
		for _, stmt := range prepared {
			_ = stmt.Close()
		}
	}

	failedIdx := -1
	var failErr error
	for i, j := range batch {
		stmt := prepared[j.sql]
		if stmt == nil {
			stmt, err = tx.PrepareContext(stmtCtx, j.sql)
			if err != nil {
				failedIdx, failErr = i, err
				break
			}
			prepared[j.sql] = stmt
		}
		if _, err := stmt.ExecContext(stmtCtx, j.args...); err != nil {
			failedIdx, failErr = i, err
			break
		}
	}
	closePrepared()
	if failedIdx < 0 {
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			slog.Warn("batch commit failed; will retry", "err", err, "jobs", len(batch))
			return batch
		}
		a.finish(batch, telemetry, nil)
		return nil
	}

	// A statement failed, which poisons the whole transaction. Roll back and
	// re-apply the batch one job at a time under savepoints so a single bad
	// statement cannot take its neighbours with it.
	_ = tx.Rollback()
	slog.Warn("batch stmt failed; isolating", "err", failErr, "jobs", len(batch))
	return a.commitIsolated(batch, telemetry)
}

// commitIsolated is the recovery pass: one SAVEPOINT per job so a failure
// rolls back only itself. Deliberately NOT the hot path -- at ~1500 jobs/s
// two extra statements per job to guard against a rare event is the wrong
// trade, so this only runs after a batch has already failed once.
func (a *asyncWriter) commitIsolated(batch []writeJob, telemetry bool) []writeJob {
	ctx, cancel := context.WithTimeout(context.Background(), txBudget)
	defer cancel()

	tx, err := a.db.write.BeginTx(ctx, nil)
	if err != nil {
		return batch
	}
	stmtCtx := ctx

	var retry []writeJob
	applied, shed := 0, 0
	for _, j := range batch {
		if _, err := tx.ExecContext(stmtCtx, "SAVEPOINT job"); err != nil {
			// Cannot even open a savepoint; treat the remainder as retryable.
			_ = tx.Rollback()
			return append(retry, batch[applied+shed:]...)
		}
		_, execErr := tx.ExecContext(stmtCtx, j.sql, j.args...)
		if execErr == nil {
			if _, err := tx.ExecContext(stmtCtx, "RELEASE job"); err != nil {
				_ = tx.Rollback()
				return append(retry, batch[applied+shed:]...)
			}
			applied++
			continue
		}
		// ROLLBACK TO is what makes this work: database/sql cannot see
		// sqlite3_get_autocommit, so the savepoint result is the only signal
		// that the transaction is still usable.
		if _, err := tx.ExecContext(stmtCtx, "ROLLBACK TO job"); err != nil {
			_ = tx.Rollback()
			return append(retry, batch[applied+shed:]...)
		}
		_, _ = tx.ExecContext(stmtCtx, "RELEASE job")

		j.attempts++
		if permanentWriteFailure(execErr) || j.attempts >= maxJobAttempts {
			// Deterministic failure -- an FK violation against a row that no
			// longer exists will fail identically forever. Shed it, loudly,
			// rather than looping on it.
			slog.Warn("shedding permanently failing async write",
				"err", execErr, "attempts", j.attempts, "sql", truncSQL(j.sql))
			a.shed.Add(1)
			shed++
			continue
		}
		retry = append(retry, j)
	}

	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return batch
	}
	if shed > 0 {
		// Route to the same counter the priority would have used, so
		// WriterStats keeps meaning what it says: CriticalFailures is
		// "critical work we could not write", DroppedTelemetry is
		// "best-effort work we gave up on".
		if telemetry {
			a.dropped.Add(uint64(shed))
		} else {
			a.failed.Add(uint64(shed))
		}
	}
	if len(retry) > 0 {
		a.retried.Add(uint64(len(retry)))
	}
	return retry
}

// permanentWriteFailure reports whether an error will recur identically on
// every retry. Constraint violations and malformed statements are about the
// statement itself; everything else (busy, locked, I/O) is transient.
func permanentWriteFailure(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case sqlite3.SQLITE_CONSTRAINT, sqlite3.SQLITE_ERROR, sqlite3.SQLITE_MISMATCH:
			return true
		}
		return false
	}
	// Unknown error shape: treat as transient and let maxJobAttempts be the
	// termination backstop.
	return false
}

func truncSQL(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

func (a *asyncWriter) finish(batch []writeJob, telemetry bool, err error) {
	if telemetry && err != nil {
		a.dropped.Add(uint64(len(batch)))
	} else if err != nil {
		a.failed.Add(uint64(len(batch)))
	}
}

// stop signals shutdown and waits for the consumer to flush what is queued.
// Idempotent. The deadline bounds how long a wedged write connection can
// hold up process exit.
func (a *asyncWriter) stop(timeout time.Duration) {
	a.closeOnce.Do(func() {
		close(a.stopRequested)
		a.enqueueMu.Lock()
		close(a.quit)
		a.enqueueMu.Unlock()
	})
	select {
	case <-a.done:
	case <-time.After(timeout):
		slog.Warn("async writer did not drain before shutdown deadline",
			"timeout", timeout,
			"critical_queued", len(a.critical), "telemetry_queued", len(a.telemetry))
	}
}
