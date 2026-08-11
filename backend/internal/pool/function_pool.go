package pool

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
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
	memoryBytes int64 // per-worker memory.max budget for admission accounting
	cpuUnits    int64 // thousandths of a declared CPU per worker
	scaleToZero bool  // pool_config.scale_to_zero

	// hostMem is the global tracker for admission control. We need a
	// back-reference here (not just on the controller) so every coordinator
	// spawn reserves memory before launch and releases the exact admitted
	// amount when that worker exits.
	hostMem *hostMemTracker

	idle               chan *sandbox.Worker
	busy               atomic.Int64 // workers currently handling a request
	queued             atomic.Int64
	spawning           atomic.Int64
	spawnSlots         chan struct{} // at most four concurrent starts for this pool
	workerReservations sync.Map      // *sandbox.Worker -> workerReservation

	// Autoscaler signal state — guarded by sigMu.
	sigMu            sync.Mutex
	arrivals         []time.Time
	serviceSamples   []time.Duration
	spawnSamples     []time.Duration
	queueWaitSamples []time.Duration
	lastArrival      time.Time
	belowTargetSince time.Time
	limitingReason   string
	memSamples       []int64

	// Lifetime counters for metrics.
	spawned          atomic.Int64
	killed           atomic.Int64
	dynamicMax       atomic.Int64 // last computed memory/cpu/operator cap (published for metrics)
	desired          atomic.Int64
	rejections       atomic.Int64
	capacityTimeouts atomic.Int64
	arrivalsTotal    atomic.Int64

	// mu guards the slow paths (spawn decision, drain). The fast path is
	// channel-only and lock-free.
	mu      sync.Mutex
	closing atomic.Bool
	retired chan struct{} // closed exactly once when this generation is retired
	retire  sync.Once

	// Per-function concurrency cap. concSem is a buffered channel acting
	// as a semaphore: capacity = max_concurrency. nil means unlimited.
	// concPolicy is "queue" (block on the cap) or "reject" (return
	// ErrFunctionBusy). The cap and policy come from the function row;
	// changing them via PUT triggers RefreshForDeploy which recreates the
	// pool with a fresh sem.
	concSem    chan struct{}
	concPolicy string

	spawnFn      func(ctx context.Context) (*sandbox.Worker, error)
	reclaimFn    func() bool
	requestSpawn func()
}

// acquireSlot tries to occupy a concurrency slot. Returns nil on success
// (caller must call releaseSlot when done), ErrFunctionBusy with
// reject policy when the cap is full, or the ctx error if the queue
// wait timed out. Cap = 0 means unlimited (no semaphore configured).
func (p *functionPool) acquireSlot(ctx context.Context) error {
	if p.closing.Load() {
		return errPoolRetired
	}
	if p.concSem == nil {
		return nil
	}
	if p.concPolicy == "reject" {
		select {
		case p.concSem <- struct{}{}:
			if p.closing.Load() {
				p.releaseSlot()
				return errPoolRetired
			}
			return nil
		case <-p.retired:
			return errPoolRetired
		default:
			return ErrFunctionBusy
		}
	}
	// "queue" policy: block until a slot frees or ctx fires.
	select {
	case p.concSem <- struct{}{}:
		if p.closing.Load() {
			p.releaseSlot()
			return errPoolRetired
		}
		return nil
	case <-p.retired:
		return errPoolRetired
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

func (p *functionPool) recordArrival(now time.Time) {
	p.arrivalsTotal.Add(1)
	p.sigMu.Lock()
	p.arrivals = append(p.arrivals, now)
	p.lastArrival = now
	p.pruneArrivalsLocked(now)
	p.sigMu.Unlock()
}

func (p *functionPool) recordLatency(d time.Duration) {
	p.sigMu.Lock()
	p.serviceSamples = appendDurationSample(p.serviceSamples, d, 512)
	p.sigMu.Unlock()
}

func (p *functionPool) recordQueueWait(d time.Duration) {
	p.sigMu.Lock()
	p.queueWaitSamples = appendDurationSample(p.queueWaitSamples, d, 512)
	p.sigMu.Unlock()
}

func (p *functionPool) recordSpawn(d time.Duration) {
	p.sigMu.Lock()
	p.spawnSamples = appendDurationSample(p.spawnSamples, d, 256)
	p.sigMu.Unlock()
}

func appendDurationSample(samples []time.Duration, d time.Duration, limit int) []time.Duration {
	if d < 0 {
		d = 0
	}
	samples = append(samples, d)
	if len(samples) > limit {
		samples = append([]time.Duration(nil), samples[len(samples)-limit:]...)
	}
	return samples
}

func durationP95(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	copyOf := append([]time.Duration(nil), samples...)
	sort.Slice(copyOf, func(i, j int) bool { return copyOf[i] < copyOf[j] })
	idx := (95*len(copyOf)+99)/100 - 1
	if idx < 0 {
		idx = 0
	}
	return copyOf[idx]
}

func (p *functionPool) pruneArrivalsLocked(now time.Time) {
	cutoff := now.Add(-stableWindow)
	i := 0
	for i < len(p.arrivals) && p.arrivals[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		p.arrivals = append([]time.Time(nil), p.arrivals[i:]...)
	}
}

type demandSnapshot struct {
	StableRate                         float64
	BurstRate                          float64
	ServiceP95, SpawnP95, QueueWaitP95 time.Duration
	LastArrival                        time.Time
	MemoryP95                          int64
}

type workerReservation struct {
	memoryBytes int64
	cpuUnits    int64
}

func (p *functionPool) snapshotDemand(now time.Time) demandSnapshot {
	p.sigMu.Lock()
	defer p.sigMu.Unlock()
	p.pruneArrivalsLocked(now)
	burstCutoff := now.Add(-panicWindow)
	burst := 0
	for _, at := range p.arrivals {
		if !at.Before(burstCutoff) {
			burst++
		}
	}
	mem := append([]int64(nil), p.memSamples...)
	sort.Slice(mem, func(i, j int) bool { return mem[i] < mem[j] })
	var memP95 int64
	if len(mem) > 0 {
		memP95 = mem[(95*len(mem)+99)/100-1]
	}
	return demandSnapshot{
		StableRate: float64(len(p.arrivals)) / stableWindow.Seconds(),
		BurstRate:  float64(burst) / panicWindow.Seconds(),
		ServiceP95: durationP95(p.serviceSamples), SpawnP95: durationP95(p.spawnSamples),
		QueueWaitP95: durationP95(p.queueWaitSamples), LastArrival: p.lastArrival,
		MemoryP95: memP95,
	}
}

func (p *functionPool) admissionBytes() int64 {
	d := p.snapshotDemand(time.Now())
	if d.MemoryP95 <= 0 {
		return p.memoryBytes
	}
	bytes := d.MemoryP95
	if bytes < 16<<20 {
		bytes = 16 << 20
	}
	if bytes > p.memoryBytes {
		bytes = p.memoryBytes
	}
	return bytes
}

// acquire returns an idle worker or asks the global coordinator to admit one.
// If the effective cap is reached it waits for an idle worker or cancellation.
func (p *functionPool) acquire(ctx context.Context) (*AcquireResult, error) {
	if p.closing.Load() {
		return nil, errPoolRetired
	}
	// Fast path: non-blocking pop from idle.
	select {
	case w := <-p.idle:
		if p.closing.Load() {
			p.killWorker(w)
			return nil, errPoolRetired
		}
		if p.isUnusable(w) {
			p.killWorker(w)
			return p.acquire(ctx)
		}
		p.busy.Add(1)
		return &AcquireResult{Worker: w, ColdStart: false}, nil
	default:
	}
	// Production pools route every new worker through the manager's global
	// round-robin scheduler. Hand-built unit-test pools retain direct spawn.
	if p.requestSpawn != nil {
		p.requestSpawn()
		return p.waitForIdle(ctx)
	}
	return p.acquireDirect(ctx)
}

func (p *functionPool) acquireDirect(ctx context.Context) (*AcquireResult, error) {
	select {
	case w := <-p.idle:
		if p.closing.Load() {
			p.killWorker(w)
			return nil, errPoolRetired
		} else if p.isUnusable(w) {
			p.killWorker(w)
			// Fall through to spawn below.
		} else {
			p.busy.Add(1)
			return &AcquireResult{Worker: w, ColdStart: false}, nil
		}
	default:
	}

	// Decide whether to spawn. Cap by the controller's effective maximum (CPU /
	// memory / operator-cap min), not just the operator hard cap. Without
	// this, a hand-built test pool could grow past the capacity calculated by
	// the controller. Production pools never use this direct path.
	dyn := int(p.dynamicMax.Load())
	cap := p.max
	if dyn > 0 && dyn < cap {
		cap = dyn
	}
	p.mu.Lock()
	total := int(p.busy.Load()+p.spawning.Load()) + len(p.idle)
	canSpawn := total < cap && !p.closing.Load()
	if canSpawn {
		select {
		case p.spawnSlots <- struct{}{}:
			p.spawning.Add(1)
		default:
			canSpawn = false
		}
	}
	p.mu.Unlock()

	if canSpawn {
		started := time.Now()
		defer func() { <-p.spawnSlots }()
		// Reserve the worker's memory budget *before* the spawn so the host
		// memory accounting reflects the new worker immediately. The
		// autoscaler does the same in scaleUp().
		reservation := workerReservation{memoryBytes: p.admissionBytes(), cpuUnits: p.cpuUnits}
		if p.hostMem != nil {
			if !p.hostMem.reserve(reservation.memoryBytes, reservation.cpuUnits) {
				reclaimed := p.reclaimFn != nil && p.reclaimFn()
				if !reclaimed || !p.hostMem.reserve(reservation.memoryBytes, reservation.cpuUnits) {
					p.spawning.Add(-1)
					return nil, ErrMemoryExhausted
				}
			}
		}
		w, err := p.spawnFn(ctx)
		if err != nil {
			p.spawning.Add(-1)
			if p.hostMem != nil {
				p.hostMem.release(reservation.memoryBytes, reservation.cpuUnits)
			}
			return nil, err
		}
		p.spawning.Add(-1)
		p.busy.Add(1)
		p.recordSpawn(time.Since(started))
		p.spawned.Add(1)
		p.workerReservations.Store(w, reservation)
		// Retirement may have happened while spawnFn was starting the
		// process. Never hand that stale worker to the caller; release its
		// accounting and let Manager.Acquire retry on the new generation.
		p.mu.Lock()
		retired := p.closing.Load()
		p.mu.Unlock()
		if retired {
			p.busy.Add(-1)
			p.killWorker(w)
			return nil, errPoolRetired
		}
		return &AcquireResult{Worker: w, ColdStart: true}, nil
	}

	return p.waitForIdle(ctx)
}

func (p *functionPool) waitForIdle(ctx context.Context) (*AcquireResult, error) {
	select {
	case w := <-p.idle:
		if p.closing.Load() {
			p.killWorker(w)
			return nil, errPoolRetired
		} else if p.isUnusable(w) {
			p.killWorker(w)
			// Recursive retry with same ctx — but avoid unbounded recursion
			// by just attempting to spawn once more if under max now.
			return p.acquire(ctx)
		}
		p.busy.Add(1)
		return &AcquireResult{Worker: w, ColdStart: w.Served.Load() == 0}, nil
	case <-p.retired:
		return nil, errPoolRetired
	case <-ctx.Done():
		p.capacityTimeouts.Add(1)
		return nil, ErrPoolAtCapacity
	}
}

// markRetired closes the generation notification exactly once. Callers hold
// p.mu so no worker can be published between the closing transition and the
// idle drain performed by the manager.
func (p *functionPool) markRetired() {
	p.closing.Store(true)
	p.retire.Do(func() { close(p.retired) })
}

// release returns the worker to the pool unless it errored or is unusable.
// Also kills the worker when the idle channel already holds at least the
// effective host/operator cap, preventing a completed burst from parking
// workers that the controller can no longer admit.
func (p *functionPool) release(w *sandbox.Worker, reqErr error) {
	p.busy.Add(-1)

	// Sample cgroup v2 memory for observed worker-memory p95 admission.
	cgPath := w.GetCgroupPath()
	if cgPath != "" {
		memCur := sandbox.ReadCgroupMemCurrent(cgPath)
		p.sigMu.Lock()
		if memCur > 0 {
			p.memSamples = append(p.memSamples, memCur)
			if len(p.memSamples) > 256 {
				p.memSamples = append([]int64(nil), p.memSamples[len(p.memSamples)-256:]...)
			}
		}
		p.sigMu.Unlock()
	}

	// Serialize the closing check and idle publication with retirement. If
	// release checked closing and then parked lock-free, retirePool could mark
	// the generation closed, drain an empty channel, and have this stale worker
	// arrive immediately afterward.
	p.mu.Lock()
	if reqErr != nil || p.isUnusable(w) || p.closing.Load() {
		p.mu.Unlock()
		p.killWorker(w)
		return
	}

	// Aggressive prune: don't park excess workers above the autoscaler's
	// current cap. dynamicMax==0 only at first boot (autoscaler hasn't
	// computed yet) — fall through to the original cap-only check there.
	dyn := int(p.dynamicMax.Load())
	if dyn > 0 && len(p.idle) >= dyn {
		p.mu.Unlock()
		p.killWorker(w)
		return
	}

	// Non-blocking push to the idle channel. If the channel is full we're
	// shrinking (pool max was reduced, or race) — kill the worker.
	select {
	case p.idle <- w:
		p.mu.Unlock()
	default:
		p.mu.Unlock()
		p.killWorker(w)
	}
}

// isUnusable returns true if the worker should not be reused.
func (p *functionPool) isUnusable(w *sandbox.Worker) bool {
	if w == nil || w.IsDead() {
		return true
	}
	// idleTTL is a pool-level no-demand signal owned by the controller. A
	// worker's age is not idle time and must not silently violate min_warm.
	if w.IsExpired(0, p.maxUses) {
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
	reservation := workerReservation{memoryBytes: p.memoryBytes, cpuUnits: p.cpuUnits}
	if value, ok := p.workerReservations.LoadAndDelete(w); ok {
		reservation = value.(workerReservation)
	}
	if p.hostMem != nil {
		p.hostMem.release(reservation.memoryBytes, reservation.cpuUnits)
	}
}

// sweep walks the idle channel, killing dead/max-use workers and putting live
// ones back. Pool idle expiry and scale-down are controller decisions.
func (p *functionPool) sweep(defaultMaxUses int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing.Load() {
		return
	}
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
	p.mu.Lock()
	p.markRetired()
	p.mu.Unlock()
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
