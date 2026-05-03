package mcp

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/oauth"
)

// permSet is the set of permission strings (read/write/admin/invoke)
// granted to a request's caller. Tool registration consults this to
// decide which tools are visible.
type permSet map[string]bool

// has reports whether the caller has the named permission.
func (p permSet) has(name string) bool { return p != nil && p[name] }

// extractToken pulls the bearer token from the Authorization header
// (preferred, matches spec) or the X-Orva-API-Key header (parity with
// our REST API). Returns the empty string if neither is set.
func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if rest, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Orva-API-Key"))
}

// authenticateRequest looks up the bearer token against the API-key
// store first, then falls through to oauth_access_tokens if the
// API-key lookup misses. Returns a resolved *APIKey-shaped value
// (synthesized from the OAuth row when that path matched) so all
// downstream code — permission checks, activity logging — stays
// uniform regardless of which credential the caller presented.
//
// Returns nil + false on any failure: missing token, unknown token in
// both stores, expired key, revoked OAuth token, transient DB error.
func authenticateRequest(db *database.Database, r *http.Request) (*database.APIKey, bool) {
	tok := extractToken(r)
	if tok == "" || db == nil {
		return nil, false
	}
	hash := sha256.Sum256([]byte(tok))
	keyHash := hex.EncodeToString(hash[:])

	key, err := db.GetAPIKeyByHash(keyHash)
	if err == nil {
		if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
			return nil, false
		}
		// Touch last_used_at asynchronously — same pattern as REST middleware.
		db.Async(func() { _ = db.UpdateAPIKeyLastUsed(keyHash) })
		return key, true
	}
	if err != sql.ErrNoRows {
		// Real DB error — fail closed.
		return nil, false
	}

	// API-key miss: try the OAuth access-token store. Browser-based
	// connectors (claude.ai web, ChatGPT) reach us through this path.
	return resolveOAuthAccessToken(db, tok)
}

// resolveOAuthAccessToken looks the bearer up as an OAuth access token
// and synthesises a *database.APIKey so the rest of the MCP layer
// (permission gating, activity feed) doesn't need to know which token
// flavor matched. ID/Name come from the OAuth client — that's what an
// operator wants to see in the audit log ("ChatGPT (auto-registered)"
// did this), not an opaque token storage ID.
func resolveOAuthAccessToken(db *database.Database, plaintext string) (*database.APIKey, bool) {
	row, err := db.GetOAuthAccessTokenByAccessHash(oauth.HashToken(plaintext))
	if err != nil || row == nil {
		return nil, false
	}
	if row.IsExpired(time.Now()) {
		return nil, false
	}

	perms := oauth.ScopeToPermissions(row.Scope)
	permsJSON, _ := json.Marshal(perms)

	// Pull the friendly client name. Best-effort — if the join fails,
	// we still authenticate (the row is valid), just with the raw
	// client_id as the actor label.
	label := row.ClientID
	if c, cerr := db.GetOAuthClientByID(row.ClientID); cerr == nil && c.ClientName != "" {
		label = c.ClientName
	}

	return &database.APIKey{
		ID:          row.ID,
		Name:        label,
		KeyHash:     row.AccessTokenHash,
		Permissions: string(permsJSON),
	}, true
}

// resolvePermissions runs authenticateRequest and returns a parsed
// permission set. Empty set if auth failed (callers should already
// have rejected with 401 at that point — this is a belt-and-braces
// fallback).
func resolvePermissions(db *database.Database, r *http.Request) permSet {
	key, ok := authenticateRequest(db, r)
	if !ok {
		return permSet{}
	}
	var list []string
	_ = json.Unmarshal([]byte(key.Permissions), &list)
	out := make(permSet, len(list))
	for _, p := range list {
		out[p] = true
	}
	return out
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
		// RFC 9728: resource_metadata is a URL the client fetches to
		// learn how to authenticate. The PRM doc itself lists no
		// OAuth servers + bearer_methods_supported=["header"], which
		// signals "send a static bearer token, no OAuth flow needed".
		w.Header().Set("WWW-Authenticate",
			`Bearer realm="orva", resource_metadata_uri="/.well-known/oauth-protected-resource"`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
