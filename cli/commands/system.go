package commands

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "System commands",
	Long:  "View system health, metrics, and on-disk storage.",
}

var systemHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check system health",
	Long: `Print the server's health document (version, uptime, component status).

  orva system health
  orva system health | jq .version`,
	Args: cobra.NoArgs,
	RunE: runSystemHealth,
}

var systemMetricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "View system metrics",
	Long: `Print the server's runtime metrics as JSON.

  orva system metrics | jq .`,
	Args: cobra.NoArgs,
	RunE: runSystemMetrics,
}

var systemStorageCmd = &cobra.Command{
	Use:   "storage",
	Short: "Show on-disk storage usage",
	Long: `Summarize the data directory's storage: orva.db, its WAL sidecar, and the
functions tree, plus the combined total.

  orva system storage
  orva system storage -o json | jq .total_bytes`,
	Args: cobra.NoArgs,
	RunE: runSystemStorage,
}

var systemDBStatsCmd = &cobra.Command{
	Use:   "db-stats",
	Short: "Show on-disk storage breakdown",
	Long: `Print orva.db / WAL / functions tree sizes plus the SQLite page-level
breakdown the Settings UI uses to decide whether VACUUM would reclaim
anything.

  orva system db-stats
  orva system db-stats -o json | jq .db_free_pages`,
	Args: cobra.NoArgs,
	RunE: runSystemDBStats,
}

var systemVacuumCmd = &cobra.Command{
	Use:   "vacuum",
	Short: "Compact the SQLite database (VACUUM)",
	Long: `Run PRAGMA wal_checkpoint(TRUNCATE) followed by VACUUM on orva.db. Holds an
exclusive lock for the duration; writes are blocked until it returns. Prints
freed bytes on success.

  orva system vacuum`,
	Args: cobra.NoArgs,
	RunE: runSystemVacuum,
}

func init() {
	systemCmd.AddCommand(systemHealthCmd)
	systemCmd.AddCommand(systemMetricsCmd)
	systemCmd.AddCommand(systemStorageCmd)
	systemCmd.AddCommand(systemDBStatsCmd)
	systemCmd.AddCommand(systemVacuumCmd)
}

func runSystemHealth(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/system/health")
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	return emitRaw(raw)
}

func runSystemMetrics(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/system/metrics.json")
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	return emitRaw(raw)
}

// runSystemStorage prints the data-directory storage summary. In JSON mode it
// emits the raw API response; otherwise a readable two-column summary.
func runSystemStorage(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/system/storage")
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var s storageInfo
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	t := newTable("COMPONENT", "SIZE")
	t.row("orva.db", humanBytes(s.DBBytes))
	if s.WALBytes > 0 {
		t.row("orva.db-wal", humanBytes(s.WALBytes))
	}
	t.row("functions/", humanBytes(s.FunctionsBytes))
	t.row("total", humanBytes(s.TotalBytes))
	t.flush()
	return nil
}

// runSystemDBStats prints the storage breakdown with SQLite page-level
// detail. In JSON mode it emits the raw API response.
func runSystemDBStats(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/system/storage")
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var s storageInfo
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	fmt.Printf("orva.db        %s  (%d pages x %d bytes; %d free pages reclaimable)\n",
		humanBytes(s.DBBytes), s.DBPages, s.DBPageSize, s.DBFreePages)
	if s.WALBytes > 0 {
		fmt.Printf("orva.db-wal    %s\n", humanBytes(s.WALBytes))
	}
	fmt.Printf("functions/     %s\n", humanBytes(s.FunctionsBytes))
	fmt.Printf("---------------------------------\n")
	fmt.Printf("total          %s\n", humanBytes(s.TotalBytes))
	return nil
}

// runSystemVacuum issues the VACUUM, prints freed bytes + duration.
// Worth noting: this is not a dry-run; the call holds an exclusive
// lock on the live DB until the server-side handler returns.
func runSystemVacuum(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	infof(cmd, "Running VACUUM (writes will block briefly)...")
	resp, err := client.Post("/api/v1/system/vacuum", nil)
	if err != nil {
		return err
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var r struct {
		BeforeBytes int64 `json:"before_bytes"`
		AfterBytes  int64 `json:"after_bytes"`
		FreedBytes  int64 `json:"freed_bytes"`
		DurationMS  int64 `json:"duration_ms"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	okf(cmd, "VACUUM complete in %d ms — freed %s (%s → %s)",
		r.DurationMS, humanBytes(r.FreedBytes), humanBytes(r.BeforeBytes), humanBytes(r.AfterBytes))
	return nil
}

// storageInfo mirrors the GET /api/v1/system/storage response.
type storageInfo struct {
	DBBytes        int64 `json:"db_bytes"`
	DBPages        int64 `json:"db_pages"`
	DBPageSize     int64 `json:"db_page_size"`
	DBFreePages    int64 `json:"db_free_pages"`
	WALBytes       int64 `json:"wal_bytes"`
	FunctionsBytes int64 `json:"functions_bytes"`
	TotalBytes     int64 `json:"total_bytes"`
}

// humanBytes renders a byte count as a short human-readable string
// (KB / MB / GB). The CLI uses base-1024 units to match what `du -h`
// shows — operators expect those numbers to line up.
func humanBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	suffixes := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	i := -1
	for v >= k && i < len(suffixes)-1 {
		v /= k
		i++
	}
	return fmt.Sprintf("%.2f %s", v, suffixes[i])
}
