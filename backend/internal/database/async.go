package database

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// writeJob is a single INSERT/UPDATE the batched writer will apply.
type writeJob struct {
	sql  string
	args []any
}

// asyncWriter runs a single goroutine that consumes writeJobs from a
// buffered channel and commits them in small transactions. This replaces
// the goroutine-per-call pattern which, at sustained 500+ req/s, churns a
// goroutine and a separate SQLite transaction per invoke.
//
// The writer batches up to batchMax jobs or flushes every flushEvery
// interval — whichever comes first. That gives bounded per-job latency
// while amortizing fsync cost across dozens of rows.
type asyncWriter struct {
	db         *Database
	critical   chan writeJob
	telemetry  chan writeJob
	done       chan struct{}
	batchMax   int
	flushEvery time.Duration
	dropped    atomic.Uint64
	timeouts   atomic.Uint64
	failed     atomic.Uint64
}

func newAsyncWriter(db *Database) *asyncWriter {
	return &asyncWriter{
		db:         db,
		critical:   make(chan writeJob, 1024),
		telemetry:  make(chan writeJob, 1024),
		done:       make(chan struct{}),
		batchMax:   50,
		flushEvery: 50 * time.Millisecond,
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
	select {
	case db.writer.critical <- writeJob{sql: statement, args: args}:
		return nil
	case <-ctx.Done():
		db.writer.timeouts.Add(1)
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
	select {
	case db.writer.telemetry <- writeJob{sql: statement, args: args}:
	default:
		db.writer.dropped.Add(1)
	}
}

type WriterStats struct {
	CriticalDepth    int
	TelemetryDepth   int
	CriticalCap      int
	TelemetryCap     int
	CriticalTimeouts uint64
	CriticalFailures uint64
	DroppedTelemetry uint64
}

func (db *Database) WriterStats() WriterStats {
	if db == nil || db.writer == nil {
		return WriterStats{}
	}
	return WriterStats{
		CriticalDepth: len(db.writer.critical), TelemetryDepth: len(db.writer.telemetry),
		CriticalCap: cap(db.writer.critical), TelemetryCap: cap(db.writer.telemetry),
		CriticalTimeouts: db.writer.timeouts.Load(), CriticalFailures: db.writer.failed.Load(),
		DroppedTelemetry: db.writer.dropped.Load(),
	}
}

// start launches the consumer goroutine. Idempotent — called at most once
// per Database instance.
func (a *asyncWriter) start() {
	a.db.asyncWG.Add(1)
	go func() {
		defer a.db.asyncWG.Done()
		a.run()
	}()
}

// run is the consumer loop. Drains the channel into batched transactions.
func (a *asyncWriter) run() {
	ticker := time.NewTicker(a.flushEvery)
	defer ticker.Stop()
	critical := a.critical
	telemetry := a.telemetry

	criticalBatch := make([]writeJob, 0, a.batchMax)
	telemetryBatch := make([]writeJob, 0, a.batchMax)
	flushCritical := func() {
		if len(criticalBatch) == 0 {
			return
		}
		a.commit(criticalBatch, false)
		criticalBatch = criticalBatch[:0]
	}
	flushTelemetry := func() {
		if len(telemetryBatch) == 0 {
			return
		}
		a.commit(telemetryBatch, true)
		telemetryBatch = telemetryBatch[:0]
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
			flushCritical()
			flushTelemetry()
		}
		if critical == nil && telemetry == nil {
			flushCritical()
			flushTelemetry()
			close(a.done)
			return
		}
	}
}

func (a *asyncWriter) commit(batch []writeJob, telemetry bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := a.db.write.BeginTx(ctx, nil)
	if err != nil {
		a.finish(batch, telemetry, err)
		return
	}
	for _, j := range batch {
		if _, err := tx.Exec(j.sql, j.args...); err != nil {
			slog.Warn("batch stmt failed", "err", err)
			_ = tx.Rollback()
			a.finish(batch, telemetry, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Warn("batch commit failed", "err", err)
		_ = tx.Rollback()
		a.finish(batch, telemetry, err)
		return
	}
	a.finish(batch, telemetry, nil)
}

func (a *asyncWriter) finish(batch []writeJob, telemetry bool, err error) {
	if telemetry && err != nil {
		a.dropped.Add(uint64(len(batch)))
	} else if err != nil {
		a.failed.Add(uint64(len(batch)))
	}
}

// stop closes the job channel and waits for the consumer to drain.
func (a *asyncWriter) stop() {
	close(a.critical)
	close(a.telemetry)
	<-a.done
}
