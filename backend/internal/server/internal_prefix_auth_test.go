package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestSDKHTTPRoutesAreRemoved(t *testing.T) {
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
	tc := newTestServer(t)
	for _, route := range routes {
		for _, token := range []string{"", "bogus-sdk-token", legacySDKToken} {
			for _, operator := range []bool{false, true} {
				req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
				req.Header.Set("X-Orva-Internal-Token", token)
				want := http.StatusUnauthorized
				if operator {
					tc.setAuth(req)
					want = http.StatusNotFound
				}
				// A handler-level 404 is not proof the route is unregistered.
				if _, pattern := tc.srv.router.mux.Handler(req); pattern != "/" {
					t.Fatalf("%s %s still has route %q", route.method, route.path, pattern)
				}
				w := httptest.NewRecorder()
				tc.srv.router.ServeHTTP(w, req)
				if w.Code != want {
					t.Errorf("%s %s operator=%v token-present=%v: got %d, want %d; %s",
						route.method, route.path, operator, token != "", w.Code, want, w.Body.String())
				}
			}
		}
	}
}

func TestLegacySDKTokenDoesNotBypassOperatorAuthentication(t *testing.T) {
	tc := newTestServer(t)
	r := tc.srv.router
	for _, path := range []string{"/api/v1/_kv/fn_x/k", "/api/v1/_internal/crons", "/api/v1/jobs"} {
		t.Run(path, func(t *testing.T) {
			for _, operator := range []bool{false, true} {
				var reached bool
				spy := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					reached = true
					if req.Header.Get("X-Orva-Caller-Function") != "" {
						t.Error("legacy caller header reached the handler")
					}
					w.WriteHeader(http.StatusNoContent)
				})
				gate := authMiddleware(r.db, &r.keyCache, &r.sessionCache, spy)
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
				req.Header.Set("X-Orva-Internal-Token", legacySDKToken)
				req.Header.Set("X-Orva-Caller-Function", "forged")
				want := http.StatusUnauthorized
				if operator {
					tc.setAuth(req)
					want = http.StatusNoContent
				}
				w := httptest.NewRecorder()
				gate.ServeHTTP(w, req)
				if reached != operator || w.Code != want {
					t.Fatalf("operator=%v reached=%v status=%d, want %d", operator, reached, w.Code, want)
				}
			}
		})
	}
}

func TestPublicJobsRequiresOperatorKeyAndIgnoresSDKAttribution(t *testing.T) {
	tc := newTestServer(t)
	fn := &database.Function{
		ID: "fn-job-target", Name: "job-target", Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 64, CPUs: 0.5, NetworkMode: "none", Status: "active",
		EnvVars: map[string]string{},
	}
	if err := tc.srv.router.db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"bogus-sdk-token", legacySDKToken} {
		for _, operator := range []bool{false, true} {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"function_name":"job-target"}`))
			req.Header.Set("X-Orva-Internal-Token", token)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-Orva-Caller-Function", "fn-sdk-caller")
			req.Header.Set("X-Orva-Trace-Id", "forged-trace")
			req.Header.Set("X-Orva-Span-Id", "forged-span")
			want := http.StatusUnauthorized
			if operator {
				tc.setAuth(req)
				want = http.StatusCreated
			}
			w := httptest.NewRecorder()
			tc.srv.router.ServeHTTP(w, req)
			if w.Code != want {
				t.Fatalf("operator=%v status=%d, want %d: %s", operator, w.Code, want, w.Body.String())
			}
			if operator {
				var job database.Job
				if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
					t.Fatal(err)
				}
				if job.FunctionID != fn.ID || job.EnqueuedByFunctionID != "" || job.TraceID != "" || job.ParentSpanID != "" {
					t.Fatal("operator job acquired SDK attribution")
				}
			}
		}
	}
	jobs, err := tc.srv.router.db.ListJobs("", fn.ID, 100)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("expected only the two operator jobs: count=%d err=%v", len(jobs), err)
	}
}

// A retired v2-shaped credential is inert regardless of its former signing key.
const legacySDKToken = "v2.Zm5feA.MDEyMzQ1Njc4OWFiY2RlZg.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
