package oauth

import (
	"net/http"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/urlhint"
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
	dcrCap         = 10.0
	dcrRefillPerHr = 10.0
	dcrIdleTimeout = 2 * time.Hour
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

// dcrClientIP is the DCR limiter's bucket key. It used to read the leftmost
// X-Forwarded-For entry unconditionally, which gave a caller who varied one
// header no rate limit at all on the only abuse control POST /register has.
// urlhint.ClientIP is now the one place that decision lives.
func dcrClientIP(r *http.Request) string { return urlhint.ClientIP(r) }
