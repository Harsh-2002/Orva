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
	fnID      string
	min       int
	max       int    // resource-derived channel ceiling, possibly lowered by max_warm
	maxReason string // source of max for limiting_reason telemetry
	idleTTL   time.Duration
	maxUses   int64

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
	arrivals         [60]arrivalBucket
	serviceSamples   durationRing
	spawnSamples     durationRing
	queueWaitSamples durationRing
	lastArrival      time.Time
	belowTargetSince time.Time
	limitingReason   string

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
	// A failed asynchronous spawn must wake current waiters with its real
	// cause (not let a policy/configuration failure masquerade as queue load).
	spawnError   error
	spawnErrorCh chan struct{}

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

// arrivalBucket keeps request-rate accounting constant-space regardless of
// traffic volume. Second-level resolution is sufficient for the 6s burst and
// 60s stable controller windows; admission itself remains event-driven.
type arrivalBucket struct {
	second int64
	count  uint64
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
	second := now.Unix()
	index := second % int64(len(p.arrivals))
	if index < 0 {
		index += int64(len(p.arrivals))
	}
	bucket := &p.arrivals[index]
	if bucket.second != second {
		bucket.second = second
		bucket.count = 0
	}
	bucket.count++
	p.lastArrival = now
	p.sigMu.Unlock()
}

func (p *functionPool) recordLatency(d time.Duration) {
	p.sigMu.Lock()
	p.serviceSamples.add(d, 512)
	p.sigMu.Unlock()
}

func (p *functionPool) recordQueueWait(d time.Duration) {
	p.sigMu.Lock()
	p.queueWaitSamples.add(d, 512)
	p.sigMu.Unlock()
}

func (p *functionPool) recordSpawn(d time.Duration) {
	p.sigMu.Lock()
	p.spawnSamples.add(d, 256)
	p.sigMu.Unlock()
}

// durationRing retains the newest bounded sample window. Once full, recording
// overwrites one slot instead of allocating and copying the entire window on
// every completed invocation. Percentiles do not depend on sample order.
type durationRing struct {
	samples []time.Duration
	next    int
}

func (r *durationRing) add(d time.Duration, limit int) {
	if d < 0 {
		d = 0
	}
	if len(r.samples) < limit {
		r.samples = append(r.samples, d)
		return
	}
	r.samples[r.next] = d
	r.next = (r.next + 1) % limit
}

func durationP95(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	// snapshotDemand owns this copy, so sorting cannot delay recorders.
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	idx := (95*len(samples)+99)/100 - 1
	if idx < 0 {
		idx = 0
	}
	return samples[idx]
}

type demandSnapshot struct {
	StableRate                         float64
	BurstRate                          float64
	ServiceP95, SpawnP95, QueueWaitP95 time.Duration
	LastArrival                        time.Time
}

type workerReservation struct {
	memoryBytes int64
	cpuUnits    int64
}

func (p *functionPool) snapshotDemand(now time.Time) demandSnapshot {
	p.sigMu.Lock()
	second := now.Unix()
	var stable, burst uint64
	for _, bucket := range p.arrivals {
		age := second - bucket.second
		if age >= 0 && age < int64(len(p.arrivals)) {
			stable += bucket.count
			if age < int64(panicWindow/time.Second) {
				burst += bucket.count
			}
		}
	}
	service := append([]time.Duration(nil), p.serviceSamples.samples...)
	spawn := append([]time.Duration(nil), p.spawnSamples.samples...)
	queueWait := append([]time.Duration(nil), p.queueWaitSamples.samples...)
	lastArrival := p.lastArrival
	p.sigMu.Unlock()

	return demandSnapshot{
		StableRate: float64(stable) / stableWindow.Seconds(),
		BurstRate:  float64(burst) / panicWindow.Seconds(),
		ServiceP95: durationP95(service), SpawnP95: durationP95(spawn),
		QueueWaitP95: durationP95(queueWait), LastArrival: lastArrival,
	}
}

func (p *functionPool) admissionBytes() int64 {
	// A recent RSS percentile is not a bound on arbitrary user code. Every
	// worker can grow to memory.max concurrently, so reserve that guarantee.
	return p.memoryBytes
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
	p.mu.Lock()
	spawnErrorCh := p.spawnErrorCh
	p.mu.Unlock()
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
	case <-spawnErrorCh:
		p.mu.Lock()
		spawnError := p.spawnError
		p.mu.Unlock()
		if spawnError != nil {
			return nil, spawnError
		}
		return p.acquire(ctx)
	case <-ctx.Done():
		p.capacityTimeouts.Add(1)
		return nil, ErrPoolAtCapacity
	}
}

func (p *functionPool) notifySpawnError(err error) {
	p.mu.Lock()
	if p.spawnErrorCh == nil {
		p.spawnErrorCh = make(chan struct{})
	}
	if p.spawnError == nil {
		close(p.spawnErrorCh)
	}
	p.spawnError = err
	p.mu.Unlock()
}

// markRetired closes the generation notification exactly once. Callers hold
// p.mu so no worker can be published between the closing transition and the
// idle drain performed by the manager.
func (p *functionPool) markRetired() {
	p.closing.Store(true)
	p.retire.Do(func() { close(p.retired) })
}

// release returns the worker to the pool unless it errored or is unusable.
// Capacity changes are handled by the controller's hysteretic scale-down.
// Killing on every release when dynamicMax briefly falls below the current
// worker count causes a spawn/kill loop under fluctuating memory headroom.
func (p *functionPool) release(w *sandbox.Worker, reqErr error) {
	p.busy.Add(-1)

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

	// Non-blocking push to the idle channel. If the channel is full we're
	// shrinking the configured pool max or racing another release — kill it.
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
