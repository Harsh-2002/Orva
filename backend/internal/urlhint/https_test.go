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
