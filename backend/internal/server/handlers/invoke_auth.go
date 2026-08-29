package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/urlhint"
)

// authorizeInvoke applies the per-function auth_mode policy. Returns "" on
// success and a non-empty error code (with HTTP status set on w) on failure.
// Modes:
//
//	none          — public; always allowed
//	platform_key  — requires an Orva session cookie, OR an API key (via
//	                X-Orva-API-Key or Authorization: Bearer) that carries the
//	                "invoke" permission
//	signed        — requires X-Orva-Signature: sha256=<hex(hmac(secret, ts.body))>
//	                and X-Orva-Timestamp: <unix-seconds, ±5min skew tolerance>
//
// On signed mode the request body is consumed for verification, then replaced
// on r.Body so the downstream proxy can stream it again. Bodies larger than
// the platform max (already enforced by bodySizeMiddleware) cap the cost.
func (h *InvokeHandler) authorizeInvoke(w http.ResponseWriter, r *http.Request, fn *database.Function) string {
	mode := fn.AuthMode
	if mode == "" {
		mode = database.AuthModeNone
	}

	switch mode {
	case database.AuthModeNone:
		return ""

	case database.AuthModePlatformKey:
		// Session cookie first, and unconditionally: this is the dashboard's
		// own path (its invoke client sends credentials), so keeping it ahead
		// of the key checks is what keeps the UI's blast radius zero.
		if cookie, err := r.Cookie("session_token"); err == nil {
			if _, err := h.DB.GetSessionUser(cookie.Value); err == nil {
				return ""
			}
		}
		// X-Orva-API-Key, or the Authorization: Bearer form the management
		// API also accepts. Bearer is only safe to accept here because
		// proxy.SanitizeForwardedHeaders withholds orva_-prefixed
		// Authorization values from the sandbox — otherwise authorizing a
		// function would hand it the key that did the authorizing.
		apiKey := r.Header.Get("X-Orva-API-Key")
		if apiKey == "" {
			if b := r.Header.Get("Authorization"); len(b) > 7 && strings.EqualFold(b[:7], "bearer ") {
				apiKey = strings.TrimSpace(b[7:])
			}
		}
		if apiKey == "" {
			writeInvokeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"this function requires an Orva session cookie, X-Orva-API-Key header, or Authorization: Bearer <key>")
			return "UNAUTHORIZED"
		}
		hash := sha256.Sum256([]byte(apiKey))
		key, err := h.DB.GetAPIKeyByHash(hex.EncodeToString(hash[:]))
		if err != nil {
			writeInvokeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid API key")
			return "UNAUTHORIZED"
		}
		if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
			writeInvokeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "API key expired")
			return "UNAUTHORIZED"
		}
		// /fn/ skips authMiddleware entirely, and requiredPermission never
		// returns "invoke" — so this is the only place the invoke permission
		// can be enforced for HTTP. Without it a key scoped to ["read"] can
		// POST here and cause arbitrary side effects, while the same key is
		// correctly denied invoke_function over MCP and OAuth. Fails closed:
		// PermissionsList degrades to an empty slice on malformed JSON.
		if !slices.Contains(key.PermissionsList(), "invoke") {
			slog.Warn("invoke denied: api key lacks the invoke permission",
				"function", fn.ID, "key_id", key.ID, "key_name", key.Name,
				"permissions", key.PermissionsList())
			writeInvokeAuthError(w, http.StatusForbidden, "FORBIDDEN",
				"API key lacks the invoke permission required to call this function")
			return "FORBIDDEN"
		}
		return ""

	case database.AuthModeSigned:
		// Lookup the per-function signing secret. The function owner stores
		// a value under ORVA_SIGNING_SECRET via the secrets API; if it's
		// missing the function is mis-configured, return 500 so the misconfig
		// is loud rather than silently passing every request.
		secrets, err := h.Secrets.GetForFunction(fn.ID)
		if err != nil || secrets[database.SigningSecretKey] == "" {
			writeInvokeAuthError(w, http.StatusInternalServerError, "SIGNING_NOT_CONFIGURED",
				"auth_mode=signed but ORVA_SIGNING_SECRET is not set in function secrets")
			return "SIGNING_NOT_CONFIGURED"
		}
		secret := secrets[database.SigningSecretKey]

		sigHeader := r.Header.Get(database.SignatureHeader)
		tsHeader := r.Header.Get(database.SignatureTimestamp)
		if sigHeader == "" || tsHeader == "" {
			writeInvokeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"missing X-Orva-Signature and/or X-Orva-Timestamp")
			return "UNAUTHORIZED"
		}
		ts, err := strconv.ParseInt(tsHeader, 10, 64)
		if err != nil {
			writeInvokeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid X-Orva-Timestamp")
			return "UNAUTHORIZED"
		}
		// Reject obviously stale signatures so a leaked sig can't be replayed
		// for hours. ±5min matches Stripe / GitHub / Slack convention.
		now := time.Now().Unix()
		if abs(now-ts) > 5*60 {
			writeInvokeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"X-Orva-Timestamp outside ±5min skew window")
			return "UNAUTHORIZED"
		}

		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		// Restore the body for the downstream proxy. nopCloser is a no-op so
		// the proxy can call Close() without harm.
		r.Body = io.NopCloser(strings.NewReader(string(body)))

		mac := hmac.New(sha256.New, []byte(secret))
		// Signing payload is "<ts>.<body>" — same shape Stripe uses, simple
		// to reproduce in any language.
		mac.Write([]byte(tsHeader))
		mac.Write([]byte("."))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(sigHeader), []byte(expected)) != 1 {
			writeInvokeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED", "signature mismatch")
			return "UNAUTHORIZED"
		}
		return ""
	}

	// Unknown mode — defensive default-deny (validated values should be
	// caught at the CRUD layer, but if the column ever drifts we fail closed).
	writeInvokeAuthError(w, http.StatusInternalServerError, "INTERNAL", "unknown auth_mode: "+mode)
	return "INTERNAL"
}

// clientIP is the rate-limit bucket key for both callers here — the
// per-function invoke limit and the login throttle. Both want the same thing:
// an address the caller cannot pick. urlhint.ClientIP is the single source of
// truth, shared with the OAuth DCR limiter.
func clientIP(r *http.Request) string { return urlhint.ClientIP(r) }

func writeInvokeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + jsonEscape(message) + `"}}`))
}

func jsonEscape(s string) string {
	// Cheap inline escape — avoids importing encoding/json just for tiny
	// error envelopes. Only the four characters that break a JSON string
	// literal need escaping here.
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return r.Replace(s)
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
