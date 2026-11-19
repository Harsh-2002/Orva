package proxy

import (
	"net/http"
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

	// The control is the opaque origin. allow-same-origin would hand the
	// document back the dashboard's origin and undo the whole thing --
	// worse, combined with allow-scripts it lets a page remove its own
	// sandbox.
	if !strings.Contains(csp, "sandbox ") {
		t.Errorf("CSP does not sandbox the document: %s", csp)
	}
	if strings.Contains(csp, "allow-same-origin") {
		t.Errorf("CSP grants allow-same-origin, defeating the isolation: %s", csp)
	}
	for _, want := range []string{"frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
}

// TestFunctionResponseGuardsDoNotBreakInteractivePages — the first version
// of this policy used default-src 'none' with a bare `sandbox`, which made
// every HTML function inert: no script, no form submission. Orva ships a
// guestbook template that does both, so that policy broke a shipped
// showcase. Isolation must come from the opaque origin, not from refusing
// to run the page.
func TestFunctionResponseGuardsDoNotBreakInteractivePages(t *testing.T) {
	rec := httptest.NewRecorder()
	applyFunctionResponseGuards(rec)
	csp := rec.Header().Get("Content-Security-Policy")

	for _, need := range []string{"allow-scripts", "allow-forms"} {
		if !strings.Contains(csp, need) {
			t.Errorf("CSP withholds %s, which breaks the shipped guestbook "+
				"template (inline script + form POST): %s", need, csp)
		}
	}
	if strings.Contains(csp, "default-src 'none'") {
		t.Errorf("default-src 'none' blocks the page's own resources: %s", csp)
	}
	if strings.Contains(csp, "form-action 'none'") {
		t.Errorf("form-action 'none' blocks the guestbook's form POST: %s", csp)
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

// Replay and invocation share one helper: a handler cannot opt out of the
// guards by sending its own policy, and transfer headers are not relayed.
func TestCopyFunctionResponseHeadersGuardsEveryEntrypoint(t *testing.T) {
	rec := httptest.NewRecorder()
	CopyFunctionResponseHeaders(rec, map[string]string{
		"Content-Type":            "text/html",
		"Content-Security-Policy": "default-src *",
		"X-Content-Type-Options":  "sniff",
		"Content-Length":          "999",
		"Content-Encoding":        "gzip",
		"X-Custom":                "kept",
	})
	h := rec.Header()
	if h.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox ") ||
		strings.Contains(h.Get("Content-Security-Policy"), "allow-same-origin") {
		t.Fatalf("handler overrode the isolation guards: %v", h)
	}
	if h.Get("Content-Length") != "" || h.Get("Content-Encoding") != "" {
		t.Fatalf("transfer headers were relayed: %v", h)
	}
	if h.Get("Content-Type") != "text/html" || h.Get("X-Custom") != "kept" {
		t.Fatalf("ordinary headers were dropped: %v", h)
	}
}

// Every credential the invoke path verifies is kept out of a captured request,
// including the signed-invocation proof.
func TestCapturedRequestsRedactEveryCredentialHeader(t *testing.T) {
	hdr := http.Header{}
	for _, name := range []string{"Authorization", "Cookie", "X-Orva-API-Key", "X-Orva-Internal-Token",
		"Proxy-Authorization", "X-Orva-Signature", "X-Orva-Timestamp"} {
		hdr.Set(name, "secret-value")
	}
	hdr.Set("Content-Type", "application/json")
	got := redactCapturedHeaders(hdr)
	for name, value := range got {
		if name == "content-type" {
			if value != "application/json" {
				t.Fatalf("ordinary header altered: %q", value)
			}
			continue
		}
		if value != "[REDACTED]" {
			t.Fatalf("%s was persisted in clear: %q", name, value)
		}
	}
	if len(got) != 8 {
		t.Fatalf("unexpected captured headers: %v", got)
	}
}
