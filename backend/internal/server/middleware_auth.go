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

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
)

// sessionCacheEntry is a short-TTL memo of a successful session lookup so
// the UI dashboard doesn't hammer SQLite with GetSessionUser on every poll.
// The TTL bounds a stale entry; it does NOT make eviction unnecessary. An
// earlier version of this comment claimed it did, and on the strength of that
// sessionCache was declared inside this function where no handler could reach
// it -- so logout, revoke-device and the password-change purge each deleted a
// row and left the cookie working. The Router owns the map now and every one
// of those paths evicts.
type sessionCacheEntry struct {
	validUntil time.Time // re-check DB after this
}

const sessionCacheTTL = 30 * time.Second

// keyCacheEntry memos a successful API-key lookup, on the same short TTL as
// sessionCacheEntry above and for the same reason -- plus one more.
//
// Evicting a revoked key is a duty each delete call site has to remember, and
// the MCP tool forgot for long enough to ship: a key revoked from the AI
// sidebar went on authenticating every /api/v1/* request until the process
// restarted, because nothing here ever dropped it. Both call sites evict now,
// but the next one added inherits the same trap. A TTL is what makes that a
// thirty-second bug rather than an unbounded one.
type keyCacheEntry struct {
	key        *database.APIKey
	validUntil time.Time
}

const keyCacheTTL = 30 * time.Second

// authMiddleware validates API key authentication and permission checks.
// Uses an in-memory cache to avoid hitting SQLite on every request. keyCache is
// owned by the Router and shared so every path that revokes a key can evict its
// entry immediately; the TTL on those entries is the backstop for the one that
// forgets.
func authMiddleware(db *database.Database, keyCache, sessionCache *sync.Map, sdkAuth *sdkauth.Authenticator, next http.Handler) http.Handler {

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
		// Internal SDK endpoints authenticate with a process-signed,
		// function-scoped credential rather than an API key. The handlers
		// enforce route-specific scope. Strip the legacy caller header: the
		// verified claim is carried only in the request actor context, while
		// each handler verifies the signed credential independently.
		if strings.HasPrefix(r.URL.Path, "/api/v1/_kv/") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/_internal/") {
			caller, _ := sdkAuth.Verify(r.Header.Get("X-Orva-Internal-Token"))
			r.Header.Del("X-Orva-Caller-Function")
			actor := &Actor{Source: "sdk", Type: "scoped_token", ID: caller, Label: caller}
			next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
			return
		}
		// Worker SDK requests bearing a valid scoped credential bypass the
		// public auth gate; this lets jobs.enqueue() from inside a sandbox
		// share the /api/v1/jobs route with the dashboard. The token MUST
		// verify against the process signing key — a merely-present header is not
		// enough (a non-empty check would let any value skip authentication
		// entirely, and the downstream handler trusts this middleware to
		// have authenticated). On mismatch we do NOT short-circuit: fall
		// through to normal session/API-key auth, so a stray header on a
		// real request still authenticates and a bogus-token-only request
		// gets a 401 like any other unauthenticated caller.
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs" {
			r.Header.Del("X-Orva-Caller-Function")
			caller, err := sdkAuth.Verify(r.Header.Get("X-Orva-Internal-Token"))
			if err == nil {
				actor := &Actor{Source: "sdk", Type: "scoped_token", ID: caller, Label: caller}
				next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
				return
			}
		}
		r.Header.Del("X-Orva-Caller-Function")

		// Try session cookie first (browser UI).
		if cookie, err := r.Cookie("session_token"); err == nil {
			now := time.Now()
			if cached, ok := sessionCache.Load(cookie.Value); ok {
				if entry := cached.(sessionCacheEntry); now.Before(entry.validUntil) {
					actor := &Actor{Source: "web", Type: "session", ID: cookie.Value[:8], Label: "session"}
					next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
					return
				}
				sessionCache.Delete(cookie.Value)
			}
			if user, err := db.GetSessionUser(cookie.Value); err == nil {
				sessionCache.Store(cookie.Value, sessionCacheEntry{validUntil: now.Add(sessionCacheTTL)})
				label := "session"
				if user != nil && user.Username != "" {
					label = user.Username
				}
				actor := &Actor{Source: "web", Type: "session", ID: cookie.Value[:8], Label: label}
				next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
				return
			}
		}

		// Fall back to API key header (CLI, automation). Accept either
		// the canonical X-Orva-API-Key header or an Authorization: Bearer
		// header so CLI tools that mirror MCP convention work.
		apiKey := r.Header.Get("X-Orva-API-Key")
		if apiKey == "" {
			if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
				apiKey = strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
			}
		}
		if apiKey == "" {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authenticated", RequestID(r.Context()))
			return
		}

		// Prefix-first dispatch — channel tokens (`orva_chn_*`) and
		// OAuth access-token plaintexts (`orva_oat_*`) MUST NOT pass
		// the API-key gate. Channel tokens have no Orva-management
		// authority by design; OAuth tokens use a separate dashboard
		// path. Reject explicitly so a leaked channel token can't
		// silently fall through and confuse the caller.
		if strings.HasPrefix(apiKey, "orva_chn_") {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"channel tokens are MCP-only; use an API key for /api/v1/* requests",
				RequestID(r.Context()))
			return
		}
		if strings.HasPrefix(apiKey, "orva_oat_") || strings.HasPrefix(apiKey, "orva_ort_") {
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"OAuth access tokens are MCP-only; use an API key for /api/v1/* requests",
				RequestID(r.Context()))
			return
		}

		// Hash the key and look it up.
		hash := sha256.Sum256([]byte(apiKey))
		keyHash := hex.EncodeToString(hash[:])

		// Check in-memory cache first.
		var key *database.APIKey
		keyNow := time.Now()
		if cached, ok := keyCache.Load(keyHash); ok {
			if entry := cached.(keyCacheEntry); keyNow.Before(entry.validUntil) {
				key = entry.key
			} else {
				keyCache.Delete(keyHash)
			}
		}
		if key == nil {
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
			keyCache.Store(keyHash, keyCacheEntry{key: key, validUntil: keyNow.Add(keyCacheTTL)})
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

		// Tag the request with the resolved API-key identity so
		// downstream observers (notably the activity log) know who
		// called us.
		actor := &Actor{Source: "api", Type: "api_key", ID: key.ID, Label: key.Name}
		next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))

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
	// The in-product AI assistant operates the whole instance via tool calls
	// and stores provider keys; the entire surface is admin-only (the dashboard
	// session resolves to admin). Conversations are a shared operator space.
	if strings.HasPrefix(path, "/api/v1/ai/") {
		return "admin"
	}
	if path == "/api/v1/pool/config" && (method == http.MethodPut || method == http.MethodPost) {
		return "admin"
	}
	// Channel tokens are long-lived bearer credentials whose tools route
	// straight into invokeFunction, which bypasses the function's auth_mode
	// by design. Minting one at "write" while minting an API key needs
	// "admin" let a write-scoped key issue itself a credential outranking
	// itself: a CI key deliberately scoped without "invoke" could create a
	// channel over a payments function and then call it unsigned. Expiry is
	// opt-in, so the token also outlived the key that made it.
	if strings.HasPrefix(path, "/api/v1/channels") {
		return "admin"
	}
	// The firewall surface is instance-wide security policy: egress
	// blocklists, and the DNS every sandbox resolves through. At "write" a
	// key scoped for deploys could repoint every sandbox's resolver. Same
	// class as backup and key minting, so gate it the same way.
	if strings.HasPrefix(path, "/api/v1/firewall") {
		return "admin"
	}
	// Backup / restore touch the entire data dir; admin-only.
	if path == "/api/v1/backup" || path == "/api/v1/restore" {
		return "admin"
	}
	// Storage stats are read-only but VACUUM rewrites the live DB; gate
	// both behind admin so the whole card is operator-only and the UI
	// can hide it for non-admin keys via 403 fingerprinting.
	if path == "/api/v1/system/storage" || path == "/api/v1/system/vacuum" {
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
