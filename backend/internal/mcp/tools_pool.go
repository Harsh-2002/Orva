package mcp

import (
	"context"

	"github.com/Harsh-2002/Orva/internal/database"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type PoolConfigView struct {
	FunctionID        string `json:"function_id"`
	MinWarm           int    `json:"min_warm"`
	MaxWarm           int    `json:"max_warm"`
	IdleTTLSeconds    int    `json:"idle_ttl_seconds"`
	TargetConcurrency int    `json:"target_concurrency"`
	ScaleToZero       bool   `json:"scale_to_zero"`
}

type GetPoolConfigInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (fn_...) or name"`
}

type SetPoolConfigInput struct {
	FunctionID        string `json:"function_id"`
	MinWarm           *int   `json:"min_warm,omitempty"`
	MaxWarm           *int   `json:"max_warm,omitempty"`
	IdleTTLSeconds    *int   `json:"idle_ttl_seconds,omitempty"`
	TargetConcurrency *int   `json:"target_concurrency,omitempty" jsonschema:"req per worker before scale-up considered"`
	ScaleToZero       *bool  `json:"scale_to_zero,omitempty"`
}

func toPoolConfigView(c *database.PoolConfig) PoolConfigView {
	return PoolConfigView{
		FunctionID: c.FunctionID, MinWarm: c.MinWarm, MaxWarm: c.MaxWarm,
		IdleTTLSeconds: c.IdleTTLS, TargetConcurrency: c.TargetConcurrency,
		ScaleToZero: c.ScaleToZero,
	}
}

func registerPoolTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_pool_config",
				Description: "Get the autoscaler pool config for a function (min_warm, max_warm, idle_ttl, target_concurrency, scale_to_zero). Returns nulls/defaults if no override is configured.",
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
						IdleTTLSeconds: 600, TargetConcurrency: 10,
					}, nil
				}
				return nil, toPoolConfigView(cfg), nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "set_pool_config",
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
						IdleTTLS: 600, TargetConcurrency: 10,
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
				if in.TargetConcurrency != nil {
					cfg.TargetConcurrency = *in.TargetConcurrency
				}
				if in.ScaleToZero != nil {
					cfg.ScaleToZero = *in.ScaleToZero
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
	})
}
