package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// ChannelHandler powers /api/v1/channels. A channel is a named
// bundle of N functions plus a static bearer token; presenting the
// token at /mcp exposes only those functions as MCP tools.
type ChannelHandler struct {
	DB *database.Database
}

// channelTokenPlaintext returns "orva_chn_<32 hex>" — 128 bits of
// entropy, prefix lets the auth dispatcher route without a DB lookup.
func channelTokenPlaintext() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "orva_chn_" + hex.EncodeToString(raw), nil
}

// channelTokenPrefix returns the 16-char public prefix used in UI
// list views: "orva_chn_" (9 chars) + 7 hex chars from the random body.
func channelTokenPrefix(plaintext string) string {
	if len(plaintext) > 16 {
		return plaintext[:16]
	}
	return plaintext
}

func hashChannelToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ── request/response types ───────────────────────────────────────────

type createChannelRequest struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	FunctionIDs  []string          `json:"function_ids"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
	ExpiresInDays *int             `json:"expires_in_days,omitempty"`
}

type updateChannelRequest struct {
	Name        *string    `json:"name,omitempty"`
	Description *string    `json:"description,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type setChannelFunctionsRequest struct {
	FunctionIDs  []string          `json:"function_ids"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
}

type channelListItem struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Prefix        string     `json:"prefix"`
	FunctionCount int        `json:"function_count"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
}

// ── handlers ─────────────────────────────────────────────────────────

// List handles GET /api/v1/channels.
func (h *ChannelHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rows, err := h.DB.ListChannels()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list channels", reqID)
		return
	}
	out := make([]channelListItem, 0, len(rows))
	for _, c := range rows {
		n, _ := h.DB.CountChannelFunctions(c.ID)
		out = append(out, channelListItem{
			ID:            c.ID,
			Name:          c.Name,
			Description:   c.Description,
			Prefix:        c.TokenPrefix,
			FunctionCount: n,
			ExpiresAt:     c.ExpiresAt,
			CreatedAt:     c.CreatedAt,
			LastUsedAt:    c.LastUsedAt,
		})
	}
	respond.JSON(w, http.StatusOK, map[string]any{"channels": out})
}

// Create handles POST /api/v1/channels. Returns the plaintext token
// in the response body — the ONLY time it leaves the server.
func (h *ChannelHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	var req createChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if req.Name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	if len(req.FunctionIDs) == 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "at least one function_id is required", reqID)
		return
	}
	// Tool-name collision guard. If two bundled functions both map to
	// the same MCP tool name (e.g. "stripe-charge" + "stripe_charge"),
	// reject up-front with a clear message.
	if err := h.DB.CheckChannelToolNameCollision(req.FunctionIDs); err != nil {
		if errors.Is(err, database.ErrToolNameCollision) {
			respond.Error(w, http.StatusBadRequest, "TOOL_NAME_COLLISION", err.Error(), reqID)
			return
		}
		respond.Error(w, http.StatusBadRequest, "INVALID_FUNCTION", err.Error(), reqID)
		return
	}

	// Resolve expiry: explicit ExpiresAt wins; ExpiresInDays is the UI shortcut.
	if req.ExpiresAt == nil && req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		t := time.Now().UTC().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
		req.ExpiresAt = &t
	}

	plaintext, err := channelTokenPlaintext()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to generate token", reqID)
		return
	}

	channel := &database.Channel{
		ID:          ids.New(),
		Name:        req.Name,
		Description: req.Description,
		TokenHash:   hashChannelToken(plaintext),
		TokenPrefix: channelTokenPrefix(plaintext),
		ExpiresAt:   req.ExpiresAt,
	}
	if err := h.DB.InsertChannel(channel); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create channel", reqID)
		return
	}
	// added_by_actor_id stays empty for v1 — the auth middleware
	// stashes the Actor in request context but the handlers package
	// can't import the server package without a cycle. Audit field
	// will be populated in a follow-up that lifts Actor into a shared
	// context package.
	actorID := ""
	if err := h.DB.SetChannelFunctions(channel.ID, req.FunctionIDs, req.Descriptions, actorID); err != nil {
		// Roll back — the DAL doesn't wrap the create+set in one
		// transaction, so we delete the channel we just made if the
		// junction insert fails. The CASCADE on channels
		// deletes any partial junction rows.
		_ = h.DB.DeleteChannel(channel.ID)
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to bind functions", reqID)
		return
	}

	respond.JSON(w, http.StatusCreated, map[string]any{
		"id":            channel.ID,
		"name":          channel.Name,
		"description":   channel.Description,
		"token":         plaintext,
		"prefix":        channel.TokenPrefix,
		"function_ids":  req.FunctionIDs,
		"expires_at":    channel.ExpiresAt,
		"created_at":    channel.CreatedAt,
	})
}

// Get handles GET /api/v1/channels/{id}.
func (h *ChannelHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	c, err := h.DB.GetChannelByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "channel not found", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to load channel", reqID)
		return
	}
	cfRows, _ := h.DB.ListChannelFunctionRecords(c.ID)
	respond.JSON(w, http.StatusOK, map[string]any{
		"id":             c.ID,
		"name":           c.Name,
		"description":    c.Description,
		"prefix":         c.TokenPrefix,
		"function_ids":   c.FunctionIDs,
		"functions":      cfRows,
		"expires_at":     c.ExpiresAt,
		"created_at":     c.CreatedAt,
		"updated_at":     c.UpdatedAt,
		"last_used_at":   c.LastUsedAt,
	})
}

// Update handles PATCH /api/v1/channels/{id} — name/description/expiry.
func (h *ChannelHandler) Update(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	c, err := h.DB.GetChannelByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "channel not found", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "load failed", reqID)
		return
	}

	var req updateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	name := c.Name
	if req.Name != nil {
		name = *req.Name
	}
	desc := c.Description
	if req.Description != nil {
		desc = *req.Description
	}
	expires := c.ExpiresAt
	if req.ExpiresAt != nil {
		expires = req.ExpiresAt
	}
	if err := h.DB.UpdateChannelMetadata(id, name, desc, expires); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "update failed", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "description": desc, "expires_at": expires})
}

// SetFunctions handles PUT /api/v1/channels/{id}/functions —
// replace the function set. Tool-name collisions rejected with 400.
func (h *ChannelHandler) SetFunctions(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if _, err := h.DB.GetChannelByID(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "channel not found", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "load failed", reqID)
		return
	}
	var req setChannelFunctionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if err := h.DB.CheckChannelToolNameCollision(req.FunctionIDs); err != nil {
		if errors.Is(err, database.ErrToolNameCollision) {
			respond.Error(w, http.StatusBadRequest, "TOOL_NAME_COLLISION", err.Error(), reqID)
			return
		}
		respond.Error(w, http.StatusBadRequest, "INVALID_FUNCTION", err.Error(), reqID)
		return
	}
	// added_by_actor_id stays empty for v1 — the auth middleware
	// stashes the Actor in request context but the handlers package
	// can't import the server package without a cycle. Audit field
	// will be populated in a follow-up that lifts Actor into a shared
	// context package.
	actorID := ""
	if err := h.DB.SetChannelFunctions(id, req.FunctionIDs, req.Descriptions, actorID); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "update failed", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"function_ids": req.FunctionIDs})
}

// Rotate handles POST /api/v1/channels/{id}/rotate — re-issue the
// token. Returns plaintext ONCE.
func (h *ChannelHandler) Rotate(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	plaintext, err := channelTokenPlaintext()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "rng failed", reqID)
		return
	}
	if err := h.DB.RotateChannelToken(id, hashChannelToken(plaintext), channelTokenPrefix(plaintext)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "channel not found", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "rotation failed", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"id":     id,
		"token":  plaintext,
		"prefix": channelTokenPrefix(plaintext),
	})
}

// Delete handles DELETE /api/v1/channels/{id}.
func (h *ChannelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if err := h.DB.DeleteChannel(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "delete failed", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"id": id, "status": "deleted"})
}
