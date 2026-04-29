package handlers

import (
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
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
}

// MetricsJSONShape is the structured snapshot the UI consumes. The text
// /metrics endpoint stays for Prometheus scrapers; this is the cheaper
// path for the dashboard so it doesn't have to parse Prom text.
type MetricsJSONShape struct {
	UptimeSeconds  int64                `json:"uptime_seconds"`
	Host           hostBlock            `json:"host"`
	Totals         totalsBlock          `json:"totals"`
	Rates          ratesBlock           `json:"rates"`
	ActiveRequests int64                `json:"active_requests"`
	LatencyMS      latencyBlock         `json:"latency_ms"`
	Sandbox        sandboxBlock         `json:"sandbox"`
	BuildQueue     buildQueueBlock      `json:"build_queue"`
	Pools          []poolBlock          `json:"pools"`
}

type hostBlock struct {
	NumCPU         int    `json:"num_cpu"`
	NumGoroutines  int    `json:"num_goroutines"`
	OrvaAllocMB    int64  `json:"orva_alloc_mb"`
	MemTotalMB     int64  `json:"mem_total_mb"`
	MemAvailableMB int64  `json:"mem_available_mb"`
	MemReservedMB  int64  `json:"mem_reserved_mb"`
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
	FunctionID    string  `json:"function_id"`
	FunctionName  string  `json:"function_name"`
	Idle          int     `json:"idle"`
	Busy          int64   `json:"busy"`
	Spawned       int64   `json:"spawned"`
	Killed        int64   `json:"killed"`
	ScaleUps      int64   `json:"scale_ups"`
	ScaleDowns    int64   `json:"scale_downs"`
	RateEWMA      float64 `json:"rate_ewma"`
	LatencyEWMAms float64 `json:"latency_ewma_ms"`
	DynamicMax    int64   `json:"dynamic_max"`
	Target        int     `json:"target"`
	MemUsedAvgMB  float64 `json:"mem_used_avg_mb"`  // EWMA of memory.current at release; 0 if cgroups disabled
	CPUFracAvg    float64 `json:"cpu_frac_avg"`     // EWMA of CPU fraction per invocation (0–1)
	MemLimitMB    int64   `json:"mem_limit_mb"`     // configured memory_mb for this function
	CPULimit      float64 `json:"cpu_limit"`        // configured cpus for this function (0 = uncapped)
}

// Health handles GET /api/v1/system/health.
func (h *SystemHandler) Health(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(h.StartTime).Seconds()

	var active, total int64
	if h.Sandbox != nil {
		active, total = h.Sandbox.Stats()
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	resp := map[string]any{
		"status":         "healthy",
		"version":        "0.1.0",
		"uptime_seconds": int(uptime),
		"database": map[string]any{
			"status": "ok",
		},
		"sandbox": map[string]any{
			"active_executions":   active,
			"lifetime_executions": total,
		},
		"host": map[string]any{
			"num_cpu":     runtime.NumCPU(),
			"num_goroutine": runtime.NumGoroutine(),
			"alloc_mb":    int(memStats.Alloc / 1024 / 1024),
		},
	}

	respond.JSON(w, http.StatusOK, resp)
}

// GetMetrics handles GET /api/v1/system/metrics.
func (h *SystemHandler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	snap := h.Metrics.Snapshot()

	var active, total int64
	if h.Sandbox != nil {
		active, total = h.Sandbox.Stats()
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, "orva_invocations_total %d\n", snap.TotalInvocations)
	fmt.Fprintf(w, "orva_cold_starts_total %d\n", snap.ColdStarts)
	fmt.Fprintf(w, "orva_warm_hits_total %d\n", snap.WarmHits)
	fmt.Fprintf(w, "orva_builds_total %d\n", snap.TotalBuilds)
	fmt.Fprintf(w, "orva_build_errors_total %d\n", snap.BuildErrors)
	fmt.Fprintf(w, "orva_active_requests %d\n", snap.ActiveRequests)
	fmt.Fprintf(w, "orva_invocation_duration_ms{quantile=\"0.5\"} %d\n", snap.P50MS)
	fmt.Fprintf(w, "orva_invocation_duration_ms{quantile=\"0.95\"} %d\n", snap.P95MS)
	fmt.Fprintf(w, "orva_invocation_duration_ms{quantile=\"0.99\"} %d\n", snap.P99MS)
	fmt.Fprintf(w, "orva_sandbox_active %d\n", active)
	fmt.Fprintf(w, "orva_sandbox_total %d\n", total)

	// Per-function warm pool stats — exposed so operators can see which
	// functions are hot, which keep getting killed (OOMing, crashing), and
	// the cold-start rate per fn.
	if h.PoolMgr != nil {
		for _, s := range h.PoolMgr.Stats() {
			fmt.Fprintf(w, "orva_pool_idle{function_id=%q} %d\n", s.FunctionID, s.Idle)
			fmt.Fprintf(w, "orva_pool_busy{function_id=%q} %d\n", s.FunctionID, s.Busy)
			fmt.Fprintf(w, "orva_pool_spawned_total{function_id=%q} %d\n", s.FunctionID, s.Spawned)
			fmt.Fprintf(w, "orva_pool_killed_total{function_id=%q} %d\n", s.FunctionID, s.Killed)
			fmt.Fprintf(w, "orva_pool_scale_events_total{function_id=%q,direction=\"up\"} %d\n", s.FunctionID, s.ScaleUps)
			fmt.Fprintf(w, "orva_pool_scale_events_total{function_id=%q,direction=\"down\"} %d\n", s.FunctionID, s.ScaleDowns)
			fmt.Fprintf(w, "orva_pool_rate_ewma{function_id=%q} %.2f\n", s.FunctionID, s.RateEWMA)
			fmt.Fprintf(w, "orva_pool_latency_ewma_ms{function_id=%q} %.2f\n", s.FunctionID, s.LatencyEWMAms)
			fmt.Fprintf(w, "orva_pool_max_dynamic{function_id=%q} %d\n", s.FunctionID, s.DynamicMax)
			fmt.Fprintf(w, "orva_pool_target_concurrency{function_id=%q} %d\n", s.FunctionID, s.Target)
			if s.MemUsedAvgBytes > 0 {
				fmt.Fprintf(w, "orva_pool_mem_used_avg_bytes{function_id=%q} %d\n", s.FunctionID, s.MemUsedAvgBytes)
			}
			if s.CPUFracAvg > 0 {
				fmt.Fprintf(w, "orva_pool_cpu_frac_avg{function_id=%q} %.4f\n", s.FunctionID, s.CPUFracAvg)
			}
		}
		tot, avail, res := h.PoolMgr.HostMemStats()
		fmt.Fprintf(w, "orva_host_mem_total_bytes %d\n", tot)
		fmt.Fprintf(w, "orva_host_mem_available_bytes %d\n", avail)
		fmt.Fprintf(w, "orva_host_mem_reserved_bytes %d\n", res)
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
				FunctionID:    s.FunctionID,
				FunctionName:  name,
				Idle:          s.Idle,
				Busy:          s.Busy,
				Spawned:       s.Spawned,
				Killed:        s.Killed,
				ScaleUps:      s.ScaleUps,
				ScaleDowns:    s.ScaleDowns,
				RateEWMA:      s.RateEWMA,
				LatencyEWMAms: s.LatencyEWMAms,
				DynamicMax:    s.DynamicMax,
				Target:        s.Target,
				MemUsedAvgMB:  float64(s.MemUsedAvgBytes) / 1024 / 1024,
				CPUFracAvg:    s.CPUFracAvg,
				MemLimitMB:    memLimitMB,
				CPULimit:      cpuLimit,
			})
		}
	}

	return out
}
