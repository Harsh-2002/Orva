package oauth

import "testing"

func TestValidateRedirectURIs(t *testing.T) {
	cases := []struct {
		name    string
		uris    []string
		wantErr bool
	}{
		{"empty rejected", []string{}, true},
		{"https ok", []string{"https://example.com/cb"}, false},
		{"http loopback ok", []string{"http://127.0.0.1:33445/cb"}, false},
		{"http localhost ok", []string{"http://localhost:9999/cb"}, false},
		{"http public rejected", []string{"http://example.com/cb"}, true},
		{"fragment rejected", []string{"https://example.com/cb#frag"}, true},
		{"custom scheme ok", []string{"my-app://callback"}, false},
		{"bare scheme rejected", []string{"://"}, true},
		{"https mixed with bad rejected", []string{"https://ok.com/cb", "http://evil.com/cb"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateRedirectURIs(c.uris)
			if (err != nil) != c.wantErr {
				t.Errorf("ValidateRedirectURIs(%v) err=%v, wantErr=%v", c.uris, err, c.wantErr)
			}
		})
	}
}

func TestMatchRedirectURI_ExactMatch(t *testing.T) {
	registered := []string{"https://example.com/cb", "https://example.com/cb2"}
	if !MatchRedirectURI(registered, "https://example.com/cb") {
		t.Error("exact match should succeed")
	}
	if MatchRedirectURI(registered, "https://example.com/other") {
		t.Error("non-matching path should fail")
	}
}

func TestMatchRedirectURI_LoopbackPortRelaxation(t *testing.T) {
	// RFC 8252 §8.10: native apps may register http://127.0.0.1/cb
	// (or any port) and authorize from a different port.
	registered := []string{"http://127.0.0.1:0/cb"}
	if !MatchRedirectURI(registered, "http://127.0.0.1:54321/cb") {
		t.Error("loopback port relaxation should accept different port on same path")
	}
	if MatchRedirectURI(registered, "http://127.0.0.1:54321/other") {
		t.Error("loopback relaxation must still require path match")
	}
	if MatchRedirectURI(registered, "https://127.0.0.1:54321/cb") {
		t.Error("loopback relaxation must not switch scheme")
	}
}

func TestMatchRedirectURI_NonLoopbackHttpNotRelaxed(t *testing.T) {
	registered := []string{"http://example.com:80/cb"}
	if MatchRedirectURI(registered, "http://example.com:8080/cb") {
		t.Error("port relaxation must only apply to loopback hosts")
	}
}

func TestValidateGrantTypes(t *testing.T) {
	if ValidateGrantTypes(nil) != nil {
		t.Error("empty grant_types should be allowed (defaults applied later)")
	}
	if ValidateGrantTypes([]string{"authorization_code", "refresh_token"}) != nil {
		t.Error("supported grants should validate")
	}
	if ValidateGrantTypes([]string{"authorization_code", "client_credentials"}) == nil {
		t.Error("unsupported client_credentials should be rejected")
	}
}

func TestValidateAuthMethod(t *testing.T) {
	for _, m := range []string{"", "none", "client_secret_basic", "client_secret_post"} {
		if ValidateAuthMethod(m) != nil {
			t.Errorf("method %q should validate", m)
		}
	}
	if ValidateAuthMethod("private_key_jwt") == nil {
		t.Error("unsupported method should be rejected")
	}
}
