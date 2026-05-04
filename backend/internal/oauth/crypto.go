// Package oauth implements the Orva OAuth 2.1 authorization server: DCR,
// PKCE-protected authorization code grant, refresh-token rotation, and
// the consent flow. Together with the MCP Go SDK's auth/ middleware, this
// makes the /mcp endpoint addable as a custom connector in claude.ai web
// and ChatGPT web (both require full OAuth 2.1 — neither accepts
// user-pasted bearer tokens in their UI).
//
// Layout:
//
//	crypto.go    token + secret generation, SHA256 hashing, PKCE S256 verifier
//	scopes.go    scope parsing, scope→permSet mapping, scope intersection
//	codes.go     authorization-code minting + redemption
//	tokens.go    access/refresh token minting + rotation
//	clients.go   DCR validation + client_id minting
//	consent.go   consent-screen rendering (Go html/template)
//	handlers.go  the six HTTP handlers (register / authorize x2 / token / revoke / discovery)
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// Token / secret prefixes for plaintext bearer credentials. Storage
// IDs (oauth_clients.id, oauth_access_tokens.id, oauth_clients.client_id)
// are now UUIDv7 — see NewClientStorageID / NewTokenStorageID / NewClientID
// below. Only the credential plaintexts keep the prefixed-random form.
const (
	TokenPrefixAccess  = "orva_oat_" // OAuth Access Token
	TokenPrefixRefresh = "orva_ort_" // OAuth Refresh Token
	TokenPrefixSecret  = "ocs_"      // OAuth Client Secret
	TokenPrefixCode    = "oac_"      // OAuth Authorization Code
)

// randomBytes returns n cryptographically random bytes. Panics on
// system-entropy failure — Go's crypto/rand panics on Linux only when
// /dev/urandom is unreachable, which means the box is dead anyway.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("oauth: crypto/rand failed: " + err.Error())
	}
	return b
}

// urlSafeToken returns base64url(no-pad) of n random bytes.
// 22 chars from 16 bytes ≈ 128 bits of entropy — comfortably above the
// OAuth 2.1 §6.1 recommendation of 128 bits for authorization codes.
func urlSafeToken(byteLen int) string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(byteLen))
}

// NewAccessToken returns a fresh access token plaintext of the form
// "orva_oat_<22 url-safe>". Callers SHA256-hash before storage and
// return the plaintext to the client exactly once.
//
// These plaintexts MUST stay cryptographically random — UUIDv7 leaks
// 48 bits of timestamp and would be a credential downgrade.
func NewAccessToken() string  { return TokenPrefixAccess + urlSafeToken(16) }
func NewRefreshToken() string { return TokenPrefixRefresh + urlSafeToken(16) }
func NewAuthCode() string     { return TokenPrefixCode + urlSafeToken(24) }
func NewClientSecret() string { return TokenPrefixSecret + urlSafeToken(24) }

// NewClientID is the wire-side OAuth client identifier (RFC 7591).
// It's public, not a credential, so UUIDv7 is fine — and lining up
// with our other identifiers means client tooling that reads /register
// responses sees a familiar shape.
func NewClientID() string { return ids.New() }

// NewClientStorageID / NewTokenStorageID are UUIDv7 storage PKs for
// oauth_clients / oauth_access_tokens. Time-sortable so newest grants
// appear at the right edge of the index. Distinct from the wire-side
// client_id (NewClientID) so we could rotate the public identifier
// without rewriting FK chains — though both are now UUIDv7.
func NewClientStorageID() string { return ids.New() }
func NewTokenStorageID() string  { return ids.New() }

// HashToken returns the SHA256 hex digest of a token plaintext. We store
// only the hash; the plaintext leaves the server exactly once at issue
// time, mirroring the api_keys at-rest posture.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// VerifyPKCE verifies a code_verifier against a stored code_challenge per
// RFC 7636 §4.6 (S256 only — OAuth 2.1 §7.5.2 forbids the deprecated
// "plain" method).
//
// The challenge is BASE64URL-no-pad(SHA256(code_verifier)). We compute
// the same and compare in constant time to prevent timing-leak attacks
// against the challenge value itself.
func VerifyPKCE(codeVerifier, codeChallenge, method string) bool {
	if method != "S256" {
		// OAuth 2.1 §7.5.2 mandates S256; "plain" is forbidden.
		return false
	}
	if codeVerifier == "" || codeChallenge == "" {
		return false
	}
	// RFC 7636 §4.1: code_verifier = high-entropy cryptographic random
	// STRING using the unreserved characters [A-Z]/[a-z]/[0-9]/-._~,
	// minimum 43 chars, maximum 128 chars. We accept any string that
	// hashes to the challenge — RFC compliance is the client's job.
	sum := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(codeChallenge)) == 1
}
