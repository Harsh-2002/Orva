package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gonanoid "github.com/matoous/go-nanoid/v2"

	"github.com/Harsh-2002/Orva/internal/database"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── shared types ──────────────────────────────────────────────────

// FunctionView is the JSON-shaped output for any tool returning a
// function record. Matches the REST shape minus the internal Image
// field, which agents don't need.
type FunctionView struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Runtime           string            `json:"runtime"`
	Entrypoint        string            `json:"entrypoint"`
	TimeoutMS         int64             `json:"timeout_ms"`
	MemoryMB          int64             `json:"memory_mb"`
	CPUs              float64           `json:"cpus"`
	EnvVars           map[string]string `json:"env_vars,omitempty"`
	NetworkMode       string            `json:"network_mode"`
	MaxConcurrency    int               `json:"max_concurrency"`
	ConcurrencyPolicy string            `json:"concurrency_policy"`
	AuthMode          string            `json:"auth_mode"`
	RateLimitPerMin   int               `json:"rate_limit_per_min"`
	Version           int               `json:"version"`
	Status            string            `json:"status"`
	CodeHash          string            `json:"code_hash,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

func toFunctionView(fn *database.Function) FunctionView {
	if fn == nil {
		return FunctionView{}
	}
	return FunctionView{
		ID:                fn.ID,
		Name:              fn.Name,
		Runtime:           fn.Runtime,
		Entrypoint:        fn.Entrypoint,
		TimeoutMS:         fn.TimeoutMS,
		MemoryMB:          fn.MemoryMB,
		CPUs:              fn.CPUs,
		EnvVars:           fn.EnvVars,
		NetworkMode:       fn.NetworkMode,
		MaxConcurrency:    fn.MaxConcurrency,
		ConcurrencyPolicy: fn.ConcurrencyPolicy,
		AuthMode:          fn.AuthMode,
		RateLimitPerMin:   fn.RateLimitPerMin,
		Version:           fn.Version,
		Status:            fn.Status,
		CodeHash:          fn.CodeHash,
		CreatedAt:         fn.CreatedAt,
		UpdatedAt:         fn.UpdatedAt,
	}
}

// resolveFunction looks up a function by id (preferred) or name.
// Returns the *database.Function record or an error.
func resolveFunction(deps Deps, idOrName string) (*database.Function, error) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return nil, errors.New("function_id or name is required")
	}
	// id form: starts with "fn_"
	if strings.HasPrefix(idOrName, "fn_") {
		fn, err := deps.Registry.Get(idOrName)
		if err == nil {
			return fn, nil
		}
	}
	// fall back to name lookup via DB
	fn, err := deps.DB.GetFunctionByName(idOrName)
	if err != nil {
		return nil, fmt.Errorf("function not found: %s", idOrName)
	}
	return fn, nil
}

// ─── list_functions ────────────────────────────────────────────────

type ListFunctionsInput struct {
	Runtime string `json:"runtime,omitempty" jsonschema:"filter to one runtime (node22|node24|python313|python314)"`
	Status  string `json:"status,omitempty" jsonschema:"filter by status (active|inactive|created|building|error)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"page size, default 50, max 200"`
	Offset  int    `json:"offset,omitempty" jsonschema:"skip this many items, default 0"`
	Search  string `json:"search,omitempty" jsonschema:"case-insensitive substring match against name and id"`
}

type ListFunctionsOutput struct {
	Functions []FunctionView `json:"functions"`
	Total     int            `json:"total"`
	Limit     int            `json:"limit"`
	Offset    int            `json:"offset"`
}

// ─── get_function ──────────────────────────────────────────────────

type GetFunctionInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (fn_...) or name"`
}

// ─── create_function ───────────────────────────────────────────────

type CreateFunctionInput struct {
	Name              string            `json:"name" jsonschema:"unique function name (lowercase, dash-separated)"`
	Runtime           string            `json:"runtime" jsonschema:"one of node22 node24 python313 python314"`
	Entrypoint        string            `json:"entrypoint,omitempty" jsonschema:"defaults: handler.js for Node, handler.py for Python"`
	TimeoutMS         int64             `json:"timeout_ms,omitempty" jsonschema:"per-invocation timeout in ms, default 30000"`
	MemoryMB          int64             `json:"memory_mb,omitempty" jsonschema:"sandbox memory in MB, default 128"`
	CPUs              float64           `json:"cpus,omitempty" jsonschema:"CPU shares (fractional ok), default 0.5"`
	EnvVars           map[string]string `json:"env_vars,omitempty" jsonschema:"plaintext env vars (use set_secret for credentials)"`
	NetworkMode       string            `json:"network_mode,omitempty" jsonschema:"none (default, loopback only) or egress (outbound HTTPS allowed)"`
	MaxConcurrency    int               `json:"max_concurrency,omitempty" jsonschema:"max parallel invocations, 0 (default) = unlimited"`
	ConcurrencyPolicy string            `json:"concurrency_policy,omitempty" jsonschema:"queue (default) or reject when at max"`
	AuthMode          string            `json:"auth_mode,omitempty" jsonschema:"none (default, public) or platform_key or signed"`
	RateLimitPerMin   int               `json:"rate_limit_per_min,omitempty" jsonschema:"per-IP rate limit, 0 (default) = unlimited"`
}

// ─── update_function ───────────────────────────────────────────────

type UpdateFunctionInput struct {
	FunctionID        string             `json:"function_id" jsonschema:"function id (fn_...) or name"`
	Name              *string            `json:"name,omitempty"`
	Entrypoint        *string            `json:"entrypoint,omitempty"`
	TimeoutMS         *int64             `json:"timeout_ms,omitempty"`
	MemoryMB          *int64             `json:"memory_mb,omitempty"`
	CPUs              *float64           `json:"cpus,omitempty"`
	EnvVars           *map[string]string `json:"env_vars,omitempty"`
	NetworkMode       *string            `json:"network_mode,omitempty" jsonschema:"none or egress — flipping triggers warm-pool drain"`
	MaxConcurrency    *int               `json:"max_concurrency,omitempty"`
	ConcurrencyPolicy *string            `json:"concurrency_policy,omitempty"`
	AuthMode          *string            `json:"auth_mode,omitempty"`
	RateLimitPerMin   *int               `json:"rate_limit_per_min,omitempty"`
	Status            *string            `json:"status,omitempty" jsonschema:"active or inactive"`
}

// ─── delete_function ───────────────────────────────────────────────

type DeleteFunctionInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (fn_...) or name"`
	Confirm    bool   `json:"confirm" jsonschema:"must be true — guards against runaway agent loops"`
}

type DeletedOutput struct {
	DeletedID string `json:"deleted_id"`
}

// ─── get_function_source ───────────────────────────────────────────

type GetFunctionSourceInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (fn_...) or name"`
}

type GetFunctionSourceOutput struct {
	Code         string `json:"code"`
	Dependencies string `json:"dependencies,omitempty"`
	Runtime      string `json:"runtime"`
	Entrypoint   string `json:"entrypoint"`
}

// ─── helpers ───────────────────────────────────────────────────────

var validRuntimesSet = map[string]bool{
	"node22": true, "node24": true, "python313": true, "python314": true,
}

func runtimeIsNode(r string) bool   { return r == "node22" || r == "node24" }
func runtimeIsPython(r string) bool { return r == "python313" || r == "python314" }

var userSettableStatuses = map[string]bool{"active": true, "inactive": true}

// ─── registration ──────────────────────────────────────────────────

func registerFunctionTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_functions",
				Description: "List all functions on this Orva instance. Supports pagination (limit/offset) and filtering by runtime, status, or a substring match. Always call this first when an agent says 'work on the X function' so you can resolve the id.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListFunctionsInput) (*mcpsdk.CallToolResult, ListFunctionsOutput, error) {
				lim := in.Limit
				if lim <= 0 {
					lim = 50
				}
				if lim > 200 {
					lim = 200
				}
				res, err := deps.DB.ListFunctions(database.ListFunctionsParams{
					Status: in.Status, Runtime: in.Runtime, Limit: lim, Offset: in.Offset,
				})
				if err != nil {
					return nil, ListFunctionsOutput{}, err
				}
				out := ListFunctionsOutput{Total: res.Total, Limit: lim, Offset: in.Offset}
				q := strings.ToLower(strings.TrimSpace(in.Search))
				for _, fn := range res.Functions {
					if q != "" {
						if !strings.Contains(strings.ToLower(fn.Name), q) && !strings.Contains(strings.ToLower(fn.ID), q) {
							continue
						}
					}
					out.Functions = append(out.Functions, toFunctionView(fn))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_function",
				Description: "Fetch one function by id or name. Returns the full record including resource limits, env_vars, network_mode, auth_mode, and rate_limit_per_min. Pass either the fn_ id or the human name.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetFunctionInput) (*mcpsdk.CallToolResult, FunctionView, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, FunctionView{}, err
				}
				return nil, toFunctionView(fn), nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_function_source",
				Description: "Read the deployed source code of a function (handler.py / handler.js plus requirements.txt or package.json if any). Useful for an agent that wants to inspect or modify what's running.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetFunctionSourceInput) (*mcpsdk.CallToolResult, GetFunctionSourceOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, GetFunctionSourceOutput{}, err
				}
				out, err := readFunctionSource(deps, fn)
				return nil, out, err
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "create_function",
				Description: "Create a new function shell (no code yet). The function starts in `created` status — call deploy_function_inline next to ship code and have it activate.",
				Annotations: &mcpsdk.ToolAnnotations{
					DestructiveHint: ptrFalse(),
					OpenWorldHint:   ptrFalse(),
				},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in CreateFunctionInput) (*mcpsdk.CallToolResult, FunctionView, error) {
				fn, err := createFunction(deps, in)
				if err != nil {
					return nil, FunctionView{}, err
				}
				return nil, toFunctionView(fn), nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "update_function",
				Description: "Patch a function's settings. Any field omitted is left unchanged. Flipping memory/cpus/env_vars/network_mode drains the warm pool so the next invocation respawns with the new config.",
				Annotations: &mcpsdk.ToolAnnotations{
					IdempotentHint: true,
					OpenWorldHint:  ptrFalse(),
				},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in UpdateFunctionInput) (*mcpsdk.CallToolResult, FunctionView, error) {
				fn, err := updateFunction(deps, in)
				if err != nil {
					return nil, FunctionView{}, err
				}
				return nil, toFunctionView(fn), nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_function",
				Description: "Permanently delete a function and all its deployments, secrets, routes, and execution history. Pass confirm=true. Irreversible — only do this when the user has explicitly asked.",
				Annotations: &mcpsdk.ToolAnnotations{
					DestructiveHint: ptrTrue(),
					OpenWorldHint:   ptrFalse(),
				},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteFunctionInput) (*mcpsdk.CallToolResult, DeletedOutput, error) {
				if !in.Confirm {
					return nil, DeletedOutput{}, errors.New("delete refused: pass confirm=true to acknowledge irreversibility")
				}
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, DeletedOutput{}, err
				}
				if err := deps.Registry.Delete(fn.ID); err != nil {
					return nil, DeletedOutput{}, err
				}
				if deps.PoolMgr != nil {
					deps.PoolMgr.DrainAndRemove(fn.ID)
				}
				return nil, DeletedOutput{DeletedID: fn.ID}, nil
			},
		)
	})
}

// ─── helpers shared with deploy tools ──────────────────────────────

// createFunction does the same validation + default-application + registry
// insert that FunctionHandler.Create does. Lifted here so MCP doesn't go
// through HTTP.
func createFunction(deps Deps, in CreateFunctionInput) (*database.Function, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("name is required")
	}
	if !validRuntimesSet[in.Runtime] {
		return nil, fmt.Errorf("unsupported runtime: %s (one of node22, node24, python313, python314)", in.Runtime)
	}
	if !database.ValidNetworkMode(in.NetworkMode) {
		return nil, fmt.Errorf("invalid network_mode: %s (allowed: none, egress)", in.NetworkMode)
	}
	if !database.ValidConcurrencyPolicy(in.ConcurrencyPolicy) {
		return nil, fmt.Errorf("invalid concurrency_policy: %s (allowed: queue, reject)", in.ConcurrencyPolicy)
	}
	if !database.ValidAuthMode(in.AuthMode) {
		return nil, fmt.Errorf("invalid auth_mode: %s (allowed: none, platform_key, signed)", in.AuthMode)
	}
	if in.MaxConcurrency < 0 {
		return nil, errors.New("max_concurrency must be >= 0 (0 = unlimited)")
	}
	if in.RateLimitPerMin < 0 {
		return nil, errors.New("rate_limit_per_min must be >= 0 (0 = unlimited)")
	}

	if in.Entrypoint == "" {
		switch {
		case runtimeIsNode(in.Runtime):
			in.Entrypoint = "handler.js"
		case runtimeIsPython(in.Runtime):
			in.Entrypoint = "handler.py"
		}
	}
	if in.TimeoutMS <= 0 {
		in.TimeoutMS = 30000
	}
	if in.MemoryMB <= 0 {
		in.MemoryMB = 128
	}
	if in.CPUs <= 0 {
		in.CPUs = 0.5
	}
	if in.NetworkMode == "" {
		in.NetworkMode = database.NetworkModeNone
	}
	if in.ConcurrencyPolicy == "" {
		in.ConcurrencyPolicy = database.ConcurrencyPolicyQueue
	}
	if in.AuthMode == "" {
		in.AuthMode = database.AuthModeNone
	}
	if in.EnvVars == nil {
		in.EnvVars = map[string]string{}
	}

	suffix, err := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 12)
	if err != nil {
		return nil, fmt.Errorf("id generation failed: %w", err)
	}
	fnID := "fn_" + suffix

	fn := &database.Function{
		ID:                fnID,
		Name:              in.Name,
		Runtime:           in.Runtime,
		Entrypoint:        in.Entrypoint,
		TimeoutMS:         in.TimeoutMS,
		MemoryMB:          in.MemoryMB,
		CPUs:              in.CPUs,
		EnvVars:           in.EnvVars,
		NetworkMode:       in.NetworkMode,
		MaxConcurrency:    in.MaxConcurrency,
		ConcurrencyPolicy: in.ConcurrencyPolicy,
		AuthMode:          in.AuthMode,
		RateLimitPerMin:   in.RateLimitPerMin,
		Status:            "created",
		Version:           1,
	}
	if err := deps.Registry.Set(fn); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, fmt.Errorf("function name already exists: %s", in.Name)
		}
		return nil, err
	}
	return fn, nil
}

// updateFunction patches the function record. Mirrors the
// pool-drain rules the REST handler uses: changes that affect the
// spawn config trigger PoolRefresh.
func updateFunction(deps Deps, in UpdateFunctionInput) (*database.Function, error) {
	fn, err := resolveFunction(deps, in.FunctionID)
	if err != nil {
		return nil, err
	}
	drainPool := false

	if in.Name != nil {
		fn.Name = *in.Name
	}
	if in.Entrypoint != nil {
		fn.Entrypoint = *in.Entrypoint
	}
	if in.TimeoutMS != nil {
		if *in.TimeoutMS != fn.TimeoutMS {
			drainPool = true
		}
		fn.TimeoutMS = *in.TimeoutMS
	}
	if in.MemoryMB != nil {
		if *in.MemoryMB != fn.MemoryMB {
			drainPool = true
		}
		fn.MemoryMB = *in.MemoryMB
	}
	if in.CPUs != nil {
		if *in.CPUs != fn.CPUs {
			drainPool = true
		}
		fn.CPUs = *in.CPUs
	}
	if in.EnvVars != nil {
		fn.EnvVars = *in.EnvVars
		drainPool = true
	}
	if in.NetworkMode != nil {
		if !database.ValidNetworkMode(*in.NetworkMode) {
			return nil, fmt.Errorf("invalid network_mode: %s (allowed: none, egress)", *in.NetworkMode)
		}
		if *in.NetworkMode != fn.NetworkMode {
			drainPool = true
		}
		fn.NetworkMode = *in.NetworkMode
	}
	if in.MaxConcurrency != nil {
		if *in.MaxConcurrency < 0 {
			return nil, errors.New("max_concurrency must be >= 0")
		}
		if *in.MaxConcurrency != fn.MaxConcurrency {
			drainPool = true
		}
		fn.MaxConcurrency = *in.MaxConcurrency
	}
	if in.ConcurrencyPolicy != nil {
		if !database.ValidConcurrencyPolicy(*in.ConcurrencyPolicy) {
			return nil, fmt.Errorf("invalid concurrency_policy: %s (allowed: queue, reject)", *in.ConcurrencyPolicy)
		}
		if *in.ConcurrencyPolicy != fn.ConcurrencyPolicy {
			drainPool = true
		}
		fn.ConcurrencyPolicy = *in.ConcurrencyPolicy
	}
	if in.AuthMode != nil {
		if !database.ValidAuthMode(*in.AuthMode) {
			return nil, fmt.Errorf("invalid auth_mode: %s (allowed: none, platform_key, signed)", *in.AuthMode)
		}
		fn.AuthMode = *in.AuthMode
	}
	if in.RateLimitPerMin != nil {
		if *in.RateLimitPerMin < 0 {
			return nil, errors.New("rate_limit_per_min must be >= 0")
		}
		fn.RateLimitPerMin = *in.RateLimitPerMin
	}
	if in.Status != nil {
		if !userSettableStatuses[*in.Status] {
			return nil, fmt.Errorf("status must be one of: active, inactive")
		}
		fn.Status = *in.Status
	}

	if err := deps.Registry.Set(fn); err != nil {
		return nil, err
	}
	if drainPool && deps.PoolMgr != nil {
		deps.PoolMgr.RefreshForDeploy(fn.ID)
	}
	return fn, nil
}

// readFunctionSource reads handler.{py|js} + dependencies file from
// the function's `current/` symlink. Returns empty strings if the
// function has never been deployed.
func readFunctionSource(deps Deps, fn *database.Function) (GetFunctionSourceOutput, error) {
	out := GetFunctionSourceOutput{Runtime: fn.Runtime, Entrypoint: fn.Entrypoint}
	if deps.DataDir == "" {
		return out, errors.New("data dir not configured")
	}
	currentDir := filepath.Join(deps.DataDir, "functions", fn.ID, "current")
	codeBytes, err := os.ReadFile(filepath.Join(currentDir, fn.Entrypoint))
	if err == nil {
		out.Code = string(codeBytes)
	}
	depsFile := "requirements.txt"
	if runtimeIsNode(fn.Runtime) {
		depsFile = "package.json"
	}
	if depsBytes, err := os.ReadFile(filepath.Join(currentDir, depsFile)); err == nil {
		out.Dependencies = string(depsBytes)
	}
	return out, nil
}

// fnLockGuard returns a per-function mutex for serializing operations
// like rollback against in-flight deploys. Falls back to a package-
// global mutex if PoolMgr isn't wired (tests).
var fallbackLock sync.Mutex

func fnLockGuard(deps Deps, fnID string) *sync.Mutex {
	if deps.PoolMgr != nil {
		return deps.PoolMgr.FunctionLock(fnID)
	}
	return &fallbackLock
}
