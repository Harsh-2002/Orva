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
