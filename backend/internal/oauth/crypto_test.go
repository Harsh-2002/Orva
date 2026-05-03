package oauth

import "testing"

func TestVerifyPKCE_RFC7636AppendixB(t *testing.T) {
	// Canonical test vector from RFC 7636 Appendix B.
	// code_verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// code_challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	// method         = "S256"
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if !VerifyPKCE(verifier, challenge, "S256") {
		t.Fatal("RFC 7636 Appendix B vector failed to verify")
	}
}

func TestVerifyPKCE_RejectsPlainMethod(t *testing.T) {
	// OAuth 2.1 §7.5.2 forbids the deprecated "plain" method. Even if
	// the strings match, S256-only enforcement must reject "plain".
	if VerifyPKCE("same", "same", "plain") {
		t.Fatal("plain method should be rejected per OAuth 2.1 §7.5.2")
	}
}

func TestVerifyPKCE_RejectsTamperedVerifier(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if VerifyPKCE(verifier+"x", challenge, "S256") {
		t.Fatal("tampered verifier accepted — constant-time compare broken")
	}
	if VerifyPKCE(verifier, challenge+"x", "S256") {
		t.Fatal("tampered challenge accepted")
	}
}

func TestVerifyPKCE_RejectsEmpty(t *testing.T) {
	if VerifyPKCE("", "anything", "S256") {
		t.Fatal("empty verifier accepted")
	}
	if VerifyPKCE("anything", "", "S256") {
		t.Fatal("empty challenge accepted")
	}
}

func TestNewTokensHaveExpectedPrefixes(t *testing.T) {
	cases := []struct {
		name string
		gen  func() string
		pre  string
	}{
		{"access", NewAccessToken, TokenPrefixAccess},
		{"refresh", NewRefreshToken, TokenPrefixRefresh},
		{"code", NewAuthCode, TokenPrefixCode},
		{"client", NewClientID, TokenPrefixClient},
		{"secret", NewClientSecret, TokenPrefixSecret},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok := c.gen()
			if len(tok) <= len(c.pre) {
				t.Fatalf("%s: token too short: %q", c.name, tok)
			}
			if tok[:len(c.pre)] != c.pre {
				t.Fatalf("%s: missing prefix %q in %q", c.name, c.pre, tok)
			}
			// Sanity: regenerate should differ.
			if c.gen() == tok {
				t.Fatalf("%s: two consecutive tokens collided — entropy bug", c.name)
			}
		})
	}
}

func TestHashTokenIsDeterministic(t *testing.T) {
	tok := "orva_oat_abc123"
	if HashToken(tok) != HashToken(tok) {
		t.Fatal("HashToken must be deterministic")
	}
	if HashToken(tok) == HashToken(tok+"x") {
		t.Fatal("HashToken collision on close inputs — sha256 broken?")
	}
}
