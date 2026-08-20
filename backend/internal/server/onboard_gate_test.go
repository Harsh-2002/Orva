package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func onboardBody(t *testing.T, user string) *bytes.Reader {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"username": user, "password": "supersecret123"})
	return bytes.NewReader(b)
}

// TestVirginInstanceCanOnboardFromTheBrowser is the regression for a P0 I
// introduced: gating onboarding on "any API key exists" broke the documented
// first-run flow outright, because server.New mints a bootstrap-admin key on
// EVERY boot -- so a key is present before anyone has done anything, and the
// dashboard's onboarding form has no field for one (nor should it need one
// on first run).
func TestVirginInstanceCanOnboardFromTheBrowser(t *testing.T) {
	tc := newTestServer(t)

	// Model a virgin instance: only the auto-minted bootstrap key, no
	// operator keys, no functions. newTestServer seeds a key named
	// "test-admin", so rename it to what the server actually mints.
	if _, err := tc.srv.db.WriteDB().Exec(
		`UPDATE api_keys SET name = ?`, database.BootstrapKeyName); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/v1/auth/onboard", onboardBody(t, "alice"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("browser onboarding on a virgin instance returned %d: %s",
			w.Code, w.Body.String())
	}
}

// TestUsedInstanceRefusesUnauthenticatedOnboard — the hole being closed: an
// operator who only ever uses API keys never onboards, so has_user stays
// false forever and this endpoint (exempt from the auth middleware, and
// handing back a session cookie that bypasses the permission model) would
// let a stranger claim a working instance.
func TestUsedInstanceRefusesUnauthenticatedOnboard(t *testing.T) {
	t.Run("operator-minted key is evidence of use", func(t *testing.T) {
		tc := newTestServer(t)
		// newTestServer's key is named "test-admin" -- an operator key.
		req := httptest.NewRequest("POST", "/api/v1/auth/onboard", onboardBody(t, "mallory"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		tc.srv.router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s; want 401", w.Code, w.Body.String())
		}
	})

	t.Run("a deployed function is evidence of use", func(t *testing.T) {
		tc := newTestServer(t)
		if _, err := tc.srv.db.WriteDB().Exec(
			`UPDATE api_keys SET name = ?`, database.BootstrapKeyName); err != nil {
			t.Fatal(err)
		}
		if err := tc.srv.db.InsertFunction(&database.Function{
			ID: "019df200-7b00-7e00-9c00-aab1cd2e3f50", Name: "live",
			Runtime: "node", Entrypoint: "handler.js", Status: "active",
		}); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/v1/auth/onboard", onboardBody(t, "mallory"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		tc.srv.router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s; want 401", w.Code, w.Body.String())
		}
	})

	t.Run("an admin key still gets through", func(t *testing.T) {
		tc := newTestServer(t)
		req := httptest.NewRequest("POST", "/api/v1/auth/onboard", onboardBody(t, "operator"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Orva-API-Key", tc.apiKey)
		w := httptest.NewRecorder()
		tc.srv.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s; want 200", w.Code, w.Body.String())
		}
	})
}
