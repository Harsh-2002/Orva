package mcp

import (
	"context"
	"fmt"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type PoolConfigView struct {
	FunctionID     string `json:"function_id"`
	MinWarm        int    `json:"min_warm"`
	MaxWarm        int    `json:"max_warm"`
	IdleTTLSeconds int    `json:"idle_ttl_seconds"`
	ScaleToZero    bool   `json:"scale_to_zero"`
}

type GetPoolConfigInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name"`
}

type SetPoolConfigInput struct {
	FunctionID     string `json:"function_id"`
	MinWarm        *int   `json:"min_warm,omitempty"`
	MaxWarm        *int   `json:"max_warm,omitempty"`
	IdleTTLSeconds *int   `json:"idle_ttl_seconds,omitempty"`
	ScaleToZero    *bool  `json:"scale_to_zero,omitempty"`
}

func toPoolConfigView(c *database.PoolConfig) PoolConfigView {
	return PoolConfigView{
		FunctionID: c.FunctionID, MinWarm: c.MinWarm, MaxWarm: c.MaxWarm,
		IdleTTLSeconds: c.IdleTTLS,
		ScaleToZero:    c.ScaleToZero,
	}
}

func registerPoolTools(rc *regCtx) {
	deps := rc.deps
	rc.group = "pool"
	// REST gates GET /api/v1/pool/config at "read" -- only PUT/POST is
	// admin. permissions.go claims to mirror requiredPermission, and a
	// read-scoped key could inspect pool config over REST while the MCP
	// tool was invisible to it.
	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:        "get_pool_config",
			Title:       "Get Pool Config",
			Description: "Get the Pool Controller v2 config for a function (min_warm, max_warm, idle_ttl, scale_to_zero). Returns defaults if no override is configured.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetPoolConfigInput) (*mcpsdk.CallToolResult, PoolConfigView, error) {
			fn, err := resolveFunction(deps, in.FunctionID)
			if err != nil {
				return nil, PoolConfigView{}, err
			}
			cfg, err := deps.DB.GetPoolConfig(fn.ID)
			if err != nil {
				// no row = use defaults
				return nil, PoolConfigView{
					FunctionID: fn.ID, MinWarm: 1, MaxWarm: 50,
					IdleTTLSeconds: 600,
				}, nil
			}
			return nil, toPoolConfigView(cfg), nil
		},
	)

	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:        "set_pool_config",
			Title:       "Set Pool Config",
			Description: "Tune the autoscaler for a function. Any field omitted retains its current value. Changes apply to new sandbox spawns; existing warm workers keep their behavior until recycled.",
			Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in SetPoolConfigInput) (*mcpsdk.CallToolResult, PoolConfigView, error) {
			fn, err := resolveFunction(deps, in.FunctionID)
			if err != nil {
				return nil, PoolConfigView{}, err
			}
			cfg, err := deps.DB.GetPoolConfig(fn.ID)
			if err != nil {
				cfg = &database.PoolConfig{
					FunctionID: fn.ID, MinWarm: 1, MaxWarm: 50,
					IdleTTLS: 600,
				}
			}
			if in.MinWarm != nil {
				cfg.MinWarm = *in.MinWarm
			}
			if in.MaxWarm != nil {
				cfg.MaxWarm = *in.MaxWarm
			}
			if in.IdleTTLSeconds != nil {
				cfg.IdleTTLS = *in.IdleTTLSeconds
			}
			if in.ScaleToZero != nil {
				cfg.ScaleToZero = *in.ScaleToZero
			}
			if in.MinWarm != nil {
				if cfg.ScaleToZero && cfg.MinWarm != 0 {
					return nil, PoolConfigView{}, fmt.Errorf("scale_to_zero=true requires min_warm=0")
				}
				if !cfg.ScaleToZero && cfg.MinWarm < 1 {
					return nil, PoolConfigView{}, fmt.Errorf("scale_to_zero=false requires min_warm>=1")
				}
			}
			if in.ScaleToZero != nil && in.MinWarm == nil {
				if cfg.ScaleToZero {
					cfg.MinWarm = 0
				} else if cfg.MinWarm < 1 {
					cfg.MinWarm = 1
				}
			}
			if cfg.MaxWarm < 1 || cfg.MinWarm > cfg.MaxWarm || cfg.IdleTTLS < 0 {
				return nil, PoolConfigView{}, fmt.Errorf("require min_warm <= max_warm, max_warm >= 1, and idle_ttl_seconds >= 0")
			}
			// max_warm sizes the pool's idle channel; reject rather than clamp.
			if cfg.MaxWarm > pool.MaxWarmLimit {
				return nil, PoolConfigView{}, fmt.Errorf("max_warm must be <= %d", pool.MaxWarmLimit)
			}
			if err := deps.DB.UpsertPoolConfig(cfg); err != nil {
				return nil, PoolConfigView{}, err
			}
			if deps.PoolMgr != nil {
				deps.PoolMgr.RefreshForDeploy(fn.ID)
			}
			return nil, toPoolConfigView(cfg), nil
		},
	)
}
