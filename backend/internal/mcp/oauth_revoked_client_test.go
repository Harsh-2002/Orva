package mcp

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/oauth"
)

// Removing an OAuth application has to stop its tokens working HERE, because
// /mcp is the surface those tokens exist for. It did not: this resolver fetched
// the client row purely to put a friendly name on the actor label and read
// straight past RevokedAt, so a retired application kept full access for the
// remaining lifetime of each access token — up to an hour after the operator
// removed it, on the screen that says it is gone.
func TestRevokedOAuthClientCannotAuthenticateToMCP(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "mcp-oauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	const clientID = "client_retired"
	const plaintext = "orva_oat_test_token_0123456789abcdef"
	now := time.Now().UTC()

	if err := db.InsertOAuthClient(&database.OAuthClient{
		ID: "row_1", ClientID: clientID, ClientName: "Retired App",
		RedirectURIs: `["https://app.example/cb"]`, GrantTypes: `["authorization_code"]`,
		ResponseTypes: `["code"]`, TokenEndpointAuthMethod: "none", Scope: "read invoke",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertOAuthAccessToken(&database.OAuthAccessToken{
		ID: "tok_1", AccessTokenHash: oauth.HashToken(plaintext),
		ClientID: clientID, UserID: 1, Scope: "read invoke",
		IssuedAt: now, AccessExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)

	// It authenticates while the application is connected, or the assertion
	// below would pass against a resolver that rejects everything.
	if _, ok := resolveOAuthAccessToken(db, plaintext, req); !ok {
		t.Fatal("a live grant for a connected application was rejected")
	}

	// The operator removes the application. The access token row is untouched
	// by this test on purpose: the point is that the CLIENT being retired is
	// enough, so a token that RevokeOAuthClient somehow missed still fails.
	if _, err := db.WriteDB().Exec(
		`UPDATE oauth_clients SET revoked_at = CURRENT_TIMESTAMP WHERE client_id = ?`, clientID); err != nil {
		t.Fatal(err)
	}

	if _, ok := resolveOAuthAccessToken(db, plaintext, req); ok {
		t.Error("a retired application's access token still authenticated to /mcp")
	}
}

// The client lookup is best-effort for a reason: the token row is what
// authenticates, so a transient read failure must not lock out a legitimate
// client. Only a positively-revoked row rejects. A grant whose client row is
// missing entirely still works, exactly as before.
func TestAMissingOAuthClientRowStillAuthenticates(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "mcp-oauth-orphan.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	const plaintext = "orva_oat_orphan_token_0123456789abcdef"
	now := time.Now().UTC()
	if err := db.InsertOAuthAccessToken(&database.OAuthAccessToken{
		ID: "tok_orphan", AccessTokenHash: oauth.HashToken(plaintext),
		ClientID: "client_that_has_no_row", UserID: 1, Scope: "read",
		IssuedAt: now, AccessExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	p, ok := resolveOAuthAccessToken(db, plaintext, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if !ok {
		t.Fatal("a grant with no client row was rejected: the lookup is a label, not an authenticator")
	}
	if p.Label != "client_that_has_no_row" {
		t.Errorf("label = %q, want the raw client id as the fallback", p.Label)
	}
}
