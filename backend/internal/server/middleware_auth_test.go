package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestInternalTokenBypassClosed is the regression guard for the auth bypass:
// authMiddleware must only skip the job enqueue gate for a verified scoped
// SDK credential. A merely-present (bogus) X-Orva-Internal-Token must NOT
// grant access — it has to fall through to normal session/API-key auth and
// get a 401, exactly like an unauthenticated caller.
//
// SDK credentials must never grant access to the broader operator API.
func TestScopedSDKCredentialCannotBypassOperatorAuth(t *testing.T) {
	tc := newTestServer(t)
	real := mintLive(tc.srv.router.sdkAuth, "function-a")
	if real == "" {
		t.Fatal("scoped SDK credential is empty")
	}

	// A protected admin-surface endpoint that the live exploit hit.
	const target = "/api/v1/functions"

	cases := []struct {
		name       string
		setup      func(*http.Request)
		wantStatus int
	}{
		{
			name:       "no auth is rejected",
			setup:      func(*http.Request) {},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "bogus internal token is rejected (the bypass)",
			setup: func(r *http.Request) {
				r.Header.Set("X-Orva-Internal-Token", "totally-bogus-value")
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "empty internal token header is rejected",
			setup: func(r *http.Request) {
				r.Header.Set("X-Orva-Internal-Token", "")
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "valid SDK credential cannot access operator APIs",
			setup: func(r *http.Request) {
				r.Header.Set("X-Orva-Internal-Token", real)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "valid API key is accepted (normal path unaffected)",
			setup: func(r *http.Request) {
				r.Header.Set("X-Orva-API-Key", tc.apiKey)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "valid API key plus a stray bogus internal token still authenticates",
			setup: func(r *http.Request) {
				r.Header.Set("X-Orva-API-Key", tc.apiKey)
				r.Header.Set("X-Orva-Internal-Token", "stray-header")
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", target, nil)
			c.setup(req)
			w := httptest.NewRecorder()
			tc.srv.router.ServeHTTP(w, req)
			if w.Code != c.wantStatus {
				t.Errorf("%s %s: got status %d, want %d (body: %s)",
					req.Method, target, w.Code, c.wantStatus, w.Body.String())
			}
		})
	}
}
