package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// PoolConfigHandler exposes per-function autoscaler tuning; only writes require admin.
type PoolConfigHandler struct {
	DB          *database.Database
	Registry    *registry.Registry
	PoolRefresh func(functionID string) // optional: tears down running pool so next acquire reads new config
	// ManagedRefresh rotates only workers. The returned cleanup runs after unlocking the owner.
	ManagedRefresh func(*database.PoolConfig) func()
}

type poolConfigBody struct {
	FunctionID     string `json:"function_id"`
	MinWarm        *int   `json:"min_warm"`
	MaxWarm        *int   `json:"max_warm"`
	IdleTTLSeconds *int   `json:"idle_ttl_seconds"`
	ScaleToZero    *bool  `json:"scale_to_zero"`
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
		if h.ManagedRefresh != nil && !errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to read pool config", reqID)
			return
		}
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

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&raw); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if _, stale := raw["target_concurrency"]; stale {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "target_concurrency was removed; Pool Controller v2 sizes workers automatically from arrival rate, service time, and spawn time", reqID)
		return
	}
	encoded, _ := json.Marshal(raw)
	var body poolConfigBody
	if err := json.Unmarshal(encoded, &body); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if body.FunctionID == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "function_id is required", reqID)
		return
	}
	cfg, err := h.save(r.Context(), body)
	var invalid poolConfigValidation
	switch {
	case err == nil:
		respond.JSON(w, http.StatusOK, cfg)
	case errors.As(err, &invalid):
		respond.Error(w, http.StatusBadRequest, "VALIDATION", invalid.Error(), reqID)
	case errors.Is(err, database.ErrOperatorDenied):
		respond.Error(w, http.StatusForbidden, "FORBIDDEN", "Current operator authority required", reqID)
	case errors.Is(err, errPoolFunctionNotFound):
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
	default:
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to upsert pool config", reqID)
	}
}

type poolConfigValidation string

func (e poolConfigValidation) Error() string { return string(e) }

var errPoolFunctionNotFound = errors.New("function not found")

func (h *PoolConfigHandler) save(ctx context.Context, body poolConfigBody) (*database.PoolConfig, error) {
	if _, err := h.Registry.Get(body.FunctionID); err != nil {
		return nil, errPoolFunctionNotFound
	}
	var cleanup func()
	if h.ManagedRefresh != nil {
		lock := h.Registry.MutationLock(body.FunctionID)
		lock.Lock()
		defer func() {
			lock.Unlock()
			if cleanup != nil {
				cleanup()
			}
		}()
		fn, err := h.Registry.Get(body.FunctionID)
		if err != nil || fn.Status == database.FunctionStatusDeleting {
			return nil, errPoolFunctionNotFound
		}
	}
	// Start from any existing row so PUT can act as a partial update.
	cfg, err := h.DB.GetPoolConfig(body.FunctionID)
	if h.ManagedRefresh != nil && err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err != nil || cfg == nil {
		cfg = &database.PoolConfig{
			FunctionID: body.FunctionID,
			MinWarm:    1, MaxWarm: 0, IdleTTLS: 600, ScaleToZero: false,
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
	if body.ScaleToZero != nil {
		cfg.ScaleToZero = *body.ScaleToZero
	}
	if body.MinWarm != nil {
		if cfg.ScaleToZero && cfg.MinWarm != 0 {
			return nil, poolConfigValidation("scale_to_zero=true requires min_warm=0")
		}
		if !cfg.ScaleToZero && cfg.MinWarm < 1 {
			return nil, poolConfigValidation("scale_to_zero=false requires min_warm>=1")
		}
	}
	if body.ScaleToZero != nil && body.MinWarm == nil {
		if cfg.ScaleToZero {
			cfg.MinWarm = 0
		} else if cfg.MinWarm < 1 {
			cfg.MinWarm = 1
		}
	}

	if cfg.MinWarm < 0 || cfg.MaxWarm < 0 || (cfg.MaxWarm > 0 && cfg.MinWarm > cfg.MaxWarm) {
		return nil, poolConfigValidation("require min_warm >= 0 and max_warm >= min_warm, or max_warm=0 for automatic capacity")
	}
	if cfg.IdleTTLS < 0 {
		return nil, poolConfigValidation("idle_ttl_seconds must be >= 0")
	}
	if h.ManagedRefresh != nil {
		ref, ok := auth.OperatorFromContext(ctx)
		if !ok {
			return nil, database.ErrOperatorDenied
		}
		if err := h.DB.UpsertPoolConfigAsOperator(ctx, ref, cfg); err != nil {
			return nil, err
		}
		cleanup = h.ManagedRefresh(cfg)
		return cfg, nil
	}
	if err := h.DB.UpsertPoolConfig(cfg); err != nil {
		return nil, err
	}
	if h.PoolRefresh != nil {
		h.PoolRefresh(body.FunctionID)
	}
	return cfg, nil
}
