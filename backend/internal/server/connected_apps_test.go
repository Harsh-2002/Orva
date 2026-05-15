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
)

// TestConnectedAppsListAndRevoke exercises the Settings → Connected
// applications surface end-to-end: walk the OAuth flow, then verify
// GET /api/v1/oauth/connected-apps surfaces the grant, DELETE removes
// it, and the revoked token immediately 401s on /mcp.
func TestConnectedAppsListAndRevoke(t *testing.T) {
	tc := newTestServer(t)
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")

	// Mint a grant via the full OAuth flow.
	access := mintOAuthGrant(t, tc, cookie, "ChatGPT")

	// LIST should now show one row.
	req := httptest.NewRequest("GET", "/api/v1/oauth/connected-apps", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", w.Code, w.Body.String())
	}
	var listResp struct {
		Apps []map[string]any `json:"apps"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Apps) != 1 {
		t.Fatalf("expected 1 app, got %d: %+v", len(listResp.Apps), listResp.Apps)
	}
	if listResp.Apps[0]["client_name"] != "ChatGPT" {
		t.Errorf("client_name = %v, want ChatGPT", listResp.Apps[0]["client_name"])
	}
	id, _ := listResp.Apps[0]["id"].(string)
	if id == "" {
		t.Fatal("missing id in list response")
	}

	// LIST without a session cookie should 401.
	req = httptest.NewRequest("GET", "/api/v1/oauth/connected-apps", nil)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("list w/o cookie: status=%d (want 401)", w.Code)
	}

	// REVOKE the row.
	req = httptest.NewRequest("DELETE", "/api/v1/oauth/connected-apps/"+id, nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: status=%d body=%s", w.Code, w.Body.String())
	}

	// LIST should now be empty.
	req = httptest.NewRequest("GET", "/api/v1/oauth/connected-apps", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	_ = json.NewDecoder(w.Body).Decode(&listResp)
	if len(listResp.Apps) != 0 {
		t.Errorf("after revoke, list should be empty; got %d", len(listResp.Apps))
	}

	// MCP call with the revoked bearer must 401.
	mcpReq := httptest.NewRequest("POST", "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	mcpReq.Header.Set("Authorization", "Bearer "+access)
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Accept", "application/json, text/event-stream")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, mcpReq)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token still works on /mcp: status=%d", w.Code)
	}

	// Re-revoking the same id is a 404, not a 500.
	req = httptest.NewRequest("DELETE", "/api/v1/oauth/connected-apps/"+id, nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("re-revoke: status=%d (want 404)", w.Code)
	}
}

// TestConnectedAppsListSurvivesAccessTokenExpiry is the regression test
// for the bug where the Settings → Connected applications list would
// hide grants ~1h after authorization (when the access token expired)
// even though the refresh token was still valid. claude.ai and ChatGPT
// silently refresh access tokens via the refresh_token grant, so the
// UI should treat the row as still connected.
func TestConnectedAppsListSurvivesAccessTokenExpiry(t *testing.T) {
	tc := newTestServer(t)
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")
	_ = mintOAuthGrant(t, tc, cookie, "claude.ai")

	// Backdate the access_expires_at past so the row's *access* token
	// is expired but its *refresh* token is still fresh — exactly the
	// state a connector lands in 1h after authorization.
	_, err := tc.srv.db.WriteDB().Exec(`
		UPDATE oauth_access_tokens
		SET access_expires_at = datetime('now', '-2 hours')
		WHERE user_id = (SELECT id FROM users WHERE username = 'alice')`)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/oauth/connected-apps", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", w.Code, w.Body.String())
	}
	var listResp struct {
		Apps []map[string]any `json:"apps"`
	}
	_ = json.NewDecoder(w.Body).Decode(&listResp)
	if len(listResp.Apps) != 1 {
		t.Fatalf("expected 1 app despite expired access token; got %d: %+v",
			len(listResp.Apps), listResp.Apps)
	}
	if listResp.Apps[0]["client_name"] != "claude.ai" {
		t.Errorf("client_name = %v, want claude.ai", listResp.Apps[0]["client_name"])
	}

	// Now also expire the refresh token — the row SHOULD disappear,
	// because there's nothing left to refresh from.
	if _, err := tc.srv.db.WriteDB().Exec(`
		UPDATE oauth_access_tokens
		SET refresh_expires_at = datetime('now', '-1 day')
		WHERE user_id = (SELECT id FROM users WHERE username = 'alice')`); err != nil {
		t.Fatalf("expire refresh: %v", err)
	}

	req = httptest.NewRequest("GET", "/api/v1/oauth/connected-apps", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	_ = json.NewDecoder(w.Body).Decode(&listResp)
	if len(listResp.Apps) != 0 {
		t.Errorf("after both tokens expired, list should be empty; got %d", len(listResp.Apps))
	}
}

// mintOAuthGrant runs the full DCR + authorize + token flow and
// returns the issued access-token plaintext.
func mintOAuthGrant(t *testing.T, tc *testContext, cookie *http.Cookie, clientName string) string {
	t.Helper()
	regBody := `{
        "client_name": "` + clientName + `",
        "redirect_uris": ["http://127.0.0.1:33445/cb"],
        "token_endpoint_auth_method": "none"
    }`
	req := httptest.NewRequest("POST", "/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.40:1"
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("DCR: %d %s", w.Code, w.Body.String())
	}
	var dcr map[string]any
	_ = json.NewDecoder(w.Body).Decode(&dcr)
	clientID, _ := dcr["client_id"].(string)

	verifier := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklm123456789-_"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	form := url.Values{}
	form.Set("decision", "allow")
	form.Set("client_id", clientID)
	form.Set("redirect_uri", "http://127.0.0.1:33445/cb")
	form.Set("scope", "read invoke")
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
		t.Fatalf("authorize POST did not return code: %s", w.Header().Get("Location"))
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
		t.Fatalf("token: %d %s", w.Code, w.Body.String())
	}
	var tok map[string]any
	_ = json.NewDecoder(w.Body).Decode(&tok)
	access, _ := tok["access_token"].(string)
	if access == "" {
		t.Fatal("token response missing access_token")
	}
	return access
}
