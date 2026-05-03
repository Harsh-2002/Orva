package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSessionsList confirms GET /api/v1/auth/sessions returns the
// onboarded session with current=true and prefix-only token. The full
// token must NEVER appear in the response.
func TestSessionsList(t *testing.T) {
	tc := newTestServer(t)
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")

	req := httptest.NewRequest("GET", "/api/v1/auth/sessions", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, cookie.Value) {
		t.Errorf("response leaked full session token: %s", body)
	}
	var resp struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(resp.Sessions))
	}
	if resp.Sessions[0]["current"] != true {
		t.Errorf("first session should be current: %+v", resp.Sessions[0])
	}
	prefix, _ := resp.Sessions[0]["prefix"].(string)
	if !strings.HasPrefix(cookie.Value, prefix) {
		t.Errorf("prefix %q is not a prefix of cookie value", prefix)
	}
	if len(prefix) < 16 {
		t.Errorf("prefix too short: %d chars", len(prefix))
	}
}

// TestRevokeSelfRejected ensures the calling session can't accidentally
// revoke itself — that's what /auth/logout is for.
func TestRevokeSelfRejected(t *testing.T) {
	tc := newTestServer(t)
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")

	prefix := cookie.Value[:16]
	req := httptest.NewRequest("DELETE", "/api/v1/auth/sessions/"+prefix, nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("self-revoke: status=%d (want 400) body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "CANT_DELETE_SELF") {
		t.Errorf("expected CANT_DELETE_SELF error code: %s", w.Body.String())
	}

	// The session must still be valid afterwards.
	req = httptest.NewRequest("GET", "/api/v1/auth/sessions", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("session unexpectedly invalidated by self-revoke attempt")
	}
}

// TestRevokeOtherSession: log in twice (creating a second session),
// then have session A revoke session B by prefix. B must 401 on the
// next request; A must keep working.
func TestRevokeOtherSession(t *testing.T) {
	tc := newTestServer(t)
	cookieA := onboardAndLogin(t, tc, "alice", "supersecret123")
	cookieB := loginAgain(t, tc, "alice", "supersecret123")

	// Sanity: both cookies work.
	for name, c := range map[string]*http.Cookie{"A": cookieA, "B": cookieB} {
		req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
		req.AddCookie(c)
		w := httptest.NewRecorder()
		tc.srv.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("/auth/me with cookie %s: status=%d", name, w.Code)
		}
	}

	// A revokes B (using B's prefix).
	prefixB := cookieB.Value[:16]
	req := httptest.NewRequest("DELETE", "/api/v1/auth/sessions/"+prefixB, nil)
	req.AddCookie(cookieA)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("A revoking B: status=%d body=%s", w.Code, w.Body.String())
	}

	// B must now be invalid.
	req = httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.AddCookie(cookieB)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked session still valid: status=%d", w.Code)
	}

	// A must still work.
	req = httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.AddCookie(cookieA)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("self-revoke during cross-revoke: status=%d", w.Code)
	}
}

// loginAgain re-logs the existing user via /auth/login to simulate a
// second browser. Returns the new session_token cookie.
func loginAgain(t *testing.T, tc *testContext, username, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "session_token" {
			return c
		}
	}
	t.Fatal("login did not set session_token cookie")
	return nil
}
