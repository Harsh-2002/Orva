package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Revoking an API key through the AI assistant or an MCP client left it
// working on the whole REST API.
//
// authMiddleware caches keys by hash in a sync.Map with no TTL, so a key stops
// authenticating only once something evicts it. The REST delete handler always
// did that; the MCP tool called DeleteAPIKey and nothing else, because the
// eviction was a second copy of the idea rather than the same one. /mcp
// resolves credentials from the database directly, so the key died on exactly
// the surface the operator revoked it from and lived on every other one --
// until the process restarted.
//
// This drives the router's real /mcp route, so it covers the wiring as well as
// the tool: a Deps built without the callback fails here even though the tool
// itself is correct.
func TestKeyRevokedThroughMCPStopsWorkingOnTheRESTAPI(t *testing.T) {
	tc := newTestServer(t)
	r := tc.srv.router

	const victim = "orva_victim_key_that_gets_revoked_0123456789"
	sum := sha256.Sum256([]byte(victim))
	victimHash := hex.EncodeToString(sum[:])
	if err := r.db.InsertAPIKey(&database.APIKey{
		ID: "key_victim01", KeyHash: victimHash, Prefix: "orva_vic",
		Name: "revoked-via-mcp", Permissions: `["invoke","read","write","admin"]`,
	}); err != nil {
		t.Fatal(err)
	}

	probe := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil)
		req.Header.Set("X-Orva-API-Key", victim)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// Warm the cache. Without this the test passes for the wrong reason: the
	// middleware would miss, fall through to the database, find nothing and
	// 401 whether or not anything was evicted.
	if got := probe(); got == http.StatusUnauthorized {
		t.Fatalf("the key did not authenticate before revocation (got %d)", got)
	}
	if _, cached := r.keyCache.Load(victimHash); !cached {
		t.Fatal("the key never entered the auth cache, so an eviction cannot be observed here")
	}

	// Revoke it the way the AI sidebar and any MCP client do.
	// The 2026-07-28 transport carries the handshake in params._meta on every
	// request rather than in a session, so a bare tools/call is rejected as
	// invalid params before it reaches the tool.
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "delete_api_key",
			"arguments": map[string]any{"key_id": "key_victim01", "confirm": true},
			"_meta": map[string]any{
				mcpsdk.MetaKeyProtocolVersion:    "2026-07-28",
				mcpsdk.MetaKeyClientCapabilities: map[string]any{},
				mcpsdk.MetaKeyClientInfo:         map[string]any{"name": "orva-revocation-test", "version": "1"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	req.Host = "orva.test"
	req.Header.Set("Authorization", "Bearer "+tc.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "delete_api_key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("tools/call delete_api_key returned %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatalf("delete_api_key reported an error: %s", w.Body.String())
	}

	if got := probe(); got != http.StatusUnauthorized {
		t.Errorf("a revoked key still authenticated: GET /api/v1/functions returned %d, want 401", got)
	}
	if _, cached := r.keyCache.Load(victimHash); cached {
		t.Error("the revoked key is still in the auth cache")
	}
}

// The TTL is the backstop for a revocation path that forgets to evict. Both
// current ones remember, so this simulates the one that does not: delete the
// row underneath the cache and leave the entry in place, exactly as an
// un-wired call site would.
func TestAStaleKeyCacheEntryExpiresOnItsOwn(t *testing.T) {
	tc := newTestServer(t)
	r := tc.srv.router

	const orphan = "orva_orphan_key_never_evicted_0123456789abc"
	sum := sha256.Sum256([]byte(orphan))
	orphanHash := hex.EncodeToString(sum[:])
	if err := r.db.InsertAPIKey(&database.APIKey{
		ID: "key_orphan01", KeyHash: orphanHash, Prefix: "orva_orp",
		Name: "orphaned", Permissions: `["invoke","read","write","admin"]`,
	}); err != nil {
		t.Fatal(err)
	}

	probe := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil)
		req.Header.Set("X-Orva-API-Key", orphan)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if got := probe(); got == http.StatusUnauthorized {
		t.Fatalf("the key did not authenticate before revocation (got %d)", got)
	}

	// Revoke at the database only -- no eviction, the bug this guards against.
	if err := r.db.DeleteAPIKey("key_orphan01"); err != nil {
		t.Fatal(err)
	}
	if got := probe(); got != http.StatusOK {
		t.Fatalf("expected the stale cache entry to still authenticate (got %d); "+
			"if this fails the test is no longer measuring the TTL", got)
	}

	// Age the entry past its TTL rather than sleeping for it.
	cached, ok := r.keyCache.Load(orphanHash)
	if !ok {
		t.Fatal("no cache entry to age")
	}
	entry := cached.(keyCacheEntry)
	entry.validUntil = time.Now().Add(-time.Second)
	r.keyCache.Store(orphanHash, entry)

	if got := probe(); got != http.StatusUnauthorized {
		t.Errorf("a stale entry past its TTL still authenticated: got %d, want 401", got)
	}
}

// Ending a session has the same two halves as revoking a key: delete the row,
// and evict the memo the auth middleware answers from. Only the first half was
// ever done, and sessionCache was declared inside authMiddleware so no handler
// could have done the second.
//
// The window mattered more here than for keys: the session branch grants
// access without consulting permissions at all, so a cookie that survives its
// own logout is full operator access on every /api/v1/* route until the memo
// ages out. For the password-change purge that is precisely the window the
// purge exists to close.
func TestEndingASessionStopsTheCookieImmediately(t *testing.T) {
	for _, tc := range []struct {
		name string
		// end revokes the session under test, given the router and the cookie.
		end func(t *testing.T, r *Router, token string, userID int64)
	}{
		{
			name: "logout",
			end: func(t *testing.T, r *Router, token string, _ int64) {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
				req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("logout returned %d: %s", w.Code, w.Body.String())
				}
			},
		},
		{
			name: "password change purges other sessions",
			end: func(t *testing.T, r *Router, token string, userID int64) {
				// A second session for the same operator does the changing, so
				// the one under test is an "other" session.
				other, err := r.db.CreateSession(userID, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				body := `{"old_password":"hunter2hunter2","new_password":"correct-horse-battery"}`
				req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(body))
				req.AddCookie(&http.Cookie{Name: "session_token", Value: other.Token})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("change-password returned %d: %s", w.Code, w.Body.String())
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newTestServer(t)
			r := ctx.srv.router

			user, err := r.db.CreateUser("operator", "hunter2hunter2")
			if err != nil {
				t.Fatal(err)
			}
			sess, err := r.db.CreateSession(user.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}

			probe := func() int {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil)
				req.AddCookie(&http.Cookie{Name: "session_token", Value: sess.Token})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				return w.Code
			}

			// Warm the memo, or the middleware just misses and 401s for a
			// reason that has nothing to do with eviction.
			if got := probe(); got == http.StatusUnauthorized {
				t.Fatalf("the session did not authenticate before revocation (got %d)", got)
			}
			if _, cached := r.sessionCache.Load(sess.Token); !cached {
				t.Fatal("the session never entered the memo, so an eviction cannot be observed here")
			}

			tc.end(t, r, sess.Token, user.ID)

			if got := probe(); got != http.StatusUnauthorized {
				t.Errorf("a revoked session cookie still authenticated: GET /api/v1/functions returned %d, want 401", got)
			}
			if _, cached := r.sessionCache.Load(sess.Token); cached {
				t.Error("the revoked session is still in the memo")
			}
		})
	}
}
