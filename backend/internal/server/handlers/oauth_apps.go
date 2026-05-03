package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// OAuthAppsHandler powers Settings → Connected applications. Read +
// revoke for the current user's OAuth grants. We deliberately don't
// expose the access-token hash; the row ID is the identifier.
type OAuthAppsHandler struct {
	DB *database.Database
}

// List returns the current user's active OAuth grants.
func (h *OAuthAppsHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	user, ok := userFromSessionCookie(h.DB, r)
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "session required", reqID)
		return
	}
	apps, err := h.DB.ListActiveOAuthAccessTokens(user.ID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list connected apps", reqID)
		return
	}
	if apps == nil {
		apps = []*database.ConnectedApp{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"apps": apps})
}

// Revoke flips revoked_at on the named grant if it belongs to the
// caller. Idempotent at the request level: a second DELETE returns
// 404 (the row no longer matches the active-only filter), which is
// fine for a UI that already removed it.
func (h *OAuthAppsHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	user, ok := userFromSessionCookie(h.DB, r)
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "session required", reqID)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "id required", reqID)
		return
	}
	if err := h.DB.RevokeOAuthAccessTokenByID(id, user.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "no such connected app", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to revoke", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// userFromSessionCookie is shared between OAuthAppsHandler and the
// session-management handlers below. Both are session-only — they
// don't accept API-key auth — because they expose user-specific data
// keyed by the human operator, not by an automation token.
func userFromSessionCookie(db *database.Database, r *http.Request) (*database.User, bool) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		return nil, false
	}
	user, err := db.GetSessionUser(cookie.Value)
	if err != nil {
		return nil, false
	}
	return user, true
}
