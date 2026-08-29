package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/urlhint"
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

// TestRateLimitBucketIgnoresForgedForwardedFor — clientIP read X-Forwarded-For
// unconditionally, so any caller could mint a fresh bucket per request and had
// no per-function rate limit and no login throttle at all. The header now only
// decides the bucket when the operator declared a proxy.
func TestRateLimitBucketIgnoresForgedForwardedFor(t *testing.T) {
	req := func(xff string) *http.Request {
		r := httptest.NewRequest("POST", "/fn/demo", nil)
		r.RemoteAddr = "198.51.100.9:54321"
		r.Header.Set("X-Forwarded-For", xff)
		return r
	}
	setTrust := func(t *testing.T, v bool) {
		prev := urlhint.TrustProxyHeaders
		urlhint.TrustProxyHeaders = v
		t.Cleanup(func() { urlhint.TrustProxyHeaders = prev })
	}

	t.Run("opt-in off: rotating the header cannot mint buckets", func(t *testing.T) {
		setTrust(t, false)
		rl := newRateLimiter()
		allowed := 0
		for i := 0; i < 12; i++ {
			if rl.Allow("fn-demo", clientIP(req(fmt.Sprintf("203.0.113.%d", i))), 5) {
				allowed++
			}
		}
		if allowed != 5 {
			t.Errorf("12 requests with 12 forged X-Forwarded-For values got %d through a limit of 5", allowed)
		}
	})

	t.Run("opt-in on: the entry the proxy appended wins", func(t *testing.T) {
		setTrust(t, true)
		// The client forged 10.9.9.9; the proxy appended the peer it saw.
		if got := clientIP(req("10.9.9.9, 203.0.113.7")); got != "203.0.113.7" {
			t.Fatalf("clientIP = %q, want the proxy-appended 203.0.113.7", got)
		}
		rl := newRateLimiter()
		for i := 0; i < 5; i++ {
			rl.Allow("fn-demo", clientIP(req("10.9.9.9, 203.0.113.7")), 5)
		}
		if rl.Allow("fn-demo", clientIP(req("203.0.113.7, 203.0.113.7")), 5) {
			t.Error("a client that varies its own X-Forwarded-For prefix escaped its bucket")
		}
		if !rl.Allow("fn-demo", clientIP(req("10.9.9.9, 203.0.113.8")), 5) {
			t.Error("a different client behind the proxy shares one bucket")
		}
	})
}
