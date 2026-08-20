package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFunctionResponseGuards — function output shares the dashboard's
// origin, and the session cookie is Path=/ with SameSite=Lax. A public
// function (auth_mode defaults to "none") that reflects its input would
// otherwise be same-origin XSS able to drive /api/v1/* with the operator's
// cookie.
func TestFunctionResponseGuards(t *testing.T) {
	rec := httptest.NewRecorder()
	applyFunctionResponseGuards(rec)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy on function output")
	}
	for _, want := range []string{
		"default-src 'none'", "frame-ancestors 'none'", "base-uri 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Error("CSP permits inline script, which is the whole attack")
	}
}

// TestHopByHopResponseHeadersAreNotRelayed — the adapter copies every
// header off its fetch Response. A handler that proxies an upstream returns
// content-encoding: gzip and the compressed content-length alongside a body
// the adapter already decoded; relaying either truncates the response.
func TestHopByHopResponseHeadersAreNotRelayed(t *testing.T) {
	relayed := []string{"Content-Type", "Cache-Control", "X-Custom", "ETag"}
	dropped := []string{
		"Content-Length", "Content-Encoding", "Transfer-Encoding",
		"Connection", "Keep-Alive", "Upgrade", "Trailer", "TE",
	}
	for _, k := range relayed {
		if hopByHopResponseHeader(k) {
			t.Errorf("%q must be relayed to the client", k)
		}
	}
	for _, k := range dropped {
		if !hopByHopResponseHeader(k) {
			t.Errorf("%q describes the adapter's transfer and must not be relayed", k)
		}
		if !hopByHopResponseHeader(strings.ToLower(k)) {
			t.Errorf("%q must be matched case-insensitively", k)
		}
	}
}
