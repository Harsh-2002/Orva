package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
)

// sessionCacheEntry is a short-TTL memo of a successful session lookup so
// the UI dashboard doesn't hammer SQLite with GetSessionUser on every poll.
// TTL is short enough that logout propagates without explicit invalidation.
type sessionCacheEntry struct {
	validUntil time.Time // re-check DB after this
}

const sessionCacheTTL = 30 * time.Second

// authMiddleware validates API key authentication and permission checks.
// Uses an in-memory cache to avoid hitting SQLite on every request.
func authMiddleware(db *database.Database, next http.Handler) http.Handler {
	var keyCache sync.Map     // keyHash -> *database.APIKey
	var sessionCache sync.Map // token -> sessionCacheEntry

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth on public endpoints: health, UI, root redirect, auth routes.
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/system/health" {
			next.ServeHTTP(w, r)
			return
		}
		// /fn/ (invoke) and /mcp do not start with /api/ — they bypass this
		// middleware entirely. Custom routes also don't start with /api/ so
		// they are naturally unauthenticated here; per-function auth_mode is
		// enforced inside InvokeHandler.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		// Auth routes establish the session — they must not require prior auth.
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			next.ServeHTTP(w, r)
			return
		}

		// Try session cookie first (browser UI).
		if cookie, err := r.Cookie("session_token"); err == nil {
			now := time.Now()
			if cached, ok := sessionCache.Load(cookie.Value); ok {
				if entry := cached.(sessionCacheEntry); now.Before(entry.validUntil) {
					next.ServeHTTP(w, r)
					return
				}
				sessionCache.Delete(cookie.Value)
			}
			if _, err := db.GetSessionUser(cookie.Value); err == nil {
				sessionCache.Store(cookie.Value, sessionCacheEntry{validUntil: now.Add(sessionCacheTTL)})
				next.ServeHTTP(w, r)
				return
			}
		}

		// Fall back to API key header (CLI, automation).
		apiKey := r.Header.Get("X-Orva-API-Key")
		if apiKey == "" {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authenticated", RequestID(r.Context()))
			return
		}

		// Hash the key and look it up.
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := hex.EncodeToString(hash[:])

		// Check in-memory cache first.
		var key *database.APIKey
		if cached, ok := keyCache.Load(keyHash); ok {
			key = cached.(*database.APIKey)
		} else {
			var err error
			key, err = db.GetAPIKeyByHash(keyHash)
			if err != nil {
				if err == sql.ErrNoRows {
					writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid API key", RequestID(r.Context()))
				} else {
					writeAuthError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "authentication service temporarily unavailable", RequestID(r.Context()))
				}
				return
			}
			keyCache.Store(keyHash, key)
		}

		// Check expiry.
		if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
			keyCache.Delete(keyHash)
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "API key expired", RequestID(r.Context()))
			return
		}

		// Parse permissions.
		var permissions []string
		json.Unmarshal([]byte(key.Permissions), &permissions)
		permSet := make(map[string]bool, len(permissions))
		for _, p := range permissions {
			permSet[p] = true
		}

		// Determine required permission.
		requiredPerm := requiredPermission(r.Method, r.URL.Path)
		if !permSet[requiredPerm] {
			writeAuthError(w, http.StatusForbidden, "FORBIDDEN", "insufficient permissions, requires: "+requiredPerm, RequestID(r.Context()))
			return
		}

		next.ServeHTTP(w, r)

		// Update last_used_at asynchronously (db.Async so db.Close waits).
		db.Async(func() { db.UpdateAPIKeyLastUsed(keyHash) })
	})
}

// requiredPermission determines the required permission for a request.
func requiredPermission(method, path string) string {
	// Key management and pool config require "admin" permission.
	if strings.HasPrefix(path, "/api/v1/keys") {
		return "admin"
	}
	if path == "/api/v1/pool/config" && (method == http.MethodPut || method == http.MethodPost) {
		return "admin"
	}

	// GET requests require "read" permission.
	if method == http.MethodGet {
		return "read"
	}

	// POST/PUT/DELETE (non-invoke) require "write" permission.
	return "write"
}

func writeAuthError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    message,
			"request_id": requestID,
		},
	})
}
