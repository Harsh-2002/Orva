// Package urlhint resolves the canonical "scheme://host" base URL of
// an Orva instance from an inbound HTTP request.
//
// We can't hard-code a base URL because operators run Orva on
// localhost during dev, behind reverse proxies in staging, and on
// custom domains in prod. Several places need this same answer:
//
//   - OAuth issuer / endpoint URLs (RFC 8414 metadata)
//   - MCP `invoke_url` field on list_functions / get_function
//   - WWW-Authenticate `resource_metadata` URLs
//   - Audience binding for OAuth tokens (RFC 8707)
//
// All of them must agree on the same value or some validation will
// reject things downstream. Centralising the inference here keeps the
// answer consistent.
package urlhint

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
)

// BaseURL infers the canonical "scheme://host" identifier for this
// Orva instance from an inbound HTTP request. We trust X-Forwarded-Proto
// when present (typical reverse-proxy setup) and r.TLS for direct TLS
// termination. Otherwise fall back to "http", which is correct for
// localhost loopback in tests and dev.
func BaseURL(r *http.Request) string {
	scheme := "http"
	if IsHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// IsHTTPS reports whether the request reached Orva over TLS, either
// directly or through a proxy that said so.
//
// The header is a list, not a value. One proxy sends
// "X-Forwarded-Proto: https"; a chain that appends rather than replaces
// sends "https, http" -- HAProxy's `add-header`, Envoy and several
// ingress controllers do this, as does any nginx configured
// "$http_x_forwarded_proto, $scheme". (Plain nginx replaces, so the
// common Cloudflare-in-front case yields a single value either way.)
// Comparing the whole header against "https" reads an appended list as
// plaintext, which costs such an instance the Secure flag on its session
// cookie and gets it http:// OAuth issuer URLs over a TLS connection.
//
// Per RFC 7239 the leftmost element is the hop closest to the client, so
// the first entry is the scheme the client actually spoke. The last is
// the proxy-to-Orva hop, which is the plaintext one.
//
// Only ever upgrades: a client that sets the header itself can claim
// https it does not have, which costs it its own session, and can never
// use it to strip Secure from someone else's.
func IsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// TrustProxyHeaders is set once at startup from ORVA_TRUSTED_PROXY. Off by
// default: X-Forwarded-For is client-settable, so trusting it unconditionally
// hands every rate limiter a bucket key the caller picks per request.
var TrustProxyHeaders bool

var untrustedProxyHeaderOnce sync.Once

// ClientIP returns the address rate limiters bucket on. Without the operator's
// opt-in that is the TCP peer, which cannot be forged over a completed
// handshake. With it, the RIGHTMOST X-Forwarded-For entry: nginx, Caddy and
// Traefik append the peer they saw, so every entry further left was supplied
// by the client.
func ClientIP(r *http.Request) string {
	// Values, not Get: HAProxy's `option forwardfor` and several ingress
	// controllers append a second X-Forwarded-For line instead of merging.
	xff := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	xri := ""
	if v := r.Header.Values("X-Real-IP"); len(v) > 0 {
		xri = v[len(v)-1]
	}
	if TrustProxyHeaders {
		last := xff
		if i := strings.LastIndexByte(xff, ','); i >= 0 {
			last = xff[i+1:]
		}
		if ip := normalizeIP(last); ip != "" {
			return ip
		}
		if ip := normalizeIP(xri); ip != "" {
			return ip
		}
	} else if xff != "" || xri != "" {
		untrustedProxyHeaderOnce.Do(func() {
			slog.Warn("ignoring proxy forwarding headers; rate limits are keyed on the peer address and all proxied clients share one bucket",
				"hint", "set ORVA_TRUSTED_PROXY=true only if a proxy you control rewrites X-Forwarded-For",
				"peer", r.RemoteAddr)
		})
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip := normalizeIP(host); ip != "" {
			return ip
		}
		return host
	}
	return r.RemoteAddr
}

// normalizeIP canonicalises an address so " 1.2.3.4 ", "1.2.3.4" and
// "1.2.3.4:9999" cannot each claim their own rate-limit bucket.
func normalizeIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	return ""
}
