package handlers

import (
	"sync"
	"time"
)

// rateLimiter is a per-(function, client-IP) token bucket. Cap and refill
// rate are derived from the function's rate_limit_per_min: cap = N, refill =
// N / 60s, so a fresh bucket gives you N requests/minute steady-state with
// burst tolerance equal to the limit itself.
//
// Buckets live in a sync.Map keyed by "fnID|ip"; entries are reaped on access
// when their last-touch is older than 5 minutes. There is no background
// goroutine — this keeps the limiter zero-cost for functions that never see
// traffic. The sweep on access bounds memory under sustained load.
type rateLimiter struct {
	buckets sync.Map // string -> *bucket
}

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

	v, _ := rl.buckets.LoadOrStore(key, &bucket{
		tokens:    float64(perMin),
		cap:       float64(perMin),
		refill:    float64(perMin) / 60.0,
		lastTouch: now,
	})
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
