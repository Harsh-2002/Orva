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
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
