package pool

import (
	"context"
	"log/slog"
	"math"
	"runtime"
	"time"
)

// Autoscaler parameters — these match Knative KPA defaults and are tuned
// for ~100s of per-function pools on a single host.
const (
	scalerTick      = 2 * time.Second
	stableWindow    = 60 * time.Second
	panicWindow     = 6 * time.Second
	panicThreshold  = 2.0
	utilFactor      = 0.7
	scaleDownStep   = 0.5  // at most 50% shrink per tick — release path also prunes,
	//                          so this is just the catch-up rate for stragglers
	scaleDownGrace  = 1    // 1 tick (~20s) below target before scale-down. Was 3 ticks
	//                          (60 s) which left burst-spawned idle workers parked
	//                          long enough to noticeably slow down the UI on small hosts.
)

// scaler owns the control loop; one per Manager.
type scaler struct {
	pm      *Manager
	tick    time.Duration
	stop    chan struct{}
	hostMem *hostMemTracker
	// samplesPerWindow is the ring-buffer depth for the stable window.
	samplesPerWindow int
	panicSamples     int
}

func newScaler(pm *Manager, hm *hostMemTracker) *scaler {
	return &scaler{
		pm:               pm,
		hostMem:          hm,
		tick:             scalerTick,
		stop:             make(chan struct{}),
		samplesPerWindow: int(stableWindow / scalerTick), // 30
		panicSamples:     int(panicWindow / scalerTick),  //  3
	}
}

func (s *scaler) run() {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	slog.Info("autoscaler started",
		"tick", s.tick, "stable_window", stableWindow, "panic_window", panicWindow)

	for {
		select {
		case <-t.C:
			s.evaluateAll()
		case <-s.stop:
			slog.Info("autoscaler stopped")
			return
		}
	}
}

func (s *scaler) shutdown() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

// evaluateAll ticks every pool and runs the scale decision.
func (s *scaler) evaluateAll() {
	bucketSec := s.tick.Seconds()
	s.pm.pools.Range(func(k, v any) bool {
		p := v.(*functionPool)
		if p.closing.Load() {
			return true
		}
		// Roll the 1-sec bucket forward and snapshot inflight.
		p.tick(bucketSec)
		s.evaluate(p)
		return true
	})
}

// evaluate is the per-pool decision. Called from the scaler goroutine only.
func (s *scaler) evaluate(p *functionPool) {
	desired, reason := s.computeDesired(p)
	current := int(p.busy.Load()) + len(p.idle)

	switch {
	case desired > current:
		want := desired - current
		go s.scaleUp(p, want, reason)
	case desired < current:
		// Rate-limit shrink: at most 20% per tick AND require stable window
		// to have been below target for `scaleDownGrace` consecutive ticks.
		maxShrink := int(math.Ceil(float64(current) * scaleDownStep))
		if maxShrink < 1 {
			maxShrink = 1
		}
		shrink := current - desired
		if shrink > maxShrink {
			shrink = maxShrink
		}
		// Only scale down when the cooldown has elapsed.
		if p.belowTargetTicks < scaleDownGrace {
			return
		}
		s.scaleDown(p, shrink, reason)
	}
}

// computeDesired returns the worker count the pool "should" have based on
// Knative two-window smoothing + Little's-Law floor + operator/memory caps.
// Also updates p.belowTargetTicks for scale-down gating.
func (s *scaler) computeDesired(p *functionPool) (int, string) {
	// Samples.
	stableConc := p.windowMean(s.samplesPerWindow)
	panicConc := p.windowMean(s.panicSamples)

	target := float64(p.target)
	if target <= 0 {
		target = 10
	}
	perWorker := target * utilFactor

	desiredStable := int(math.Ceil(stableConc / perWorker))
	desiredPanic := int(math.Ceil(panicConc / perWorker))

	// Panic mode: ratio = recent concurrency / current workers.
	cur := int(p.busy.Load()) + len(p.idle)
	if cur < 1 {
		cur = 1
	}
	ratio := panicConc / float64(cur)
	inPanic := ratio >= panicThreshold

	desired := desiredStable
	reason := "stable"
	if inPanic && desiredPanic > desired {
		desired = desiredPanic
		reason = "panic"
	}

	// Little's-Law floor: if request rate and latency are known, ensure we
	// have enough workers to absorb steady-state demand.
	rate, latMs := p.snapshotSignals()
	if rate > 0 && latMs > 0 {
		w := latMs / 1000.0
		ll := int(math.Ceil((rate * w) / utilFactor))
		if ll > desired {
			desired = ll
			reason = "littles-law"
		}
	}

	// min / max / memory / cpu caps.
	minWarm := p.min
	if minWarm < 0 {
		minWarm = 0
	}
	if !p.scaleToZero && minWarm < 1 {
		minWarm = 1
	}
	if desired < minWarm {
		desired = minWarm
	}

	dyn := s.dynamicMax(p)
	p.dynamicMax.Store(int64(dyn))
	if desired > dyn {
		desired = dyn
	}
	if desired < 0 {
		desired = 0
	}

	// Update the below-target counter for scale-down gating. Uses stable
	// window only (panicConc is deliberately volatile).
	if stableConc < target*utilFactor {
		p.belowTargetTicks++
	} else {
		p.belowTargetTicks = 0
	}

	return desired, reason
}

// dynamicMax bounds the pool by operator cap, memory, and CPU headroom.
// It adds back the function's own current reserved workers so we're asking
// "how large could this pool be", not "how much free memory is left after
// counting ourselves".
func (s *scaler) dynamicMax(p *functionPool) int {
	opCap := p.max
	if opCap <= 0 {
		opCap = 5
	}
	cpuCap := runtime.NumCPU() * 8
	if opCap < cpuCap {
		cpuCap = opCap
	}

	if s.hostMem == nil || p.memoryBytes <= 0 {
		return cpuCap
	}
	// Headroom = currently-available-for-workers + my own current reservation.
	cur := int64(p.busy.Load()) + int64(len(p.idle))
	myReserved := cur * p.memoryBytes
	avail := s.hostMem.availableForWorkers() + myReserved
	memFit := int(avail / p.memoryBytes)
	if memFit < 0 {
		memFit = 0
	}
	capped := cpuCap
	if memFit < capped {
		capped = memFit
	}
	return capped
}

// scaleUp spawns up to `want` new workers, bounded by hostMem reservation.
// Runs in its own goroutine because spawn is slow (nsjail fork + VM boot).
func (s *scaler) scaleUp(p *functionPool, want int, reason string) {
	if want <= 0 {
		return
	}
	spawned := 0
	for i := 0; i < want; i++ {
		if p.closing.Load() {
			break
		}
		// Memory admission: skip if budget is gone.
		if p.memoryBytes > 0 && s.hostMem != nil {
			if !s.hostMem.reserve(p.memoryBytes) {
				break
			}
		}
		// Bookkeeping: bump busy so we reflect "worker exists" during spawn;
		// it'll flip to idle when we push to the channel.
		w, err := p.spawnFn(context.Background())
		if err != nil {
			if p.memoryBytes > 0 && s.hostMem != nil {
				s.hostMem.release(p.memoryBytes)
			}
			slog.Warn("autoscaler spawn failed", "fn", p.fnID, "err", err)
			break
		}
		p.spawned.Add(1)
		// Push to idle non-blockingly; if the channel is full we're racing
		// another scale-up — kill the excess to keep the invariant clean.
		select {
		case p.idle <- w:
			spawned++
		default:
			_ = w.Kill()
			if p.memoryBytes > 0 && s.hostMem != nil {
				s.hostMem.release(p.memoryBytes)
			}
		}
	}
	if spawned > 0 {
		p.scaleUps.Add(int64(spawned))
		slog.Debug("pool scaled up", "fn", p.fnID, "spawned", spawned, "reason", reason)
	}
}

// scaleDown kills up to `want` idle workers (oldest first in the FIFO sense
// of the channel, meaning just pop them off and don't re-insert). Never
// touches busy workers. Releases memory reservations.
func (s *scaler) scaleDown(p *functionPool, want int, reason string) {
	if want <= 0 {
		return
	}
	killed := 0
	for i := 0; i < want; i++ {
		select {
		case w := <-p.idle:
			_ = w.Quit(200 * time.Millisecond)
			if p.memoryBytes > 0 && s.hostMem != nil {
				s.hostMem.release(p.memoryBytes)
			}
			p.killed.Add(1)
			killed++
		default:
			// No idle workers to kill right now; stop.
			i = want
		}
	}
	if killed > 0 {
		p.scaleDowns.Add(int64(killed))
		slog.Debug("pool scaled down", "fn", p.fnID, "killed", killed, "reason", reason)
	}
}

// ensureSignals lazily initialises the per-pool signal ring once we know
// how long the stable window is. Called at pool-creation time.
func (s *scaler) ensureSignals(p *functionPool) {
	p.sigMu.Lock()
	defer p.sigMu.Unlock()
	if p.inflightSamples == nil {
		p.inflightSamples = make([]int64, s.samplesPerWindow)
	}
}

