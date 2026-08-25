package handlers

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sessionCookie returns the session_token cookie a handler wrote, failing the
// test if it wrote none.
func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "session_token" {
			return c
		}
	}
	t.Fatal("no session_token cookie was written")
	return nil
}

// The Secure flag used to come from ORVA_SECURE_COOKIES alone, so an operator
// who put Orva behind Caddy or nginx and did not know about the env var shipped
// a 7-day full-admin session cookie without it. The request already carries the
// real scheme, so it decides.
//
// The homelab case is why this is not simply always true: on a plain http://
// LAN address a Secure cookie is never sent back, and the dashboard cannot log
// in at all.
func TestSessionCookieSecureFollowsTheRequestScheme(t *testing.T) {
	cases := []struct {
		name     string
		override bool
		tls      bool
		xfp      string
		want     bool
	}{
		{"plain http on a LAN", false, false, "", false},
		{"direct TLS", false, true, "", true},
		{"behind an https proxy", false, false, "https", true},
		{"proxy header cased oddly", false, false, "HTTPS", true},
		{"proxy reports http", false, false, "http", false},
		// The header is a list once more than one proxy touches it. Without
		// these the whole table passes against a whole-header compare, which
		// is the thing this is here to pin.
		{"chained proxies", false, false, "https, http", true},
		{"chained, no space", false, false, "https,http", true},
		{"chained from plaintext", false, false, "http, http", false},
		{"env override on plain http", true, false, "", true},
		{"override wins over an http proxy header", true, false, "http", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &AuthHandler{SecureCookies: tc.override}
			r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				r.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			w := httptest.NewRecorder()
			h.setSessionCookie(w, r, "tok", h.sessionMaxAge())

			c := sessionCookie(t, w)
			if c.Secure != tc.want {
				t.Errorf("Secure = %v, want %v", c.Secure, tc.want)
			}
			if !c.HttpOnly {
				t.Error("HttpOnly = false: the session cookie must never be readable from script")
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax", c.SameSite)
			}
			if c.Path != "/" {
				t.Errorf("Path = %q, want /", c.Path)
			}
		})
	}
}

// The logout clear was written out as its own http.Cookie literal and had
// drifted from the three that set the cookie: no Secure, no SameSite. A
// clearing cookie whose attributes do not match the one being replaced is the
// kind of thing that works in every browser until it does not, and there was
// no reason for it to differ in the first place.
//
// Logout only touches the database when the request actually carries a
// session, so this needs no fixture.
func TestLogoutClearsWithTheAttributesItSet(t *testing.T) {
	h := &AuthHandler{}
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()

	h.Logout(w, r)

	c := sessionCookie(t, w)
	if c.Value != "" || c.MaxAge >= 0 {
		t.Fatalf("logout cookie = %q maxage %d, want an expiring empty value", c.Value, c.MaxAge)
	}
	if !c.Secure {
		t.Error("Secure = false on an https request: the clear must carry the same attributes as the cookie it replaces")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly = false")
	}
}

// The three handlers that hand out a session must actually go through the
// helper. Testing setSessionCookie alone would pass while one of them quietly
// re-inlined its own http.Cookie literal -- which is how the logout copy came
// to be missing two attributes in the first place.
func TestSessionHandlersAllRouteThroughTheHelper(t *testing.T) {
	db := newTestDB(t)
	h := &AuthHandler{DB: db}

	user, err := db.CreateUser("operator", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.CreateSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Every one of these arrives over TLS, so every one must set Secure.
	login := httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"username":"operator","password":"correct-horse-battery"}`))
	refresh := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	refresh.AddCookie(&http.Cookie{Name: "session_token", Value: sess.Token})
	logout := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)

	for _, tc := range []struct {
		name string
		run  func(w http.ResponseWriter)
	}{
		{"Login", func(w http.ResponseWriter) { h.Login(w, login) }},
		{"Refresh", func(w http.ResponseWriter) { h.Refresh(w, refresh) }},
		{"Logout", func(w http.ResponseWriter) { h.Logout(w, logout) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, r := range []*http.Request{login, refresh, logout} {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			tc.run(w)
			if w.Code != http.StatusOK {
				t.Fatalf("%s returned %d: %s", tc.name, w.Code, w.Body.String())
			}
			c := sessionCookie(t, w)
			if !c.Secure {
				t.Errorf("%s wrote a cookie without Secure over TLS", tc.name)
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("%s wrote SameSite = %v, want Lax", tc.name, c.SameSite)
			}
			if !c.HttpOnly {
				t.Errorf("%s wrote a cookie without HttpOnly", tc.name)
			}
		})
	}
}
