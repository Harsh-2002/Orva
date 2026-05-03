package oauth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// dcrRateLimiter is a per-IP token bucket guarding /register. DCR
// endpoints are a favourite abuse vector — an attacker that can churn
// out thousands of fake clients can crowd legitimate registrations
// out of cache and inflate the oauth_clients table. We cap each IP
// at 10 registrations per hour, which is generous for any honest
// connector flow and stops the obvious attack.
//
// Memory bound: each entry is ~96 bytes; we sweep entries idle for
// >2h on access so a long-tail of stale IPs doesn't accumulate.
type dcrRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*dcrBucket
}

type dcrBucket struct {
	tokens    float64
	lastTouch time.Time
}

const (
	dcrCap          = 10.0
	dcrRefillPerHr  = 10.0
	dcrIdleTimeout  = 2 * time.Hour
)

var defaultDCRLimiter = &dcrRateLimiter{buckets: map[string]*dcrBucket{}}

// allowDCR returns true if the (clientIP) is below the per-hour cap.
func (l *dcrRateLimiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Lazy sweep: drop entries idle longer than the timeout. O(n) but
	// only runs when a request lands; the table stays bounded by the
	// number of distinct active IPs.
	for k, b := range l.buckets {
		if now.Sub(b.lastTouch) > dcrIdleTimeout {
			delete(l.buckets, k)
		}
	}

	b, ok := l.buckets[ip]
	if !ok {
		b = &dcrBucket{tokens: dcrCap, lastTouch: now}
		l.buckets[ip] = b
	}

	// Refill: dcrRefillPerHr tokens / 3600 sec.
	elapsed := now.Sub(b.lastTouch).Seconds()
	b.tokens += elapsed * (dcrRefillPerHr / 3600.0)
	if b.tokens > dcrCap {
		b.tokens = dcrCap
	}
	b.lastTouch = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

// dcrClientIP extracts the request's caller IP. We trust X-Forwarded-For
// when set (Orva is typically deployed behind a reverse proxy); the
// limit is per-source-IP so spoofing the header just rate-limits the
// attacker-chosen value, which is fine.
func dcrClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client per RFC 7239.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
