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
// store. Returns the resolved key (with permissions JSON) and true
// on success; nil + false on any failure (missing token, bad token,
// expired key, transient DB error).
func authenticateRequest(db *database.Database, r *http.Request) (*database.APIKey, bool) {
	tok := extractToken(r)
	if tok == "" || db == nil {
		return nil, false
	}
	hash := sha256.Sum256([]byte(tok))
	keyHash := hex.EncodeToString(hash[:])

	key, err := db.GetAPIKeyByHash(keyHash)
	if err != nil {
		// sql.ErrNoRows = unknown key. Anything else = DB problem; fail
		// closed (auth denied) since the agent will just retry anyway.
		_ = err == sql.ErrNoRows
		return nil, false
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return nil, false
	}

	// Touch last_used_at asynchronously — same pattern as REST middleware.
	db.Async(func() { _ = db.UpdateAPIKeyLastUsed(keyHash) })

	return key, true
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
// of the REST API emits — agents see a consistent error shape.
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
