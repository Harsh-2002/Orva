package oauth

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestDCRRateLimiter_BurstThenDeny(t *testing.T) {
	l := &dcrRateLimiter{buckets: map[string]*dcrBucket{}}
	allowed := 0
	for i := 0; i < 12; i++ {
		if l.allow("198.51.100.7") {
			allowed++
		}
	}
	// dcrCap is the burst — should let 10 through immediately, then deny.
	if allowed != 10 {
		t.Errorf("burst granted = %d, want 10", allowed)
	}
}

func TestDCRRateLimiter_PerIPIsolation(t *testing.T) {
	l := &dcrRateLimiter{buckets: map[string]*dcrBucket{}}
	for i := 0; i < 10; i++ {
		l.allow("198.51.100.7")
	}
	// Different IP should still be allowed (independent bucket).
	if !l.allow("198.51.100.8") {
		t.Error("different IP should not be rate-limited")
	}
}

// TestDCRClientIP_XForwardedFor — the header is honoured ONLY behind a
// declared proxy. It used to be trusted unconditionally, and this limiter is
// the sole abuse control on POST /register (everything else there is
// metadata validation with no authentication), so a caller who varied one
// header had no rate limit at all -- while each distinct value allocated a
// bucket that lived for hours.
func TestDCRClientIP_XForwardedFor(t *testing.T) {
	t.Run("trusted proxy: honoured", func(t *testing.T) {
		prev := TrustForwardedFor
		TrustForwardedFor = true
		t.Cleanup(func() { TrustForwardedFor = prev })

		r := httptest.NewRequest("POST", "/register", nil)
		r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
		r.RemoteAddr = "10.0.0.1:54321"
		if got := dcrClientIP(r); got != "203.0.113.5" {
			t.Errorf("XFF not honored behind a trusted proxy: got %q", got)
		}
	})

	t.Run("untrusted: ignored", func(t *testing.T) {
		prev := TrustForwardedFor
		TrustForwardedFor = false
		t.Cleanup(func() { TrustForwardedFor = prev })

		r := httptest.NewRequest("POST", "/register", nil)
		r.Header.Set("X-Forwarded-For", "203.0.113.5")
		r.RemoteAddr = "198.51.100.9:54321"
		if got := dcrClientIP(r); got != "198.51.100.9" {
			t.Errorf("a client-settable header decided the rate-limit bucket: got %q", got)
		}
	})

	t.Run("rotating the header cannot escape the bucket", func(t *testing.T) {
		prev := TrustForwardedFor
		TrustForwardedFor = false
		t.Cleanup(func() { TrustForwardedFor = prev })

		seen := map[string]bool{}
		for i := 0; i < 50; i++ {
			r := httptest.NewRequest("POST", "/register", nil)
			r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
			r.RemoteAddr = "198.51.100.9:54321"
			seen[dcrClientIP(r)] = true
		}
		if len(seen) != 1 {
			t.Errorf("50 rotated header values produced %d buckets; want 1", len(seen))
		}
	})
}

func TestDCRClientIP_NoXFFFallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest("POST", "/register", nil)
	r.RemoteAddr = "203.0.113.5:54321"
	got := dcrClientIP(r)
	if got != "203.0.113.5" {
		t.Errorf("RemoteAddr fallback: got %q", got)
	}
}
