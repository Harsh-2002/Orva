package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	authpkg "github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestBrowserSessionUUIDCaptureAndCache(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser("capture-user", "test-only-password")
	if err != nil {
		t.Fatal(err)
	}
	// Production migration remains off; this fixture installs the reviewed schema shape.
	if _, err := db.WriteDB().Exec(`ALTER TABLE sessions ADD COLUMN id TEXT;
		CREATE UNIQUE INDEX idx_sessions_identity ON sessions(id)`); err != nil {
		t.Fatal(err)
	}
	session, err := db.CreateSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := db.ResolveSessionIdentityByToken(context.Background(), session.Token)
	if err != nil || identity.ID == "" || identity.ID != session.ID {
		t.Fatal("session creation did not capture a resolvable identity")
	}
	var keys, sessions sync.Map
	seen := 0
	handler := authMiddleware(db, &keys, &sessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref, ok := authpkg.OperatorFromContext(r.Context())
		if !ok || ref != (authpkg.OperatorReference{Kind: authpkg.KindSession, ID: identity.ID}) {
			t.Fatal("trusted session identity missing or replaced by author input")
		}
		actor := ActorFromContext(r.Context())
		if actor == nil || actor.ID != identity.ID || actor.Label != user.Username {
			t.Fatal("session audit identity changed on cache path")
		}
		if aiRef, ok := authpkg.OperatorFromContext(turnCtx(r)); !ok || aiRef != ref {
			t.Fatal("AI turn lost session provenance")
		}
		seen++
		w.WriteHeader(http.StatusNoContent)
	}))
	for range 2 {
		ctx := authpkg.WithOperator(context.Background(), &authpkg.Principal{Kind: authpkg.KindAPIKey, ID: ids.New()})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil).WithContext(ctx)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: session.Token})
		req.Header.Set("X-Orva-Operator-ID", ids.New())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatal("session authentication changed")
		}
	}
	if seen != 2 {
		t.Fatal("cache path did not reach handler")
	}
	entry, ok := sessions.Load(session.Token)
	if !ok || entry.(sessionCacheEntry).identityID != identity.ID {
		t.Fatal("cache omitted stable identity")
	}
}

func TestSessionCacheExpiryAndLegacyContext(t *testing.T) {
	t.Run("identity cache cannot outlive session", func(t *testing.T) {
		db, err := database.New(filepath.Join(t.TempDir(), "expiry.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := db.Migrate(); err != nil {
			t.Fatal(err)
		}
		user, err := db.CreateUser("expiry-user", "test-only-password")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.WriteDB().Exec(`ALTER TABLE sessions ADD COLUMN id TEXT;
			CREATE UNIQUE INDEX idx_sessions_identity ON sessions(id)`); err != nil {
			t.Fatal(err)
		}
		session, err := db.CreateSession(user.ID, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		var keys, sessions sync.Map
		called := false
		handler := authMiddleware(db, &keys, &sessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: session.Token})
		handler.ServeHTTP(httptest.NewRecorder(), req)
		cached, ok := sessions.Load(session.Token)
		if !called || !ok || cached.(sessionCacheEntry).validUntil.After(session.ExpiresAt) {
			t.Fatal("cached authentication can outlive the observed session")
		}
	})
	t.Run("legacy and malformed identities clear inheritance", func(t *testing.T) {
		ctx := authpkg.WithOperator(context.Background(), &authpkg.Principal{Kind: authpkg.KindAPIKey, ID: ids.New()})
		for _, entry := range []sessionCacheEntry{{}, {identityID: "invalid-id"}} {
			next := sessionContext(ctx, "short", entry)
			if _, ok := authpkg.OperatorFromContext(next); ok {
				t.Fatal("unidentified session inherited publication authority")
			}
		}
	})
}
