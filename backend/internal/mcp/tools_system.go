package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpVacuumMu serializes system_vacuum invocations from the MCP layer.
// Independent from the HTTP handler's mutex because MCP tools call the
// DB directly without going through the HTTP path. Two operators —
// one in the dashboard and one via an agent — can still race, but
// SQLite's own EXCLUSIVE lock is the ultimate serialization point;
// this mutex just keeps things tidy and predictable from one side.
var mcpVacuumMu sync.Mutex

// ─── system_health ──────────────────────────────────────────────────

type SystemHealthOutput struct {
	Status            string `json:"status"`
	Database          string `json:"database"`
	SandboxRuntime    string `json:"sandbox_runtime"`
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

func registerSystemTools(rc *regCtx) {
	deps := rc.deps
	rc.group = "system"
	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:        "system_health",
			Title:       "System Health",
			Description: "Health check for the Orva instance. Returns version, uptime, sandbox counters, and host resources. Use this to confirm an Orva instance is reachable before doing anything else.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, SystemHealthOutput, error) {
			var active, total int64
			if deps.Proxy != nil && deps.Proxy.Sandbox != nil {
				active, total = deps.Proxy.Sandbox.Stats()
			}

			// Actually probe. This returned a hardcoded "healthy" and looked
			// at nothing, so the tool whose description says "use this to
			// confirm an Orva instance is reachable before doing anything
			// else" reported healthy on a wedged database and on a host with
			// no nsjail -- where every single invocation fails. The REST
			// health endpoint has always done both checks.
			status := "healthy"
			dbStatus := "ok"
			if deps.DB != nil {
				pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				if err := deps.DB.Ping(pingCtx); err != nil {
					dbStatus, status = "error", "degraded"
				}
			}
			// Informational, like the REST handler: an instance with no
			// nsjail is still reachable, it just cannot run anything.
			sandboxRuntime := "ok"
			if deps.NsjailBin != "" {
				if _, err := os.Stat(deps.NsjailBin); err != nil {
					sandboxRuntime = "unavailable"
				}
			}

			return nil, SystemHealthOutput{
				Status:            status,
				Database:          dbStatus,
				SandboxRuntime:    sandboxRuntime,
				Version:           orDefault(deps.Version, "0.1.0"),
				UptimeSeconds:     int64(time.Since(startTime).Seconds()),
				SandboxActive:     active,
				SandboxLifetime:   total,
				HostNumCPU:        runtime.NumCPU(),
				HostNumGoroutines: runtime.NumGoroutine(),
			}, nil
		},
	)

	// ─── system_metrics ──────────────────────────────────────────────
	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:        "system_metrics",
			Title:       "System Metrics",
			Description: "Return a snapshot of Orva's invocation/build/latency counters and per-function pool stats. Useful for an agent that wants to see how loaded the platform is or which functions are hot.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, SystemMetricsOutput, error) {
			return nil, buildSystemMetrics(deps), nil
		},
	)

	// ─── system_storage ──────────────────────────────────────────────
	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:        "system_storage",
			Title:       "System Storage",
			Description: "Return on-disk sizes for orva.db, the WAL sidecar, and the functions/ tree. Useful before deciding to run system_vacuum — db_free_pages × db_page_size is the upper bound on reclaimable bytes.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, SystemStorageOutput, error) {
			return nil, buildSystemStorage(deps), nil
		},
	)

	// ─── system_vacuum ──────────────────────────────────────────────
	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:        "system_vacuum",
			Title:       "System Vacuum",
			Description: "Run PRAGMA wal_checkpoint(TRUNCATE) followed by VACUUM on orva.db. DESTRUCTIVE: holds an exclusive lock and rewrites the database; every other writer blocks until it returns. Pass confirm=true to actually run; without confirm the tool returns the would-be reclaimable bytes without touching the DB.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in SystemVacuumInput) (*mcpsdk.CallToolResult, SystemVacuumOutput, error) {
			if !in.Confirm {
				info := buildSystemStorage(deps)
				return nil, SystemVacuumOutput{
					DryRun:           true,
					BeforeBytes:      info.DBBytes,
					ReclaimableBytes: info.DBFreePages * info.DBPageSize,
				}, nil
			}
			return runMCPVacuum(deps)
		},
	)

	// ─── list_runtimes ──────────────────────────────────────────────
	regAddTool(rc, permRead,
		&mcpsdk.Tool{
			Name:        "list_runtimes",
			Title:       "List Runtimes",
			Description: "List the language runtimes Orva supports (Node.js, Python — specific minor versions). Each entry includes its id (use as the `runtime` field on create_function), display name, default entrypoint filename, and accepted file extensions.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ListRuntimesOutput, error) {
			return nil, ListRuntimesOutput{Runtimes: supportedRuntimes()}, nil
		},
	)
}

// ─── shared types ──────────────────────────────────────────────────

type SystemMetricsOutput struct {
	UptimeSeconds  int64          `json:"uptime_seconds"`
	Totals         MetricsTotals  `json:"totals"`
	LatencyMS      MetricsLatency `json:"latency_ms"`
	ActiveRequests int64          `json:"active_requests"`
	SandboxActive  int64          `json:"sandbox_active"`
	BuildQueue     MetricsBuildQ  `json:"build_queue"`
	Host           MetricsHost    `json:"host"`
	Pools          []MetricsPool  `json:"pools"`
}

type MetricsHost struct {
	EffectiveCPUWorkers int   `json:"effective_cpu_workers"`
	EffectiveMemoryMB   int64 `json:"effective_memory_capacity_mb"`
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
	FunctionID       string  `json:"function_id"`
	Idle             int     `json:"idle"`
	Busy             int64   `json:"busy"`
	Queued           int64   `json:"queued"`
	Spawning         int64   `json:"spawning"`
	DesiredWorkers   int64   `json:"desired_workers"`
	EffectiveMax     int64   `json:"effective_max"`
	QueueWaitP95MS   float64 `json:"queue_wait_p95_ms"`
	ServiceP95MS     float64 `json:"service_p95_ms"`
	ColdStartP95MS   float64 `json:"cold_start_p95_ms"`
	LimitingReason   string  `json:"limiting_reason"`
	Arrivals         int64   `json:"arrivals"`
	Rejections       int64   `json:"rejections"`
	CapacityTimeouts int64   `json:"capacity_timeouts"`
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
		out.Host = MetricsHost{
			EffectiveCPUWorkers: deps.PoolMgr.EffectiveCPUCapacity(),
			EffectiveMemoryMB:   deps.PoolMgr.EffectiveMemoryCapacity() / 1024 / 1024,
		}
		for _, s := range deps.PoolMgr.Stats() {
			out.Pools = append(out.Pools, MetricsPool{
				FunctionID: s.FunctionID, Idle: s.Idle, Busy: s.Busy,
				Queued: s.Queued, Spawning: s.Spawning,
				DesiredWorkers: s.Desired, EffectiveMax: s.EffectiveMax,
				QueueWaitP95MS: s.QueueWaitP95MS, ServiceP95MS: s.ServiceP95MS,
				ColdStartP95MS: s.ColdStartP95MS, LimitingReason: s.LimitingReason,
				Arrivals: s.Arrivals, Rejections: s.Rejections, CapacityTimeouts: s.CapacityTimeouts,
			})
		}
	}
	return out
}

type RuntimeInfo struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Language       string   `json:"language"`
	DefaultHandler string   `json:"default_handler"`
	Extensions     []string `json:"extensions"`
}

type ListRuntimesOutput struct {
	Runtimes []RuntimeInfo `json:"runtimes"`
}

func supportedRuntimes() []RuntimeInfo {
	return []RuntimeInfo{
		{ID: "node", Name: "Node.js 24 (current)", Version: "24", Language: "javascript", DefaultHandler: "handler.js", Extensions: []string{".js", ".mjs", ".cjs", ".ts"}},
		{ID: "python", Name: "Python 3.14 (current)", Version: "3.14", Language: "python", DefaultHandler: "handler.py", Extensions: []string{".py"}},
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

// ─── system_storage / system_vacuum types & helpers ─────────────────

// SystemStorageOutput mirrors handlers.StorageInfo — kept independent
// so the MCP package doesn't pull the HTTP handlers into its import
// graph (which would cycle on the registry tests).
type SystemStorageOutput struct {
	DBBytes        int64 `json:"db_bytes"`
	DBPages        int64 `json:"db_pages"`
	DBPageSize     int64 `json:"db_page_size"`
	DBFreePages    int64 `json:"db_free_pages"`
	WALBytes       int64 `json:"wal_bytes"`
	FunctionsBytes int64 `json:"functions_bytes"`
	TotalBytes     int64 `json:"total_bytes"`
}

type SystemVacuumInput struct {
	// Confirm must be true to actually run VACUUM. Without it the tool
	// returns a dry-run preview so an agent can show the operator how
	// much VACUUM would reclaim before committing.
	Confirm bool `json:"confirm" jsonschema:"set to true to actually execute VACUUM; false returns a dry-run preview"`
}

type SystemVacuumOutput struct {
	DryRun           bool  `json:"dry_run"`
	BeforeBytes      int64 `json:"before_bytes"`
	AfterBytes       int64 `json:"after_bytes,omitempty"`
	FreedBytes       int64 `json:"freed_bytes,omitempty"`
	DurationMS       int64 `json:"duration_ms,omitempty"`
	ReclaimableBytes int64 `json:"reclaimable_bytes,omitempty"` // dry-run only
}

func buildSystemStorage(deps Deps) SystemStorageOutput {
	out := SystemStorageOutput{}
	if deps.DB == nil {
		return out
	}
	dbPath := deps.DB.Path()
	if st, err := os.Stat(dbPath); err == nil {
		out.DBBytes = st.Size()
	}
	if st, err := os.Stat(dbPath + "-wal"); err == nil {
		out.WALBytes = st.Size()
	}
	if rdb := deps.DB.ReadDB(); rdb != nil {
		_ = rdb.QueryRow(`PRAGMA page_count`).Scan(&out.DBPages)
		_ = rdb.QueryRow(`PRAGMA page_size`).Scan(&out.DBPageSize)
		_ = rdb.QueryRow(`PRAGMA freelist_count`).Scan(&out.DBFreePages)
	}
	if deps.DataDir != "" {
		out.FunctionsBytes = mcpDirSize(filepath.Join(deps.DataDir, "functions"))
	}
	out.TotalBytes = out.DBBytes + out.WALBytes + out.FunctionsBytes
	return out
}

func runMCPVacuum(deps Deps) (*mcpsdk.CallToolResult, SystemVacuumOutput, error) {
	if deps.DB == nil {
		return nil, SystemVacuumOutput{}, fmt.Errorf("database not wired")
	}
	mcpVacuumMu.Lock()
	defer mcpVacuumMu.Unlock()

	dbPath := deps.DB.Path()
	var before int64
	if st, err := os.Stat(dbPath); err == nil {
		before = st.Size()
	}
	started := time.Now()
	if _, err := deps.DB.WriteDB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return nil, SystemVacuumOutput{}, fmt.Errorf("wal_checkpoint: %w", err)
	}
	if _, err := deps.DB.WriteDB().Exec(`VACUUM`); err != nil {
		return nil, SystemVacuumOutput{}, fmt.Errorf("vacuum: %w", err)
	}
	duration := time.Since(started)
	var after int64
	if st, err := os.Stat(dbPath); err == nil {
		after = st.Size()
	}
	freed := before - after
	if freed < 0 {
		freed = 0
	}
	return nil, SystemVacuumOutput{
		BeforeBytes: before,
		AfterBytes:  after,
		FreedBytes:  freed,
		DurationMS:  duration.Milliseconds(),
	}, nil
}

func mcpDirSize(root string) int64 {
	var total int64
	if root == "" {
		return 0
	}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
