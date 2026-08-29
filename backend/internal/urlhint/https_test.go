package urlhint

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

// X-Forwarded-Proto is a list, not a value. One proxy sends "https"; chained
// ones append, so Cloudflare in front of nginx sends "https, http" -- the
// client spoke https to the first hop and the last hop spoke plaintext to
// Orva. Comparing the whole header against "https" reads that as plaintext,
// which cost such an instance both the Secure flag on its session cookie and
// an https:// OAuth issuer URL.
func TestIsHTTPSReadsTheFirstForwardedProto(t *testing.T) {
	cases := []struct {
		name string
		tls  bool
		xfp  string
		want bool
	}{
		{"no header, no tls", false, "", false},
		{"direct tls", true, "", true},
		{"single proxy", false, "https", true},
		{"single proxy, plaintext", false, "http", false},
		{"chained proxies", false, "https, http", true},
		{"chained, no space", false, "https,http", true},
		{"chained, three hops", false, "https, http, http", true},
		{"chained from plaintext", false, "http, http", false},
		{"odd casing", false, "HTTPS", true},
		{"padded", false, "  https  ", true},
		{"tls wins over a header claiming plaintext", true, "http", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://orva.example/x", nil)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				r.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			if got := IsHTTPS(r); got != tc.want {
				t.Errorf("IsHTTPS(X-Forwarded-Proto=%q, tls=%v) = %v, want %v", tc.xfp, tc.tls, got, tc.want)
			}
		})
	}
}

// The base URL every OAuth and MCP consumer must agree on is derived from the
// same answer, so it inherits the fix rather than carrying its own copy.
func TestBaseURLFollowsChainedProxies(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://orva.example/x", nil)
	r.Header.Set("X-Forwarded-Proto", "https, http")
	if got := BaseURL(r); got != "https://orva.example" {
		t.Errorf("BaseURL = %q, want https://orva.example: an OAuth issuer advertised as http:// over a TLS connection is rejected downstream", got)
	}
}

func TestIsHTTPSHandlesANilRequest(t *testing.T) {
	if IsHTTPS(nil) {
		t.Error("IsHTTPS(nil) = true, want false")
	}
}

func withTrust(t *testing.T, v bool) {
	prev := TrustProxyHeaders
	TrustProxyHeaders = v
	t.Cleanup(func() { TrustProxyHeaders = prev })
}

// The header decided the rate-limit bucket unconditionally, so one header per
// request defeated every limiter.
func TestClientIP_UntrustedHeadersIgnored(t *testing.T) {
	withTrust(t, false)
	for _, h := range []string{"X-Forwarded-For", "X-Real-IP"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "198.51.100.9:443"
		r.Header.Set(h, "203.0.113.5")
		if got := ClientIP(r); got != "198.51.100.9" {
			t.Errorf("%s decided the bucket while untrusted: got %q", h, got)
		}
	}
}

// nginx, Caddy and Traefik all append the peer they saw, so everything left of
// the last entry came from the client and is forgeable even behind a proxy.
func TestClientIP_TrustedTakesRightmost(t *testing.T) {
	withTrust(t, true)
	cases := map[string]string{
		"10.9.9.9, 203.0.113.7":        "203.0.113.7",
		"203.0.113.7":                  "203.0.113.7",
		" 10.9.9.9 , 203.0.113.7":      "203.0.113.7",
		"10.9.9.9, [2001:db8::1]:9999": "2001:db8::1",
	}
	for xff, want := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.1:443"
		r.Header.Set("X-Forwarded-For", xff)
		if got := ClientIP(r); got != want {
			t.Errorf("ClientIP(%q) = %q, want %q", xff, got, want)
		}
	}
}

// A junk header must not become its own bucket, and must not shadow the peer.
func TestClientIP_TrustedFallsBackOnGarbage(t *testing.T) {
	withTrust(t, true)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.9:443"
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := ClientIP(r); got != "198.51.100.9" {
		t.Errorf("garbage header: got %q, want the peer address", got)
	}
}

// Header.Get returns only the FIRST X-Forwarded-For line. HAProxy's
// `option forwardfor` and several ingress controllers append a second line
// rather than merging, so Get takes the rightmost entry of the CLIENT's list.
func TestClientIP_TrustedReadsEveryForwardedForLine(t *testing.T) {
	withTrust(t, true)
	seen := map[string]bool{}
	for _, forged := range []string{"6.6.6.1", "6.6.6.2", "6.6.6.3"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.1:443"
		r.Header.Add("X-Forwarded-For", forged)
		r.Header.Add("X-Forwarded-For", "203.0.113.7")
		seen[ClientIP(r)] = true
	}
	if len(seen) != 1 || !seen["203.0.113.7"] {
		t.Errorf("a second X-Forwarded-For line let the client pick the bucket: %v", seen)
	}
}
