package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// getHealth issues GET /api/v1/system/health and decodes the envelope.
func getHealth(t *testing.T, tc *testContext) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/system/health", nil)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode health body: %v (raw: %s)", err, w.Body.String())
	}
	return w.Code, body
}

// TestHealthHealthyWhenDBUp: a running server with a reachable DB reports 200 +
// "healthy". The sandbox runtime is reported but does not affect the status, so
// this holds whether or not nsjail happens to be installed on the test box.
func TestHealthHealthyWhenDBUp(t *testing.T) {
	tc := newTestServer(t)

	code, body := getHealth(t, tc)
	if code != http.StatusOK {
		t.Fatalf("healthy server: got status %d, want 200 (body: %v)", code, body)
	}
	if body["status"] != "healthy" {
		t.Errorf("got status %q, want healthy", body["status"])
	}
	db, _ := body["database"].(map[string]any)
	if db["status"] != "ok" {
		t.Errorf("database.status = %v, want ok", db["status"])
	}
	// runtime is informational; assert it is reported, not its value (nsjail
	// presence varies by machine).
	sb, _ := body["sandbox"].(map[string]any)
	if rt, _ := sb["runtime"].(string); rt != "ok" && rt != "unavailable" {
		t.Errorf("sandbox.runtime = %v, want ok|unavailable", sb["runtime"])
	}
}

// TestHealthDegradedOnDBFailure is the core H2 fix: a broken database must make
// /health return 503 + "degraded" instead of a hardcoded green. We simulate the
// failure by closing the read pool the Ping runs against.
func TestHealthDegradedOnDBFailure(t *testing.T) {
	tc := newTestServer(t)

	// Break the DB the health check pings. Closing just the read pool leaves
	// the cleanup's full Close() safe (it ignores the read-close error).
	if err := tc.srv.db.ReadDB().Close(); err != nil {
		t.Fatalf("close read pool: %v", err)
	}

	code, body := getHealth(t, tc)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("dead DB: got status %d, want 503 (body: %v)", code, body)
	}
	if body["status"] != "degraded" {
		t.Errorf("got status %q, want degraded", body["status"])
	}
	db, _ := body["database"].(map[string]any)
	if db["status"] != "error" {
		t.Errorf("database.status = %v, want error", db["status"])
	}
}
