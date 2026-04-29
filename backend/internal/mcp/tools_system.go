package mcp

import (
	"context"
	"runtime"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── system_health ──────────────────────────────────────────────────

type SystemHealthOutput struct {
	Status            string `json:"status"`
	Version           string `json:"version"`
	UptimeSeconds     int64  `json:"uptime_seconds"`
	SandboxActive     int64  `json:"sandbox_active"`
	SandboxLifetime   int64  `json:"sandbox_lifetime"`
	HostNumCPU        int    `json:"host_num_cpu"`
	HostNumGoroutines int    `json:"host_num_goroutines"`
}

// startTime is captured the first time NewHandler runs so system_health
// can report uptime. Captured here rather than threaded through Deps
// to keep the wire-up surface small.
var startTime = time.Now()

func registerSystemTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "system_health",
				Description: "Health check for the Orva instance. Returns version, uptime, sandbox counters, and host resources. Use this to confirm an Orva instance is reachable before doing anything else.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, SystemHealthOutput, error) {
				var active, total int64
				if deps.Proxy != nil && deps.Proxy.Sandbox != nil {
					active, total = deps.Proxy.Sandbox.Stats()
				}
				return nil, SystemHealthOutput{
					Status:            "healthy",
					Version:           orDefault(deps.Version, "0.1.0"),
					UptimeSeconds:     int64(time.Since(startTime).Seconds()),
					SandboxActive:     active,
					SandboxLifetime:   total,
					HostNumCPU:        runtime.NumCPU(),
					HostNumGoroutines: runtime.NumGoroutine(),
				}, nil
			},
		)
	})

	// ─── system_metrics ──────────────────────────────────────────────
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "system_metrics",
				Description: "Return a snapshot of Orva's invocation/build/latency counters and per-function pool stats. Useful for an agent that wants to see how loaded the platform is or which functions are hot.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, SystemMetricsOutput, error) {
				return nil, buildSystemMetrics(deps), nil
			},
		)
	})

	// ─── list_runtimes ──────────────────────────────────────────────
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_runtimes",
				Description: "List the language runtimes Orva supports (Node.js, Python — specific minor versions). Each entry includes its id (use as the `runtime` field on create_function), display name, default entrypoint filename, and accepted file extensions.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ListRuntimesOutput, error) {
				return nil, ListRuntimesOutput{Runtimes: supportedRuntimes()}, nil
			},
		)
	})
}

// ─── shared types ──────────────────────────────────────────────────

type SystemMetricsOutput struct {
	UptimeSeconds  int64           `json:"uptime_seconds"`
	Totals         MetricsTotals   `json:"totals"`
	LatencyMS      MetricsLatency  `json:"latency_ms"`
	ActiveRequests int64           `json:"active_requests"`
	SandboxActive  int64           `json:"sandbox_active"`
	BuildQueue     MetricsBuildQ   `json:"build_queue"`
	Pools          []MetricsPool   `json:"pools"`
}

type MetricsTotals struct {
	Invocations int64 `json:"invocations"`
	ColdStarts  int64 `json:"cold_starts"`
	WarmHits    int64 `json:"warm_hits"`
	Builds      int64 `json:"builds"`
	BuildErrors int64 `json:"build_errors"`
}

type MetricsLatency struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	P99 int64 `json:"p99"`
}

type MetricsBuildQ struct {
	Pending int `json:"pending"`
	Workers int `json:"workers"`
}

type MetricsPool struct {
	FunctionID    string  `json:"function_id"`
	Idle          int     `json:"idle"`
	Busy          int64   `json:"busy"`
	Spawned       int64   `json:"spawned"`
	Killed        int64   `json:"killed"`
	RateEWMA      float64 `json:"rate_ewma"`
	LatencyEWMAms float64 `json:"latency_ewma_ms"`
}

func buildSystemMetrics(deps Deps) SystemMetricsOutput {
	out := SystemMetricsOutput{
		UptimeSeconds: int64(time.Since(startTime).Seconds()),
	}
	if deps.Metrics != nil {
		snap := deps.Metrics.Snapshot()
		out.Totals = MetricsTotals{
			Invocations: snap.TotalInvocations,
			ColdStarts:  snap.ColdStarts,
			WarmHits:    snap.WarmHits,
			Builds:      snap.TotalBuilds,
			BuildErrors: snap.BuildErrors,
		}
		out.LatencyMS = MetricsLatency{P50: snap.P50MS, P95: snap.P95MS, P99: snap.P99MS}
		out.ActiveRequests = snap.ActiveRequests
	}
	if deps.Proxy != nil && deps.Proxy.Sandbox != nil {
		out.SandboxActive, _ = deps.Proxy.Sandbox.Stats()
	}
	if deps.BuildQueue != nil {
		out.BuildQueue = MetricsBuildQ{Pending: deps.BuildQueue.QueuedDepth(), Workers: deps.BuildQueue.Workers()}
	}
	if deps.PoolMgr != nil {
		for _, s := range deps.PoolMgr.Stats() {
			out.Pools = append(out.Pools, MetricsPool{
				FunctionID:    s.FunctionID,
				Idle:          s.Idle,
				Busy:          s.Busy,
				Spawned:       s.Spawned,
				Killed:        s.Killed,
				RateEWMA:      s.RateEWMA,
				LatencyEWMAms: s.LatencyEWMAms,
			})
		}
	}
	return out
}

type RuntimeInfo struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Language       string   `json:"language"`
	DefaultHandler string   `json:"default_handler"`
	Extensions     []string `json:"extensions"`
}

type ListRuntimesOutput struct {
	Runtimes []RuntimeInfo `json:"runtimes"`
}

func supportedRuntimes() []RuntimeInfo {
	return []RuntimeInfo{
		{ID: "node22", Name: "Node.js 22 (Active LTS)", Language: "javascript", DefaultHandler: "handler.js", Extensions: []string{".js", ".mjs", ".cjs"}},
		{ID: "node24", Name: "Node.js 24 (Current LTS)", Language: "javascript", DefaultHandler: "handler.js", Extensions: []string{".js", ".mjs", ".cjs"}},
		{ID: "python313", Name: "Python 3.13", Language: "python", DefaultHandler: "handler.py", Extensions: []string{".py"}},
		{ID: "python314", Name: "Python 3.14", Language: "python", DefaultHandler: "handler.py", Extensions: []string{".py"}},
	}
}

func ptrTrue() *bool  { v := true; return &v }
func ptrFalse() *bool { v := false; return &v }

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
