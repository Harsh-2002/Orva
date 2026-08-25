package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Everything under /api/v1/_kv/ and /api/v1/_internal/ authenticates with the
// process-signed SDK credential rather than an API key. The gate used to
// discard the Verify error and call the handler anyway, so those prefixes were
// authenticated by each handler remembering to re-verify -- eleven routes, all
// of which happened to. Nothing was exploitable; the next route added would
// have been, silently.
//
// The list is deliberately exhaustive rather than a sample. A route added to
// either prefix without a matching entry here is the case this test exists to
// catch, so keep it in step with router.go.
//
// Note this passes against the unfixed gate too, and says nothing on its own:
// every route that exists today re-verifies, which is why nothing was
// exploitable. It pins that the outcome stays 401 whichever layer produces it.
// The gate itself is pinned by TestTheGateRejectsBeforeTheHandlerRuns below,
// which is the test that fails without the fix.
func TestEveryInternalPrefixRouteRejectsABadCredential(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodPut, "/api/v1/_kv/fn_x/k"},
		{http.MethodGet, "/api/v1/_kv/fn_x/k"},
		{http.MethodDelete, "/api/v1/_kv/fn_x/k"},
		{http.MethodGet, "/api/v1/_kv/fn_x"},
		{http.MethodPost, "/api/v1/_kv/fn_x/batch"},
		{http.MethodPost, "/api/v1/_kv/fn_x/k/incr"},
		{http.MethodPost, "/api/v1/_kv/fn_x/k/cas"},
		{http.MethodPost, "/api/v1/_internal/invoke/some-fn"},
		{http.MethodPost, "/api/v1/_internal/invoke/some-fn/stream"},
		{http.MethodPost, "/api/v1/_internal/spans"},
		{http.MethodPost, "/api/v1/_internal/crons"},
	}

	credentials := []struct{ name, token string }{
		{"absent", ""},
		{"garbage", "totally-bogus"},
		{"well-shaped but unsigned", "v1.Zm5feA.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	}

	tc := newTestServer(t)
	for _, route := range routes {
		for _, cred := range credentials {
			t.Run(route.path+"/"+cred.name, func(t *testing.T) {
				req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				if cred.token != "" {
					req.Header.Set("X-Orva-Internal-Token", cred.token)
				}
				w := httptest.NewRecorder()
				tc.srv.router.ServeHTTP(w, req)
				if w.Code != http.StatusUnauthorized {
					t.Errorf("%s %s with a %s credential returned %d, want 401: this prefix must be authenticated by the gate, not by each handler remembering to",
						route.method, route.path, cred.name, w.Code)
				}
			})
		}
	}
}

// A real credential still gets through the gate. Without this the test above
// would pass against a gate that rejected everything.
func TestAValidSDKCredentialPassesTheInternalPrefixGate(t *testing.T) {
	tc := newTestServer(t)
	token := tc.srv.router.sdkAuth.Mint("fn_x")
	if token == "" {
		t.Fatal("could not mint an SDK credential")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/_kv/fn_x/missing-key", nil)
	req.Header.Set("X-Orva-Internal-Token", token)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	// The key does not exist, so 404 -- but reaching a 404 proves the gate let
	// it past, which a 401 would not.
	if w.Code == http.StatusUnauthorized {
		t.Errorf("a valid SDK credential was rejected at the gate: %s", w.Body.String())
	}
}

// The property the fix actually changes: rejection happens AT THE GATE, so a
// handler behind these prefixes never runs for an unauthenticated caller.
//
// Testing this through the router cannot work -- every real route re-verifies,
// so the outcome is 401 either way. A spy handler standing in for a route that
// forgot to re-verify is the whole point: it is the route someone adds next.
func TestTheGateRejectsBeforeTheHandlerRuns(t *testing.T) {
	tc := newTestServer(t)
	r := tc.srv.router

	for _, path := range []string{"/api/v1/_kv/fn_x/k", "/api/v1/_internal/crons"} {
		t.Run(path, func(t *testing.T) {
			var reached bool
			spy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			gate := authMiddleware(r.db, &r.keyCache, &r.sessionCache, r.sdkAuth, spy)

			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			req.Header.Set("X-Orva-Internal-Token", "not-a-real-credential")
			w := httptest.NewRecorder()
			gate.ServeHTTP(w, req)

			if reached {
				t.Error("an unauthenticated request reached the handler: these prefixes are authenticated by the gate, not by each handler remembering to re-verify")
			}
			if w.Code != http.StatusUnauthorized {
				t.Errorf("gate returned %d, want 401", w.Code)
			}
		})
	}

	// And a real credential still gets through, or the assertion above would
	// be satisfied by a gate that rejects everything.
	var reached bool
	spy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true })
	gate := authMiddleware(r.db, &r.keyCache, &r.sessionCache, r.sdkAuth, spy)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/_kv/fn_x/k", nil)
	req.Header.Set("X-Orva-Internal-Token", r.sdkAuth.Mint("fn_x"))
	gate.ServeHTTP(httptest.NewRecorder(), req)
	if !reached {
		t.Error("a valid SDK credential was rejected at the gate")
	}
}
