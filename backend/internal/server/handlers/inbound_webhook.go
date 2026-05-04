// Package handlers — inbound webhook trigger CRUD (v0.4 C2a).
//
// The CRUD handlers live under /api/v1/functions/{fn_id}/inbound-webhooks
// and require a normal Orva session / API key. The PUBLIC trigger that
// external services POST to (POST /webhook/{id}) is separate; see
// inbound_webhook_trigger.go.
package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// InboundWebhookHandler exposes per-function CRUD for inbound triggers.
// It does NOT serve the public /webhook/{id} POST — that's owned by
// InboundTriggerHandler so the auth boundary stays explicit.
type InboundWebhookHandler struct {
	DB       *database.Database
	Registry *registry.Registry
}

// resolveFnID accepts either an ID (fn_xxx) or a friendly name and
// returns the canonical function ID. Mirrors the KV / fixtures helpers
// so /functions/shrt/inbound-webhooks works the same as
// /functions/fn_n4r39…/inbound-webhooks.
func (h *InboundWebhookHandler) resolveFnID(idOrName string) (string, bool) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return "", false
	}
	if ids.IsUUID(idOrName) {
		if _, err := h.Registry.Get(idOrName); err == nil {
			return idOrName, true
		}
	}
	if fn, err := h.DB.GetFunctionByName(idOrName); err == nil && fn != nil {
		return fn.ID, true
	}
	return "", false
}

type inboundCreateRequest struct {
	Name            string `json:"name"`
	SignatureFormat string `json:"signature_format,omitempty"`
	SignatureHeader string `json:"signature_header,omitempty"`
	Active          *bool  `json:"active,omitempty"`
}

type inboundUpdateRequest struct {
	Name            *string `json:"name,omitempty"`
	SignatureFormat *string `json:"signature_format,omitempty"`
	SignatureHeader *string `json:"signature_header,omitempty"`
	Active          *bool   `json:"active,omitempty"`
}

// List handles GET /api/v1/functions/{fn_id}/inbound-webhooks.
func (h *InboundWebhookHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	rows, err := h.DB.ListInboundWebhooksForFunction(fnID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "list inbound webhooks failed: "+err.Error(), reqID)
		return
	}
	if rows == nil {
		rows = []*database.InboundWebhook{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"inbound_webhooks": rows})
}

// Create handles POST /api/v1/functions/{fn_id}/inbound-webhooks.
// Returns the freshly minted secret in the response body — once. Get/
// List afterwards only carry the preview, mirroring outbound webhook
// + api_keys flows.
func (h *InboundWebhookHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	var req inboundCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	format := strings.TrimSpace(req.SignatureFormat)
	if format == "" {
		format = "hmac_sha256_hex"
	}
	if _, ok := database.AllowedInboundFormats[format]; !ok {
		respond.Error(w, http.StatusBadRequest, "VALIDATION",
			"signature_format must be one of: hmac_sha256_hex, hmac_sha256_base64, github, stripe, slack", reqID)
		return
	}
	header := strings.TrimSpace(req.SignatureHeader)
	if header == "" {
		header = database.DefaultSignatureHeader(format)
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}

	secret := database.NewInboundWebhookSecret()
	row := &database.InboundWebhook{
		FunctionID:      fnID,
		Name:            name,
		Secret:          secret,
		SignatureHeader: header,
		SignatureFormat: format,
		Active:          active,
	}
	if err := h.DB.InsertInboundWebhook(row); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "create failed: "+err.Error(), reqID)
		return
	}
	row.SecretPreview = secret[:8] + "…"
	respond.JSON(w, http.StatusCreated, map[string]any{
		"inbound_webhook": row,
		"secret":          secret,
		"trigger_url":     "/webhook/" + row.ID,
	})
}

// Get handles GET /api/v1/functions/{fn_id}/inbound-webhooks/{id}.
// Plaintext secret is never returned.
func (h *InboundWebhookHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	id := r.PathValue("id")
	row, err := h.DB.GetInboundWebhook(id)
	if err != nil || row.FunctionID != fnID {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "inbound webhook not found", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, row)
}

// Update handles PUT /api/v1/functions/{fn_id}/inbound-webhooks/{id}.
// Any subset of {name, signature_format, signature_header, active} may
// be supplied; omitted fields keep their previous values.
func (h *InboundWebhookHandler) Update(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	id := r.PathValue("id")
	row, err := h.DB.GetInboundWebhook(id)
	if err != nil || row.FunctionID != fnID {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "inbound webhook not found", reqID)
		return
	}
	var req inboundUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "name cannot be empty", reqID)
			return
		}
		row.Name = name
	}
	if req.SignatureFormat != nil {
		f := strings.TrimSpace(*req.SignatureFormat)
		if _, ok := database.AllowedInboundFormats[f]; !ok {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "unknown signature_format", reqID)
			return
		}
		// If the operator changes the format AND has the legacy default
		// header for the old format, swap to the new format's default
		// so the test button keeps working without an extra click.
		if row.SignatureHeader == database.DefaultSignatureHeader(row.SignatureFormat) {
			row.SignatureHeader = database.DefaultSignatureHeader(f)
		}
		row.SignatureFormat = f
	}
	if req.SignatureHeader != nil {
		h := strings.TrimSpace(*req.SignatureHeader)
		if h == "" {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "signature_header cannot be empty", reqID)
			return
		}
		row.SignatureHeader = h
	}
	if req.Active != nil {
		row.Active = *req.Active
	}
	if err := h.DB.UpdateInboundWebhook(row); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "update failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, row)
}

// Delete handles DELETE /api/v1/functions/{fn_id}/inbound-webhooks/{id}.
func (h *InboundWebhookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	id := r.PathValue("id")
	row, err := h.DB.GetInboundWebhook(id)
	if err != nil || row.FunctionID != fnID {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "inbound webhook not found", reqID)
		return
	}
	if err := h.DB.DeleteInboundWebhook(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "delete failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}
