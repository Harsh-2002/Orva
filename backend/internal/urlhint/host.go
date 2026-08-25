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
	"net/http"
	"strings"
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
// The header is a list, not a value. A single proxy sends
// "X-Forwarded-Proto: https", but chained ones append -- Cloudflare in
// front of nginx sends "https, http", meaning the client spoke https to
// the first hop. Comparing the whole header against "https" reads that
// as plaintext, which is how an instance behind two proxies served
// http:// OAuth issuer URLs over a TLS connection and dropped Secure
// from its session cookie. The first entry is the client's scheme.
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
