package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/auth"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// TestChannelE2E_TokenRejectedAtRESTAPI — channel tokens have no
// business hitting /api/v1/*. The auth middleware must explicitly
// reject `orva_chn_*` Bearer tokens with 401.
func TestChannelE2E_TokenRejectedAtRESTAPI(t *testing.T) {
	tc := newTestServer(t)

	for _, path := range []string{"/api/v1/functions", "/api/v1/system/storage", "/api/v1/channels"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer orva_chn_0123456789abcdef0123456789abcdef")
		w := httptest.NewRecorder()
		tc.srv.router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s with channel bearer: status=%d (want 401)", path, w.Code)
		}
		// And the message should be specific so a confused operator
		// gets a clear hint.
		if !strings.Contains(w.Body.String(), "channel tokens are MCP-only") {
			t.Errorf("%s: error message doesn't mention channel restriction; body=%s",
				path, w.Body.String())
		}
	}
}

// TestChannelE2E_FullFlow — create function trio, bundle two of them
// into a channel, confirm the channel token authenticates at /mcp,
// confirm the third (non-bundled) function is NOT exposed.
func TestChannelE2E_FullFlow(t *testing.T) {
	tc := newTestServer(t)

	// Seed three functions directly via the DAL (skipping the deploy
	// pipeline keeps the test focused on the channel behavior).
	mustFn := func(name string) string {
		t.Helper()
		fn := &database.Function{
			Name: name, Runtime: "python314", Entrypoint: "handler.py",
			MemoryMB: 64, CPUs: 0.5, Status: "active", AuthMode: "none",
			NetworkMode: "none", ConcurrencyPolicy: "queue",
		}
		fn.ID = newTestUUID(t)
		if err := tc.srv.db.InsertFunction(fn); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return fn.ID
	}
	emailID := mustFn("email-sender")
	summarizeID := mustFn("summarize-text")
	dangerousID := mustFn("delete-everything")
	_ = dangerousID

	// Create a channel containing email + summarize (NOT the
	// dangerous one).
	createBody, _ := json.Marshal(map[string]any{
		"name":         "support-bot",
		"function_ids": []string{emailID, summarizeID},
	})
	req := httptest.NewRequest("POST", "/api/v1/channels", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create channel: status=%d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.NewDecoder(w.Body).Decode(&created)
	connToken, _ := created["token"].(string)
	if !strings.HasPrefix(connToken, "orva_chn_") {
		t.Fatalf("token has wrong prefix: %q", connToken)
	}

	// Initialize MCP session with the channel token.
	mcpInit := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(mcpInit))
	req.Header.Set("Authorization", "Bearer "+connToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mcp initialize w/ channel token: status=%d body=%s", w.Code, w.Body.String())
	}
	sessionID := w.Header().Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatalf("no Mcp-Session-Id in initialize")
	}

	// tools/list — must contain ONLY the bundled functions, NO Orva
	// management tools.
	listReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(listReq))
	req.Header.Set("Authorization", "Bearer "+connToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("tools/list: %d %s", w.Code, body)
	}
	// Bundled function names converted to snake_case.
	for _, want := range []string{`"email_sender"`, `"summarize_text"`} {
		if !strings.Contains(body, want) {
			t.Errorf("tools/list missing %q\nbody excerpt: %s", want, truncateChannelBody(body))
		}
	}
	// Excluded function and Orva-mgmt tools must NOT be visible.
	for _, forbidden := range []string{
		`"delete_everything"`,
		`"list_functions"`,
		`"create_function"`,
		`"delete_function"`,
		`"system_health"`,
		`"get_orva_docs"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("tools/list LEAKED %q to channel token\nbody excerpt: %s",
				forbidden, truncateChannelBody(body))
		}
	}
}

// TestChannelE2E_ToolNameCollisionRejected — two function names
// that snake_case to the same MCP tool name should be rejected at
// create time with 400.
func TestChannelE2E_ToolNameCollisionRejected(t *testing.T) {
	tc := newTestServer(t)

	a := &database.Function{
		Name: "stripe-charge", Runtime: "python314", Entrypoint: "handler.py",
		MemoryMB: 64, CPUs: 0.5, Status: "active", AuthMode: "none",
		NetworkMode: "none", ConcurrencyPolicy: "queue",
	}
	b := &database.Function{
		Name: "stripe_charge", Runtime: "python314", Entrypoint: "handler.py",
		MemoryMB: 64, CPUs: 0.5, Status: "active", AuthMode: "none",
		NetworkMode: "none", ConcurrencyPolicy: "queue",
	}
	a.ID = newTestUUID(t)
	b.ID = newTestUUID(t)
	if err := tc.srv.db.InsertFunction(a); err != nil {
		t.Fatal(err)
	}
	if err := tc.srv.db.InsertFunction(b); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"name":         "stripe",
		"function_ids": []string{a.ID, b.ID},
	})
	req := httptest.NewRequest("POST", "/api/v1/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("collision: status=%d (want 400) body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "tool") || !strings.Contains(w.Body.String(), "stripe_charge") {
		t.Errorf("collision error message should name the tool: body=%s", w.Body.String())
	}
}

// TestChannelE2E_RotateInvalidatesOldToken — rotate the channel,
// confirm old token 401s and new token works.
func TestChannelE2E_RotateInvalidatesOldToken(t *testing.T) {
	tc := newTestServer(t)

	fn := &database.Function{
		Name: "echo", Runtime: "python314", Entrypoint: "handler.py",
		MemoryMB: 64, CPUs: 0.5, Status: "active", AuthMode: "none",
		NetworkMode: "none", ConcurrencyPolicy: "queue",
	}
	fn.ID = newTestUUID(t)
	if err := tc.srv.db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	// Create channel
	body, _ := json.Marshal(map[string]any{"name": "echo-only", "function_ids": []string{fn.ID}})
	req := httptest.NewRequest("POST", "/api/v1/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.NewDecoder(w.Body).Decode(&created)
	connID, _ := created["id"].(string)
	oldToken, _ := created["token"].(string)

	// Rotate.
	req = httptest.NewRequest("POST", "/api/v1/channels/"+connID+"/rotate", nil)
	tc.setAuth(req)
	w = httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	var rotated map[string]any
	_ = json.NewDecoder(w.Body).Decode(&rotated)
	newToken, _ := rotated["token"].(string)
	if newToken == oldToken {
		t.Error("rotated token equals old token")
	}

	// Old token at /mcp must 401.
	mcpReq := func(tok string) int {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		tc.srv.router.ServeHTTP(w, r)
		return w.Code
	}
	if got := mcpReq(oldToken); got != http.StatusUnauthorized {
		t.Errorf("old token after rotate: status=%d (want 401)", got)
	}
	if got := mcpReq(newToken); got == http.StatusUnauthorized {
		t.Errorf("new token after rotate: status=%d (must NOT be 401)", got)
	}
}

// TestChannelE2E_PrincipalTypeWiresUp — Principal type compiles and
// the auth Kind constants are in sync (regression guard against
// future renames that would silently break channel dispatch).
func TestChannelE2E_PrincipalTypeWiresUp(t *testing.T) {
	if auth.KindChannel != "channel" {
		t.Errorf("KindChannel = %q, want channel", auth.KindChannel)
	}
	if auth.KindAPIKey != "api_key" {
		t.Errorf("KindAPIKey = %q, want api_key", auth.KindAPIKey)
	}
	p := &auth.Principal{Kind: auth.KindChannel, Channel: &auth.ChannelRef{}}
	if p.Channel == nil {
		t.Error("ChannelRef must be settable on Principal")
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func truncateChannelBody(s string) string {
	if len(s) <= 800 {
		return s
	}
	return s[:800] + "..."
}

// newTestUUID returns a fresh UUIDv7 string for seeding test rows
// without going through the full deploy pipeline.
func newTestUUID(t *testing.T) string {
	t.Helper()
	return ids.New()
}
