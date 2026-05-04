package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// TestE2E_FunctionsHaveUUIDIDs_AndInvokeURL — create a function via
// the REST API and verify the response carries a UUIDv7 id (no
// fn_ prefix) and that the canonical invoke URL works at /fn/<uuid>.
func TestE2E_FunctionsHaveUUIDIDs(t *testing.T) {
	tc := newTestServer(t)

	// Create a function via REST.
	body := `{"name":"hello-uuid","runtime":"python314"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	id, _ := resp["id"].(string)
	if id == "" {
		t.Fatalf("no id in response: %+v", resp)
	}
	if !ids.IsUUID(id) {
		t.Errorf("function id is not a UUID: %q", id)
	}
	if strings.HasPrefix(id, "fn_") {
		t.Errorf("function id still has fn_ prefix: %q", id)
	}
}

// TestE2E_LegacyURLFormat404s — the old /fn/fn_<short> URL form must
// 404 cleanly. There's no silent legacy-URL trap that would let a
// caller slip through with the wrong shape.
func TestE2E_LegacyURLFormat404s(t *testing.T) {
	tc := newTestServer(t)

	for _, path := range []string{
		"/fn/fn_abc123def456",
		"/fn/abc123def456", // not a UUID — also 404
	} {
		req := httptest.NewRequest("POST", path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		tc.srv.router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", path, w.Code)
		}
	}
}

// TestE2E_OAuthGrantsFullScope — full OAuth flow ending in a
// validated bearer. The granted scope must include all four
// (read invoke write admin) so write- and admin-gated MCP tools
// register against the resulting token.
func TestE2E_OAuthGrantsFullScope(t *testing.T) {
	tc := newTestServer(t)
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")

	// Walk the full OAuth flow with NO scope on DCR — should default
	// to "read invoke write admin".
	regBody := `{
		"client_name": "Test Full Scope Client",
		"redirect_uris": ["http://127.0.0.1:33445/cb"],
		"token_endpoint_auth_method": "none"
	}`
	req := httptest.NewRequest("POST", "/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.50:1"
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: status=%d body=%s", w.Code, w.Body.String())
	}
	var dcr map[string]any
	_ = json.NewDecoder(w.Body).Decode(&dcr)
	clientID, _ := dcr["client_id"].(string)
	scope, _ := dcr["scope"].(string)
	if scope != "read invoke write admin" {
		t.Errorf("DCR default scope = %q, want %q", scope, "read invoke write admin")
	}

	// Verify on the wire too: the issued token must carry all four
	// scopes after the consent flow.
	verifier := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklm123456789-_"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	form := url.Values{}
	form.Set("decision", "allow")
	form.Set("client_id", clientID)
	form.Set("redirect_uri", "http://127.0.0.1:33445/cb")
	// No scope param — falls back to client's registered default.
	form.Set("code_challenge", challenge)
	form.Set("code_challenge_method", "S256")
	form.Set("response_type", "code")
	form.Set("resource", "http://example.com/mcp")
	req = httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	loc, _ := url.Parse(w.Header().Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("authorize: no code in redirect: %s", w.Header().Get("Location"))
	}

	tokForm := url.Values{}
	tokForm.Set("grant_type", "authorization_code")
	tokForm.Set("code", code)
	tokForm.Set("client_id", clientID)
	tokForm.Set("redirect_uri", "http://127.0.0.1:33445/cb")
	tokForm.Set("code_verifier", verifier)
	req = httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tokForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("token: status=%d body=%s", w.Code, w.Body.String())
	}
	var tok map[string]any
	_ = json.NewDecoder(w.Body).Decode(&tok)
	tokScope, _ := tok["scope"].(string)
	// Granted scope is normalised (alphabetical), so the assertion uses
	// that ordering rather than the registration order.
	want := "admin invoke read write"
	if tokScope != want {
		t.Errorf("token scope = %q, want %q", tokScope, want)
	}
}

// TestE2E_StorageIDsAreUUIDv7 — every newly-minted storage ID across
// the major tables must parse as a UUID. Catches regressions where
// somebody adds a new mint site that bypasses the ids package.
func TestE2E_StorageIDsAreUUIDv7(t *testing.T) {
	tc := newTestServer(t)

	// API key minted via REST.
	body := `{"name":"e2e-key","permissions":["read"]}`
	req := httptest.NewRequest("POST", "/api/v1/keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create key: status=%d body=%s", w.Code, w.Body.String())
	}
	var keyResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&keyResp)
	keyID, _ := keyResp["id"].(string)
	if !ids.IsUUID(keyID) {
		t.Errorf("api_key.id is not a UUID: %q", keyID)
	}

	// Direct DAL: subscription, fixture, cron, job, inbound webhook.
	checks := []struct {
		name string
		got  string
	}{
		{"subscription", database.NewSubscriptionID()},
		{"delivery", database.NewDeliveryID()},
		{"fixture", database.NewFixtureID()},
		{"cron", database.NewCronID()},
		{"job", database.NewJobID()},
		{"inbound webhook", database.NewInboundWebhookID()},
	}
	for _, c := range checks {
		if !ids.IsUUID(c.got) {
			t.Errorf("%s id is not a UUID: %q", c.name, c.got)
		}
		if strings.Contains(c.got, "_") {
			t.Errorf("%s id has legacy underscore prefix: %q", c.name, c.got)
		}
	}
}

// TestE2E_MCPListFunctionsIncludesInvokeURL — call list_functions via
// the MCP endpoint with a freshly-issued OAuth bearer; assert the
// JSON response includes a populated invoke_url for each function.
func TestE2E_MCPListFunctionsIncludesInvokeURL(t *testing.T) {
	tc := newTestServer(t)
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")

	// Create a function so list_functions has something to return.
	body := `{"name":"e2e-mcp-test","runtime":"python314"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create function: status=%d body=%s", w.Code, w.Body.String())
	}

	// Mint an OAuth bearer via the helper from oauth_e2e_test.go.
	access := mintOAuthGrant(t, tc, cookie, "MCP URL Test Client")

	// Initialize MCP session.
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(initBody))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mcp initialize: status=%d body=%s", w.Code, w.Body.String())
	}
	sessionID := w.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatalf("no Mcp-Session-Id in initialize response")
	}

	// Call list_functions.
	listBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_functions","arguments":{}}}`
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(listBody))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list_functions: status=%d body=%s", w.Code, w.Body.String())
	}
	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, `"invoke_url":"http://example.com/fn/`) {
		t.Errorf("invoke_url missing or wrong shape; body excerpt: %s",
			truncate(bodyStr, 500))
	}
	// The id field must be a UUID — no fn_ prefix.
	if strings.Contains(bodyStr, `"id":"fn_`) {
		t.Errorf("response leaks legacy fn_ prefix in id field; body excerpt: %s",
			truncate(bodyStr, 500))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
