package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
)

// authorizeInvoke applies the per-function auth_mode policy. Returns "" on
// success and a non-empty error code (with HTTP status set on w) on failure.
// Modes:
//
//	none          — public; always allowed
//	platform_key  — requires Orva session cookie OR X-Orva-API-Key header
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
		// Accept either path the management API accepts: session cookie OR
		// X-Orva-API-Key header. We do a *minimal* validation here — we just
		// need to know the credential is real and unexpired. Permissions are
		// not enforced at this layer because the caller already chose to
		// gate this function on platform-only access; a valid management key
		// is, by definition, allowed to invoke.
		if cookie, err := r.Cookie("session_token"); err == nil {
			if _, err := h.DB.GetSessionUser(cookie.Value); err == nil {
				return ""
			}
		}
		apiKey := r.Header.Get("X-Orva-API-Key")
		if apiKey == "" {
			writeInvokeAuthError(w, http.StatusUnauthorized, "UNAUTHORIZED",
				"this function requires an Orva session cookie or X-Orva-API-Key header")
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

// clientIP returns the best-effort identifier for rate-limit bucketing. Falls
// back to RemoteAddr's host portion if no proxy headers are present.
func clientIP(r *http.Request) string {
	// X-Forwarded-For is comma-separated; the leftmost entry is the
	// originating client. Trust this only if the operator runs Orva
	// behind a proxy — the alternative (RemoteAddr) is the only honest
	// signal in the no-proxy case anyway.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

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
