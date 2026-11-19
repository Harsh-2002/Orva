package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagedChangePassword(t *testing.T) {
	f := newManagedOpsFixture(t)
	_, err := f.db.WriteDB().Exec(`ALTER TABLE sessions ADD COLUMN id TEXT; CREATE UNIQUE INDEX idx_sessions_identity ON sessions(id)`)
	managedMust(t, err)
	const username, previous, replacement = "password-fixture", "old-test-password", "new-test-password"
	user, err := f.db.CreateUser(username, previous)
	managedMust(t, err)
	current, err := f.db.CreateSession(user.ID, time.Hour)
	managedMust(t, err)
	other, err := f.db.CreateSession(user.ID, time.Hour)
	managedMust(t, err)
	cache := map[string]bool{current.Token: true, other.Token: true}
	mux := http.NewServeMux()
	mountManagedWebAuth(mux, f.db, &managedRouteDeps{InvalidateSession: func(token string) {
		delete(cache, token)
	}})
	send := func(method, path, token, origin, bearer string, body any, want int) {
		t.Helper()
		raw, err := json.Marshal(body)
		managedMust(t, err)
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if bearer != "" {
			req.Header.Set("X-Orva-API-Key", bearer)
		}
		resp := httptest.NewRecorder()
		mux.ServeHTTP(resp, req)
		if resp.Code != want {
			t.Fatalf("%s %s: status %d, want %d", method, path, resp.Code, want)
		}
	}
	const change, origin, me = "/api/v1/auth/change-password", "http://example.com", "/api/v1/auth/me"
	changeBody := map[string]string{"old_password": previous, "new_password": replacement}
	admin, _ := f.key("password-test-admin", `["admin","write"]`)
	send("GET", me, other.Token, "", "", nil, 200)
	send("POST", change, "", origin, admin, changeBody, 401)
	send("POST", change, current.Token, "http://unrelated.invalid", "", changeBody, 401)
	send("POST", change, current.Token, origin, "", map[string]string{"old_password": "incorrect-password", "new_password": replacement}, 400)
	send("GET", me, other.Token, "", "", nil, 200)
	send("POST", change, current.Token, origin, "", changeBody, 200)
	send("GET", me, current.Token, "", "", nil, 200)
	send("GET", me, other.Token, "", "", nil, 401)
	if !cache[current.Token] || cache[other.Token] {
		t.Fatal("password change did not evict exactly the other session")
	}
	const login = "/api/v1/auth/login"
	send("POST", login, "", origin, "", map[string]string{"username": username, "password": previous}, 401)
	send("POST", login, "", origin, "", map[string]string{"username": username, "password": replacement}, 200)
}
