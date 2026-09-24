package database

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// writeJob is a single INSERT/UPDATE the batched writer will apply.
type writeJob struct {
	sql        string
	args       []any
	functionID string
	lease      *ExecutionLease
	bulkInsert bool
	// enqueuedAt starts before channel admission, so queue-wait telemetry
	// includes any time spent waiting for a slot as well as commit time.
	enqueuedAt int64
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
	maxActivityQueueBytes  = 8 << 20  // 8 MiB
	maxTelemetryQueueBytes = 32 << 20 // 32 MiB
)

type writeKind uint8

const (
	writeCritical writeKind = iota
	writeActivity
	writeTelemetry
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
	db       *Database
	critical chan writeJob
	// criticalSlots covers both queued jobs and invocations that reserved a
	// completion slot before execution. A slot returns only after commit.
	criticalSlots chan struct{}
	activity      chan writeJob
	telemetry     chan writeJob
	done          chan struct{}

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
	activityBytes  atomic.Int64
	telemetryBytes atomic.Int64

	dropped         atomic.Uint64
	droppedActivity atomic.Uint64
	deletedWrites   atomic.Uint64
	timeouts        atomic.Uint64
	failed          atomic.Uint64
	shed            atomic.Uint64
	retried         atomic.Uint64
	timing          [3]writerTiming
}

type writerTiming struct {
	attempts       atomic.Uint64
	committedJobs  atomic.Uint64
	connectionNS   atomic.Uint64
	statementNS    atomic.Uint64
	commitNS       atomic.Uint64
	queueWaitNS    atomic.Uint64
	queueWaitCount atomic.Uint64
}

func newAsyncWriter(db *Database) *asyncWriter {
	a := &asyncWriter{
		db:            db,
		critical:      make(chan writeJob, 1024),
		criticalSlots: make(chan struct{}, 1024),
		activity:      make(chan writeJob, 1024),
		telemetry:     make(chan writeJob, 1024),
		done:          make(chan struct{}),
		quit:          make(chan struct{}),
		stopRequested: make(chan struct{}),
		batchMax:      200,
		flushEvery:    50 * time.Millisecond,
	}
	for range cap(a.criticalSlots) {
		a.criticalSlots <- struct{}{}
	}
	return a
}

// ExecutionLease reserves capacity for the final execution row before user
// code runs. Cancel only releases an unused lease; once a job is queued, the
// writer owns it until commit, deletion, or an explicit failed write.
type ExecutionLease struct {
	writer *asyncWriter
	state  atomic.Uint32 // 0: caller-owned, 1: writer-owned, 2: released
}

var errWriterStopping = errors.New("execution writer is stopping")

func (l *ExecutionLease) Cancel() {
	if l != nil && l.writer != nil && l.state.CompareAndSwap(0, 2) {
		l.writer.criticalSlots <- struct{}{}
	}
}

func (l *ExecutionLease) transfer() bool {
	return l != nil && l.state.CompareAndSwap(0, 1)
}

func (l *ExecutionLease) done() {
	if l != nil && l.writer != nil && l.state.CompareAndSwap(1, 2) {
		l.writer.criticalSlots <- struct{}{}
	}
}

// ReserveExecution gives an invocation one durable-writer queue slot before
// it executes. Waiting or rejection here has no function side effects.
func (db *Database) ReserveExecution(ctx context.Context) (*ExecutionLease, error) {
	if db.writer == nil {
		return &ExecutionLease{}, nil
	}
	a := db.writer
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.stopRequested:
		return nil, errWriterStopping
	case <-a.criticalSlots:
		select {
		case <-a.stopRequested:
			a.criticalSlots <- struct{}{}
			return nil, errWriterStopping
		default:
		}
		return &ExecutionLease{writer: a}, nil
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

func (db *Database) asyncExecFunction(functionID, statement string, args ...any) error {
	return db.asyncExecFunctionReserved(functionID, nil, false, statement, args...)
}

func (db *Database) asyncExecFunctionReserved(functionID string, lease *ExecutionLease, bulkInsert bool, statement string, args ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.asyncExecCriticalReserved(ctx, functionID, lease, bulkInsert, statement, args...); err != nil {
		slog.Warn("critical async write failed", "function_id", functionID, "err", err)
		return err
	}
	return nil
}

// AsyncExecCritical applies bounded backpressure and reports queue or deadline
// failures. Once enqueued, the writer owns the job. It never creates an
// overflow goroutine or makes request latency wait for SQLite fsync.
func (db *Database) AsyncExecCritical(ctx context.Context, statement string, args ...any) error {
	return db.asyncExecCritical(ctx, "", statement, args...)
}

func (db *Database) asyncExecCritical(ctx context.Context, functionID, statement string, args ...any) error {
	return db.asyncExecCriticalReserved(ctx, functionID, nil, false, statement, args...)
}

func (db *Database) asyncExecCriticalReserved(ctx context.Context, functionID string, lease *ExecutionLease, bulkInsert bool, statement string, args ...any) error {
	if db.writer == nil {
		if lease != nil {
			lease.Cancel()
		}
		_, err := db.write.ExecContext(ctx, statement, args...)
		return err
	}
	a := db.writer
	if lease == nil {
		var err error
		lease, err = db.ReserveExecution(ctx)
		if err != nil {
			if errors.Is(err, errWriterStopping) {
				_, writeErr := db.write.ExecContext(ctx, statement, args...)
				return writeErr
			}
			a.timeouts.Add(1)
			return err
		}
	} else if lease.writer != a {
		return errors.New("execution lease belongs to another writer")
	}
	// A reader keeps publication ahead of the final drain. Shutdown wakes
	// blocked senders first, then takes the exclusive lock before closing quit.
	a.enqueueMu.RLock()
	select {
	case <-a.stopRequested:
		a.enqueueMu.RUnlock()
		lease.Cancel()
		_, err := db.write.ExecContext(ctx, statement, args...)
		return err
	default:
	}
	j := writeJob{sql: statement, args: args, functionID: functionID, lease: lease, bulkInsert: bulkInsert, bytes: jobBytes(statement, args) + len(functionID), enqueuedAt: time.Now().UnixNano()}
	if !reserveQueueBytes(&a.criticalBytes, int64(j.bytes), maxCriticalQueueBytes) {
		a.enqueueMu.RUnlock()
		lease.Cancel()
		a.timeouts.Add(1)
		return errors.New("critical write queue is over its byte budget")
	}
	if !lease.transfer() {
		a.criticalBytes.Add(int64(-j.bytes))
		a.enqueueMu.RUnlock()
		return errors.New("execution lease already used")
	}
	select {
	case <-a.stopRequested:
		a.criticalBytes.Add(int64(-j.bytes))
		a.enqueueMu.RUnlock()
		lease.done()
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
		lease.done()
		a.timeouts.Add(1)
		return ctx.Err()
	}
}

// AsyncExecActivity reserves a lane for operator-visible activity records.
// Like optional telemetry, it never blocks request completion.
func (db *Database) AsyncExecActivity(statement string, args ...any) {
	db.asyncExecBestEffort(writeActivity, "", false, statement, args...)
}

func (db *Database) asyncExecActivityBulk(statement string, args ...any) {
	db.asyncExecBestEffort(writeActivity, "", true, statement, args...)
}

// AsyncExecTelemetry queues optional replay, log, or span data. A full queue
// drops the record and increments an observable counter.
func (db *Database) AsyncExecTelemetry(statement string, args ...any) {
	db.asyncExecBestEffort(writeTelemetry, "", false, statement, args...)
}

func (db *Database) asyncExecFunctionTelemetry(functionID, statement string, args ...any) {
	db.asyncExecBestEffort(writeTelemetry, functionID, false, statement, args...)
}

func (db *Database) asyncExecBestEffort(kind writeKind, functionID string, bulkInsert bool, statement string, args ...any) {
	if db.writer == nil {
		if _, err := db.write.Exec(statement, args...); err != nil {
			slog.Warn("direct telemetry write failed", "err", err)
		}
		return
	}
	a := db.writer
	queue, bytes, limit := a.telemetry, &a.telemetryBytes, int64(maxTelemetryQueueBytes)
	if kind == writeActivity {
		queue, bytes, limit = a.activity, &a.activityBytes, maxActivityQueueBytes
	}
	a.enqueueMu.RLock()
	select {
	case <-a.stopRequested:
		a.enqueueMu.RUnlock()
		a.recordDropped(kind, 1)
		return
	default:
	}
	j := writeJob{sql: statement, args: args, functionID: functionID, bulkInsert: bulkInsert, bytes: jobBytes(statement, args) + len(functionID), enqueuedAt: time.Now().UnixNano()}
	if !reserveQueueBytes(bytes, int64(j.bytes), limit) {
		a.enqueueMu.RUnlock()
		a.recordDropped(kind, 1)
		return
	}
	select {
	case <-a.stopRequested:
		bytes.Add(int64(-j.bytes))
		a.enqueueMu.RUnlock()
		a.recordDropped(kind, 1)
	case queue <- j:
		a.enqueueMu.RUnlock()
	default:
		bytes.Add(int64(-j.bytes))
		a.enqueueMu.RUnlock()
		a.recordDropped(kind, 1)
	}
}

func (a *asyncWriter) recordDropped(kind writeKind, count uint64) {
	a.dropped.Add(count)
	if kind == writeActivity {
		a.droppedActivity.Add(count)
	}
}

type WriterStats struct {
	CriticalDepth     int
	ActivityDepth     int
	TelemetryDepth    int
	CriticalCap       int
	ActivityCap       int
	TelemetryCap      int
	CriticalBytes     int64
	ActivityBytes     int64
	TelemetryBytes    int64
	CriticalCapBytes  int64
	ActivityCapBytes  int64
	TelemetryCapBytes int64
	CriticalTimeouts  uint64
	CriticalFailures  uint64
	DroppedTelemetry  uint64
	DroppedActivity   uint64
	DeletedWrites     uint64
	ShedWrites        uint64
	Timing            [3]WriterTimingStats
}

type WriterTimingStats struct {
	Attempts       uint64
	CommittedJobs  uint64
	ConnectionNS   uint64
	StatementNS    uint64
	CommitNS       uint64
	QueueWaitNS    uint64
	QueueWaitCount uint64
}

func (db *Database) WriterStats() WriterStats {
	if db == nil || db.writer == nil {
		return WriterStats{}
	}
	stats := WriterStats{
		CriticalDepth: len(db.writer.critical), ActivityDepth: len(db.writer.activity), TelemetryDepth: len(db.writer.telemetry),
		CriticalCap: cap(db.writer.critical), ActivityCap: cap(db.writer.activity), TelemetryCap: cap(db.writer.telemetry),
		CriticalBytes: db.writer.criticalBytes.Load(), ActivityBytes: db.writer.activityBytes.Load(), TelemetryBytes: db.writer.telemetryBytes.Load(),
		CriticalCapBytes: maxCriticalQueueBytes, ActivityCapBytes: maxActivityQueueBytes, TelemetryCapBytes: maxTelemetryQueueBytes,
		CriticalTimeouts: db.writer.timeouts.Load(), CriticalFailures: db.writer.failed.Load(),
		DroppedTelemetry: db.writer.dropped.Load(), DroppedActivity: db.writer.droppedActivity.Load(),
		DeletedWrites: db.writer.deletedWrites.Load(), ShedWrites: db.writer.shed.Load(),
	}
	for i := range stats.Timing {
		t := &db.writer.timing[i]
		stats.Timing[i] = WriterTimingStats{
			Attempts: t.attempts.Load(), CommittedJobs: t.committedJobs.Load(),
			ConnectionNS: t.connectionNS.Load(), StatementNS: t.statementNS.Load(),
			CommitNS: t.commitNS.Load(), QueueWaitNS: t.queueWaitNS.Load(),
			QueueWaitCount: t.queueWaitCount.Load(),
		}
	}
	return stats
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
	activity := a.activity
	telemetry := a.telemetry

	criticalBatch := make([]writeJob, 0, a.batchMax)
	activityBatch := make([]writeJob, 0, a.batchMax)
	telemetryBatch := make([]writeJob, 0, a.batchMax)

	var criticalBackoff, activityBackoff, telemetryBackoff time.Duration
	var criticalUntil, activityUntil, telemetryUntil time.Time

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
		retry := a.commit(criticalBatch, writeCritical)
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
	flushActivity := func() {
		if len(activityBatch) == 0 {
			return
		}
		retry := a.commit(activityBatch, writeActivity)
		a.activityBytes.Add(batchBytes(retry) - batchBytes(activityBatch))
		if len(retry) == 0 {
			activityBatch = release(activityBatch)
			activityBackoff, activityUntil = 0, time.Time{}
			activity = a.activity
			return
		}
		activityBatch = retainRetryBatch(activityBatch, retry)
		activityBackoff = nextBackoff(activityBackoff)
		activityUntil = time.Now().Add(activityBackoff)
		activity = nil
	}
	flushTelemetry := func() {
		if len(telemetryBatch) == 0 {
			return
		}
		retry := a.commit(telemetryBatch, writeTelemetry)
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
			case job := <-a.activity:
				activityBatch = append(activityBatch, job)
				continue
			case job := <-a.telemetry:
				telemetryBatch = append(telemetryBatch, job)
				continue
			default:
			}
			break
		}
		criticalUntil, activityUntil, telemetryUntil = time.Time{}, time.Time{}, time.Time{}
		if len(criticalBatch) > 0 {
			if retry := a.commit(criticalBatch, writeCritical); len(retry) > 0 {
				a.failed.Add(uint64(len(retry)))
				releaseJobs(retry)
				slog.Warn("async writer shutting down with unwritten critical jobs",
					"jobs", len(retry))
			}
		}
		if len(activityBatch) > 0 {
			if retry := a.commit(activityBatch, writeActivity); len(retry) > 0 {
				a.recordDropped(writeActivity, uint64(len(retry)))
			}
		}
		if len(telemetryBatch) > 0 {
			if retry := a.commit(telemetryBatch, writeTelemetry); len(retry) > 0 {
				a.recordDropped(writeTelemetry, uint64(len(retry)))
			}
		}
		// No producer can publish after quit closes. Every retained job has
		// either been committed or explicitly counted as failed/dropped.
		a.criticalBytes.Store(0)
		a.activityBytes.Store(0)
		a.telemetryBytes.Store(0)
		close(a.done)
	}

	for {
		// Prefer execution rows only near the critical queue's high-water
		// mark. Using one-batch pressure here starved the operator activity
		// feed at ordinary sustained load even after activity INSERTs were
		// grouped; most rows were shed without any actual writer failure.
		criticalPressure := len(a.critical) >= cap(a.critical)*3/4
		activityInput := activity
		if criticalPressure {
			activityInput = nil
		}
		// Optional capture cannot displace queued execution or activity.
		optional := telemetry
		if len(a.critical) > 0 || len(a.activity) > 0 {
			optional = nil
		}
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
		case job, ok := <-activityInput:
			if !ok {
				activity = nil
				continue
			}
			activityBatch = append(activityBatch, job)
			if len(activityBatch) >= a.batchMax {
				flushActivity()
			}
		case job, ok := <-optional:
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
			if !criticalPressure && now.After(activityUntil) {
				flushActivity()
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
func (a *asyncWriter) commit(batch []writeJob, kind writeKind) []writeJob {
	a.db.lifecycleMu.RLock()
	defer a.db.lifecycleMu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), txBudget)
	defer cancel()
	work, discarded, err := a.filterDeleted(ctx, batch)
	if err != nil {
		return batch
	}
	if discarded > 0 {
		a.deletedWrites.Add(uint64(discarded))
	}
	if len(work) == 0 {
		return nil
	}

	timing := &a.timing[kind]
	timing.attempts.Add(1)
	connectionStart := time.Now()
	tx, err := a.db.write.BeginTx(ctx, nil)
	timing.connectionNS.Add(uint64(time.Since(connectionStart)))
	if err != nil {
		// Could not get the single write connection -- almost always because
		// a VACUUM or a backup is holding it. Nothing is wrong with the work,
		// so hand it back to be retried rather than dropping it.
		return work
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
	statementStart := time.Now()
	for i := 0; i < len(work); {
		j := work[i]
		statement := j.sql
		args := j.args
		groupEnd := i + 1
		if j.bulkInsert {
			argCount := len(j.args)
			for groupEnd < len(work) && work[groupEnd].bulkInsert && work[groupEnd].sql == j.sql &&
				(argCount+len(work[groupEnd].args)) <= 30000 {
				argCount += len(work[groupEnd].args)
				groupEnd++
			}
			if groupEnd > i+1 {
				if bulk := bulkInsertSQL(j.sql, groupEnd-i); bulk != "" {
					statement = bulk
					args = make([]any, 0, argCount)
					for _, grouped := range work[i:groupEnd] {
						args = append(args, grouped.args...)
					}
				} else {
					groupEnd = i + 1
				}
			}
		}
		stmt := prepared[statement]
		if stmt == nil {
			stmt, err = tx.PrepareContext(stmtCtx, statement)
			if err != nil {
				failedIdx, failErr = i, err
				break
			}
			prepared[statement] = stmt
		}
		if _, err := stmt.ExecContext(stmtCtx, args...); err != nil {
			failedIdx, failErr = i, err
			break
		}
		i = groupEnd
	}
	timing.statementNS.Add(uint64(time.Since(statementStart)))
	closePrepared()
	if failedIdx < 0 {
		commitStart := time.Now()
		if err := tx.Commit(); err != nil {
			timing.commitNS.Add(uint64(time.Since(commitStart)))
			_ = tx.Rollback()
			slog.Warn("batch commit failed; will retry", "err", err, "jobs", len(batch))
			return work
		}
		timing.commitNS.Add(uint64(time.Since(commitStart)))
		timing.committedJobs.Add(uint64(len(work)))
		committedAt := time.Now().UnixNano()
		var queueWaitNS, queueWaitCount uint64
		for _, job := range work {
			if job.enqueuedAt > 0 && committedAt >= job.enqueuedAt {
				queueWaitNS += uint64(committedAt - job.enqueuedAt)
				queueWaitCount++
			}
		}
		timing.queueWaitNS.Add(queueWaitNS)
		timing.queueWaitCount.Add(queueWaitCount)
		releaseJobs(work)
		return nil
	}

	// A statement failed, which poisons the whole transaction. Roll back and
	// re-apply the batch one job at a time under savepoints so a single bad
	// statement cannot take its neighbours with it.
	_ = tx.Rollback()
	slog.Warn("batch stmt failed; isolating", "err", failErr, "jobs", len(work))
	return a.commitIsolated(work, kind)
}

// bulkInsertSQL repeats the VALUES tuple for INSERT statements explicitly
// marked by their caller. An unrecognized statement falls back to
// per-row execution, preserving the generic async writer contract.
func bulkInsertSQL(statement string, rows int) string {
	if rows < 2 {
		return ""
	}
	idx := strings.LastIndex(statement, "VALUES (")
	if idx < 0 {
		return ""
	}
	prefix := statement[:idx+len("VALUES ")]
	tuple := strings.TrimSpace(statement[idx+len("VALUES "):])
	if !strings.HasPrefix(tuple, "(") || !strings.HasSuffix(tuple, ")") {
		return ""
	}
	var b strings.Builder
	b.Grow(len(prefix) + rows*(len(tuple)+1))
	b.WriteString(prefix)
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(tuple)
	}
	return b.String()
}

func releaseJobs(jobs []writeJob) {
	for _, job := range jobs {
		job.lease.done()
	}
}

func (a *asyncWriter) filterDeleted(ctx context.Context, batch []writeJob) ([]writeJob, int, error) {
	var keep []writeJob
	var discarded []writeJob
	var known map[string]bool
	for i, job := range batch {
		alive := true
		if job.functionID != "" {
			if _, ok := a.db.liveFunctions.Load(job.functionID); !ok {
				if known == nil {
					known = make(map[string]bool)
				}
				var found bool
				found, ok = known[job.functionID]
				if !ok {
					var exists int
					if err := a.db.write.QueryRowContext(ctx,
						"SELECT EXISTS(SELECT 1 FROM functions WHERE id = ?)", job.functionID).Scan(&exists); err != nil {
						return nil, 0, err
					}
					found = exists != 0
					known[job.functionID] = found
					if found {
						a.db.liveFunctions.Store(job.functionID, struct{}{})
					}
				}
				alive = found
			}
		}
		if !alive {
			discarded = append(discarded, job)
			if keep == nil {
				keep = make([]writeJob, 0, len(batch)-1)
				keep = append(keep, batch[:i]...)
			}
			continue
		}
		if keep != nil {
			keep = append(keep, job)
		}
	}
	if keep == nil {
		return batch, 0, nil
	}
	releaseJobs(discarded)
	return keep, len(batch) - len(keep), nil
}

// commitIsolated is the recovery pass: one SAVEPOINT per job so a failure
// rolls back only itself. Deliberately NOT the hot path -- at ~1500 jobs/s
// two extra statements per job to guard against a rare event is the wrong
// trade, so this only runs after a batch has already failed once.
func (a *asyncWriter) commitIsolated(batch []writeJob, kind writeKind) []writeJob {
	ctx, cancel := context.WithTimeout(context.Background(), txBudget)
	defer cancel()

	tx, err := a.db.write.BeginTx(ctx, nil)
	if err != nil {
		return batch
	}
	stmtCtx := ctx

	var retry, resolved []writeJob
	shed := 0
	for _, j := range batch {
		if _, err := tx.ExecContext(stmtCtx, "SAVEPOINT job"); err != nil {
			// The transaction rolled back, including earlier successful jobs.
			_ = tx.Rollback()
			return batch
		}
		_, execErr := tx.ExecContext(stmtCtx, j.sql, j.args...)
		if execErr == nil {
			if _, err := tx.ExecContext(stmtCtx, "RELEASE job"); err != nil {
				_ = tx.Rollback()
				return batch
			}
			resolved = append(resolved, j)
			continue
		}
		// ROLLBACK TO is what makes this work: database/sql cannot see
		// sqlite3_get_autocommit, so the savepoint result is the only signal
		// that the transaction is still usable.
		if _, err := tx.ExecContext(stmtCtx, "ROLLBACK TO job"); err != nil {
			_ = tx.Rollback()
			return batch
		}
		_, _ = tx.ExecContext(stmtCtx, "RELEASE job")

		j.attempts++
		if permanentWriteFailure(execErr) || j.attempts >= maxJobAttempts {
			// Deterministic failure -- an FK violation against a row that no
			// longer exists will fail identically forever. Shed it, loudly,
			// rather than looping on it.
			slog.Warn("shedding permanently failing async write",
				"err", execErr, "attempts", j.attempts, "sql", truncSQL(j.sql))
			shed++
			resolved = append(resolved, j)
			continue
		}
		retry = append(retry, j)
	}

	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return batch
	}
	releaseJobs(resolved)
	if shed > 0 {
		a.shed.Add(uint64(shed))
		// Route to the same counter the priority would have used, so
		// WriterStats keeps meaning what it says: CriticalFailures is
		// "critical work we could not write", DroppedTelemetry is
		// "best-effort work we gave up on".
		if kind == writeCritical {
			a.failed.Add(uint64(shed))
		} else {
			a.recordDropped(kind, uint64(shed))
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
			"critical_queued", len(a.critical), "activity_queued", len(a.activity), "telemetry_queued", len(a.telemetry))
	}
}
