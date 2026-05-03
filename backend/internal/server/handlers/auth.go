package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// AuthHandler handles user authentication endpoints.
type AuthHandler struct {
	DB                *database.Database
	SecureCookies     bool // set when Orva is behind an HTTPS reverse proxy
	SessionMaxAgeSecs int  // 0 → default 7 days
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

// Status handles GET /auth/status — returns whether any users exist.
func (h *AuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	count, err := h.DB.CountUsers()
	if err != nil {
		respond.JSON(w, http.StatusOK, map[string]any{"has_user": false})
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"has_user": count > 0})
}

// Onboard handles POST /auth/onboard — creates the first admin user.
func (h *AuthHandler) Onboard(w http.ResponseWriter, r *http.Request) {
	count, _ := h.DB.CountUsers()
	if count > 0 {
		respond.Error(w, http.StatusConflict, "ALREADY_SETUP", "admin user already exists", "")
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
		Secure:   h.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   h.sessionMaxAge(),
	})

	respond.JSON(w, http.StatusOK, map[string]any{
		"user": user,
	})
}

// Login handles POST /auth/login — authenticates and returns session cookie.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
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
		Secure:   h.SecureCookies,
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
		Secure:   h.SecureCookies,
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
