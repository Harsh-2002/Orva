package handlers

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"crypto/sha256"
	"encoding/hex"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"log/slog"
	"slices"
	"strings"
)

// loginAttemptsPerMin bounds POST /api/v1/auth/login per client IP. Login sits
// behind the /api/v1/auth/ auth-middleware bypass, so without this a caller
// could brute-force credentials as fast as bcrypt allows. 10/min/IP is
// comfortable for a human (mistyped passwords) and throttles automated
// guessing to a crawl; bcrypt's cost is the second brake.
const loginAttemptsPerMin = 10

// AuthHandler handles user authentication endpoints.
type AuthHandler struct {
	DB                *database.Database
	SecureCookies     bool // set when Orva is behind an HTTPS reverse proxy
	SessionMaxAgeSecs int  // 0 → default 7 days

	// loginLimiter throttles login attempts per client IP. Lazily built so
	// existing tests that construct AuthHandler with zero-value fields keep
	// working (mirrors InvokeHandler's rateLimiter pattern).
	loginLimiterOnce sync.Once
	loginLimiter     *rateLimiter
}

func (h *AuthHandler) sessionMaxAge() int {
	if h.SessionMaxAgeSecs > 0 {
		return h.SessionMaxAgeSecs
	}
	return 7 * 24 * 60 * 60 // 604800 — 7 days
}

func (h *AuthHandler) sessionTTL() time.Duration {
	return time.Duration(h.sessionMaxAge()) * time.Second
}

// secureCookie decides the Secure flag for the session cookie.
//
// It used to be the ORVA_SECURE_COOKIES env var alone, defaulting to false,
// so an operator who put Orva behind Caddy or nginx TLS and did not know to
// set it shipped a 7-day full-admin session cookie without Secure. Any page
// could then force it onto a cleartext http:// request and anyone on the
// network path could read it. The request already tells us the real scheme
// -- urlhint derives it for the OAuth issuer and MCP paths -- so use that,
// keeping the env var as an override for setups we cannot detect.
func (h *AuthHandler) secureCookie(r *http.Request) bool {
	if h.SecureCookies {
		return true
	}
	if r != nil && r.TLS != nil {
		return true
	}
	if r != nil && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

// instanceInUse reports whether anyone has actually set this instance up,
// and what the evidence was (for the refusal log).
//
// Deliberately NOT "an API key exists": the server mints its own bootstrap
// key on every boot, so that test is true on a completely fresh install.
func (h *AuthHandler) instanceInUse() (bool, string, error) {
	keys, err := h.DB.CountOperatorAPIKeys()
	if err != nil {
		return false, "", err
	}
	if keys > 0 {
		return true, "operator-minted api keys", nil
	}
	fns, err := h.DB.CountFunctions()
	if err != nil {
		return false, "", err
	}
	if fns > 0 {
		return true, "deployed functions", nil
	}
	return false, "", nil
}

// callerHoldsAdminKey verifies an admin API key presented directly on the
// request. Onboard lives under /api/v1/auth/, which the auth middleware
// skips wholesale, so the check has to happen here.
func (h *AuthHandler) callerHoldsAdminKey(r *http.Request) bool {
	raw := r.Header.Get("X-Orva-API-Key")
	if raw == "" {
		if v := r.Header.Get("Authorization"); len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			raw = strings.TrimSpace(v[7:])
		}
	}
	if raw == "" {
		return false
	}
	sum := sha256.Sum256([]byte(raw))
	key, err := h.DB.GetAPIKeyByHash(hex.EncodeToString(sum[:]))
	if err != nil || key == nil {
		return false
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return false
	}
	return slices.Contains(key.PermissionsList(), "admin")
}

// Status handles GET /auth/status — returns whether any users exist.
func (h *AuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	count, err := h.DB.CountUsers()
	if err != nil {
		// Fail CLOSED. Reporting has_user:false on a read error advertises an
		// unclaimed instance, and /auth/onboard is inside the auth-middleware
		// bypass, so this value is the only thing standing between a stranger
		// and an admin session.
		slog.Error("auth status: user count failed", "error", err)
		respond.JSON(w, http.StatusOK, map[string]any{"has_user": true})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"has_user": count > 0})
}

// Onboard handles POST /auth/onboard — creates the first admin user.
func (h *AuthHandler) Onboard(w http.ResponseWriter, r *http.Request) {
	count, err := h.DB.CountUsers()
	if err != nil {
		// The error was discarded here, which resolved to count==0 and
		// allowed admin creation. This check is the ONLY gate on the
		// endpoint: /api/v1/auth/ is exempt from the auth middleware.
		slog.Error("onboard: user count failed", "error", err)
		respond.Error(w, http.StatusServiceUnavailable, "UNAVAILABLE",
			"cannot verify setup state; refusing to create an admin user", "")
		return
	}
	if count > 0 {
		respond.Error(w, http.StatusConflict, "ALREADY_SETUP", "admin user already exists", "")
		return
	}
	// CreateUser is only ever called from here, so an operator who uses API
	// keys exclusively leaves has_user false permanently and this endpoint
	// open to anyone who can reach the port. /api/v1/auth/ is exempt from the
	// auth middleware and a session cookie bypasses the permission model
	// entirely, so that is unauthenticated full admin.
	//
	// The gate is "is this instance IN USE", not "does an API key exist".
	// server.New mints a bootstrap-admin key on every boot, so an API key is
	// present from the very first start -- gating on CountAPIKeys made the
	// documented 30-second browser onboarding return 401 on a virgin
	// instance, because the dashboard's onboarding form has no key field and
	// none should be needed on first run.
	//
	// Operator-minted keys or deployed functions mean somebody has set this
	// up, and claiming it then requires proving control.
	inUse, why, err := h.instanceInUse()
	if err != nil {
		slog.Error("onboard: could not determine setup state", "error", err)
		respond.Error(w, http.StatusServiceUnavailable, "UNAVAILABLE",
			"cannot verify setup state; refusing to create an admin user", "")
		return
	}
	if inUse && !h.callerHoldsAdminKey(r) {
		slog.Warn("onboard refused: instance is already in use and the caller "+
			"presented no admin key", "evidence", why, "remote", r.RemoteAddr)
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"this instance is already set up; present an admin API key to create a dashboard user", "")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", "")
		return
	}
	if req.Username == "" || req.Password == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "username and password required", "")
		return
	}
	if len(req.Password) < 8 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "password must be at least 8 characters", "")
		return
	}

	user, err := h.DB.CreateUser(req.Username, req.Password)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create user", "")
		return
	}

	// Create session and set cookie.
	session, err := h.DB.CreateSession(user.ID, h.sessionTTL())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create session", "")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   h.sessionMaxAge(),
	})

	respond.JSON(w, http.StatusOK, map[string]any{
		"user": user,
	})
}

// Login handles POST /auth/login — authenticates and returns session cookie.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	// Per-IP brute-force throttle. This endpoint bypasses the API-key
	// middleware, so the limiter is the only rate control in front of the
	// password check.
	h.loginLimiterOnce.Do(func() { h.loginLimiter = newRateLimiter() })
	if !h.loginLimiter.Allow("login", clientIP(r), loginAttemptsPerMin) {
		respond.ErrorWithDetail(w, http.StatusTooManyRequests, respond.ErrorOpts{
			Code:        "RATE_LIMITED",
			Message:     "too many login attempts, slow down",
			RequestID:   r.Header.Get("X-Request-ID"),
			RetryAfterS: 60,
		})
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", "")
		return
	}

	user, err := h.DB.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password", "")
		return
	}

	session, err := h.DB.CreateSession(user.ID, h.sessionTTL())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create session", "")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   h.sessionMaxAge(),
	})

	respond.JSON(w, http.StatusOK, map[string]any{
		"user": user,
	})
}

// Me handles GET /auth/me — returns the current authenticated user along
// with the session expiry timestamp so the UI can render an "expiring
// soon" prompt without guessing.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authenticated", "")
		return
	}

	user, err := h.DB.GetSessionUser(cookie.Value)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid session", "")
		return
	}

	// Also fetch the session expiry. If this fails we still return the user
	// (Me must remain backward-compatible) but omit expires_at.
	out := map[string]any{
		"id":         user.ID,
		"username":   user.Username,
		"created_at": user.CreatedAt,
	}
	if sess, err := h.DB.GetSession(cookie.Value); err == nil {
		out["expires_at"] = sess.ExpiresAt
	}
	respond.JSON(w, http.StatusOK, out)
}

// Refresh handles POST /auth/refresh — issues a new 7-day session cookie
// for the current authenticated user and revokes the old one. The UI
// calls this when the user accepts the "session expiring soon" toast.
// Atomicity: create-then-delete; if the create fails we keep the old
// session valid; if the delete fails the old session expires naturally
// at its TTL.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authenticated", "")
		return
	}
	user, err := h.DB.GetSessionUser(cookie.Value)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid session", "")
		return
	}
	newSession, err := h.DB.CreateSession(user.ID, h.sessionTTL())
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create session", "")
		return
	}
	// Best-effort revoke of the old token.
	_ = h.DB.DeleteSession(cookie.Value)

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    newSession.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   h.sessionMaxAge(),
	})
	respond.JSON(w, http.StatusOK, map[string]any{
		"expires_at": newSession.ExpiresAt,
		"user": map[string]any{
			"id":         user.ID,
			"username":   user.Username,
			"created_at": user.CreatedAt,
		},
	})
}

// ChangePassword handles POST /auth/change-password — verifies the current
// password then replaces it with the new one. The session remains valid.
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "not authenticated", "")
		return
	}
	user, err := h.DB.GetSessionUser(cookie.Value)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid session", "")
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", "")
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION_ERROR", "old_password and new_password required", "")
		return
	}
	if len(req.NewPassword) < 8 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION_ERROR", "password must be at least 8 characters", "")
		return
	}

	if err := h.DB.UpdateUserPassword(user.ID, req.OldPassword, req.NewPassword); err != nil {
		if err == database.ErrWrongPassword {
			respond.Error(w, http.StatusBadRequest, "WRONG_PASSWORD", "current password is incorrect", "")
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to update password", "")
		return
	}

	// Revoke every OTHER session for this user so a password change actually
	// evicts anyone holding a stolen session cookie. The current session
	// (cookie.Value) is kept so the operator making the change stays logged in.
	// Best-effort: a revocation failure must not fail the (already-committed)
	// password change.
	if _, err := h.DB.DeleteUserSessionsExcept(user.ID, cookie.Value); err != nil {
		respond.JSON(w, http.StatusOK, map[string]string{
			"status":  "password_changed",
			"warning": "password updated but other sessions could not be revoked",
		})
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"status": "password_changed"})
}

// sessionTokenPrefixLen is the number of hex chars the dashboard
// receives instead of the full session token. 16 hex = 64 bits of
// entropy — unique enough to identify a row, far short of the full
// 256-bit token so a leaked URL can't authenticate.
const sessionTokenPrefixLen = 16

// Sessions handles GET /auth/sessions — list every active session for
// the current user. Each row is returned with a *prefix* of its
// token, never the full value (that would defeat the cookie's
// HTTP-only protection). The row matching the calling cookie is
// flagged `current: true` so the UI can mark it.
func (h *AuthHandler) Sessions(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "session required", "")
		return
	}
	user, err := h.DB.GetSessionUser(cookie.Value)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "session expired", "")
		return
	}
	sessions, err := h.DB.ListSessionsForUser(user.ID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list sessions", "")
		return
	}
	out := make([]map[string]any, 0, len(sessions))
	for _, s := range sessions {
		prefix := s.Token
		if len(prefix) > sessionTokenPrefixLen {
			prefix = prefix[:sessionTokenPrefixLen]
		}
		out = append(out, map[string]any{
			"prefix":     prefix,
			"created_at": s.CreatedAt,
			"expires_at": s.ExpiresAt,
			"current":    s.Token == cookie.Value,
		})
	}
	respond.JSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// RevokeSession handles DELETE /auth/sessions/{prefix} — kills the
// session whose token starts with the given prefix. By default it
// refuses to delete the calling session (use Logout for that, which
// also clears the cookie). Pass ?allow_self=1 to override — useful
// for a future "log out everywhere" button.
func (h *AuthHandler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "session required", "")
		return
	}
	user, err := h.DB.GetSessionUser(cookie.Value)
	if err != nil {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "session expired", "")
		return
	}
	prefix := r.PathValue("prefix")
	if len(prefix) < 8 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "prefix must be at least 8 hex chars", "")
		return
	}
	// Self-protection: the calling session's prefix is the same as
	// the cookie's first N chars. Refuse unless the operator opted in.
	myPrefix := cookie.Value
	if len(myPrefix) > len(prefix) {
		myPrefix = myPrefix[:len(prefix)]
	}
	if myPrefix == prefix && r.URL.Query().Get("allow_self") != "1" {
		respond.Error(w, http.StatusBadRequest, "CANT_DELETE_SELF",
			"refusing to revoke the calling session — use POST /auth/logout instead", "")
		return
	}
	if err := h.DB.DeleteSessionByPrefix(prefix, user.ID); err != nil {
		if err == database.ErrAmbiguousSessionPrefix {
			respond.Error(w, http.StatusBadRequest, "AMBIGUOUS_PREFIX", "prefix matches multiple sessions", "")
			return
		}
		// sql.ErrNoRows or any other miss
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "no such session", "")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Logout handles POST /auth/logout — clears session.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_token")
	if err == nil {
		h.DB.DeleteSession(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	respond.JSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}
