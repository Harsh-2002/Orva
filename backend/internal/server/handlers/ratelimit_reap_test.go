package handlers

import (
	"fmt"
	"testing"
	"time"
)

// TestRateLimiterReapsIdleBuckets — the type comment promised eviction for a
// long time before the code did any. Allow only ever LoadOrStore'd and
// lastTouch was never read for eviction, so the map grew with every unique
// source address and never gave anything back. The login throttle is keyed
// "login"|ip and is reachable unauthenticated, so an internet-facing
// instance grew RSS monotonically.
func TestRateLimiterReapsIdleBuckets(t *testing.T) {
	rl := newRateLimiter()

	// Simulate a flood of distinct source addresses.
	const distinct = 6000
	for i := 0; i < distinct; i++ {
		rl.Allow("login", fmt.Sprintf("203.0.113.%d", i), 60)
	}
	if got := rl.len(); got == 0 {
		t.Fatal("fixture created no buckets")
	}

	// Age every bucket past the TTL, then take one more request, which is
	// what triggers the sweep.
	rl.buckets.Range(func(_, v any) bool {
		b := v.(*bucket)
		b.mu.Lock()
		b.lastTouch = time.Now().Add(-2 * bucketTTL)
		b.mu.Unlock()
		return true
	})
	rl.lastSweep.Store(0)
	rl.Allow("login", "198.51.100.1", 60)

	// Only the bucket for the request that triggered the sweep should remain.
	if got := rl.len(); got > 2 {
		t.Errorf("%d idle buckets survived the sweep (started with %d); "+
			"memory grows without bound per unique source address", got, distinct)
	}
}

// TestRateLimiterKeepsActiveBuckets — the sweep must not evict a bucket that
// is still throttling someone, or the limit stops applying.
func TestRateLimiterKeepsActiveBuckets(t *testing.T) {
	rl := newRateLimiter()

	// Exhaust one bucket so it is actively denying.
	const perMin = 3
	for i := 0; i < perMin; i++ {
		if !rl.Allow("fn-a", "203.0.113.7", perMin) {
			t.Fatalf("request %d denied while tokens remained", i)
		}
	}
	if rl.Allow("fn-a", "203.0.113.7", perMin) {
		t.Fatal("bucket did not deny once its tokens were spent")
	}

	// Force a sweep. The active bucket was just touched, so it must survive.
	rl.lastSweep.Store(0)
	rl.maybeSweep(time.Now())

	if rl.Allow("fn-a", "203.0.113.7", perMin) {
		t.Error("the sweep evicted an actively-throttling bucket, resetting its tokens")
	}
}

// TestRateLimiterStillLimits — a regression guard that the reap work did not
// break the actual limiting.
func TestRateLimiterStillLimits(t *testing.T) {
	rl := newRateLimiter()
	allowed := 0
	for i := 0; i < 20; i++ {
		if rl.Allow("fn-b", "203.0.113.9", 5) {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("allowed %d of 20 with perMin=5, want 5", allowed)
	}
}
