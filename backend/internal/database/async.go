package database

import (
	"context"
	"log/slog"
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
	ch         chan writeJob
	done       chan struct{}
	batchMax   int
	flushEvery time.Duration
}

func newAsyncWriter(db *Database) *asyncWriter {
	return &asyncWriter{
		db:         db,
		ch:         make(chan writeJob, 1024),
		done:       make(chan struct{}),
		batchMax:   50,
		flushEvery: 50 * time.Millisecond,
	}
}

// AsyncExec queues a write for batched execution. Never blocks the caller
// unless the channel is full — in that case falls back to a direct Async
// goroutine so the hot path is never stalled. At sustained saturation the
// fallback signals to the operator that batchMax / buffer should grow.
func (db *Database) AsyncExec(sql string, args ...any) {
	if db.writer == nil {
		// Writer not initialized (Migrate not yet run, or legacy path).
		db.Async(func() {
			if _, err := db.write.Exec(sql, args...); err != nil {
				slog.Warn("direct async write failed", "err", err)
			}
		})
		return
	}
	select {
	case db.writer.ch <- writeJob{sql: sql, args: args}:
	default:
		// Channel full — use the old fire-and-forget goroutine so we don't
		// backpressure the request handler.
		db.Async(func() {
			if _, err := db.write.Exec(sql, args...); err != nil {
				slog.Warn("async writer overflow direct write failed", "err", err)
			}
		})
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

	batch := make([]writeJob, 0, a.batchMax)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		a.commit(batch)
		batch = batch[:0]
	}

	for {
		select {
		case job, ok := <-a.ch:
			if !ok {
				flush()
				close(a.done)
				return
			}
			batch = append(batch, job)
			// Opportunistically drain anything else already queued so we
			// commit one transaction per ~50 jobs instead of one per job.
			// `break` inside a `select` only breaks the select — use a
			// labelled outer for so we can break the loop when the channel
			// is empty.
		drain:
			for len(batch) < a.batchMax {
				select {
				case j2, ok2 := <-a.ch:
					if !ok2 {
						flush()
						close(a.done)
						return
					}
					batch = append(batch, j2)
				default:
					break drain
				}
			}
			if len(batch) >= a.batchMax {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (a *asyncWriter) commit(batch []writeJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := a.db.write.BeginTx(ctx, nil)
	if err != nil {
		slog.Warn("batch tx begin failed; falling back to per-stmt", "err", err, "n", len(batch))
		for _, j := range batch {
			if _, e := a.db.write.Exec(j.sql, j.args...); e != nil {
				slog.Warn("batch fallback stmt failed", "err", e)
			}
		}
		return
	}
	for _, j := range batch {
		if _, err := tx.Exec(j.sql, j.args...); err != nil {
			slog.Warn("batch stmt failed", "err", err)
			// Keep going — one bad stmt shouldn't abort the whole batch.
		}
	}
	if err := tx.Commit(); err != nil {
		slog.Warn("batch commit failed", "err", err)
		_ = tx.Rollback()
	}
}

// stop closes the job channel and waits for the consumer to drain.
func (a *asyncWriter) stop() {
	close(a.ch)
	<-a.done
}
