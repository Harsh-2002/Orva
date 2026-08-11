package pool

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
)

const (
	scalerTick                 = 2 * time.Second
	stableWindow               = 60 * time.Second
	panicWindow                = 6 * time.Second
	utilFactor                 = 0.70
	scaleDownStep              = 0.20
	scaleDownGrace             = 30 * time.Second
	maxConcurrentSpawnsPerPool = 4
)

// scaler is the global admission scheduler. It evaluates pools in a rotating
// order so a hot function cannot monopolize every newly available worker
// slot. Each functionPool is its own coordinator and accounts spawning before
// launch, making repeated ticks and request-path cold starts single-flight.
type scaler struct {
	pm      *Manager
	tick    time.Duration
	stop    chan struct{}
	runDone chan struct{}
	wake    chan struct{}
	hostMem *hostMemTracker
	spawnWG sync.WaitGroup

	mu     sync.Mutex
	cursor int
}

func newScaler(pm *Manager, hm *hostMemTracker) *scaler {
	return &scaler{pm: pm, hostMem: hm, tick: scalerTick, stop: make(chan struct{}), runDone: make(chan struct{}), wake: make(chan struct{}, 1)}
}

func (s *scaler) run() {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	defer close(s.runDone)
	slog.Info("pool controller v2 started", "tick", s.tick, "stable_window", stableWindow, "burst_window", panicWindow)
	for {
		select {
		case <-t.C:
			s.evaluateAll()
		case <-s.wake:
			s.evaluateAll()
		case <-s.stop:
			slog.Info("pool controller v2 stopped")
			return
		}
	}
}

func (s *scaler) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *scaler) shutdown() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.runDone
	s.spawnWG.Wait()
}

func (s *scaler) evaluateAll() {
	pools := make([]*functionPool, 0)
	s.pm.pools.Range(func(_, value any) bool {
		p := value.(*functionPool)
		if !p.closing.Load() {
			pools = append(pools, p)
		}
		return true
	})
	if len(pools) == 0 {
		return
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].fnID < pools[j].fnID })
	s.mu.Lock()
	start := s.cursor % len(pools)
	s.cursor = (start + 1) % len(pools)
	s.mu.Unlock()
	for i := range pools {
		s.evaluate(pools[(start+i)%len(pools)], time.Now())
	}
}

func (s *scaler) evaluate(p *functionPool, now time.Time) {
	desired, reason := s.computeDesiredAt(p, now)
	current := int(p.busy.Load()+p.spawning.Load()) + len(p.idle)
	capacityLimited := reason == "memory_capacity" || reason == "cpu_capacity"
	if p.queued.Load() > 0 && capacityLimited && desired <= current && s.reclaimBorrowedIdle(p) {
		// Recompute after returning the donor's reservation. This queued pool
		// gets the newly available slot in the same rotating evaluation pass.
		desired, reason = s.computeDesiredAt(p, now)
	}
	p.desired.Store(int64(desired))
	p.sigMu.Lock()
	p.limitingReason = reason
	p.sigMu.Unlock()

	if desired > current {
		p.sigMu.Lock()
		p.belowTargetSince = time.Time{}
		p.sigMu.Unlock()
		s.scaleUp(p, desired-current, reason)
		return
	}
	if desired >= current {
		p.sigMu.Lock()
		p.belowTargetSince = time.Time{}
		p.sigMu.Unlock()
		return
	}

	p.sigMu.Lock()
	if p.belowTargetSince.IsZero() {
		p.belowTargetSince = now
	}
	belowSince := p.belowTargetSince
	p.sigMu.Unlock()
	if now.Sub(belowSince) < scaleDownGrace {
		return
	}
	maxShrink := int(math.Ceil(float64(current) * scaleDownStep))
	if maxShrink < 1 {
		maxShrink = 1
	}
	shrink := current - desired
	if shrink > maxShrink {
		shrink = maxShrink
	}
	s.scaleDown(p, shrink, reason)
}

// computeDesiredAt implements the controller's documented signals. Workers
// are single-request processes, so Little's Law gives required concurrency as
// arrival-rate × wall time. Dividing by 70% leaves deliberate burst headroom.
func (s *scaler) computeDesiredAt(p *functionPool, now time.Time) (int, string) {
	d := p.snapshotDemand(now)
	serviceSeconds := d.ServiceP95.Seconds()
	spawnSeconds := d.SpawnP95.Seconds()
	stable := int(math.Ceil(d.StableRate * serviceSeconds / utilFactor))
	burst := int(math.Ceil(d.BurstRate * (serviceSeconds + spawnSeconds) / utilFactor))
	pressure := int(math.Ceil(float64(p.busy.Load()+p.queued.Load()) / utilFactor))

	desired := stable
	reason := "stable_rate"
	if burst > desired {
		desired, reason = burst, "burst_rate"
	}
	if pressure > desired {
		desired, reason = pressure, "immediate_pressure"
	}
	minWarm := p.min
	if !p.scaleToZero && minWarm < 1 {
		minWarm = 1
	}
	if desired < minWarm {
		desired, reason = minWarm, "configured_min"
	}
	if p.scaleToZero && desired == 0 {
		fullyIdle := p.busy.Load() == 0 && p.queued.Load() == 0
		idleLongEnough := d.LastArrival.IsZero() || now.Sub(d.LastArrival) >= p.idleTTL
		if !fullyIdle || !idleLongEnough {
			desired, reason = 1, "idle_ttl"
		} else {
			reason = "scale_to_zero"
		}
	}

	cap, capReason := s.dynamicMax(p, d.MemoryP95)
	p.dynamicMax.Store(int64(cap))
	if desired > cap {
		desired, reason = cap, capReason
	}
	if desired < 0 {
		desired = 0
	}
	return desired, reason
}

func (s *scaler) computeDesired(p *functionPool) (int, string) {
	return s.computeDesiredAt(p, time.Now())
}

func (s *scaler) dynamicMax(p *functionPool, observedMemoryP95 int64) (int, string) {
	opCap := p.max
	if opCap < 1 {
		opCap = 1
	}
	cpuUnits := p.cpuUnits
	if cpuUnits < 1 {
		cpuUnits = 1000
	}
	current := int64(p.busy.Load()+p.spawning.Load()) + int64(len(p.idle))
	cpuCap := int((s.hostMem.availableCPUUnits() + current*cpuUnits) / cpuUnits)
	if cpuCap < 1 {
		cpuCap = 1
	}
	effectiveCap, reason := opCap, "operator_max"
	if p.concSem != nil && cap(p.concSem) < effectiveCap {
		effectiveCap, reason = cap(p.concSem), "function_concurrency"
	}
	if cpuCap < effectiveCap {
		effectiveCap, reason = cpuCap, "cpu_capacity"
	}
	workerBytes := p.memoryBytes
	if observedMemoryP95 > 0 && observedMemoryP95 < workerBytes {
		workerBytes = observedMemoryP95
	}
	if workerBytes > 0 {
		fit := int((s.hostMem.availableForWorkers() + current*workerBytes) / workerBytes)
		if fit < effectiveCap {
			effectiveCap, reason = fit, "memory_capacity"
		}
	}
	if effectiveCap < 0 {
		effectiveCap = 0
	}
	return effectiveCap, reason
}

func (s *scaler) scaleUp(p *functionPool, want int, reason string) {
	if want > maxConcurrentSpawnsPerPool {
		want = maxConcurrentSpawnsPerPool
	}
	for i := 0; i < want; i++ {
		if !s.startSpawn(p, reason) {
			return
		}
	}
}

func (s *scaler) startSpawn(p *functionPool, reason string) bool {
	p.mu.Lock()
	cap := int(p.dynamicMax.Load())
	if cap > p.max {
		cap = p.max
	}
	if cap < 0 {
		cap = 0
	}
	total := int(p.busy.Load()+p.spawning.Load()) + len(p.idle)
	if p.closing.Load() || total >= cap {
		p.mu.Unlock()
		return false
	}
	select {
	case p.spawnSlots <- struct{}{}:
		p.spawning.Add(1) // publish before launch: repeated ticks see it
	default:
		p.mu.Unlock()
		return false
	}
	p.mu.Unlock()

	reservation := workerReservation{memoryBytes: p.admissionBytes(), cpuUnits: p.cpuUnits}
	if !s.hostMem.reserve(reservation.memoryBytes, reservation.cpuUnits) {
		if !s.reclaimBorrowedIdle(p) || !s.hostMem.reserve(reservation.memoryBytes, reservation.cpuUnits) {
			p.spawning.Add(-1)
			<-p.spawnSlots
			p.sigMu.Lock()
			p.limitingReason = "memory_capacity"
			p.sigMu.Unlock()
			return false
		}
	}
	s.spawnWG.Add(1)
	go func() {
		defer s.spawnWG.Done()
		defer func() { <-p.spawnSlots }()
		started := time.Now()
		w, err := p.spawnFn(context.Background())
		p.spawning.Add(-1)
		if err != nil {
			s.hostMem.release(reservation.memoryBytes, reservation.cpuUnits)
			p.sigMu.Lock()
			p.limitingReason = "spawn_error"
			p.sigMu.Unlock()
			slog.Warn("pool worker spawn failed", "fn", p.fnID, "err", err)
			return
		}
		p.recordSpawn(time.Since(started))
		p.spawned.Add(1)
		p.workerReservations.Store(w, reservation)
		p.mu.Lock()
		parked := false
		if !p.closing.Load() {
			select {
			case p.idle <- w:
				parked = true
			default:
			}
		}
		p.mu.Unlock()
		if !parked {
			p.killWorker(w)
			return
		}
		slog.Debug("pool scaled up", "fn", p.fnID, "reason", reason)
	}()
	return true
}

// reclaimBorrowedIdle frees one worker above a pool's configured active
// minimum, choosing the largest borrower first. Busy workers are never
// touched and configured minimums are never crossed.
func (s *scaler) reclaimBorrowedIdle(requester *functionPool) bool {
	var donor *functionPool
	bestBorrowed := 0
	s.pm.pools.Range(func(_, value any) bool {
		p := value.(*functionPool)
		if p == requester || p.closing.Load() {
			return true
		}
		current := int(p.busy.Load()+p.spawning.Load()) + len(p.idle)
		borrowed := current - p.min
		if borrowed > len(p.idle) {
			borrowed = len(p.idle)
		}
		if borrowed > bestBorrowed {
			donor, bestBorrowed = p, borrowed
		}
		return true
	})
	if donor == nil {
		return false
	}
	select {
	case w := <-donor.idle:
		donor.killWorker(w)
		return true
	default:
		return false
	}
}

func (s *scaler) scaleDown(p *functionPool, want int, reason string) {
	killed := 0
	for i := 0; i < want; i++ {
		select {
		case w := <-p.idle:
			p.killWorker(w)
			killed++
		default:
			i = want
		}
	}
	if killed > 0 {
		slog.Debug("pool scaled down", "fn", p.fnID, "killed", killed, "reason", reason)
	}
}
