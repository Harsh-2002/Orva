// Package builder's Queue moves synchronous `npm install` / `pip install`
// off the deploy HTTP path. Deploy endpoints submit a BuildJob and return
// 202 + deployment_id immediately; the queue's worker goroutines run the
// actual build in the background, streaming logs and the terminal status
// into the deployments + build_logs tables.
package builder

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/registry"
)

// ErrQueueFull is returned from Submit when the build channel is at
// capacity. The proxy maps this to 503 BUILD_QUEUE_FULL with a
// `Retry-After` hint based on current depth × avg build duration.
var ErrQueueFull = errors.New("build queue full")

// ErrQueueStopping is returned from Submit during shutdown.
var ErrQueueStopping = errors.New("build queue stopping")

// BuildJob describes a single deploy to run through the queue.
type BuildJob struct {
	DeploymentID string
	FunctionID   string
	TarballPath  string
	SubmittedAt  time.Time

	// Optional callback fired after the build terminates (success or fail).
	// Pool manager uses this to tear down stale workers and prewarm fresh ones.
	OnComplete func(fnID string, success bool)
}

// Queue runs build jobs in the background with bounded concurrency.
type Queue struct {
	bld      *Builder
	db       *database.Database
	reg      *registry.Registry
	jobs     chan BuildJob
	workers  int
	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
	stopping atomic.Bool

	// FnLock returns a per-function mutex shared with the rollback handler
	// so deploy and rollback can't interleave on the same function. Wired
	// in server.New from pool.Manager.FunctionLock. Optional — when nil
	// (e.g. unit tests) the queue runs unserialized.
	FnLock func(fnID string) *sync.Mutex

	// PublishEvent is fired on every deployment status / phase transition
	// so the SSE event hub can fan the change out to live UI clients.
	// The callback is best-effort: it must be cheap and non-blocking. nil
	// is fine — when unwired the queue still runs but UI clients fall
	// back to their initial GET (no live deploy progress).
	PublishEvent func(eventType string, data any)
}

// NewQueue constructs a Queue with NumCPU workers; `onComplete` is optional.
func NewQueue(bld *Builder, db *database.Database, reg *registry.Registry) *Queue {
	return &Queue{
		bld:     bld,
		db:      db,
		reg:     reg,
		jobs:    make(chan BuildJob, 64),
		workers: runtime.NumCPU(),
		stop:    make(chan struct{}),
	}
}

// Start spawns the worker goroutines. Safe to call once.
func (q *Queue) Start() {
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
	slog.Info("build queue started", "workers", q.workers)
}

// QueuedDepth returns the current number of pending jobs. Cheap (channel
// length read); safe to call on every metrics scrape.
func (q *Queue) QueuedDepth() int {
	if q == nil {
		return 0
	}
	return len(q.jobs)
}

// Workers returns the configured worker count.
func (q *Queue) Workers() int {
	if q == nil {
		return 0
	}
	return q.workers
}

// Submit enqueues a job. Returns ErrQueueFull when the channel is at
// capacity (caller should respond 503 + Retry-After) or ErrQueueStopping
// during shutdown.
func (q *Queue) Submit(job BuildJob) error {
	if q.stopping.Load() {
		return ErrQueueStopping
	}
	select {
	case q.jobs <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// Shutdown drains in-flight jobs (up to ctx deadline) then exits.
func (q *Queue) Shutdown(ctx context.Context) {
	q.stopping.Store(true)
	q.stopOnce.Do(func() { close(q.stop) })

	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("build queue shutdown deadline exceeded; workers still running")
	}
}

func (q *Queue) worker(id int) {
	defer q.wg.Done()
	for {
		select {
		case job := <-q.jobs:
			q.runJob(id, job)
		case <-q.stop:
			return
		}
	}
}

// runJob is the actual build: update deployment state, run the build,
// capture logs, update function row, fire OnComplete.
func (q *Queue) runJob(workerID int, job BuildJob) {
	started := time.Now()
	logger := slog.With("worker", workerID, "deployment", job.DeploymentID, "fn", job.FunctionID)
	logger.Info("build starting")

	// Serialize against rollback on the same fn so the symlink retarget
	// in ActivateVersion can never race a parallel rollback's retarget.
	if q.FnLock != nil {
		lk := q.FnLock(job.FunctionID)
		lk.Lock()
		defer lk.Unlock()
	}

	fn, err := q.reg.Get(job.FunctionID)
	if err != nil {
		q.fail(job, "load_function", fmt.Sprintf("function lookup failed: %v", err), started)
		return
	}

	// Move deployment → building.
	_ = q.db.UpdateDeploymentPhase(job.DeploymentID, "extract")
	// Previous version persists; only flip the status if there's no prior
	// active code (first deploy).
	if fn.Status == "created" || fn.Status == "error" {
		fn.Status = "building"
		_ = q.reg.SetSilent(fn)
	}

	lw := newLogWriter(q.db, job.DeploymentID)
	q.bld.Logger = lw
	defer func() { q.bld.Logger = nil }()

	_ = q.db.UpdateDeploymentPhase(job.DeploymentID, "deps")
	result, buildErr := q.bld.Build(context.Background(), fn, job.TarballPath)

	if buildErr != nil {
		logger.Warn("build failed", "err", buildErr)
		_ = lw.append("stderr", buildErr.Error())
		q.fail(job, "build", buildErr.Error(), started)
		// If there was no previous active build, flip status to error;
		// otherwise leave the previous active code serving traffic.
		if fn.Status == "building" {
			fn.Status = "error"
			_ = q.reg.SetSilent(fn)
		}
		return
	}

	fn.Image = result.ImageTag
	fn.ImageSize = result.ImageSize
	fn.CodeHash = result.CodeHash
	// For TypeScript deploys the builder rewrites the entrypoint from
	// the user's `handler.ts` to the compiled `<outDir>/<stem>.js`. We
	// persist this back onto the function row so the pool's buildEnv
	// can publish ORVA_ENTRYPOINT to the sandbox at spawn time. For
	// non-TS deploys result.Entrypoint == fn.Entrypoint, so this is a
	// no-op assignment.
	if result.Entrypoint != "" {
		fn.Entrypoint = result.Entrypoint
	}
	fn.Status = "active"
	fn.Version++
	// Silent: deployment.succeeded covers this transition for webhook
	// subscribers; the dashboard already sees the deployment event too.
	if err := q.reg.SetSilent(fn); err != nil {
		q.fail(job, "persist", err.Error(), started)
		return
	}

	// Stamp the resolved code_hash on the deployment row so the UI can
	// surface it and the rollback handler can target this version by
	// deployment_id later.
	_ = q.db.SetDeploymentCodeHash(job.DeploymentID, result.CodeHash)

	// Snapshot the function's full mutable state at the moment this build
	// succeeded — env vars, memory/cpu/timeout, network mode, auth mode,
	// rate limit, concurrency. Rollback restores all of this so reverting
	// a version reverts the entire shape of the function, not just the
	// code. Best-effort; a write failure here does not invalidate the
	// successful deploy (the rollback would gracefully degrade to "code
	// only" for this row).
	_ = q.db.SetDeploymentSnapshot(job.DeploymentID, database.SnapshotFromFunction(fn))

	// Atomically retarget `current` symlink → versions/<hash>. Failure here
	// means the new version is on disk but not active; mark the deployment
	// failed and leave the prior `current` in place.
	if err := ActivateVersion(q.bld.DataDir, fn.ID, result.CodeHash); err != nil {
		logger.Warn("activate failed", "err", err)
		_ = lw.append("stderr", "activate: "+err.Error())
		q.fail(job, "activate", err.Error(), started)
		return
	}

	dur := time.Since(started).Milliseconds()
	_ = q.db.FinishDeployment(job.DeploymentID, "succeeded", "", dur)
	logger.Info("build succeeded", "duration_ms", dur)

	q.publishStatusChange(job.DeploymentID, job.FunctionID, "succeeded", "done")

	if job.OnComplete != nil {
		job.OnComplete(job.FunctionID, true)
	}
}

func (q *Queue) fail(job BuildJob, phase, msg string, started time.Time) {
	dur := time.Since(started).Milliseconds()
	_ = q.db.UpdateDeploymentPhase(job.DeploymentID, phase)
	_ = q.db.FinishDeployment(job.DeploymentID, "failed", msg, dur)
	q.publishStatusChange(job.DeploymentID, job.FunctionID, "failed", phase)
	if job.OnComplete != nil {
		job.OnComplete(job.FunctionID, false)
	}
}

// publishStatusChange fires a deployment-event to the SSE hub. Best-effort:
// nil PublishEvent (or a slow subscriber dropping the message) doesn't
// block the build pipeline.
func (q *Queue) publishStatusChange(depID, fnID, status, phase string) {
	if q.PublishEvent == nil {
		return
	}
	q.PublishEvent("deployment", map[string]any{
		"deployment_id": depID,
		"function_id":   fnID,
		"status":        status,
		"phase":         phase,
	})
}

// Logger is the interface Builder writes to for real-time build logs.
// Today Builder uses slog; we add a narrow hook so builds targeted at a
// specific deployment capture their stdout/stderr into build_logs.
type Logger interface {
	Append(stream, line string)
}

// logWriter batches lines into the build_logs table with monotonic seq.
type logWriter struct {
	db  *database.Database
	dep string
	seq atomic.Int64
}

func newLogWriter(db *database.Database, deploymentID string) *logWriter {
	return &logWriter{db: db, dep: deploymentID}
}

// append writes a single line. Caller supplies the stream label.
func (l *logWriter) append(stream, line string) error {
	seq := l.seq.Add(1)
	return l.db.AppendBuildLog(l.dep, seq, stream, line)
}

// Append implements Logger — used by Builder when streaming subprocess output.
func (l *logWriter) Append(stream, line string) {
	_ = l.append(stream, line)
}

// StreamIntoLogs copies each line of `r` into build_logs with the given
// stream label. Returns on EOF or error. Caller closes r.
func StreamIntoLogs(db *database.Database, deploymentID, stream string, r io.Reader) error {
	lw := newLogWriter(db, deploymentID)
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 1024), 1<<20)
	for s.Scan() {
		if err := lw.append(stream, s.Text()); err != nil {
			return err
		}
	}
	return s.Err()
}
