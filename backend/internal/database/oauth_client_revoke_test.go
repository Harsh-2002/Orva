package database

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// oauth_clients.revoked_at was declared, loaded into OAuthClient.RevokedAt, and
// enforced at four points in the authorization flow — and nothing ever wrote
// it. The column, the read path and every check were built; the setter was
// never added, so an operator could revoke one grant but never the application
// holding it.
//
// That is not cosmetic while /oauth/register is open Dynamic Client
// Registration: an application whose grant you revoke can request another one.
func TestRevokeOAuthClientRetiresTheWholeApplication(t *testing.T) {
	db := newTestDB(t)
	const clientID = "client_retire_me"
	const otherID = "client_untouched"
	now := time.Now().UTC()

	for _, id := range []string{clientID, otherID} {
		if err := db.InsertOAuthClient(&OAuthClient{
			ID: "row_" + id, ClientID: id, ClientName: "App " + id,
			RedirectURIs: `["https://app.example/cb"]`, GrantTypes: `["authorization_code"]`,
			ResponseTypes: `["code"]`, TokenEndpointAuthMethod: "none", Scope: "read invoke",
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertOAuthAccessToken(&OAuthAccessToken{
			ID: "tok_" + id, AccessTokenHash: "ahash_" + id, RefreshTokenHash: "rhash_" + id,
			ClientID: id, UserID: 1, Scope: "read invoke",
			IssuedAt: now, AccessExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertOAuthAuthorizationCode(&OAuthAuthorizationCode{
			CodeHash: "code_" + id, ClientID: id, UserID: 1,
			RedirectURI: "https://app.example/cb", Scope: "read",
			CodeChallenge: "c", CodeChallengeMethod: "S256",
			ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.RevokeOAuthClient(clientID); err != nil {
		t.Fatalf("RevokeOAuthClient: %v", err)
	}

	// The client is retired, so it cannot be authorized again — that is what
	// the four existing checks in the authorization flow read.
	c, err := db.GetOAuthClientByID(clientID)
	if err != nil {
		t.Fatal(err)
	}
	if c.RevokedAt == nil {
		t.Error("client revoked_at is still NULL: the four checks that read it stay dead")
	}

	// Its live grants are gone, with the refresh hash nulled so a stored
	// refresh token cannot even match a row.
	apps, err := db.ListActiveOAuthAccessTokens(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		if app.ClientID == clientID {
			t.Errorf("a grant for the retired application is still active: %s", app.ID)
		}
	}

	// And an unexchanged code is a grant waiting to happen. Redeem is the only
	// reader, which is the operation that matters: it must find nothing.
	if _, err := db.RedeemOAuthAuthorizationCode("code_" + clientID); err == nil {
		t.Error("an unused authorization code for the retired application could still be redeemed")
	}

	// Nothing else was touched.
	other, err := db.GetOAuthClientByID(otherID)
	if err != nil {
		t.Fatal(err)
	}
	if other.RevokedAt != nil {
		t.Error("revoking one application retired another")
	}
	var stillActive bool
	for _, app := range apps {
		if app.ClientID == otherID {
			stillActive = true
		}
	}
	if !stillActive {
		t.Error("the other application's grant was revoked too")
	}
	if _, err := db.RedeemOAuthAuthorizationCode("code_" + otherID); err != nil {
		t.Errorf("the other application's authorization code was deleted: %v", err)
	}
}

// An unknown client id is "nothing to revoke", matching
// RevokeOAuthAccessTokenByID so the handler can answer 404 rather than
// reporting a removal that removed nothing.
func TestRevokeOAuthClientReportsAnUnknownClient(t *testing.T) {
	db := newTestDB(t)
	if err := db.RevokeOAuthClient("client_never_existed"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("RevokeOAuthClient(unknown) = %v, want sql.ErrNoRows", err)
	}
}

// The operator sequence that motivates removing an application is: revoke the
// grant, watch the application reconnect on its own, come back for the
// stronger control. An ownership check written against the ACTIVE grants list
// answers "no such app" at that third step, because the second step is what
// removed the evidence — so the feature is unreachable in precisely the
// situation it exists for.
func TestGrantOwnershipSurvivesRevokingTheGrant(t *testing.T) {
	db := newTestDB(t)
	const clientID = "client_comes_back"
	now := time.Now().UTC()

	if err := db.InsertOAuthAccessToken(&OAuthAccessToken{
		ID: "tok_1", AccessTokenHash: "ahash", ClientID: clientID, UserID: 7,
		Scope: "read", IssuedAt: now, AccessExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	owned, err := db.UserHasGrantForClient(7, clientID)
	if err != nil || !owned {
		t.Fatalf("owned=%v err=%v, want true before revoking", owned, err)
	}

	// Step one: the operator revokes the grant.
	if err := db.RevokeOAuthAccessTokenByID("tok_1", 7); err != nil {
		t.Fatal(err)
	}
	if apps, err := db.ListActiveOAuthAccessTokens(7); err != nil || len(apps) != 0 {
		t.Fatalf("active grants = %d err=%v, want 0: the revoke must have taken", len(apps), err)
	}

	// Step three: they come back to remove the application itself.
	owned, err = db.UserHasGrantForClient(7, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if !owned {
		t.Error("ownership was lost when the grant was revoked: removing the application is then permanently 404, which is the one flow it is for")
	}

	// And it is still scoped to the owner.
	if owned, _ := db.UserHasGrantForClient(8, clientID); owned {
		t.Error("another user was reported as owning this grant")
	}
	if owned, _ := db.UserHasGrantForClient(7, "client_unrelated"); owned {
		t.Error("ownership leaked across clients")
	}
}
