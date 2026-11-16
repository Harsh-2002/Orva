package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestOperatorReferenceCapturedOnAPIKeyCacheHitAndAITurn(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "operator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	id := ids.New()
	const plaintext = "orva_dummy_operator_capture"
	sum := sha256.Sum256([]byte(plaintext))
	if err := db.InsertAPIKey(&database.APIKey{ID: id, KeyHash: hex.EncodeToString(sum[:]), Name: "dummy", Permissions: `["read"]`}); err != nil {
		t.Fatal(err)
	}
	var keyCache, sessionCache sync.Map
	seen := 0
	handler := authMiddleware(db, &keyCache, &sessionCache, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref, ok := authpkg.OperatorFromContext(r.Context())
		if !ok || ref != (authpkg.OperatorReference{Kind: authpkg.KindAPIKey, ID: id}) {
			t.Fatal("authenticated key identity was absent or forged by a header")
		}
		aiRef, ok := authpkg.OperatorFromContext(turnCtx(r))
		if !ok || aiRef != ref {
			t.Fatal("AI turn context lost authenticated provenance")
		}
		seen++
		w.WriteHeader(http.StatusNoContent)
	}))
	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil)
		req.Header.Set("X-Orva-API-Key", plaintext)
		req.Header.Set("X-Orva-Operator-ID", ids.New())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatal("legacy read authentication changed")
		}
	}
	if seen != 2 {
		t.Fatal("cache hit did not reach the handler")
	}
}

func TestLegacyBrowserSessionCannotInheritOperatorReference(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser("dummy-operator", "test-only-password")
	if err != nil {
		t.Fatal(err)
	}
	session, err := db.CreateSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var keyCache, sessionCache sync.Map
	handler := authMiddleware(db, &keyCache, &sessionCache, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authpkg.OperatorFromContext(r.Context()); ok {
			t.Fatal("cookie prefix or inherited principal became a stable session reference")
		}
		if a := ActorFromContext(r.Context()); a == nil || a.Type != "session" {
			t.Fatal("legacy session authentication changed")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for range 2 {
		ctx := authpkg.WithOperator(context.Background(), &authpkg.Principal{Kind: authpkg.KindAPIKey, ID: ids.New()})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/functions", nil).WithContext(ctx)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: session.Token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatal("legacy session no longer authenticated")
		}
	}
}
