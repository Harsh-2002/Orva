package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// PoolConfigHandler exposes per-function autoscaler tuning. The endpoint is
// admin-gated by middleware_auth.go (PUT/POST require "admin").
type PoolConfigHandler struct {
	DB          *database.Database
	Registry    *registry.Registry
	PoolRefresh func(functionID string) // optional: tears down running pool so next acquire reads new config
}

type poolConfigBody struct {
	FunctionID        string `json:"function_id"`
	MinWarm           *int   `json:"min_warm"`
	MaxWarm           *int   `json:"max_warm"`
	IdleTTLSeconds    *int   `json:"idle_ttl_seconds"`
	TargetConcurrency *int   `json:"target_concurrency"`
	ScaleToZero       *bool  `json:"scale_to_zero"`
}

// Get handles GET /api/v1/pool/config?function_id=<id>.
func (h *PoolConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	fnID := r.URL.Query().Get("function_id")
	if fnID == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "function_id query parameter is required", reqID)
		return
	}
	if _, err := h.Registry.Get(fnID); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	cfg, err := h.DB.GetPoolConfig(fnID)
	if err != nil {
		respond.JSON(w, http.StatusOK, map[string]any{
			"function_id": fnID,
			"configured":  false,
		})
		return
	}
	respond.JSON(w, http.StatusOK, cfg)
}

// Upsert handles PUT /api/v1/pool/config.
func (h *PoolConfigHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	var body poolConfigBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if body.FunctionID == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "function_id is required", reqID)
		return
	}
	if _, err := h.Registry.Get(body.FunctionID); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}

	// Start from any existing row so PUT can act as a partial update.
	cfg, err := h.DB.GetPoolConfig(body.FunctionID)
	if err != nil || cfg == nil {
		cfg = &database.PoolConfig{
			FunctionID:        body.FunctionID,
			MinWarm:           1,
			MaxWarm:           50,
			IdleTTLS:          600,
			TargetConcurrency: 10,
			ScaleToZero:       false,
		}
	}

	if body.MinWarm != nil {
		cfg.MinWarm = *body.MinWarm
	}
	if body.MaxWarm != nil {
		cfg.MaxWarm = *body.MaxWarm
	}
	if body.IdleTTLSeconds != nil {
		cfg.IdleTTLS = *body.IdleTTLSeconds
	}
	if body.TargetConcurrency != nil {
		cfg.TargetConcurrency = *body.TargetConcurrency
	}
	if body.ScaleToZero != nil {
		cfg.ScaleToZero = *body.ScaleToZero
	}

	if cfg.MinWarm < 0 || cfg.MaxWarm < 1 || cfg.MinWarm > cfg.MaxWarm {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "require 0 <= min_warm <= max_warm and max_warm >= 1", reqID)
		return
	}
	if cfg.IdleTTLS < 0 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "idle_ttl_seconds must be >= 0", reqID)
		return
	}
	if cfg.TargetConcurrency < 1 {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "target_concurrency must be >= 1", reqID)
		return
	}

	if err := h.DB.UpsertPoolConfig(cfg); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to upsert pool config", reqID)
		return
	}

	if h.PoolRefresh != nil {
		h.PoolRefresh(body.FunctionID)
	}

	respond.JSON(w, http.StatusOK, cfg)
}
