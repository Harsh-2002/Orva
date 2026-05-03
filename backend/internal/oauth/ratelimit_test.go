package oauth

import (
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

func TestDCRClientIP_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest("POST", "/register", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	r.RemoteAddr = "10.0.0.1:54321"
	got := dcrClientIP(r)
	if got != "203.0.113.5" {
		t.Errorf("XFF not honored: got %q want 203.0.113.5", got)
	}
}

func TestDCRClientIP_NoXFFFallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest("POST", "/register", nil)
	r.RemoteAddr = "203.0.113.5:54321"
	got := dcrClientIP(r)
	if got != "203.0.113.5" {
		t.Errorf("RemoteAddr fallback: got %q", got)
	}
}
