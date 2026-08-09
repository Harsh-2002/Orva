package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestInternalTokenBypassClosed is the regression guard for the auth bypass:
// authMiddleware must only skip the API-key gate for the CORRECT per-process
// internal token. A merely-present (bogus) X-Orva-Internal-Token must NOT
// grant access — it has to fall through to normal session/API-key auth and
// get a 401, exactly like an unauthenticated caller.
//
// Before the fix, `if r.Header.Get("X-Orva-Internal-Token") != ""` let any
// non-empty value skip authentication entirely, so a request with a garbage
// header reached the handlers with full operator power.
func TestInternalTokenBypassClosed(t *testing.T) {
	tc := newTestServer(t)
	real := tc.srv.router.internalToken
	if real == "" {
		t.Fatal("router.internalToken is empty; the middleware can never authenticate the SDK path")
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
			name: "correct internal token is accepted (SDK path still works)",
			setup: func(r *http.Request) {
				r.Header.Set("X-Orva-Internal-Token", real)
			},
			wantStatus: http.StatusOK,
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
