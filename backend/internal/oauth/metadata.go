package oauth

import (
	"net/http"
	"strings"
)

// IssuerURL infers the canonical "https://host" identifier for this
// Orva instance from an HTTP request. We can't hard-code it because
// operators run Orva on localhost during dev, behind reverse proxies in
// staging, and on custom domains in prod — and the OAuth `issuer`
// MUST exactly match the URL clients used to discover us, or RFC 8414
// validators (and OIDC ones) reject the metadata.
//
// We trust X-Forwarded-Proto when present (typical reverse-proxy
// setup). r.TLS being non-nil means the request hit us directly over
// TLS. Otherwise we fall back to "http", which is correct for
// localhost loopback during integration tests.
func IssuerURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// authServerMetadata is the RFC 8414 metadata document we serve at
// /.well-known/oauth-authorization-server. Same JSON also satisfies
// ChatGPT's OIDC-discovery probe at /.well-known/openid-configuration
// once we add the OIDC-only fields.
//
// We define our own struct rather than reusing oauthex.AuthServerMeta
// because the SDK marks JWKSURI as required JSON ("jwks_uri" without
// omitempty) — we don't issue signed tokens so emitting an empty
// string would mislead strict validators. RFC 8414 §2 explicitly marks
// JWKSURI as OPTIONAL.
type authServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint"`
	RevocationEndpoint                string   `json:"revocation_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	ServiceDocumentation              string   `json:"service_documentation,omitempty"`

	// OIDC-only fields. Present in the openid-configuration response
	// because ChatGPT's discovery probe looks for them, even though we
	// don't actually issue id_tokens. Treating them as informational.
	SubjectTypesSupported            []string `json:"subject_types_supported,omitempty"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported,omitempty"`
	UserinfoEndpoint                 string   `json:"userinfo_endpoint,omitempty"`
}

// BuildAuthServerMetadata returns the RFC 8414 metadata document. The
// `oidc` flag adds OIDC-only fields (subject_types_supported, id_token
// alg list) so the same builder can serve both discovery URLs.
func BuildAuthServerMetadata(issuer string, oidc bool) authServerMetadata {
	meta := authServerMetadata{
		Issuer:                            issuer,
		AuthorizationEndpoint:             issuer + "/oauth/authorize",
		TokenEndpoint:                     issuer + "/oauth/token",
		RegistrationEndpoint:              issuer + "/register",
		RevocationEndpoint:                issuer + "/oauth/revoke",
		ScopesSupported:                   SupportedScopes(),
		ResponseTypesSupported:            SupportedResponseTypes,
		GrantTypesSupported:               SupportedGrantTypes,
		TokenEndpointAuthMethodsSupported: SupportedAuthMethods,
		CodeChallengeMethodsSupported:     []string{"S256"},
		ServiceDocumentation:              "https://github.com/Harsh-2002/Orva",
	}
	if oidc {
		// ChatGPT's discovery probe requires these to validate — our
		// tokens are opaque so the values are nominal. "public" subject
		// type is the no-pairwise default; "none" id_token alg is the
		// canonical "we don't sign id_tokens" answer.
		meta.SubjectTypesSupported = []string{"public"}
		meta.IDTokenSigningAlgValuesSupported = []string{"none"}
	}
	return meta
}
