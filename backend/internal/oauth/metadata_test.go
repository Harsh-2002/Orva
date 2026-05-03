package oauth

import (
	"crypto/tls"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssuerURL(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		xfproto string
		tls     bool
		want    string
	}{
		{"plain http", "localhost:8080", "", false, "http://localhost:8080"},
		{"direct tls", "orva.example.com", "", true, "https://orva.example.com"},
		{"behind proxy", "orva.example.com", "https", false, "https://orva.example.com"},
		{"proxy mixed case", "orva.example.com", "HTTPS", false, "https://orva.example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
			r.Host = c.host
			if c.xfproto != "" {
				r.Header.Set("X-Forwarded-Proto", c.xfproto)
			}
			if c.tls {
				r.TLS = &tls.ConnectionState{}
			}
			got := IssuerURL(r)
			if got != c.want {
				t.Errorf("IssuerURL = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildAuthServerMetadata_RequiredFields(t *testing.T) {
	meta := BuildAuthServerMetadata("https://orva.example.com", false)
	if meta.Issuer != "https://orva.example.com" {
		t.Errorf("issuer = %q", meta.Issuer)
	}
	if !strings.HasSuffix(meta.AuthorizationEndpoint, "/oauth/authorize") {
		t.Errorf("authorization_endpoint = %q", meta.AuthorizationEndpoint)
	}
	if !strings.HasSuffix(meta.TokenEndpoint, "/oauth/token") {
		t.Errorf("token_endpoint = %q", meta.TokenEndpoint)
	}
	if !strings.HasSuffix(meta.RegistrationEndpoint, "/register") {
		t.Errorf("registration_endpoint = %q", meta.RegistrationEndpoint)
	}
	// PKCE S256 must be advertised; OAuth 2.1 §7.5.2 forbids "plain".
	if len(meta.CodeChallengeMethodsSupported) != 1 || meta.CodeChallengeMethodsSupported[0] != "S256" {
		t.Errorf("code_challenge_methods_supported = %v, want [S256]", meta.CodeChallengeMethodsSupported)
	}
	// Non-OIDC build should NOT advertise OIDC fields.
	if len(meta.SubjectTypesSupported) != 0 {
		t.Errorf("subject_types_supported leaked into RFC 8414 output: %v", meta.SubjectTypesSupported)
	}
}

func TestBuildAuthServerMetadata_OIDCAddsRequiredFields(t *testing.T) {
	meta := BuildAuthServerMetadata("https://orva.example.com", true)
	if len(meta.SubjectTypesSupported) == 0 {
		t.Error("OIDC variant must advertise subject_types_supported")
	}
	if len(meta.IDTokenSigningAlgValuesSupported) == 0 {
		t.Error("OIDC variant must advertise id_token_signing_alg_values_supported")
	}
}

func TestBuildAuthServerMetadata_JSONRoundTrip(t *testing.T) {
	meta := BuildAuthServerMetadata("https://orva.example.com", true)
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: OIDC fields present in JSON.
	s := string(b)
	for _, key := range []string{
		`"issuer":"https://orva.example.com"`,
		`"authorization_endpoint":"https://orva.example.com/oauth/authorize"`,
		`"token_endpoint":"https://orva.example.com/oauth/token"`,
		`"registration_endpoint":"https://orva.example.com/register"`,
		`"revocation_endpoint":"https://orva.example.com/oauth/revoke"`,
		`"code_challenge_methods_supported":["S256"]`,
		`"subject_types_supported":["public"]`,
	} {
		if !strings.Contains(s, key) {
			t.Errorf("metadata JSON missing %q\nGot: %s", key, s)
		}
	}
}
