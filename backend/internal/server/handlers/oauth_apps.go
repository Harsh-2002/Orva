package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
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

// RemoveApplication retires a registered OAuth application entirely: its
// grants, its pending authorization codes, and its ability to be authorized
// again without the operator consenting afresh.
//
// Distinct from Revoke, which ends one grant. /oauth/register is open Dynamic
// Client Registration, so an application whose grant you revoke can ask for
// another; this is how an operator says it is finished.
//
// The effect is instance-wide, and that is the intended meaning: a registered
// application is a property of the instance, not of one operator's session, so
// "this application is finished" retires it for everybody. That differs from
// Revoke, whose user_id predicate scopes the effect to the caller's own grant
// -- do not read this as the same check.
//
// The guard here therefore scopes WHO MAY ASK, not what is affected: the
// caller must have held a grant from this client, so a signed-in operator
// cannot retire a client id they merely guessed. On a single-operator
// instance, which is what Orva is today, the two are the same thing. If Orva
// ever grows real multi-user accounts this needs revisiting alongside the
// user_id invariant documented on RevokeOAuthAccessTokenByID, because one
// operator would then be able to disconnect an application another depends on.
func (h *OAuthAppsHandler) RemoveApplication(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	user, ok := userFromSessionCookie(h.DB, r)
	if !ok {
		respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "session required", reqID)
		return
	}
	clientID := r.PathValue("client_id")
	if clientID == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "client_id required", reqID)
		return
	}

	// Ownership is "has this user ever held a grant from this client", not
	// "does one still work". Revoking a grant is the step most operators take
	// first, and it is what removes the app from the active list -- gating on
	// that list would 404 exactly the person who came back for this button.
	owned, err := h.DB.UserHasGrantForClient(user.ID, clientID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to look up connected apps", reqID)
		return
	}
	if !owned {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "no such connected app", reqID)
		return
	}

	if err := h.DB.RevokeOAuthClient(clientID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "no such connected app", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to remove application", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"status": "removed", "client_id": clientID})
}
