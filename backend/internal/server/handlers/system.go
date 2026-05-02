package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/config"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/internal/version"
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
		"version":        version.Version,
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

// writeHistogram emits Prometheus-format cumulative histogram lines for
// the given metric prefix. Format: `<name>_bucket{le="..."} <count>` for
// every boundary plus `+Inf`, then `<name>_count` and `<name>_sum`. The
// `_sum` value is in milliseconds (matches the `_ms` in the metric name).
func writeHistogram(w http.ResponseWriter, name string, h metrics.HistogramSnapshot) {
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

	fmt.Fprintf(w, "orva_invocations_total %d\n", snap.TotalInvocations)
	fmt.Fprintf(w, "orva_cold_starts_total %d\n", snap.ColdStarts)
	fmt.Fprintf(w, "orva_warm_hits_total %d\n", snap.WarmHits)
	fmt.Fprintf(w, "orva_builds_total %d\n", snap.TotalBuilds)
	fmt.Fprintf(w, "orva_build_errors_total %d\n", snap.BuildErrors)
	fmt.Fprintf(w, "orva_active_requests %d\n", snap.ActiveRequests)
	// Summary-style quantile lines kept for backwards compatibility with
	// the existing Grafana dashboard. Operators are encouraged to migrate
	// to the histogram lines below — they let Grafana draw heatmaps and
	// recompute quantiles over arbitrary windows via histogram_quantile().
	fmt.Fprintf(w, "orva_invocation_duration_ms{quantile=\"0.5\"} %d\n", snap.P50MS)
	fmt.Fprintf(w, "orva_invocation_duration_ms{quantile=\"0.95\"} %d\n", snap.P95MS)
	fmt.Fprintf(w, "orva_invocation_duration_ms{quantile=\"0.99\"} %d\n", snap.P99MS)
	// Histogram-shape duration metric. Cumulative counts: each bucket
	// includes every sample with le ≤ its boundary, plus a +Inf bucket
	// that mirrors the total count. _sum and _count complete the
	// Prometheus histogram convention.
	writeHistogram(w, "orva_invocation_duration_ms", h.Metrics.SnapshotInvocationHistogram())
	// Sandbox spawn-duration histogram. Populated from the pool layer
	// once a worker comes up. Until that wiring lands all buckets
	// remain 0 — the lines are still emitted so dashboards can be
	// pre-built without if-exists guards.
	writeHistogram(w, "orva_sandbox_spawn_duration_ms", h.Metrics.SnapshotSpawnHistogram())
	fmt.Fprintf(w, "orva_sandbox_active %d\n", active)
	fmt.Fprintf(w, "orva_sandbox_total %d\n", total)
	// Job queue depth — pending jobs that the runner hasn't claimed yet.
	// Cheap COUNT per scrape; the metrics endpoint isn't on the hot
	// path. Returns 0 cleanly when there are no pending jobs (or when
	// the DB handle is missing in tests).
	if h.DB != nil {
		var depth int64
		if rdb := h.DB.ReadDB(); rdb != nil {
			_ = rdb.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status='pending'`).Scan(&depth)
		}
		fmt.Fprintf(w, "orva_jobs_queue_depth %d\n", depth)
	}

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
	DBBytes        int64 `json:"db_bytes"`         // size of orva.db on disk
	DBPages        int64 `json:"db_pages"`         // PRAGMA page_count
	DBPageSize     int64 `json:"db_page_size"`     // PRAGMA page_size
	DBFreePages    int64 `json:"db_free_pages"`    // PRAGMA freelist_count — reclaimable on next VACUUM
	WALBytes       int64 `json:"wal_bytes"`        // size of orva.db-wal sidecar
	FunctionsBytes int64 `json:"functions_bytes"`  // recursive size of <data_dir>/functions
	TotalBytes     int64 `json:"total_bytes"`      // db + wal + functions
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
	if _, err := h.DB.WriteDB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		respond.Error(w, http.StatusInternalServerError, "VACUUM_FAILED", "wal_checkpoint: "+err.Error(), "")
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
