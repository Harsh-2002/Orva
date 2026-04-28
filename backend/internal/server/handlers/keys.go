package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// KeyHandler handles API key management endpoints.
type KeyHandler struct {
	DB *database.Database
}

// createKeyRequest is the body for creating an API key.
type createKeyRequest struct {
	Name        string     `json:"name"`
	Permissions []string   `json:"permissions"`
	ExpiresAt   *time.Time `json:"expires_at"`
	// ExpiresInDays is a convenience for UI clients that don't want to compute
	// an absolute timestamp. Ignored when ExpiresAt is set.
	ExpiresInDays *int `json:"expires_in_days"`
}

// defaultKeyPermissions is what the UI gets when it doesn't specify any.
// Operators creating keys via curl can still narrow this with the request body.
var defaultKeyPermissions = []string{"invoke", "read", "write", "admin"}

// Create handles POST /api/v1/keys.
func (h *KeyHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}

	if req.Name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	if len(req.Permissions) == 0 {
		req.Permissions = defaultKeyPermissions
	}

	// Resolve expiry: explicit ExpiresAt wins; ExpiresInDays is the UI shortcut.
	if req.ExpiresAt == nil && req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		t := time.Now().UTC().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
		req.ExpiresAt = &t
	}

	// Generate random key (32 bytes = 64 hex chars).
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to generate key", reqID)
		return
	}
	plaintextKey := "orva_" + hex.EncodeToString(rawKey)

	// SHA256 hash for storage.
	hash := sha256.Sum256([]byte(plaintextKey))
	keyHash := hex.EncodeToString(hash[:])

	// Generate key ID.
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to generate key ID", reqID)
		return
	}
	keyID := "key_" + hex.EncodeToString(idBytes)

	// Marshal permissions as JSON array.
	permsJSON, err := json.Marshal(req.Permissions)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to marshal permissions", reqID)
		return
	}

	// First 12 chars of the plaintext = "orva_" + 7 hex chars; enough to
	// distinguish keys in the UI list without revealing the secret.
	prefix := plaintextKey
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}

	apiKey := &database.APIKey{
		ID:          keyID,
		KeyHash:     keyHash,
		Prefix:      prefix,
		Name:        req.Name,
		Permissions: string(permsJSON),
		ExpiresAt:   req.ExpiresAt,
	}

	if err := h.DB.InsertAPIKey(apiKey); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to create API key", reqID)
		return
	}

	respond.JSON(w, http.StatusCreated, map[string]any{
		"id":          keyID,
		"key":         plaintextKey,
		"prefix":      prefix,
		"name":        req.Name,
		"permissions": req.Permissions,
		"expires_at":  req.ExpiresAt,
		"created_at":  time.Now().UTC(),
	})
}

// List handles GET /api/v1/keys.
func (h *KeyHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	keys, err := h.DB.ListAPIKeys()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to list API keys", reqID)
		return
	}

	respond.JSON(w, http.StatusOK, map[string]any{
		"keys": keys,
	})
}

// Delete handles DELETE /api/v1/keys/{key_id}.
func (h *KeyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	keyID := extractKeyID(r.URL.Path)
	if keyID == "" {
		respond.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "missing key ID", reqID)
		return
	}

	if err := h.DB.DeleteAPIKey(keyID); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to delete API key", reqID)
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": keyID})
}

// extractKeyID extracts the key ID from path /api/v1/keys/{key_id}.
func extractKeyID(path string) string {
	const prefix = "/api/v1/keys/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(path, prefix)
	if idx := strings.Index(remainder, "/"); idx >= 0 {
		return remainder[:idx]
	}
	return remainder
}
