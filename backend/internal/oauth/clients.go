package oauth

import (
	"errors"
	"net/url"
	"strings"
)

// Errors surfaced by ValidateRegistration. Mapped to RFC 7591 §3.2.2
// error codes by the /register handler.
var (
	ErrInvalidRedirectURI       = errors.New("invalid redirect_uri")
	ErrInvalidClientMetadata    = errors.New("invalid client_metadata")
	ErrUnsupportedGrantType     = errors.New("unsupported grant_type")
	ErrUnsupportedResponseType  = errors.New("unsupported response_type")
	ErrUnsupportedAuthMethod    = errors.New("unsupported token_endpoint_auth_method")
)

// SupportedGrantTypes / SupportedResponseTypes / SupportedAuthMethods
// are advertised in the AS metadata AND enforced at registration time.
// Only the OAuth 2.1 minimum-required surface; we intentionally don't
// support the deprecated implicit grant or client_credentials (no MCP
// connector flow needs either).
var (
	SupportedGrantTypes    = []string{"authorization_code", "refresh_token"}
	SupportedResponseTypes = []string{"code"}
	SupportedAuthMethods   = []string{"none", "client_secret_basic", "client_secret_post"}
)

// ValidateRedirectURIs enforces RFC 8252 + OAuth 2.1 §1.5: every URI
// must be either HTTPS or a loopback (localhost / 127.0.0.1 / [::1]).
// Fragments are forbidden everywhere. Returns the first failure so the
// caller can surface a precise "invalid_redirect_uri" message.
func ValidateRedirectURIs(uris []string) error {
	if len(uris) == 0 {
		return ErrInvalidRedirectURI
	}
	for _, raw := range uris {
		u, err := url.Parse(raw)
		if err != nil {
			return ErrInvalidRedirectURI
		}
		if u.Fragment != "" {
			return ErrInvalidRedirectURI
		}
		host := u.Hostname()
		switch u.Scheme {
		case "https":
			// OK — any HTTPS host.
		case "http":
			// Loopback only, per RFC 8252 §7.3 + OAuth 2.1 §1.5.
			if host != "localhost" && host != "127.0.0.1" && host != "::1" {
				return ErrInvalidRedirectURI
			}
		default:
			// Custom URI schemes (e.g. mobile apps with my-app://cb)
			// are allowed by RFC 8252 §7.1. We require a non-empty
			// scheme plus *something* after the colon so "://" alone
			// is rejected. url.Parse may stash the body in Host, Path,
			// or Opaque depending on shape.
			if u.Scheme == "" {
				return ErrInvalidRedirectURI
			}
			if u.Host == "" && u.Path == "" && u.Opaque == "" {
				return ErrInvalidRedirectURI
			}
		}
	}
	return nil
}

// MatchRedirectURI does an exact match per OAuth 2.1 §3.1.2.2 (the
// "redirect URI must match exactly one of the registered URIs"). RFC
// 8252 §8.10 carves out one exception: native apps using loopback may
// vary the port between registration and authorization request — we
// honor that for http://127.0.0.1:* and http://localhost:* registered
// URIs.
func MatchRedirectURI(registered []string, requested string) bool {
	for _, r := range registered {
		if r == requested {
			return true
		}
	}
	// Loopback port-relaxation per RFC 8252 §8.10.
	reqURL, err := url.Parse(requested)
	if err != nil {
		return false
	}
	if reqURL.Scheme != "http" {
		return false
	}
	reqHost := reqURL.Hostname()
	if reqHost != "localhost" && reqHost != "127.0.0.1" && reqHost != "::1" {
		return false
	}
	for _, r := range registered {
		regURL, err := url.Parse(r)
		if err != nil {
			continue
		}
		if regURL.Scheme != "http" {
			continue
		}
		if regURL.Hostname() != reqHost {
			continue
		}
		if regURL.Path != reqURL.Path {
			continue
		}
		// Same scheme, same host, same path — accept regardless of
		// port. This is the carve-out the RFC mandates because native
		// apps can't predict their loopback port at registration.
		return true
	}
	return false
}

// ValidateGrantTypes returns an error if any requested grant_type is
// outside SupportedGrantTypes. Empty list is treated as the default
// (authorization_code only) — same convention RFC 7591 §2 uses.
func ValidateGrantTypes(grantTypes []string) error {
	if len(grantTypes) == 0 {
		return nil
	}
	supported := setOf(SupportedGrantTypes)
	for _, g := range grantTypes {
		if _, ok := supported[g]; !ok {
			return ErrUnsupportedGrantType
		}
	}
	return nil
}

// ValidateResponseTypes mirrors ValidateGrantTypes for the response_type
// metadata entry.
func ValidateResponseTypes(responseTypes []string) error {
	if len(responseTypes) == 0 {
		return nil
	}
	supported := setOf(SupportedResponseTypes)
	for _, r := range responseTypes {
		if _, ok := supported[r]; !ok {
			return ErrUnsupportedResponseType
		}
	}
	return nil
}

// ValidateAuthMethod ensures the requested token_endpoint_auth_method is
// one we'll honor at /oauth/token. Unset → "none" (the OAuth 2.1
// default for public clients).
func ValidateAuthMethod(method string) error {
	if method == "" {
		return nil
	}
	supported := setOf(SupportedAuthMethods)
	if _, ok := supported[strings.ToLower(method)]; !ok {
		return ErrUnsupportedAuthMethod
	}
	return nil
}

func setOf(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[s] = struct{}{}
	}
	return out
}
