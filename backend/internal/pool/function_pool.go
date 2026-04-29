package pool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/internal/sandbox"
)

// functionPool holds the idle workers and live counters for one function.
// All methods are safe for concurrent use.
type functionPool struct {
	fnID    string
	min     int
	max     int // operator hard cap (from pool_config.max_warm)
	idleTTL time.Duration
	maxUses int64

	// Autoscaler inputs — set at creation time, read by scaler evaluate().
	target       int  // target_concurrency per worker (pool_config, default 10)
	memoryBytes  int64 // per-worker memory.max budget for admission accounting
	scaleToZero  bool  // pool_config.scale_to_zero

	// hostMem is the global tracker for admission control. We need a
	// back-reference here (not just on the autoscaler) so the request-path
	// lazy spawn in acquire() can reserve memory at the same time it
	// promotes the worker. Without this, mem_reserved underreports for the
	// first ~2s of a hot pool until the autoscaler's tick catches up.
	hostMem *hostMemTracker

	idle chan *sandbox.Worker
	busy atomic.Int64 // workers currently handling a request

	// Autoscaler signal state — guarded by sigMu.
	sigMu            sync.Mutex
	rateEWMA         float64 // req/s, α=0.2 on tick-sec buckets
	latencyEWMAms    float64 // dispatch duration, α=0.2
	memUsedEWMA      float64 // memory.current at release (bytes), α=0.2
	cpuFracEWMA      float64 // CPU fraction per invocation (0–1), α=0.2
	inflightSamples  []int64 // ring of recent busy counts (len = stableWindow/tick)
	inflightHead     int
	bucketCount      int64
	belowTargetTicks int // # of consecutive ticks below target (for scale-down gating)

	// Lifetime counters for metrics.
	spawned     atomic.Int64
	killed      atomic.Int64
	scaleUps    atomic.Int64
	scaleDowns  atomic.Int64
	dynamicMax  atomic.Int64 // last computed memory/cpu/operator cap (published for metrics)

	// mu guards the slow paths (spawn decision, drain). The fast path is
	// channel-only and lock-free.
	mu      sync.Mutex
	closing atomic.Bool

	// Per-function concurrency cap. concSem is a buffered channel acting
	// as a semaphore: capacity = max_concurrency. nil means unlimited.
	// concPolicy is "queue" (block on the cap) or "reject" (return
	// ErrFunctionBusy). The cap and policy come from the function row;
	// changing them via PUT triggers RefreshForDeploy which recreates the
	// pool with a fresh sem.
	concSem    chan struct{}
	concPolicy string

	spawnFn func(ctx context.Context) (*sandbox.Worker, error)
}

// acquireSlot tries to occupy a concurrency slot. Returns nil on success
// (caller must call releaseSlot when done), ErrFunctionBusy with
// reject policy when the cap is full, or the ctx error if the queue
// wait timed out. Cap = 0 means unlimited (no semaphore configured).
func (p *functionPool) acquireSlot(ctx context.Context) error {
	if p.concSem == nil {
		return nil
	}
	if p.concPolicy == "reject" {
		select {
		case p.concSem <- struct{}{}:
			return nil
		default:
			return ErrFunctionBusy
		}
	}
	// "queue" policy: block until a slot frees or ctx fires.
	select {
	case p.concSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *functionPool) releaseSlot() {
	if p.concSem == nil {
		return
	}
	select {
	case <-p.concSem:
	default:
	}
}

// recordAcquire bumps the rate EWMA. Called from Manager.Acquire on every
// successful worker handout. Cheap atomic-ish bookkeeping — the heavy
// EWMA decay happens in tick() under sigMu.
func (p *functionPool) recordAcquire() {
	p.sigMu.Lock()
	p.bucketCount++
	p.sigMu.Unlock()
}

// recordLatency feeds a dispatch duration into the latency EWMA.
func (p *functionPool) recordLatency(d time.Duration) {
	ms := float64(d.Milliseconds())
	p.sigMu.Lock()
	if p.latencyEWMAms == 0 {
		p.latencyEWMAms = ms
	} else {
		p.latencyEWMAms = 0.2*ms + 0.8*p.latencyEWMAms
	}
	p.sigMu.Unlock()
}

// tick rolls the 1-second bucket: folds this second's count into the EWMA
// and snapshots current inflight into the sliding window. Called by the
// autoscaler on its scaler tick (2s). First-call safe.
func (p *functionPool) tick(bucketSec float64) {
	p.sigMu.Lock()
	defer p.sigMu.Unlock()

	// Rate EWMA: α = 0.2 over bucketSec-sec observations.
	rate := float64(p.bucketCount) / bucketSec
	if p.rateEWMA == 0 {
		p.rateEWMA = rate
	} else {
		p.rateEWMA = 0.2*rate + 0.8*p.rateEWMA
	}
	p.bucketCount = 0

	// Ring buffer of inflight samples.
	if len(p.inflightSamples) == 0 {
		return // not initialised yet
	}
	p.inflightSamples[p.inflightHead] = p.busy.Load()
	p.inflightHead = (p.inflightHead + 1) % len(p.inflightSamples)
}

// windowMean returns the mean of the last `n` samples in the ring; used for
// the stable and panic windows. n is clamped to the ring size.
func (p *functionPool) windowMean(n int) float64 {
	p.sigMu.Lock()
	defer p.sigMu.Unlock()
	if len(p.inflightSamples) == 0 {
		return 0
	}
	if n > len(p.inflightSamples) {
		n = len(p.inflightSamples)
	}
	var sum int64
	// Walk backward from the current head — most recent n samples.
	for i := 0; i < n; i++ {
		idx := (p.inflightHead - 1 - i + len(p.inflightSamples)) % len(p.inflightSamples)
		sum += p.inflightSamples[idx]
	}
	return float64(sum) / float64(n)
}

// snapshotSignals returns a point-in-time view for logging/metrics.
func (p *functionPool) snapshotSignals() (rate, latMs float64) {
	p.sigMu.Lock()
	defer p.sigMu.Unlock()
	return p.rateEWMA, p.latencyEWMAms
}

// snapshotResourceUsage returns the per-invocation resource EWMA values.
// memBytes is 0 and cpuFrac is 0 until at least one invocation completes
// with cgroup v2 delegation enabled.
func (p *functionPool) snapshotResourceUsage() (memBytes int64, cpuFrac float64) {
	p.sigMu.Lock()
	defer p.sigMu.Unlock()
	return int64(p.memUsedEWMA), p.cpuFracEWMA
}

// stampAcquire records the wall time and cumulative CPU usage on the worker
// just before it is handed to a caller. The pool reads these back at release
// to compute per-invocation resource EWMA metrics.
func stampAcquire(w *sandbox.Worker) {
	w.AcquireAt = time.Now()
	w.AcquireUsec = sandbox.ReadCgroupCPUUsec(w.GetCgroupPath())
}

// acquire returns an idle worker or spawns a new one up to max. If at max
// it blocks on the idle channel or ctx cancellation.
func (p *functionPool) acquire(ctx context.Context) (*AcquireResult, error) {
	// Fast path: non-blocking pop from idle.
	select {
	case w := <-p.idle:
		if p.isUnusable(w) {
			p.killWorker(w)
			// Fall through to spawn below.
		} else {
			p.busy.Add(1)
			stampAcquire(w)
			return &AcquireResult{Worker: w, ColdStart: false}, nil
		}
	default:
	}

	// Decide whether to spawn. Cap by the autoscaler's dynamic_max (CPU /
	// memory / operator-cap min), not just the operator hard cap. Without
	// this, a burst can blow past what the host can comfortably hold —
	// e.g. on a 2-CPU box the autoscaler picks dynamic_max=16 but lazy
	// spawn would happily grow to operator p.max (default 50), leaving
	// dozens of idle workers that take 60-70 s for the scale-down loop
	// to clean up. Capping here turns excess load into fast 503s
	// (POOL_AT_CAPACITY) which is the correct backpressure signal.
	dyn := int(p.dynamicMax.Load())
	cap := p.max
	if dyn > 0 && dyn < cap {
		cap = dyn
	}
	p.mu.Lock()
	total := int(p.busy.Load()) + len(p.idle)
	canSpawn := total < cap && !p.closing.Load()
	if canSpawn {
		p.busy.Add(1)
	}
	p.mu.Unlock()

	if canSpawn {
		// Reserve the worker's memory budget *before* the spawn so the host
		// memory accounting reflects the new worker immediately. The
		// autoscaler does the same in scaleUp(); without it here, lazy
		// growth from cold traffic showed mem_reserved=0 for ~2s.
		if p.memoryBytes > 0 && p.hostMem != nil {
			if !p.hostMem.reserve(p.memoryBytes) {
				p.busy.Add(-1)
				return nil, ErrMemoryExhausted
			}
		}
		w, err := p.spawnFn(ctx)
		if err != nil {
			p.busy.Add(-1)
			if p.memoryBytes > 0 && p.hostMem != nil {
				p.hostMem.release(p.memoryBytes)
			}
			return nil, err
		}
		p.spawned.Add(1)
		// Count lazy growth as a scale-up event too. Operators watching the
		// autoscaler metric want "how often did this pool grow" — which
		// includes both the predictive scaler and request-path expansion.
		p.scaleUps.Add(1)
		stampAcquire(w)
		return &AcquireResult{Worker: w, ColdStart: true}, nil
	}

	// At max — block until someone releases or ctx fires. ctx-fire here
	// specifically means "the pool didn't free up in time" — surface as
	// ErrPoolAtCapacity rather than the generic ctx err so the proxy can
	// distinguish it from per-fn TimeoutMS expiry (which fires on a
	// derived ctx and surfaces as ErrTimeout from Worker.Dispatch).
	select {
	case w := <-p.idle:
		if p.isUnusable(w) {
			p.killWorker(w)
			// Recursive retry with same ctx — but avoid unbounded recursion
			// by just attempting to spawn once more if under max now.
			return p.acquire(ctx)
		}
		p.busy.Add(1)
		stampAcquire(w)
		return &AcquireResult{Worker: w, ColdStart: false}, nil
	case <-ctx.Done():
		return nil, ErrPoolAtCapacity
	}
}

// release returns the worker to the pool unless it errored or is unusable.
// Also kills the worker when the idle channel already holds at least
// `dynamicMax` workers — this prunes excess capacity from a prior burst as
// soon as their busy work finishes, instead of waiting 60-70 s for the
// autoscaler's scale-down loop to converge. Without this, a 30 s c=200
// burst left 60+ idle workers parked on a 2-CPU host and made the UI
// feel sluggish for over a minute afterward.
func (p *functionPool) release(w *sandbox.Worker, reqErr error) {
	p.busy.Add(-1)

	// Sample cgroup v2 resource usage for per-function EWMA metrics.
	cgPath := w.GetCgroupPath()
	if cgPath != "" && !w.AcquireAt.IsZero() {
		memCur := sandbox.ReadCgroupMemCurrent(cgPath)
		cpuNow := sandbox.ReadCgroupCPUUsec(cgPath)
		elapsedUsec := time.Since(w.AcquireAt).Microseconds()
		p.sigMu.Lock()
		if memCur > 0 {
			if p.memUsedEWMA == 0 {
				p.memUsedEWMA = float64(memCur)
			} else {
				p.memUsedEWMA = 0.2*float64(memCur) + 0.8*p.memUsedEWMA
			}
		}
		if cpuNow > w.AcquireUsec && elapsedUsec > 0 {
			frac := float64(cpuNow-w.AcquireUsec) / float64(elapsedUsec)
			if p.cpuFracEWMA == 0 {
				p.cpuFracEWMA = frac
			} else {
				p.cpuFracEWMA = 0.2*frac + 0.8*p.cpuFracEWMA
			}
		}
		p.sigMu.Unlock()
	}

	if reqErr != nil || p.isUnusable(w) || p.closing.Load() {
		p.killWorker(w)
		return
	}

	// Aggressive prune: don't park excess workers above the autoscaler's
	// current cap. dynamicMax==0 only at first boot (autoscaler hasn't
	// computed yet) — fall through to the original cap-only check there.
	dyn := int(p.dynamicMax.Load())
	if dyn > 0 && len(p.idle) >= dyn {
		p.killWorker(w)
		return
	}

	// Non-blocking push to the idle channel. If the channel is full we're
	// shrinking (pool max was reduced, or race) — kill the worker.
	select {
	case p.idle <- w:
	default:
		p.killWorker(w)
	}
}

// isUnusable returns true if the worker should not be reused.
func (p *functionPool) isUnusable(w *sandbox.Worker) bool {
	if w == nil || w.IsDead() {
		return true
	}
	if w.IsExpired(p.idleTTL, p.maxUses) {
		return true
	}
	return false
}

// killWorker terminates the worker and releases its memory reservation.
// Every spawn path goes through reserve(); every termination goes through
// here, so the budget stays balanced.
func (p *functionPool) killWorker(w *sandbox.Worker) {
	if w == nil {
		return
	}
	_ = w.Kill()
	p.killed.Add(1)
	if p.memoryBytes > 0 && p.hostMem != nil {
		p.hostMem.release(p.memoryBytes)
	}
}

// sweep walks the idle channel, killing expired workers and putting live
// ones back. Called periodically by the manager's reaper.
func (p *functionPool) sweep(defaultMaxUses int64) {
	n := len(p.idle)
	for i := 0; i < n; i++ {
		select {
		case w := <-p.idle:
			if p.isUnusable(w) {
				p.killWorker(w)
				continue
			}
			// Pull-and-push rotation keeps the channel FIFO-ish and gives
			// every worker a chance to age out even under heavy traffic.
			select {
			case p.idle <- w:
			default:
				p.killWorker(w)
			}
		default:
			return
		}
	}
}

// drain is called at shutdown to terminate every idle worker in parallel.
func (p *functionPool) drain(grace time.Duration) {
	p.closing.Store(true)
	var wg sync.WaitGroup
	for {
		select {
		case w := <-p.idle:
			wg.Add(1)
			go func(w *sandbox.Worker) {
				defer wg.Done()
				_ = w.Quit(grace)
				p.killed.Add(1)
			}(w)
		default:
			wg.Wait()
			return
		}
	}
}
