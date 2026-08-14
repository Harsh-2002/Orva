package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/proxy"
)

func seedInvokeKey(t *testing.T, db *database.Database, id, plaintext, perms string, expires *time.Time) {
	t.Helper()
	sum := sha256.Sum256([]byte(plaintext))
	if err := db.InsertAPIKey(&database.APIKey{
		ID:          id,
		KeyHash:     hex.EncodeToString(sum[:]),
		Name:        id,
		Permissions: perms,
		ExpiresAt:   expires,
	}); err != nil {
		t.Fatal(err)
	}
}

func platformKeyFn() *database.Function {
	return &database.Function{ID: "fn-guarded", Name: "guarded", AuthMode: database.AuthModePlatformKey}
}

// /fn/ bypasses authMiddleware and requiredPermission never returns "invoke",
// so authorizeInvoke is the only place the invoke permission can be enforced
// over HTTP. Without it a ["read"] key can POST here and cause side effects,
// while the same key is correctly refused invoke_function over MCP.
func TestPlatformKeyRequiresInvokePermission(t *testing.T) {
	db := newTestDB(t)
	seedInvokeKey(t, db, "key-read", "orva_readonly", `["read"]`, nil)
	seedInvokeKey(t, db, "key-invoke", "orva_invoker", `["invoke"]`, nil)
	seedInvokeKey(t, db, "key-writeadmin", "orva_writeadmin", `["write","admin"]`, nil)
	seedInvokeKey(t, db, "key-broken", "orva_broken", `not json`, nil)
	past := time.Now().Add(-time.Hour)
	seedInvokeKey(t, db, "key-expired", "orva_expired", `["invoke"]`, &past)

	h := &InvokeHandler{DB: db}

	cases := []struct {
		name     string
		header   string
		value    string
		wantCode string
		wantHTTP int
	}{
		{"read-only key is refused", "X-Orva-API-Key", "orva_readonly", "FORBIDDEN", http.StatusForbidden},
		{"invoke key passes", "X-Orva-API-Key", "orva_invoker", "", http.StatusOK},
		{"write+admin does not imply invoke", "X-Orva-API-Key", "orva_writeadmin", "FORBIDDEN", http.StatusForbidden},
		{"malformed permissions fail closed", "X-Orva-API-Key", "orva_broken", "FORBIDDEN", http.StatusForbidden},
		{"expiry is checked before permissions", "X-Orva-API-Key", "orva_expired", "UNAUTHORIZED", http.StatusUnauthorized},
		{"unknown key", "X-Orva-API-Key", "orva_nope", "UNAUTHORIZED", http.StatusUnauthorized},
		{"bearer form works", "Authorization", "Bearer orva_invoker", "", http.StatusOK},
		{"bearer respects permissions", "Authorization", "Bearer orva_readonly", "FORBIDDEN", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/fn/fn-guarded", nil)
			r.Header.Set(tc.header, tc.value)
			w := httptest.NewRecorder()

			got := h.authorizeInvoke(w, r, platformKeyFn())
			if got != tc.wantCode {
				t.Fatalf("authorizeInvoke = %q, want %q", got, tc.wantCode)
			}
			if tc.wantCode != "" && w.Code != tc.wantHTTP {
				t.Errorf("status = %d, want %d", w.Code, tc.wantHTTP)
			}
			// A 403 must be machine-distinguishable from a 401 — that
			// distinction is the whole migration story for a narrowed key.
			if tc.wantCode == "FORBIDDEN" {
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("response is not a JSON envelope: %v (%s)", err, w.Body.String())
				}
				if body.Error.Code != "FORBIDDEN" {
					t.Errorf("error.code = %q, want FORBIDDEN", body.Error.Code)
				}
			}
		})
	}
}

// Accepting Bearer at /fn/ is only safe because the sanitizer withholds
// orva_-prefixed Authorization values from the sandbox. If that ever stops
// being true, authorizing a function would hand it the authorizing key.
func TestBearerInvokeKeyNeverReachesTheSandbox(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/fn/fn-guarded", nil)
	r.Header.Set("Authorization", "Bearer orva_invoker")

	if v, ok := proxy.SanitizeForwardedHeaders(r.Header)["authorization"]; ok {
		t.Fatalf("authorization = %q reached the sandbox; the invoke credential must be withheld", v)
	}
}

// The dashboard authenticates with the session cookie, which is checked first
// and unconditionally — enforcing the permission must not touch the UI.
func TestPlatformKeySessionCookieUnaffected(t *testing.T) {
	db := newTestDB(t)
	user, err := db.CreateUser("operator", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.CreateSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	h := &InvokeHandler{DB: db}
	r := httptest.NewRequest(http.MethodPost, "/fn/fn-guarded", nil)
	r.AddCookie(&http.Cookie{Name: "session_token", Value: sess.Token})
	w := httptest.NewRecorder()

	if got := h.authorizeInvoke(w, r, platformKeyFn()); got != "" {
		t.Fatalf("authorizeInvoke = %q for a dashboard session, want \"\"", got)
	}
}

// auth_mode=none is the default and must stay public.
func TestAuthModeNoneUnaffected(t *testing.T) {
	h := &InvokeHandler{DB: newTestDB(t)}
	r := httptest.NewRequest(http.MethodPost, "/fn/open", nil)
	w := httptest.NewRecorder()

	if got := h.authorizeInvoke(w, r, &database.Function{ID: "open", AuthMode: database.AuthModeNone}); got != "" {
		t.Fatalf("authorizeInvoke = %q for auth_mode=none, want \"\"", got)
	}
}

func TestPlatformKeyNoCredential(t *testing.T) {
	h := &InvokeHandler{DB: newTestDB(t)}
	r := httptest.NewRequest(http.MethodPost, "/fn/fn-guarded", nil)
	w := httptest.NewRecorder()

	if got := h.authorizeInvoke(w, r, platformKeyFn()); got != "UNAUTHORIZED" {
		t.Fatalf("authorizeInvoke = %q with no credential, want UNAUTHORIZED", got)
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
