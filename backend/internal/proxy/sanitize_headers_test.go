package proxy

import (
	"net/http"
	"strings"
	"testing"
)

// Every path that hands an inbound request to sandboxed code goes through
// SanitizeForwardedHeaders. Orva's own credentials must not survive it; the
// caller's own data must.
func TestSanitizeForwardedHeadersDropsOrvaCredentials(t *testing.T) {
	h := http.Header{}
	h.Set("X-Orva-API-Key", "orva_deadbeef")
	h.Set("X-Orva-Internal-Token", "internal-secret")
	h.Set("Proxy-Authorization", "Basic abc")
	h.Set("Content-Type", "application/json")

	got := SanitizeForwardedHeaders(h)

	for _, k := range []string{"x-orva-api-key", "x-orva-internal-token", "proxy-authorization"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s reached the sandbox with value %q", k, v)
		}
	}
	if got["content-type"] != "application/json" {
		t.Errorf("content-type = %q, want it forwarded", got["content-type"])
	}
}

// The join-order trap: stripSessionCookie splits on ";", so joining the header
// values before stripping turns "mine=1, session_token=x" into a single crumb
// named "mine" and the session cookie survives. Two separate Cookie lines is
// exactly the shape that exposes it.
func TestSanitizeForwardedHeadersStripsSessionAcrossMultipleCookieLines(t *testing.T) {
	h := http.Header{}
	h.Add("Cookie", "mine=1")
	h.Add("Cookie", "session_token=deadbeef")
	h.Add("Cookie", "theirs=2; session_token=another")

	got := SanitizeForwardedHeaders(h)

	cookie := got["cookie"]
	if containsToken(cookie, "session_token") {
		t.Fatalf("cookie = %q, session_token must not reach the sandbox", cookie)
	}
	if want := "mine=1; theirs=2"; cookie != want {
		t.Errorf("cookie = %q, want %q — the caller's own cookies must survive", cookie, want)
	}
}

func TestSanitizeForwardedHeadersDropsCookieHeaderWhenOnlySession(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "session_token=abc")
	if v, ok := SanitizeForwardedHeaders(h)["cookie"]; ok {
		t.Errorf("cookie = %q, want the header omitted entirely", v)
	}
}

// A third-party bearer belongs to the function — handlers are told to verify
// JWTs themselves. An Orva-issued one is a management credential and must not
// be handed to the sandbox that it just authorized.
func TestSanitizeForwardedHeadersAuthorization(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		forward bool
	}{
		{"orva bearer key", "Bearer orva_abcdef123456", false},
		{"orva channel token", "Bearer orva_chn_abcdef", false},
		{"bare orva key", "orva_abcdef123456", false},
		{"lowercase bearer scheme", "bearer orva_abcdef", false},
		{"third-party jwt", "Bearer eyJhbGciOiJIUzI1NiJ9.e30.sig", true},
		{"basic auth", "Basic dXNlcjpwYXNz", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Authorization", tc.value)
			got, ok := SanitizeForwardedHeaders(h)["authorization"]
			if tc.forward && got != tc.value {
				t.Errorf("authorization = %q (present=%v), want it forwarded as %q", got, ok, tc.value)
			}
			if !tc.forward && ok {
				t.Errorf("authorization = %q, want an Orva credential withheld", got)
			}
		})
	}
}

// RFC 9110 §5.2: repeated field lines are one comma-joined value. The old
// hand-rolled flattener in the webhook path kept only the first.
func TestSanitizeForwardedHeadersJoinsRepeatedValues(t *testing.T) {
	h := http.Header{}
	h.Add("X-Custom", "a")
	h.Add("X-Custom", "b")
	if got := SanitizeForwardedHeaders(h)["x-custom"]; got != "a, b" {
		t.Errorf("x-custom = %q, want %q", got, "a, b")
	}
}

// containsToken reports whether a "; "-joined cookie string carries a crumb
// whose name is exactly name — so "my_session_token" does not match
// "session_token".
func containsToken(cookie, name string) bool {
	for _, crumb := range splitAndTrim(cookie, ';') {
		n, _, _ := cut(crumb, '=')
		if n == name {
			return true
		}
	}
	return false
}

func splitAndTrim(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == sep {
			part := s[start:i]
			for len(part) > 0 && part[0] == ' ' {
				part = part[1:]
			}
			for len(part) > 0 && part[len(part)-1] == ' ' {
				part = part[:len(part)-1]
			}
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

func cut(s string, sep byte) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// TestSanitizeDropsInboundOrvaNamespace — the x-orva- namespace is the
// server talking to its own adapter. A caller who could inject into it
// forged two guarantees the product documents:
//
//   - orva.webhook.parse(event).verified is literally
//     trigger === "inbound_webhook", and the reference tells handlers to
//     return 401 when it is false. Setting the header on a direct /fn/
//     call satisfied that check with no HMAC involved.
//   - x-orva-call-depth seeds ORVA_CALL_DEPTH, which the SDK forwards on
//     nested invokes and the F2F endpoint compares against its cap. A
//     negative value disabled the cap.
func TestSanitizeDropsInboundOrvaNamespace(t *testing.T) {
	h := http.Header{}
	h.Set("X-Orva-Trigger", "inbound_webhook")
	h.Set("X-Orva-Call-Depth", "-1000000")
	h.Set("X-Orva-Inbound-Webhook-Id", "attacker-chosen")
	h.Set("x-orva-function-id", "someone-elses-function")
	h.Set("X-Orva-Trace-Id", "forged")
	// A caller header that must still come through untouched.
	h.Set("X-GitHub-Event", "push")

	got := SanitizeForwardedHeaders(h)

	for k := range got {
		if strings.HasPrefix(strings.ToLower(k), "x-orva-") {
			t.Errorf("inbound %q survived sanitization with value %q", k, got[k])
		}
	}
	if got["x-github-event"] != "push" {
		t.Errorf("third-party header was dropped: %q", got["x-github-event"])
	}
}

// TestServerStampedOrvaHeadersStillReachTheSandbox — dropping the namespace
// must not break the server's own tagging, which is applied after
// sanitization. This pins the ordering the fix depends on.
func TestServerStampedOrvaHeadersStillReachTheSandbox(t *testing.T) {
	h := http.Header{}
	h.Set("X-Orva-Trigger", "inbound_webhook") // forged by the caller

	got := SanitizeForwardedHeaders(h)
	// What inbound_webhook_trigger.go and proxy.Forward do next:
	got["x-orva-trigger"] = "http"
	got["x-orva-function-id"] = "real-fn-id"

	if got["x-orva-trigger"] != "http" {
		t.Errorf("server-stamped trigger = %q, want http", got["x-orva-trigger"])
	}
	if got["x-orva-function-id"] != "real-fn-id" {
		t.Errorf("server-stamped function id = %q", got["x-orva-function-id"])
	}
}
