package handlers

import (
	"net/http"

	"github.com/Harsh-2002/Orva/internal/oauth"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// OAuthAuthServerMetadataHandler serves RFC 8414 metadata at
// /.well-known/oauth-authorization-server. claude.ai and ChatGPT both
// fetch this immediately after our PRM points them at us as the
// authorization server. The response advertises every endpoint a DCR
// client needs to bootstrap an authorization-code+PKCE flow.
func OAuthAuthServerMetadataHandler(w http.ResponseWriter, r *http.Request) {
	issuer := oauth.IssuerURL(r)
	meta := oauth.BuildAuthServerMetadata(issuer, false)
	// 5-minute cache: the document changes only when we ship a new
	// release, and clients re-fetching every request adds a chatty
	// round-trip we don't need.
	w.Header().Set("Cache-Control", "public, max-age=300")
	respond.JSON(w, http.StatusOK, meta)
}

// OpenIDConfigurationHandler serves the same metadata at the OIDC
// discovery URL with the OIDC-only fields populated. ChatGPT probes
// this URL first and only falls through to oauth-authorization-server
// if it 404s — serving the alias avoids that round-trip.
func OpenIDConfigurationHandler(w http.ResponseWriter, r *http.Request) {
	issuer := oauth.IssuerURL(r)
	meta := oauth.BuildAuthServerMetadata(issuer, true)
	w.Header().Set("Cache-Control", "public, max-age=300")
	respond.JSON(w, http.StatusOK, meta)
}
