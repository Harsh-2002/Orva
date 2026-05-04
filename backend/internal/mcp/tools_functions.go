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

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── shared types ──────────────────────────────────────────────────

// FunctionView is the JSON-shaped output for any tool returning a
// function record. Matches the REST shape minus the internal Image
// field, which agents don't need.
//
// InvokeURL is the canonical fully-qualified URL the function answers
// at. Agents should call it verbatim and never construct it from
// `id` + a base URL — the MCP server already knows its public host
// (from the inbound request) and renders the right URL per response.
// Routes lists any custom path-based routes registered for this
// function (empty if none); agents should prefer a route URL when the
// human asked for the function by route path.
type FunctionView struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	InvokeURL         string            `json:"invoke_url"`
	Routes            []string          `json:"routes,omitempty"`
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

func toFunctionView(fn *database.Function, deps Deps) FunctionView {
	if fn == nil {
		return FunctionView{}
	}
	v := FunctionView{
		ID:                fn.ID,
		Name:              fn.Name,
		Description:       fn.Description,
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
	if deps.BaseURL != "" {
		v.InvokeURL = deps.BaseURL + "/fn/" + fn.ID
	}
	// Best-effort: ListRoutes returns every route in the system; we
	// filter to this function. A miss returns an empty slice rather
	// than failing the whole tool call — routes are a hint, not load-
	// bearing.
	if deps.DB != nil && deps.BaseURL != "" {
		if all, err := deps.DB.ListRoutes(); err == nil {
			for _, r := range all {
				if r.FunctionID != fn.ID {
					continue
				}
				v.Routes = append(v.Routes, deps.BaseURL+r.Path)
			}
		}
	}
	return v
}

// resolveFunction looks up a function by id (preferred) or name.
// Returns the *database.Function record or an error.
func resolveFunction(deps Deps, idOrName string) (*database.Function, error) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return nil, errors.New("function_id or name is required")
	}
	// id form: any UUID. Tolerate the legacy "fn_" prefix in case a
	// stale client (e.g. an LLM that cached an old response) sends it.
	idOrName = strings.TrimPrefix(idOrName, "fn_")
	if ids.IsUUID(idOrName) {
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
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
}

// ─── create_function ───────────────────────────────────────────────

type CreateFunctionInput struct {
	Name              string            `json:"name" jsonschema:"unique function name (lowercase, dash-separated, URL-safe — appears in invoke_url and logs)"`
	Description       string            `json:"description" jsonschema:"REQUIRED — one-sentence summary of what the function does (e.g. 'resize uploaded images to webp thumbnails'). Surfaces in list_functions, the dashboard's function card, and channel-mode tool descriptions exposed to other agents — so this is how a future operator or LLM identifies what this function is for. Empty / placeholder values rejected."`
	Runtime           string            `json:"runtime" jsonschema:"one of node22 node24 python313 python314"`
	Entrypoint        string            `json:"entrypoint" jsonschema:"REQUIRED — handler file path relative to deploy dir (e.g. 'handler.js' for Node, 'handler.py' for Python, 'src/index.ts' for TypeScript). Set explicitly so the runtime+entrypoint pairing is intentional; mismatched values silently fail to spawn."`
	TimeoutMS         int64             `json:"timeout_ms" jsonschema:"REQUIRED — per-invocation timeout in ms. Cap on how long any single request can run before the sandbox is killed. Pick from the handler's expected work: a quick CRUD endpoint can use 5000-10000; an LLM/AI call usually 30000-60000; a heavy report 120000+. Must be > 0."`
	MemoryMB          int64             `json:"memory_mb" jsonschema:"REQUIRED — sandbox RAM in MB. Hard cap; a handler that exceeds it gets OOM-killed. Pick from runtime baseline + working set: tiny Node/Python with no deps ~64; with frameworks ~128-256; image/PDF/ML work 512+. Must be > 0."`
	CPUs              float64           `json:"cpus" jsonschema:"REQUIRED — CPU shares (fractional ok, e.g. 0.25, 0.5, 1, 2). Roughly 'how many cores worth of CPU time can this handler burn'. IO-bound (HTTP fetch, DB) → 0.25-0.5; mixed → 0.5-1; CPU-bound (image, crypto, ML) → 1+. Must be > 0."`
	EnvVars           map[string]string `json:"env_vars,omitempty" jsonschema:"plaintext env vars injected into the sandbox at spawn time (use set_secret for credentials — env_vars are stored unencrypted). Empty map is fine."`
	NetworkMode       string            `json:"network_mode" jsonschema:"REQUIRED — choose explicitly. 'none' = sandbox has loopback only; the orva.kv / orva.invoke / orva.jobs SDK calls (which reach orvad over the bridge network) will fail with ENETUNREACH. Use only for pure-compute handlers with no platform calls. 'egress' = sandbox has outbound TCP via pasta NAT (subject to firewall rules at /api/v1/firewall); REQUIRED for any handler that imports the orva module or makes external HTTPS. Pick 'egress' if the handler uses orva.kv, orva.invoke, orva.jobs, jobs.enqueue, or fetch/requests to an external URL."`
	MaxConcurrency    int               `json:"max_concurrency,omitempty" jsonschema:"max parallel invocations, 0 (default) = unlimited"`
	ConcurrencyPolicy string            `json:"concurrency_policy,omitempty" jsonschema:"queue (default) or reject when at max"`
	AuthMode          string            `json:"auth_mode" jsonschema:"REQUIRED — invocation auth gate. 'none' = anyone with the URL can invoke (use only for genuinely public endpoints); 'platform_key' = caller must present an Orva API key via X-Orva-API-Key or Bearer (use for server-to-server, internal dashboards, cron); 'signed' = caller signs the request HMAC-SHA256 over '<unix_ts>.<raw_body>' using ORVA_SIGNING_SECRET (use for partner integrations). Default-allow ('none') silently exposes data — pick consciously."`
	RateLimitPerMin   int               `json:"rate_limit_per_min,omitempty" jsonschema:"per-IP rate limit, 0 (default) = unlimited"`
}

// ─── update_function ───────────────────────────────────────────────

type UpdateFunctionInput struct {
	FunctionID        string             `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
	Name              *string            `json:"name,omitempty"`
	Description       *string            `json:"description,omitempty" jsonschema:"new one-sentence summary; pass any non-empty string to overwrite, omit to leave unchanged"`
	Entrypoint        *string            `json:"entrypoint,omitempty"`
	TimeoutMS         *int64             `json:"timeout_ms,omitempty"`
	MemoryMB          *int64             `json:"memory_mb,omitempty"`
	CPUs              *float64           `json:"cpus,omitempty"`
	EnvVars           *map[string]string `json:"env_vars,omitempty"`
	NetworkMode       *string            `json:"network_mode,omitempty" jsonschema:"'none' (loopback only — orva.kv/invoke/jobs WILL FAIL because the SDK uses HTTP over the bridge) or 'egress' (outbound TCP via pasta NAT — required for any orva SDK call or external HTTPS). Flipping triggers a hard pool drain so the next invocation respawns with the correct netns; expect cold_start=true on that invocation."`
	MaxConcurrency    *int               `json:"max_concurrency,omitempty"`
	ConcurrencyPolicy *string            `json:"concurrency_policy,omitempty"`
	AuthMode          *string            `json:"auth_mode,omitempty"`
	RateLimitPerMin   *int               `json:"rate_limit_per_min,omitempty"`
	Status            *string            `json:"status,omitempty" jsonschema:"active or inactive"`
}

// ─── delete_function ───────────────────────────────────────────────

type DeleteFunctionInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
	Confirm    bool   `json:"confirm" jsonschema:"must be true — guards against runaway agent loops"`
}

type DeletedOutput struct {
	DeletedID string `json:"deleted_id"`
}

// ─── get_function_source ───────────────────────────────────────────

type GetFunctionSourceInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (UUID) or name (legacy fn_ prefix is tolerated but unnecessary)"`
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
				Description: "List all functions on this Orva instance. Each result includes invoke_url (fully-qualified canonical URL — call this directly, do NOT build it from parts) and routes (list of custom-route URLs, if any). Use id (a UUID) to refer to a function in other MCP tools, or name for human-friendly references. Supports pagination (limit/offset) and filtering by runtime, status, or substring search.",
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
					out.Functions = append(out.Functions, toFunctionView(fn, deps))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_function",
				Description: "Fetch one function by id or name. Returns the full record including invoke_url (use verbatim to call the function over HTTP — never concatenate /fn/ + id manually), any custom routes, resource limits, env_vars, network_mode, auth_mode, and rate_limit_per_min.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in GetFunctionInput) (*mcpsdk.CallToolResult, FunctionView, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, FunctionView{}, err
				}
				return nil, toFunctionView(fn, deps), nil
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
				Name: "create_function",
				Description: "Create a new function shell (no code yet). The function starts in `created` status — call deploy_function_inline next to ship code and have it activate. " +
					"Most fields are REQUIRED so the function record carries explicit intent rather than silent defaults. Specifically you MUST provide: " +
					"`name` (URL-safe identifier), " +
					"`description` (one-sentence summary of what the function does — visible in list_functions and the dashboard), " +
					"`runtime` (node22 / node24 / python313 / python314), " +
					"`entrypoint` (handler file path; e.g. handler.js / handler.py / src/index.ts), " +
					"`timeout_ms` (per-invocation cap; pick from your handler's expected work — fast CRUD ~5000-10000, AI/LLM ~30000-60000, heavy reports 120000+), " +
					"`memory_mb` (RAM cap; tiny handlers 64, with frameworks 128-256, image/PDF/ML 512+), " +
					"`cpus` (CPU shares; IO-bound 0.25-0.5, mixed 0.5-1, CPU-bound 1+), " +
					"`network_mode` ('egress' if the handler imports `orva` / makes external HTTPS, 'none' only for pure compute), " +
					"`auth_mode` ('platform_key' / 'signed' for anything that handles user data; 'none' only for genuinely public endpoints). " +
					"Optional: env_vars, max_concurrency, concurrency_policy, rate_limit_per_min — sane defaults. " +
					"Defaulting any of the required fields silently has been a frequent source of bugs (auth=none on private endpoints, network_mode=none on SDK handlers, undersized memory) — being explicit costs ~10 extra lines and prevents the whole class of foot-gun.",
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
				return nil, toFunctionView(fn, deps), nil
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
				return nil, toFunctionView(fn, deps), nil
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
	// MCP strict-required policy: every load-bearing field must be set
	// explicitly. The schema layer rejects missing-key cases; these guards
	// catch empty-string / non-positive numerics that slip past the schema
	// for non-pointer types. Each error names the field so the agent's
	// retry has actionable feedback.
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("name is required (URL-safe identifier, e.g. 'image-resizer')")
	}
	if strings.TrimSpace(in.Description) == "" {
		return nil, errors.New(
			"description is required: pass a one-sentence summary of what " +
				"the function does (e.g. 'resize uploaded images to webp " +
				"thumbnails'). Surfaces in list_functions, the dashboard's " +
				"function card, and channel-mode tool descriptions exposed " +
				"to other agents — empty leaves an unidentifiable function.",
		)
	}
	if !validRuntimesSet[in.Runtime] {
		return nil, fmt.Errorf("unsupported runtime: %s (one of node22, node24, python313, python314)", in.Runtime)
	}
	if strings.TrimSpace(in.Entrypoint) == "" {
		return nil, errors.New(
			"entrypoint is required: handler file path (e.g. 'handler.js' " +
				"for Node, 'handler.py' for Python, 'src/index.ts' for " +
				"TypeScript). Set explicitly so the runtime+entrypoint " +
				"pairing is intentional — mismatched values silently fail " +
				"to spawn.",
		)
	}
	if in.TimeoutMS <= 0 {
		return nil, errors.New(
			"timeout_ms is required and must be > 0: per-invocation cap " +
				"on how long any single request can run before the sandbox " +
				"is killed. Quick CRUD ~5000-10000; AI/LLM ~30000-60000; " +
				"heavy reports 120000+.",
		)
	}
	if in.MemoryMB <= 0 {
		return nil, errors.New(
			"memory_mb is required and must be > 0: sandbox RAM cap. " +
				"Tiny handlers ~64; with frameworks 128-256; image/PDF/ML " +
				"512+.",
		)
	}
	if in.CPUs <= 0 {
		return nil, errors.New(
			"cpus is required and must be > 0: CPU shares (fractional ok). " +
				"IO-bound 0.25-0.5; mixed 0.5-1; CPU-bound 1+.",
		)
	}
	if strings.TrimSpace(in.NetworkMode) == "" {
		return nil, errors.New(
			"network_mode is required: choose 'egress' if the handler uses " +
				"orva.kv / orva.invoke / orva.jobs (the SDK reaches orvad " +
				"over HTTP and needs outbound network) or makes external " +
				"HTTPS calls; choose 'none' only for a pure-compute " +
				"handler with no platform or network access. Default-deny " +
				"would silently break SDK calls; default-allow would erode " +
				"sandbox isolation — so neither default is correct.",
		)
	}
	if !database.ValidNetworkMode(in.NetworkMode) {
		return nil, fmt.Errorf("invalid network_mode: %s (allowed: none, egress)", in.NetworkMode)
	}
	if strings.TrimSpace(in.AuthMode) == "" {
		return nil, errors.New(
			"auth_mode is required: 'none' (public — anyone with the URL " +
				"can invoke), 'platform_key' (caller presents an Orva API " +
				"key), or 'signed' (HMAC-SHA256 signature). Default-allow " +
				"silently exposes data, so pick consciously based on who " +
				"should be able to call this function.",
		)
	}
	if !database.ValidAuthMode(in.AuthMode) {
		return nil, fmt.Errorf("invalid auth_mode: %s (allowed: none, platform_key, signed)", in.AuthMode)
	}
	if !database.ValidConcurrencyPolicy(in.ConcurrencyPolicy) {
		return nil, fmt.Errorf("invalid concurrency_policy: %s (allowed: queue, reject)", in.ConcurrencyPolicy)
	}
	if in.MaxConcurrency < 0 {
		return nil, errors.New("max_concurrency must be >= 0 (0 = unlimited)")
	}
	if in.RateLimitPerMin < 0 {
		return nil, errors.New("rate_limit_per_min must be >= 0 (0 = unlimited)")
	}

	// All required fields validated above. Optional fields keep their
	// safe defaults: concurrency_policy=queue, env_vars=empty.
	if in.ConcurrencyPolicy == "" {
		in.ConcurrencyPolicy = database.ConcurrencyPolicyQueue
	}
	if in.EnvVars == nil {
		in.EnvVars = map[string]string{}
	}

	fnID := ids.New()

	fn := &database.Function{
		ID:                fnID,
		Name:              in.Name,
		Description:       in.Description,
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

// updateFunction patches the function record. Mirrors the pool-drain rules
// the REST handler uses:
//   - any spawn-config change (memory, cpu, env, max_concurrency, etc.)
//     triggers RefreshForDeploy — kills idles, busy workers age out
//     naturally;
//   - a network_mode flip triggers Drain — a stale netns is unsafe to
//     reuse, so we hard-reset the pool entry and the next Acquire
//     creates a fresh one with the new config.
func updateFunction(deps Deps, in UpdateFunctionInput) (*database.Function, error) {
	fn, err := resolveFunction(deps, in.FunctionID)
	if err != nil {
		return nil, err
	}
	drainPool := false
	networkModeChanged := false

	if in.Name != nil {
		fn.Name = *in.Name
	}
	if in.Description != nil {
		fn.Description = *in.Description
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
			networkModeChanged = true
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
		if networkModeChanged {
			deps.PoolMgr.Drain(fn.ID)
		} else {
			deps.PoolMgr.RefreshForDeploy(fn.ID)
		}
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
