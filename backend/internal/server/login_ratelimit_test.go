package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLoginRateLimited is the M5 guard: repeated login attempts from one IP get
// throttled with 429 instead of letting a caller brute-force credentials as
// fast as bcrypt allows. The test box has no onboarded user, so each allowed
// attempt returns 401 (invalid credentials); once the per-IP bucket empties the
// endpoint returns 429 before even checking the password.
func TestLoginRateLimited(t *testing.T) {
	tc := newTestServer(t)
	body := `{"username":"nobody","password":"whatever"}`

	got429 := false
	allowed := 0
	// Try comfortably more than the per-minute cap.
	for i := 0; i < 40; i++ {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		tc.srv.router.ServeHTTP(w, req)
		switch w.Code {
		case http.StatusTooManyRequests:
			got429 = true
			if ra := w.Header().Get("Retry-After"); ra == "" {
				t.Error("429 response missing Retry-After header")
			}
		case http.StatusUnauthorized:
			allowed++
		default:
			t.Fatalf("attempt %d: unexpected status %d (body: %s)", i, w.Code, w.Body.String())
		}
	}

	if !got429 {
		t.Fatal("login was never rate-limited across 40 rapid attempts")
	}
	if allowed == 0 {
		t.Fatal("expected some attempts to reach the credential check before throttling")
	}
	// Throttling must kick in well before all 40 attempts are spent (the exact
	// cap is a handlers-package detail; here we just assert the bucket bounds it).
	if allowed >= 40 {
		t.Errorf("allowed all %d attempts — throttle never engaged", allowed)
	}
}
