package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/oauth"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestOAuthOperatorReferenceUsesValidatedStoredAudience(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "operator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	const plaintext = "orva_oat_dummy_operator_capture"
	const resource = "https://operator.invalid/mcp"
	id := ids.New()
	if err := db.InsertOAuthAccessToken(&database.OAuthAccessToken{
		ID: id, AccessTokenHash: oauth.HashToken(plaintext), ClientID: "dummy-client",
		UserID: 1, Scope: "read", Resource: resource, AccessExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, resource, nil)
	request.Header.Set("X-Orva-Operator-ID", ids.New())
	p, ok := resolveOAuthAccessToken(db, plaintext, request)
	if !ok || p.ExpectedResource != resource || !p.Perms.Has("read") || p.Perms.Has("admin") {
		t.Fatal("OAuth capture changed authentication/permissions or lost the validated audience")
	}
	ref, ok := auth.OperatorFromContext(auth.WithOperator(context.Background(), p))
	if !ok || ref != (auth.OperatorReference{Kind: auth.KindOAuth, ID: id, ExpectedResource: resource}) {
		t.Fatal("OAuth provenance did not retain its exact credential/audience")
	}
	if _, ok := resolveOAuthAccessToken(db, plaintext, httptest.NewRequest(http.MethodPost, "https://other.invalid/mcp", nil)); ok {
		t.Fatal("operator capture bypassed existing audience authentication")
	}
	p.ExpectedResource = ""
	if _, ok := auth.OperatorFromContext(auth.WithOperator(context.Background(), p)); ok {
		t.Fatal("audience-less OAuth token gained publication provenance")
	}
}
