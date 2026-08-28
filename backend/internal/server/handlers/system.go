package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/config"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/version"
)

// SystemHandler handles system-level endpoints.
type SystemHandler struct {
	Metrics    *metrics.Metrics
	DB         *database.Database
	Sandbox    *sandbox.Limiter
	PoolMgr    *pool.Manager
	BuildQueue *builder.Queue
	Registry   *registry.Registry
	StartTime  time.Time

	// NsjailBin is the configured nsjail path, checked by Health() as an
	// informational sandbox-readiness signal (never a hard failure — Orva
	// starts without nsjail on purpose). Empty disables the check.
	NsjailBin string
}

// MetricsJSONShape is the structured snapshot the UI consumes. The text
// /metrics endpoint stays for Prometheus scrapers; this is the cheaper
// path for the dashboard so it doesn't have to parse Prom text.
type MetricsJSONShape struct {
	UptimeSeconds  int64           `json:"uptime_seconds"`
	Host           hostBlock       `json:"host"`
	Totals         totalsBlock     `json:"totals"`
	Rates          ratesBlock      `json:"rates"`
	ActiveRequests int64           `json:"active_requests"`
	LatencyMS      latencyBlock    `json:"latency_ms"`
	Sandbox        sandboxBlock    `json:"sandbox"`
	BuildQueue     buildQueueBlock `json:"build_queue"`
	Pools          []poolBlock     `json:"pools"`
}

type hostBlock struct {
	NumCPU              int   `json:"num_cpu"`
	EffectiveCPUWorkers int   `json:"effective_cpu_workers"`
	NumGoroutines       int   `json:"num_goroutines"`
	OrvaAllocMB         int64 `json:"orva_alloc_mb"`
	MemTotalMB          int64 `json:"mem_total_mb"`
	MemAvailableMB      int64 `json:"mem_available_mb"`
	MemReservedMB       int64 `json:"mem_reserved_mb"`
	EffectiveMemoryMB   int64 `json:"effective_memory_capacity_mb"`
}

type totalsBlock struct {
	Invocations int64 `json:"invocations"`
	ColdStarts  int64 `json:"cold_starts"`
	WarmHits    int64 `json:"warm_hits"`
	Builds      int64 `json:"builds"`
	BuildErrors int64 `json:"build_errors"`
}

type ratesBlock struct {
	ColdStartPct float64 `json:"cold_start_pct"` // 0–100
}

type latencyBlock struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	P99 int64 `json:"p99"`
}

type sandboxBlock struct {
	Active int64 `json:"active"`
	Total  int64 `json:"total"`
}

type buildQueueBlock struct {
	Pending int `json:"pending"`
	Workers int `json:"workers"`
}

type poolBlock struct {
	FunctionID       string  `json:"function_id"`
	FunctionName     string  `json:"function_name"`
	Idle             int     `json:"idle"`
	Busy             int64   `json:"busy"`
	Queued           int64   `json:"queued"`
	Spawning         int64   `json:"spawning"`
	Arrivals         int64   `json:"arrivals"`
	Spawned          int64   `json:"spawned"`
	Killed           int64   `json:"killed"`
	DesiredWorkers   int64   `json:"desired_workers"`
	EffectiveMax     int64   `json:"effective_max"`
	StableRate       float64 `json:"stable_rate"`
	BurstRate        float64 `json:"burst_rate"`
	QueueWaitP95MS   float64 `json:"queue_wait_p95_ms"`
	ServiceP95MS     float64 `json:"service_p95_ms"`
	ColdStartP95MS   float64 `json:"cold_start_p95_ms"`
	LimitingReason   string  `json:"limiting_reason"`
	Rejections       int64   `json:"rejections"`
	CapacityTimeouts int64   `json:"capacity_timeouts"`
	MemLimitMB       int64   `json:"mem_limit_mb"`
	CPULimit         float64 `json:"cpu_limit"`
}

// Health handles GET /api/v1/system/health.
//
// The database is the single HARD liveness gate: if a `SELECT 1` fails (locked,
// corrupt, or unmounted orva.db) the endpoint returns 503 + status "degraded"
// so Docker HEALTHCHECK, install.sh's wait loop, and any orchestrator actually
// react instead of keeping a dead instance in rotation. Sandbox reachability is
// reported but never flips the status — Orva intentionally starts before nsjail
// is present (invocations fail until it is installed) and CI boots the server
// without a sandbox on the ready-poll, so a missing nsjail must stay a 200.
func (h *SystemHandler) Health(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(h.StartTime).Seconds()

	var active, total int64
	if h.Sandbox != nil {
		active, total = h.Sandbox.Stats()
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Hard gate: probe the DB with a bounded timeout so a wedged database can't
	// hang the health probe itself.
	dbStatus := "ok"
	overall := "healthy"
	httpStatus := http.StatusOK
	if h.DB != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := h.DB.Ping(ctx); err != nil {
			dbStatus = "error"
			overall = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}
	}

	// Informational only — never changes httpStatus (see doc comment).
	sandboxRuntime := "ok"
	if h.NsjailBin != "" {
		if _, err := os.Stat(h.NsjailBin); err != nil {
			sandboxRuntime = "unavailable"
		}
	}

	// Only a container image stamps ORVA_IMAGE: the release publishes one tag
	// (:latest) and bare metal has none, so a Version-derived ref would 404.
	resp := map[string]any{
		"status":         overall,
		"version":        version.Version,
		"commit":         version.Commit,
		"build_time":     version.BuildTime,
		"image":          os.Getenv("ORVA_IMAGE"),
		"uptime_seconds": int(uptime),
		"database": map[string]any{
			"status": dbStatus,
		},
		"sandbox": map[string]any{
			"active_executions":   active,
			"lifetime_executions": total,
			"runtime":             sandboxRuntime,
		},
		"host": map[string]any{
			"num_cpu":       runtime.NumCPU(),
			"num_goroutine": runtime.NumGoroutine(),
			"alloc_mb":      int(memStats.Alloc / 1024 / 1024),
		},
	}
	if h.DB != nil {
		writer := h.DB.WriterStats()
		writerStatus := "ok"
		// Slot depth alone under-reports: a queue holding a handful of
		// captured request bodies is megabytes deep at 1% of its slots.
		if (writer.CriticalCap > 0 && writer.CriticalDepth*100/writer.CriticalCap >= 80) ||
			(writer.TelemetryCap > 0 && writer.TelemetryDepth*100/writer.TelemetryCap >= 80) ||
			(writer.CriticalCapBytes > 0 && writer.CriticalBytes*100/writer.CriticalCapBytes >= 80) ||
			(writer.TelemetryCapBytes > 0 && writer.TelemetryBytes*100/writer.TelemetryCapBytes >= 80) {
			writerStatus = "saturated"
		}
		resp["writer"] = map[string]any{
			"status": writerStatus, "critical_queue_depth": writer.CriticalDepth,
			"critical_queue_bytes":  writer.CriticalBytes,
			"telemetry_queue_bytes": writer.TelemetryBytes,
			"telemetry_queue_depth": writer.TelemetryDepth,
			"critical_timeouts":     writer.CriticalTimeouts,
			"critical_failures":     writer.CriticalFailures,
			"dropped_telemetry":     writer.DroppedTelemetry,
		}
	}

	respond.JSON(w, httpStatus, resp)
}

// promHeader emits the Prometheus `# HELP`/`# TYPE` preamble for a metric
// family. Every family must carry exactly one TYPE line for strict
// OpenMetrics/Prometheus parsers to accept the exposition.
func promHeader(w http.ResponseWriter, name, mtype, help string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, mtype)
}

// writeHistogram emits a Prometheus histogram family (`# HELP`/`# TYPE
// histogram` then `<name>_bucket{le="..."}` for every boundary plus `+Inf`,
// then `<name>_count` and `<name>_sum`). The `_sum` value is in milliseconds
// (matches the `_ms` in the metric name).
func writeHistogram(w http.ResponseWriter, name, help string, h metrics.HistogramSnapshot) {
	promHeader(w, name, "histogram", help)
	for i, le := range h.BucketsMS {
		fmt.Fprintf(w, "%s_bucket{le=\"%g\"} %d\n", name, le, h.BucketCounts[i])
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", name, h.Count)
	fmt.Fprintf(w, "%s_count %d\n", name, h.Count)
	fmt.Fprintf(w, "%s_sum %d\n", name, h.SumMS)
}

// GetMetrics handles GET /api/v1/system/metrics. Also wired at /metrics
// at the root so Prometheus scrapers can use the convention path.
func (h *SystemHandler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	snap := h.Metrics.Snapshot()

	var active, total int64
	if h.Sandbox != nil {
		active, total = h.Sandbox.Stats()
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	promHeader(w, "orva_invocations_total", "counter", "Total function invocations served.")
	fmt.Fprintf(w, "orva_invocations_total %d\n", snap.TotalInvocations)
	promHeader(w, "orva_cold_starts_total", "counter", "Invocations that required a cold sandbox spawn.")
	fmt.Fprintf(w, "orva_cold_starts_total %d\n", snap.ColdStarts)
	promHeader(w, "orva_warm_hits_total", "counter", "Invocations served by a warm pooled worker.")
	fmt.Fprintf(w, "orva_warm_hits_total %d\n", snap.WarmHits)
	promHeader(w, "orva_builds_total", "counter", "Total function builds attempted.")
	fmt.Fprintf(w, "orva_builds_total %d\n", snap.TotalBuilds)
	promHeader(w, "orva_build_errors_total", "counter", "Builds that ended in failure.")
	fmt.Fprintf(w, "orva_build_errors_total %d\n", snap.BuildErrors)
	promHeader(w, "orva_active_requests", "gauge", "Invocations currently in flight.")
	fmt.Fprintf(w, "orva_active_requests %d\n", snap.ActiveRequests)
	// Invocation latency is exposed as a single histogram family. (The former
	// orva_invocation_duration_ms{quantile=...} summary series were dropped:
	// declaring the same family name as BOTH a summary and a histogram is
	// invalid OpenMetrics and made strict scrapers reject the whole exposition.
	// p50/p95/p99 remain available on GET /api/v1/system/metrics.json, and
	// Prometheus can recompute them from the buckets via histogram_quantile().)
	writeHistogram(w, "orva_invocation_duration_ms", "Invocation wall-clock duration in milliseconds.", h.Metrics.SnapshotInvocationHistogram())
	// Sandbox spawn-duration histogram, populated by the pool layer on every
	// successful worker spawn.
	writeHistogram(w, "orva_sandbox_spawn_duration_ms", "nsjail sandbox spawn duration in milliseconds.", h.Metrics.SnapshotSpawnHistogram())
	promHeader(w, "orva_sandbox_active", "gauge", "Sandboxes currently executing.")
	fmt.Fprintf(w, "orva_sandbox_active %d\n", active)
	promHeader(w, "orva_sandbox_total", "counter", "Lifetime sandbox executions.")
	fmt.Fprintf(w, "orva_sandbox_total %d\n", total)
	// Job queue depth — pending jobs that the runner hasn't claimed yet.
	// Cheap COUNT per scrape; the metrics endpoint isn't on the hot
	// path. Returns 0 cleanly when there are no pending jobs (or when
	// the DB handle is missing in tests).
	if h.DB != nil {
		writer := h.DB.WriterStats()
		promHeader(w, "orva_writer_queue_depth", "gauge", "Pending asynchronous database writes by priority.")
		fmt.Fprintf(w, "orva_writer_queue_depth{priority=\"critical\"} %d\n", writer.CriticalDepth)
		fmt.Fprintf(w, "orva_writer_queue_depth{priority=\"telemetry\"} %d\n", writer.TelemetryDepth)
		promHeader(w, "orva_writer_critical_timeouts_total", "counter", "Critical database writes that exceeded their enqueue deadline.")
		fmt.Fprintf(w, "orva_writer_critical_timeouts_total %d\n", writer.CriticalTimeouts)
		promHeader(w, "orva_writer_critical_failures_total", "counter", "Critical database writes lost to transaction failures after enqueue.")
		fmt.Fprintf(w, "orva_writer_critical_failures_total %d\n", writer.CriticalFailures)
		promHeader(w, "orva_writer_dropped_telemetry_total", "counter", "Telemetry writes dropped because the bounded queue was full.")
		fmt.Fprintf(w, "orva_writer_dropped_telemetry_total %d\n", writer.DroppedTelemetry)
		kv := h.DB.KVMetrics()
		promHeader(w, "orva_kv_operations_total", "counter", "KV operations by type.")
		promHeader(w, "orva_kv_errors_total", "counter", "KV operation failures by type.")
		promHeader(w, "orva_kv_timeouts_total", "counter", "KV operations canceled by request or function deadlines.")
		promHeader(w, "orva_kv_latency_ms_total", "counter", "Cumulative KV operation latency in milliseconds.")
		for _, operation := range kv.Operations {
			fmt.Fprintf(w, "orva_kv_operations_total{operation=%q} %d\n", operation.Operation, operation.Count)
			fmt.Fprintf(w, "orva_kv_errors_total{operation=%q} %d\n", operation.Operation, operation.Errors)
			fmt.Fprintf(w, "orva_kv_timeouts_total{operation=%q} %d\n", operation.Operation, operation.Timeouts)
			fmt.Fprintf(w, "orva_kv_latency_ms_total{operation=%q} %.3f\n", operation.Operation, float64(operation.LatencyTotalNS)/float64(time.Millisecond))
		}
		promHeader(w, "orva_kv_batch_rollbacks_total", "counter", "Atomic KV batches rolled back after database or deadline failure.")
		fmt.Fprintf(w, "orva_kv_batch_rollbacks_total %d\n", kv.Rollbacks)
		var depth int64
		if rdb := h.DB.ReadDB(); rdb != nil {
			_ = rdb.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status='pending'`).Scan(&depth)
		}
		promHeader(w, "orva_jobs_queue_depth", "gauge", "Pending jobs not yet claimed by the runner.")
		fmt.Fprintf(w, "orva_jobs_queue_depth %d\n", depth)
	}

	// Per-function warm pool stats — exposed so operators can see which
	// functions are hot, which keep getting killed (OOMing, crashing), and
	// the cold-start rate per fn. TYPE/HELP are emitted once here; the series
	// themselves are labelled per function inside the loop.
	if h.PoolMgr != nil {
		promHeader(w, "orva_pool_idle", "gauge", "Idle warm workers per function.")
		promHeader(w, "orva_pool_busy", "gauge", "Busy workers per function.")
		promHeader(w, "orva_pool_queued", "gauge", "Invocations waiting for capacity per function.")
		promHeader(w, "orva_pool_spawning", "gauge", "Workers currently starting per function.")
		promHeader(w, "orva_pool_arrivals_total", "counter", "Invocation arrivals observed before capacity waits.")
		promHeader(w, "orva_pool_spawned_total", "counter", "Workers spawned per function.")
		promHeader(w, "orva_pool_killed_total", "counter", "Workers killed per function.")
		promHeader(w, "orva_pool_desired_workers", "gauge", "Controller desired workers per function.")
		promHeader(w, "orva_pool_effective_max", "gauge", "Effective host and operator capacity per function.")
		promHeader(w, "orva_pool_queue_wait_p95_ms", "gauge", "Observed queue-wait p95 per function.")
		promHeader(w, "orva_pool_service_p95_ms", "gauge", "Observed service-time p95 per function.")
		promHeader(w, "orva_pool_cold_start_p95_ms", "gauge", "Observed worker start p95 per function.")
		promHeader(w, "orva_pool_rejections_total", "counter", "Pool admission rejections per function.")
		promHeader(w, "orva_pool_capacity_timeouts_total", "counter", "Capacity waits that reached their deadline.")
		promHeader(w, "orva_pool_limiting_reason", "gauge", "Current limiting reason as a labeled one-hot gauge.")
		for _, s := range h.PoolMgr.Stats() {
			fmt.Fprintf(w, "orva_pool_idle{function_id=%q} %d\n", s.FunctionID, s.Idle)
			fmt.Fprintf(w, "orva_pool_busy{function_id=%q} %d\n", s.FunctionID, s.Busy)
			fmt.Fprintf(w, "orva_pool_queued{function_id=%q} %d\n", s.FunctionID, s.Queued)
			fmt.Fprintf(w, "orva_pool_spawning{function_id=%q} %d\n", s.FunctionID, s.Spawning)
			fmt.Fprintf(w, "orva_pool_arrivals_total{function_id=%q} %d\n", s.FunctionID, s.Arrivals)
			fmt.Fprintf(w, "orva_pool_spawned_total{function_id=%q} %d\n", s.FunctionID, s.Spawned)
			fmt.Fprintf(w, "orva_pool_killed_total{function_id=%q} %d\n", s.FunctionID, s.Killed)
			fmt.Fprintf(w, "orva_pool_desired_workers{function_id=%q} %d\n", s.FunctionID, s.Desired)
			fmt.Fprintf(w, "orva_pool_effective_max{function_id=%q} %d\n", s.FunctionID, s.EffectiveMax)
			fmt.Fprintf(w, "orva_pool_queue_wait_p95_ms{function_id=%q} %.3f\n", s.FunctionID, s.QueueWaitP95MS)
			fmt.Fprintf(w, "orva_pool_service_p95_ms{function_id=%q} %.3f\n", s.FunctionID, s.ServiceP95MS)
			fmt.Fprintf(w, "orva_pool_cold_start_p95_ms{function_id=%q} %.3f\n", s.FunctionID, s.ColdStartP95MS)
			fmt.Fprintf(w, "orva_pool_rejections_total{function_id=%q} %d\n", s.FunctionID, s.Rejections)
			fmt.Fprintf(w, "orva_pool_capacity_timeouts_total{function_id=%q} %d\n", s.FunctionID, s.CapacityTimeouts)
			fmt.Fprintf(w, "orva_pool_limiting_reason{function_id=%q,reason=%q} 1\n", s.FunctionID, s.LimitingReason)
		}
		tot, avail, res := h.PoolMgr.HostMemStats()
		promHeader(w, "orva_host_mem_total_bytes", "gauge", "Total host memory (bytes).")
		fmt.Fprintf(w, "orva_host_mem_total_bytes %d\n", tot)
		promHeader(w, "orva_host_mem_available_bytes", "gauge", "Available host memory (bytes).")
		fmt.Fprintf(w, "orva_host_mem_available_bytes %d\n", avail)
		promHeader(w, "orva_host_mem_reserved_bytes", "gauge", "Host memory reserved by warm pools (bytes).")
		fmt.Fprintf(w, "orva_host_mem_reserved_bytes %d\n", res)
		promHeader(w, "orva_host_effective_cpu_workers", "gauge", "Effective worker capacity derived from cgroup v2 CPU quota.")
		fmt.Fprintf(w, "orva_host_effective_cpu_workers %d\n", h.PoolMgr.EffectiveCPUCapacity())
		promHeader(w, "orva_host_effective_memory_capacity_bytes", "gauge", "Memory currently available for additional worker admission.")
		fmt.Fprintf(w, "orva_host_effective_memory_capacity_bytes %d\n", h.PoolMgr.EffectiveMemoryCapacity())
	}
}

// GetMetricsJSON handles GET /api/v1/system/metrics.json — same data as the
// Prometheus-text endpoint but pre-structured for the UI.
func (h *SystemHandler) GetMetricsJSON(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, h.BuildMetricsSnapshot())
}

// BuildMetricsSnapshot builds the same JSON shape that GetMetricsJSON
// returns, but as a value so it can be consumed by the SSE metrics
// publisher (or any other in-process caller). Cheap — atomic counters
// and one O(N pools) walk.
func (h *SystemHandler) BuildMetricsSnapshot() MetricsJSONShape {
	snap := h.Metrics.Snapshot()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	out := MetricsJSONShape{
		UptimeSeconds:  int64(time.Since(h.StartTime).Seconds()),
		ActiveRequests: snap.ActiveRequests,
		Host: hostBlock{
			NumCPU:        runtime.NumCPU(),
			NumGoroutines: runtime.NumGoroutine(),
			OrvaAllocMB:   int64(memStats.Alloc / 1024 / 1024),
		},
		Totals: totalsBlock{
			Invocations: snap.TotalInvocations,
			ColdStarts:  snap.ColdStarts,
			WarmHits:    snap.WarmHits,
			Builds:      snap.TotalBuilds,
			BuildErrors: snap.BuildErrors,
		},
		LatencyMS: latencyBlock{
			P50: snap.P50MS,
			P95: snap.P95MS,
			P99: snap.P99MS,
		},
	}

	if snap.TotalInvocations > 0 {
		out.Rates.ColdStartPct = float64(snap.ColdStarts) / float64(snap.TotalInvocations) * 100.0
	}

	if h.Sandbox != nil {
		active, total := h.Sandbox.Stats()
		out.Sandbox = sandboxBlock{Active: active, Total: total}
	}

	if h.BuildQueue != nil {
		out.BuildQueue = buildQueueBlock{
			Pending: h.BuildQueue.QueuedDepth(),
			Workers: h.BuildQueue.Workers(),
		}
	}

	if h.PoolMgr != nil {
		tot, avail, res := h.PoolMgr.HostMemStats()
		out.Host.EffectiveCPUWorkers = h.PoolMgr.EffectiveCPUCapacity()
		out.Host.EffectiveMemoryMB = h.PoolMgr.EffectiveMemoryCapacity() / 1024 / 1024
		out.Host.MemTotalMB = tot / 1024 / 1024
		out.Host.MemAvailableMB = avail / 1024 / 1024
		out.Host.MemReservedMB = res / 1024 / 1024

		// Resolve function names for nicer UI cards. Cheap — registry
		// reads are O(1) sync.Map lookups.
		stats := h.PoolMgr.Stats()
		out.Pools = make([]poolBlock, 0, len(stats))
		for _, s := range stats {
			name := s.FunctionID
			var memLimitMB int64
			var cpuLimit float64
			if h.Registry != nil {
				if fn, err := h.Registry.Get(s.FunctionID); err == nil && fn != nil {
					name = fn.Name
					memLimitMB = fn.MemoryMB
					cpuLimit = fn.CPUs
				}
			}
			out.Pools = append(out.Pools, poolBlock{
				FunctionID: s.FunctionID, FunctionName: name,
				Idle: s.Idle, Busy: s.Busy, Queued: s.Queued, Spawning: s.Spawning,
				Arrivals: s.Arrivals, Spawned: s.Spawned, Killed: s.Killed,
				DesiredWorkers: s.Desired, EffectiveMax: s.EffectiveMax,
				StableRate: s.StableRate, BurstRate: s.BurstRate,
				QueueWaitP95MS: s.QueueWaitP95MS, ServiceP95MS: s.ServiceP95MS,
				ColdStartP95MS: s.ColdStartP95MS, LimitingReason: s.LimitingReason,
				Rejections: s.Rejections, CapacityTimeouts: s.CapacityTimeouts,
				MemLimitMB: memLimitMB, CPULimit: cpuLimit,
			})
		}
	}

	return out
}

// ─── Storage breakdown + VACUUM (v0.4) ─────────────────────────────

// SystemStorageHandler exposes disk-usage breakdowns for the data dir
// plus a "compact database" action that runs SQLite VACUUM.
//
// VACUUM acquires an EXCLUSIVE lock on the database for its full
// duration — every other writer is blocked until it returns. On a
// healthy single-node Orva this is typically sub-second, but a stuffed
// activity_log + executions table can push it into multi-second
// territory. We serialize requests behind vacuumMu so two operators
// hammering the button don't queue up a stampede; the second caller
// sees a 409.
//
// Both endpoints are admin-gated by middleware_auth.go::requiredPermission.
type SystemStorageHandler struct {
	DB  *database.Database
	Cfg *config.Config

	// vacuumMu is held for the duration of a VACUUM. The TryLock pattern
	// surfaces a friendly 409 instead of stacking callers behind a
	// blocking write that could already be 30 s deep.
	vacuumMu sync.Mutex
}

// StorageInfo is the response shape for GET /api/v1/system/storage.
type StorageInfo struct {
	DBBytes        int64 `json:"db_bytes"`        // size of orva.db on disk
	DBPages        int64 `json:"db_pages"`        // PRAGMA page_count
	DBPageSize     int64 `json:"db_page_size"`    // PRAGMA page_size
	DBFreePages    int64 `json:"db_free_pages"`   // PRAGMA freelist_count — reclaimable on next VACUUM
	WALBytes       int64 `json:"wal_bytes"`       // size of orva.db-wal sidecar
	FunctionsBytes int64 `json:"functions_bytes"` // recursive size of <data_dir>/functions
	TotalBytes     int64 `json:"total_bytes"`     // db + wal + functions
}

// VacuumResult is the response shape for POST /api/v1/system/vacuum.
type VacuumResult struct {
	BeforeBytes int64 `json:"before_bytes"` // orva.db size before VACUUM
	AfterBytes  int64 `json:"after_bytes"`  // orva.db size after VACUUM
	FreedBytes  int64 `json:"freed_bytes"`  // BeforeBytes - AfterBytes (>=0 on success)
	DurationMS  int64 `json:"duration_ms"`  // wall-clock time in VACUUM (incl. WAL checkpoint)
}

// GetStorage handles GET /api/v1/system/storage. Returns DB + functions
// tree sizes plus the SQLite page-level breakdown used by the Settings
// UI to render the "compact" affordance.
func (h *SystemStorageHandler) GetStorage(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil || h.Cfg == nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "storage handler not wired", "")
		return
	}

	info := StorageInfo{}

	dbPath := h.DB.Path()
	if st, err := os.Stat(dbPath); err == nil {
		info.DBBytes = st.Size()
	}
	if st, err := os.Stat(dbPath + "-wal"); err == nil {
		info.WALBytes = st.Size()
	}

	// Page-level stats. Use the read-only handle — these pragmas don't
	// lock and the read pool has more capacity than the singleton writer.
	if rdb := h.DB.ReadDB(); rdb != nil {
		_ = rdb.QueryRow(`PRAGMA page_count`).Scan(&info.DBPages)
		_ = rdb.QueryRow(`PRAGMA page_size`).Scan(&info.DBPageSize)
		_ = rdb.QueryRow(`PRAGMA freelist_count`).Scan(&info.DBFreePages)
	}

	functionsDir := filepath.Join(h.Cfg.Data.Dir, "functions")
	info.FunctionsBytes = storageDirSize(functionsDir)

	info.TotalBytes = info.DBBytes + info.WALBytes + info.FunctionsBytes

	respond.JSON(w, http.StatusOK, info)
}

// Vacuum handles POST /api/v1/system/vacuum. Runs PRAGMA wal_checkpoint(TRUNCATE)
// to fold the WAL back into the main file, then VACUUM to repack pages
// and shrink the file. Returns the before/after sizes so the UI can
// surface "freed N MB".
//
// VACUUM holds an exclusive lock and rewrites the database — every
// writer blocks until it returns. The handler serializes requests
// through vacuumMu and returns 409 if another VACUUM is already in
// flight, so a stuck button-mash doesn't queue.
func (h *SystemStorageHandler) Vacuum(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil || h.Cfg == nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "storage handler not wired", "")
		return
	}

	if !h.vacuumMu.TryLock() {
		respond.Error(w, http.StatusConflict, "VACUUM_IN_PROGRESS", "another VACUUM is already running; retry shortly", "")
		return
	}
	defer h.vacuumMu.Unlock()

	dbPath := h.DB.Path()
	var before int64
	if st, err := os.Stat(dbPath); err == nil {
		before = st.Size()
	}

	started := time.Now()

	// Step 1: checkpoint the WAL into the main DB. Without this any
	// committed-but-uncheckpointed pages live in orva.db-wal, and the
	// shrink we're about to do isn't visible to operators looking at
	// `ls -la orva.db`. TRUNCATE truncates the WAL afterwards.
	// wal_checkpoint returns a ROW -- (busy, log_pages, checkpointed_pages) --
	// and reports contention through busy=1, not through an error. Exec
	// discards the row, so a checkpoint that was blocked by a reader read as
	// complete success and the VACUUM below then ran against an
	// uncheckpointed WAL.
	var busy, logPages, checkpointed int
	if err := h.DB.WriteDB().QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).
		Scan(&busy, &logPages, &checkpointed); err != nil {
		respond.Error(w, http.StatusInternalServerError, "VACUUM_FAILED", "wal_checkpoint: "+err.Error(), "")
		return
	}
	if busy != 0 {
		respond.Error(w, http.StatusConflict, "CHECKPOINT_BUSY",
			"could not checkpoint the WAL: another connection is holding a read lock; retry shortly", "")
		return
	}

	// Step 2: VACUUM. Rewrites every page sequentially, drops the
	// freelist, and shrinks the file. Blocks every other writer for the
	// duration — see handler comment.
	if _, err := h.DB.WriteDB().Exec(`VACUUM`); err != nil {
		respond.Error(w, http.StatusInternalServerError, "VACUUM_FAILED", err.Error(), "")
		return
	}

	duration := time.Since(started)

	var after int64
	if st, err := os.Stat(dbPath); err == nil {
		after = st.Size()
	}
	freed := before - after
	if freed < 0 {
		// VACUUM occasionally grows the file slightly when an operator
		// vacuums a database that was already tightly packed (page
		// metadata fluctuations). Surface 0 instead of a confusing
		// negative number.
		freed = 0
	}

	respond.JSON(w, http.StatusOK, VacuumResult{
		BeforeBytes: before,
		AfterBytes:  after,
		FreedBytes:  freed,
		DurationMS:  duration.Milliseconds(),
	})
}

// storageDirSize returns the cumulative size of every regular file
// beneath root. Errors during walk are tolerated (we'd rather show a
// slightly low number than fail the whole storage card); symlinks
// contribute nothing — Walk reports the link's own size, not the
// target's, and we skip them explicitly.
func storageDirSize(root string) int64 {
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
