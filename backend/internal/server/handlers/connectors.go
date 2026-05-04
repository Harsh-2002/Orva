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

// ConnectorHandler powers /api/v1/connectors. A connector is a named
// bundle of N functions plus a static bearer token; presenting the
// token at /mcp exposes only those functions as MCP tools.
type ConnectorHandler struct {
	DB *database.Database
}

// connectorTokenPlaintext returns "orva_aco_<32 hex>" — 128 bits of
// entropy, prefix lets the auth dispatcher route without a DB lookup.
func connectorTokenPlaintext() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "orva_aco_" + hex.EncodeToString(raw), nil
}

// connectorTokenPrefix returns the 16-char public prefix used in UI
// list views: "orva_aco_" (9 chars) + 7 hex chars from the random body.
func connectorTokenPrefix(plaintext string) string {
	if len(plaintext) > 16 {
		return plaintext[:16]
	}
	return plaintext
}

func hashConnectorToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ── request/response types ───────────────────────────────────────────

type createConnectorRequest struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	FunctionIDs  []string          `json:"function_ids"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
	ExpiresInDays *int             `json:"expires_in_days,omitempty"`
}

type updateConnectorRequest struct {
	Name        *string    `json:"name,omitempty"`
	Description *string    `json:"description,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type setConnectorFunctionsRequest struct {
	FunctionIDs  []string          `json:"function_ids"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
}

type connectorListItem struct {
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

// List handles GET /api/v1/connectors.
func (h *ConnectorHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	rows, err := h.DB.ListConnectors()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list connectors", reqID)
		return
	}
	out := make([]connectorListItem, 0, len(rows))
	for _, c := range rows {
		n, _ := h.DB.CountConnectorFunctions(c.ID)
		out = append(out, connectorListItem{
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
	respond.JSON(w, http.StatusOK, map[string]any{"connectors": out})
}

// Create handles POST /api/v1/connectors. Returns the plaintext token
// in the response body — the ONLY time it leaves the server.
func (h *ConnectorHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	var req createConnectorRequest
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
	if err := h.DB.CheckConnectorToolNameCollision(req.FunctionIDs); err != nil {
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

	plaintext, err := connectorTokenPlaintext()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to generate token", reqID)
		return
	}

	connector := &database.AgentConnector{
		ID:          ids.New(),
		Name:        req.Name,
		Description: req.Description,
		TokenHash:   hashConnectorToken(plaintext),
		TokenPrefix: connectorTokenPrefix(plaintext),
		ExpiresAt:   req.ExpiresAt,
	}
	if err := h.DB.InsertConnector(connector); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create connector", reqID)
		return
	}
	// added_by_actor_id stays empty for v1 — the auth middleware
	// stashes the Actor in request context but the handlers package
	// can't import the server package without a cycle. Audit field
	// will be populated in a follow-up that lifts Actor into a shared
	// context package.
	actorID := ""
	if err := h.DB.SetConnectorFunctions(connector.ID, req.FunctionIDs, req.Descriptions, actorID); err != nil {
		// Roll back — the DAL doesn't wrap the create+set in one
		// transaction, so we delete the connector we just made if the
		// junction insert fails. The CASCADE on agent_connectors
		// deletes any partial junction rows.
		_ = h.DB.DeleteConnector(connector.ID)
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to bind functions", reqID)
		return
	}

	respond.JSON(w, http.StatusCreated, map[string]any{
		"id":            connector.ID,
		"name":          connector.Name,
		"description":   connector.Description,
		"token":         plaintext,
		"prefix":        connector.TokenPrefix,
		"function_ids":  req.FunctionIDs,
		"expires_at":    connector.ExpiresAt,
		"created_at":    connector.CreatedAt,
	})
}

// Get handles GET /api/v1/connectors/{id}.
func (h *ConnectorHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	c, err := h.DB.GetConnectorByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "connector not found", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to load connector", reqID)
		return
	}
	cfRows, _ := h.DB.ListConnectorFunctionRecords(c.ID)
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

// Update handles PATCH /api/v1/connectors/{id} — name/description/expiry.
func (h *ConnectorHandler) Update(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	c, err := h.DB.GetConnectorByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "connector not found", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "load failed", reqID)
		return
	}

	var req updateConnectorRequest
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
	if err := h.DB.UpdateConnectorMetadata(id, name, desc, expires); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "update failed", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "description": desc, "expires_at": expires})
}

// SetFunctions handles PUT /api/v1/connectors/{id}/functions —
// replace the function set. Tool-name collisions rejected with 400.
func (h *ConnectorHandler) SetFunctions(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if _, err := h.DB.GetConnectorByID(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "connector not found", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "load failed", reqID)
		return
	}
	var req setConnectorFunctionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if err := h.DB.CheckConnectorToolNameCollision(req.FunctionIDs); err != nil {
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
	if err := h.DB.SetConnectorFunctions(id, req.FunctionIDs, req.Descriptions, actorID); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "update failed", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"function_ids": req.FunctionIDs})
}

// Rotate handles POST /api/v1/connectors/{id}/rotate — re-issue the
// token. Returns plaintext ONCE.
func (h *ConnectorHandler) Rotate(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	plaintext, err := connectorTokenPlaintext()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "rng failed", reqID)
		return
	}
	if err := h.DB.RotateConnectorToken(id, hashConnectorToken(plaintext), connectorTokenPrefix(plaintext)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, "NOT_FOUND", "connector not found", reqID)
			return
		}
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "rotation failed", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"id":     id,
		"token":  plaintext,
		"prefix": connectorTokenPrefix(plaintext),
	})
}

// Delete handles DELETE /api/v1/connectors/{id}.
func (h *ConnectorHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if err := h.DB.DeleteConnector(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "delete failed", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"id": id, "status": "deleted"})
}
