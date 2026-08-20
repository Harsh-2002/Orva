package handlers

import (
	"sync"
	"sync/atomic"
	"time"
)

// rateLimiter is a per-(function, client-IP) token bucket. Cap and refill
// rate are derived from the function's rate_limit_per_min: cap = N, refill =
// N / 60s, so a fresh bucket gives you N requests/minute steady-state with
// burst tolerance equal to the limit itself.
//
// Buckets live in a sync.Map keyed by "fnID|ip"; entries are reaped on access
// when their last-touch is older than bucketTTL. There is no background
// goroutine — this keeps the limiter zero-cost for functions that never see
// traffic. The sweep on access bounds memory under sustained load.
//
// That was the documented behaviour for a long time before it was the actual
// behaviour: Allow only ever did LoadOrStore, and lastTouch was written but
// never read for eviction, so the map grew with every unique source address
// and never gave anything back. Two producers feed it -- any function with
// rate_limit_per_min > 0, and the login throttle keyed "login"|ip, which is
// reachable unauthenticated.
type rateLimiter struct {
	buckets sync.Map // string -> *bucket
	// sweeping guards the reap so concurrent requests do not all walk the
	// map at once; whoever wins does the work for everyone.
	sweeping  atomic.Bool
	lastSweep atomic.Int64 // unix nanos
	tracked   atomic.Int64
}

const (
	// bucketTTL is how long an idle bucket survives. A bucket refills fully
	// in 60s, so anything untouched for well past that carries no state
	// worth keeping.
	bucketTTL = 5 * time.Minute
	// sweepEvery bounds how often the reap runs, and sweepThreshold forces
	// one regardless when the map has grown large -- a flood of unique IPs
	// arrives far faster than a timer.
	sweepEvery     = 30 * time.Second
	sweepThreshold = 4096
)

type bucket struct {
	mu        sync.Mutex
	tokens    float64
	cap       float64
	refill    float64 // tokens per second
	lastTouch time.Time
}

func newRateLimiter() *rateLimiter { return &rateLimiter{} }

// Allow consumes one token from the (fnID, ip) bucket. Returns true if the
// request may proceed, false if rate-limited. perMin == 0 short-circuits to
// "always allow" so unconfigured functions stay on the fastest path.
func (rl *rateLimiter) Allow(fnID, ip string, perMin int) bool {
	if perMin <= 0 {
		return true
	}
	key := fnID + "|" + ip
	now := time.Now()
	rl.maybeSweep(now)

	v, loaded := rl.buckets.LoadOrStore(key, &bucket{
		tokens:    float64(perMin),
		cap:       float64(perMin),
		refill:    float64(perMin) / 60.0,
		lastTouch: now,
	})
	if !loaded {
		rl.tracked.Add(1)
	}
	b := v.(*bucket)

	b.mu.Lock()
	defer b.mu.Unlock()

	// If the function's rate_limit was edited, the existing bucket may
	// still be sized for the old value. Resize on access — cheaper than a
	// pubsub from the update handler.
	cap := float64(perMin)
	if b.cap != cap {
		b.cap = cap
		b.refill = cap / 60.0
		if b.tokens > cap {
			b.tokens = cap
		}
	}

	// Refill based on elapsed time since lastTouch.
	elapsed := now.Sub(b.lastTouch).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.refill
		if b.tokens > b.cap {
			b.tokens = b.cap
		}
	}
	b.lastTouch = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

// maybeSweep performs the eviction the type comment promises: drop buckets
// untouched for bucketTTL. Rate-limited by time, and forced early once the
// map is large, because the failure mode being prevented is a flood of
// unique source addresses rather than slow growth.
func (rl *rateLimiter) maybeSweep(now time.Time) {
	last := rl.lastSweep.Load()
	due := last == 0 || now.Sub(time.Unix(0, last)) >= sweepEvery
	if !due && rl.tracked.Load() < sweepThreshold {
		return
	}
	if !rl.sweeping.CompareAndSwap(false, true) {
		return // another request is already sweeping
	}
	defer rl.sweeping.Store(false)
	rl.lastSweep.Store(now.UnixNano())

	cutoff := now.Add(-bucketTTL)
	removed := int64(0)
	rl.buckets.Range(func(k, v any) bool {
		b := v.(*bucket)
		b.mu.Lock()
		idle := b.lastTouch.Before(cutoff)
		b.mu.Unlock()
		if idle {
			rl.buckets.Delete(k)
			removed++
		}
		return true
	})
	if removed > 0 {
		rl.tracked.Add(-removed)
	}
}

// len reports how many buckets are currently tracked. Test-facing.
func (rl *rateLimiter) len() int {
	n := 0
	rl.buckets.Range(func(any, any) bool { n++; return true })
	return n
}
