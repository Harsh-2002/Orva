package mcp

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/auth"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/oauth"
)

// permSet is a re-export of auth.PermSet kept for compatibility with
// the existing tool-registration call sites (gatedAdd uses permSet).
// New code should use auth.PermSet directly.
type permSet = auth.PermSet

// has reports whether the caller has the named permission. Mirrors
// auth.PermSet.Has so the older `perms.has(...)` lowercase calls still
// resolve.
func permSetHas(p permSet, name string) bool { return p != nil && p[name] }

// extractToken pulls the bearer token from the Authorization header
// (preferred, matches spec) or the X-Orva-API-Key header (parity with
// our REST API). Returns the empty string if neither is set.
func extractToken(r *http.Request) string {
	if a := r.Header.Get("Authorization"); a != "" {
		if rest, ok := strings.CutPrefix(a, "Bearer "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Orva-API-Key"))
}

// Token-prefix constants for the prefix-first dispatch. Each branch
// resolves a distinct credential type — see package auth's doc comment.
const (
	tokenPrefixChannel = "orva_chn_"
	tokenPrefixOAuth     = "orva_oat_" // OAuth access token plaintext
	// API keys are everything else starting with "orva_". OAuth refresh
	// token plaintexts (`orva_ort_`) never reach /mcp directly — they
	// only flow through the OAuth /token endpoint.
)

// authenticateRequest resolves the inbound bearer token to a Principal.
// Branches by token prefix BEFORE any DB lookup so an `orva_chn_*`
// channel token never hits the API-key store, and an `orva_oat_*`
// OAuth token never gets misinterpreted as a (non-existent) API key.
//
// Returns nil + false on any failure: missing token, unknown token,
// expired/revoked credential, audience mismatch (OAuth), DB error.
func authenticateRequest(db *database.Database, r *http.Request) (*auth.Principal, bool) {
	tok := extractToken(r)
	if tok == "" || db == nil {
		return nil, false
	}
	switch {
	case strings.HasPrefix(tok, tokenPrefixChannel):
		return resolveChannelToken(db, tok)
	case strings.HasPrefix(tok, tokenPrefixOAuth):
		return resolveOAuthAccessToken(db, tok, r)
	case strings.HasPrefix(tok, "orva_"):
		return resolveAPIKey(db, tok)
	default:
		// Unknown shape — refuse rather than silently falling through
		// to OAuth. Strict prefix dispatch caught more bugs in the
		// wild than the previous "try everything" pattern.
		return nil, false
	}
}

// resolveAPIKey is the operator-API-key branch.
func resolveAPIKey(db *database.Database, plaintext string) (*auth.Principal, bool) {
	hash := sha256.Sum256([]byte(plaintext))
	keyHash := hex.EncodeToString(hash[:])

	key, err := db.GetAPIKeyByHash(keyHash)
	if err != nil {
		// sql.ErrNoRows is a benign miss; anything else is a DB
		// problem we fail closed on.
		_ = (err == sql.ErrNoRows)
		return nil, false
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return nil, false
	}
	db.Async(func() { _ = db.UpdateAPIKeyLastUsed(keyHash) })

	var perms []string
	_ = json.Unmarshal([]byte(key.Permissions), &perms)
	permSet := make(auth.PermSet, len(perms))
	for _, p := range perms {
		permSet[p] = true
	}
	return &auth.Principal{
		Kind:    auth.KindAPIKey,
		ID:      key.ID,
		Label:   key.Name,
		Perms:   permSet,
		Expires: key.ExpiresAt,
	}, true
}

// resolveChannelToken is the agent-channel branch. The token is
// SHA-256 hashed; we look up the channel row and bind the function
// list onto the Principal so the MCP server can register exactly
// those tools and nothing else.
func resolveChannelToken(db *database.Database, plaintext string) (*auth.Principal, bool) {
	hash := sha256.Sum256([]byte(plaintext))
	tokenHash := hex.EncodeToString(hash[:])

	c, err := db.GetChannelByTokenHash(tokenHash)
	if err != nil {
		return nil, false
	}
	if !c.IsActive(time.Now()) {
		return nil, false
	}
	db.Async(func() { _ = db.TouchChannelLastUsed(c.ID) })

	return &auth.Principal{
		Kind:  auth.KindChannel,
		ID:    c.ID,
		Label: c.Name,
		// Perms intentionally empty — channel tokens have no Orva-
		// management permissions.
		Channel: &auth.ChannelRef{
			ID:           c.ID,
			Name:         c.Name,
			Description:  c.Description,
			Instructions: c.Instructions,
			FunctionIDs:  c.FunctionIDs,
		},
		Expires: c.ExpiresAt,
	}, true
}

// resolveOAuthAccessToken is the OAuth branch. Returns a Principal
// whose Kind=KindOAuth and Perms come from the granted scope (read /
// invoke / write / admin) via oauth.ScopeToPermissions.
//
// Audience-bound: if the token row carries a `resource` (RFC 8707),
// it must match the URL the caller is hitting. Otherwise a token
// minted for orva-A.example.com/mcp could be replayed against
// orva-B.example.com/mcp. Tokens with no resource (older/CLI-style)
// skip this check.
func resolveOAuthAccessToken(db *database.Database, plaintext string, r *http.Request) (*auth.Principal, bool) {
	row, err := db.GetOAuthAccessTokenByAccessHash(oauth.HashToken(plaintext))
	if err != nil || row == nil {
		return nil, false
	}
	if row.IsExpired(time.Now()) {
		return nil, false
	}
	if row.Resource != "" && !audienceMatches(row.Resource, r) {
		return nil, false
	}
	tokenHash := row.AccessTokenHash
	db.Async(func() { _ = db.UpdateOAuthTokenLastUsed(tokenHash) })

	permList := oauth.ScopeToPermissions(row.Scope)
	permSet := make(auth.PermSet, len(permList))
	for _, p := range permList {
		permSet[p] = true
	}

	// Pull the friendly client name. Best-effort — if the join fails,
	// we still authenticate (the row is valid), just with the raw
	// client_id as the actor label.
	label := row.ClientID
	if c, cerr := db.GetOAuthClientByID(row.ClientID); cerr == nil && c.ClientName != "" {
		label = c.ClientName
	}
	return &auth.Principal{
		Kind:    auth.KindOAuth,
		ID:      row.ID,
		Label:   label,
		Perms:   permSet,
		Expires: &row.AccessExpiresAt,
	}, true
}

// audienceMatches enforces RFC 8707 audience binding. The token's
// `resource` is the canonical URL the client passed at /authorize;
// the inbound request is what they're now calling. We compare scheme
// + host + path-prefix so a token minted for `https://x.example/mcp`
// matches both `/mcp` and `/mcp/anything`, but is rejected against a
// different host.
func audienceMatches(tokenResource string, r *http.Request) bool {
	tu, err := url.Parse(tokenResource)
	if err != nil {
		return false
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	if !strings.EqualFold(tu.Scheme, scheme) {
		return false
	}
	if !strings.EqualFold(tu.Host, r.Host) {
		return false
	}
	// Path prefix match — token bound to `/mcp` covers `/mcp/`,
	// `/mcp/whatever`, etc.
	reqPath := r.URL.Path
	tokPath := tu.Path
	if tokPath == "" || tokPath == "/" {
		return true
	}
	return reqPath == tokPath || strings.HasPrefix(reqPath, tokPath+"/")
}

// resolvePermissions runs authenticateRequest and returns the principal's
// permission set. Empty set if auth failed (callers should already
// have rejected with 401 at that point — this is a belt-and-braces
// fallback). Channel tokens always return an empty set, which is
// intentional: channels don't gate by permission, they gate by the
// function list registered as tools.
func resolvePermissions(db *database.Database, r *http.Request) permSet {
	p, ok := authenticateRequest(db, r)
	if !ok || p == nil {
		return permSet{}
	}
	return p.Perms
}

// writeAuthError writes a JSON error envelope matching what the rest
// of the REST API emits — agents see a consistent error shape. On 401
// it also emits a WWW-Authenticate header per RFC 9728 pointing the
// MCP client at our Protected Resource Metadata document, so the
// client knows it's a static-bearer resource and skips OAuth-AS
// discovery (which would 404 against our Plaintext "404 page not
// found" body and break JSON parsing).
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate",
			`Bearer realm="orva", resource_metadata="/.well-known/oauth-protected-resource"`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
