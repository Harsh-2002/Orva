package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestOAuthEndToEnd walks the full claude.ai/ChatGPT-style OAuth 2.1
// flow against an in-process router: DCR → authorize (consent allow)
// → token (authorization_code) → use bearer on /mcp → refresh → revoke.
// If any step regresses the browser flow breaks too, so this single
// test gates the whole feature.
func TestOAuthEndToEnd(t *testing.T) {
	tc := newTestServer(t)

	// Onboard an admin user via the existing /auth/onboard flow so we
	// have a real session cookie for the consent screen.
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")

	// 1) DCR — register a public client (PKCE-only, no secret).
	regBody := `{
        "client_name": "Integration Test Client",
        "redirect_uris": ["http://127.0.0.1:54321/cb"],
        "token_endpoint_auth_method": "none",
        "scope": "read invoke"
    }`
	req := httptest.NewRequest("POST", "/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.20:9999"
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("DCR: status=%d body=%s", w.Code, w.Body.String())
	}
	var dcr map[string]any
	if err := json.NewDecoder(w.Body).Decode(&dcr); err != nil {
		t.Fatal(err)
	}
	clientID, _ := dcr["client_id"].(string)
	if clientID == "" {
		t.Fatal("DCR: empty client_id")
	}

	// 2) Build PKCE pair: verifier (43+ random url-safe chars) →
	//    challenge = base64url(no-pad, sha256(verifier)).
	verifier := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklm123456789-_"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// 3) GET /oauth/authorize — expect 200 HTML containing client name.
	authURL := "/oauth/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape("http://127.0.0.1:54321/cb") +
		"&scope=" + url.QueryEscape("read invoke") +
		"&state=xyz" +
		"&code_challenge=" + challenge +
		"&code_challenge_method=S256" +
		"&resource=" + url.QueryEscape("http://example.com/mcp")
	req = httptest.NewRequest("GET", authURL, nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("authorize GET: status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Integration Test Client") {
		t.Errorf("consent screen missing client name; body=%s", body)
	}
	if !strings.Contains(body, "Allow access") {
		t.Error("consent screen missing Allow button")
	}

	// 4) POST /oauth/authorize with decision=allow → expect 302 with
	//    code+state on the redirect_uri.
	form := url.Values{}
	form.Set("decision", "allow")
	form.Set("client_id", clientID)
	form.Set("redirect_uri", "http://127.0.0.1:54321/cb")
	form.Set("scope", "read invoke")
	form.Set("state", "xyz")
	form.Set("code_challenge", challenge)
	form.Set("code_challenge_method", "S256")
	form.Set("response_type", "code")
	form.Set("resource", "http://example.com/mcp")
	req = httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("authorize POST: status=%d body=%s", w.Code, w.Body.String())
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Query().Get("state") != "xyz" {
		t.Errorf("state not echoed: %s", loc.Query().Get("state"))
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %s", w.Header().Get("Location"))
	}

	// 5) Exchange code for tokens.
	tokForm := url.Values{}
	tokForm.Set("grant_type", "authorization_code")
	tokForm.Set("code", code)
	tokForm.Set("client_id", clientID)
	tokForm.Set("redirect_uri", "http://127.0.0.1:54321/cb")
	tokForm.Set("code_verifier", verifier)
	req = httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tokForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("token: status=%d body=%s", w.Code, w.Body.String())
	}
	var tok map[string]any
	if err := json.NewDecoder(w.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("token response missing tokens: %+v", tok)
	}
	if tok["token_type"] != "Bearer" {
		t.Errorf("token_type = %v, want Bearer", tok["token_type"])
	}

	// 6) Replay protection: re-using the same code must fail.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/oauth/token", strings.NewReader(tokForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code replay: status=%d (want 400)", w.Code)
	}

	// 7) Use the access token on /mcp — expect any non-401 response
	//    (the MCP SDK may 4xx on bad RPC bodies but the auth gate
	//    must let us in).
	mcpReq := httptest.NewRequest("POST", "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`))
	mcpReq.Header.Set("Authorization", "Bearer "+access)
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Accept", "application/json, text/event-stream")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, mcpReq)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("/mcp 401 with valid OAuth bearer: %s", w.Body.String())
	}

	// 8) Refresh-token grant rotates the pair.
	rfForm := url.Values{}
	rfForm.Set("grant_type", "refresh_token")
	rfForm.Set("refresh_token", refresh)
	rfForm.Set("client_id", clientID)
	req = httptest.NewRequest("POST", "/oauth/token", strings.NewReader(rfForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh: status=%d body=%s", w.Code, w.Body.String())
	}
	var tok2 map[string]any
	_ = json.NewDecoder(w.Body).Decode(&tok2)
	access2, _ := tok2["access_token"].(string)
	refresh2, _ := tok2["refresh_token"].(string)
	if access2 == access {
		t.Error("refresh did not rotate access token")
	}
	if refresh2 == refresh {
		t.Error("refresh did not rotate refresh token")
	}

	// 9) Old refresh token must now be invalid.
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/oauth/token", strings.NewReader(rfForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("old refresh re-use: status=%d (want 400)", w.Code)
	}

	// 10) Revoke the (new) access token, then /mcp should 401.
	revForm := url.Values{}
	revForm.Set("token", access2)
	req = httptest.NewRequest("POST", "/oauth/revoke", strings.NewReader(revForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("revoke: status=%d", w.Code)
	}
	mcpReq = httptest.NewRequest("POST", "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	mcpReq.Header.Set("Authorization", "Bearer "+access2)
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Accept", "application/json, text/event-stream")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, mcpReq)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token still works on /mcp: status=%d", w.Code)
	}
}

func TestOAuthDiscoveryDocuments(t *testing.T) {
	tc := newTestServer(t)
	cases := []struct {
		path        string
		mustContain []string
	}{
		{"/.well-known/oauth-authorization-server", []string{
			`"authorization_endpoint":`,
			`"token_endpoint":`,
			`"registration_endpoint":`,
			`"code_challenge_methods_supported":["S256"]`,
		}},
		{"/.well-known/openid-configuration", []string{
			`"subject_types_supported":`,
			`"id_token_signing_alg_values_supported":`,
		}},
		{"/.well-known/oauth-protected-resource", []string{
			`"resource":`,
			`"authorization_servers":`,
		}},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", c.path, nil)
			w := httptest.NewRecorder()
			tc.srv.router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			for _, frag := range c.mustContain {
				if !strings.Contains(body, frag) {
					t.Errorf("missing %q in %s\nbody: %s", frag, c.path, body)
				}
			}
		})
	}
}

func TestOAuthAudienceBinding(t *testing.T) {
	tc := newTestServer(t)
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")

	// Register + walk through the flow with resource bound to a
	// DIFFERENT host than the test server (httptest = "example.com").
	regBody := `{
        "client_name": "Test",
        "redirect_uris": ["http://127.0.0.1:33445/cb"],
        "token_endpoint_auth_method": "none"
    }`
	req := httptest.NewRequest("POST", "/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.30:1"
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
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
	// Resource claims a host the request is NOT actually hitting.
	form.Set("resource", "https://other.example.com/mcp")
	req = httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	loc, _ := url.Parse(w.Header().Get("Location"))
	code := loc.Query().Get("code")

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
	var tok map[string]any
	_ = json.NewDecoder(w.Body).Decode(&tok)
	access, _ := tok["access_token"].(string)

	// Token bound to other.example.com but used against example.com
	// (the httptest default host) → MUST 401.
	mcpReq := httptest.NewRequest("POST", "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	mcpReq.Header.Set("Authorization", "Bearer "+access)
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Accept", "application/json, text/event-stream")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, mcpReq)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("audience-mismatched token accepted: status=%d", w.Code)
	}
}

func TestOAuthRedirectURIRejectsHTTPNonLoopback(t *testing.T) {
	tc := newTestServer(t)
	body := `{"client_name":"x","redirect_uris":["http://attacker.com/cb"]}`
	req := httptest.NewRequest("POST", "/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.1:1"
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("public http: status=%d (want 400)", w.Code)
	}
}

// TestConsentScreenIsNativeOrva — the consent template should match
// the dashboard's visual identity (Inter font + the Orva primary
// purple wordmark) and render brand-specific glyphs for known
// integrations. When admin scope is granted, a prominent red banner
// must appear so the operator can't miss the elevated risk.
func TestConsentScreenIsNativeOrva(t *testing.T) {
	tc := newTestServer(t)
	cookie := onboardAndLogin(t, tc, "alice", "supersecret123")

	regBody := `{
		"client_name": "ChatGPT",
		"redirect_uris": ["http://127.0.0.1:33445/cb"],
		"token_endpoint_auth_method": "none"
	}`
	req := httptest.NewRequest("POST", "/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.99:1"
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("DCR: %d %s", w.Code, w.Body.String())
	}
	var dcr map[string]any
	_ = json.NewDecoder(w.Body).Decode(&dcr)
	clientID, _ := dcr["client_id"].(string)

	authURL := "/oauth/authorize?response_type=code&client_id=" + clientID +
		"&redirect_uri=" + url.QueryEscape("http://127.0.0.1:33445/cb") +
		"&code_challenge=abcXYZ&code_challenge_method=S256&state=zz"
	req = httptest.NewRequest("GET", authURL, nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("consent GET: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	mustContain := []string{
		// Native Orva chrome: same brand mark, font, and colour
		// tokens as the dashboard's Login.vue.
		"Inter:wght",
		`#12111C`,    // dashboard --color-background hex, baked into the inline CSS
		`#553F83`,    // dashboard --color-primary (the Orva purple)
		`f(x)`, // logo glyph
		">Orva<",
		// Application identity row uses the brand-aware icon class
		// chosen by iconForConsent — emerald for ChatGPT.
		`app-icon chatgpt`,
		// Granular per-scope bullets (full access by default).
		"Read functions",
		"Invoke deployed functions",
		"Create, update, and delete",
		"Manage API keys",
		// Admin scope present → red full-access banner.
		"Full administrative access",
		// Buttons and footnote.
		"Allow access",
		"Deny",
		"Settings → Connected applications",
	}
	for _, s := range mustContain {
		if !strings.Contains(body, s) {
			t.Errorf("consent screen missing: %q", s)
		}
	}

	// Must NOT inline the OLD purple-blob theme (this was the legacy
	// design that didn't match the dashboard).
	if strings.Contains(body, "linear-gradient(135deg, #7c5cff 0%") {
		t.Error("consent screen still uses the legacy off-brand gradient")
	}
}

// onboardAndLogin creates the first admin user via /auth/onboard
// (which both creates the row and sets a session_token cookie) and
// returns that cookie for subsequent requests.
func onboardAndLogin(t *testing.T, tc *testContext, username, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/onboard", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("onboard: %d %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "session_token" {
			return c
		}
	}
	t.Fatal("onboard did not set session_token cookie")
	return nil
}
