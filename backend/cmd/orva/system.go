package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var systemCmd = &cobra.Command{
	Use:   "system",
	Short: "System commands",
	Long:  "View system health and metrics.",
}

var systemHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check system health",
	Run:   runSystemHealth,
}

var systemMetricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "View system metrics",
	Run:   runSystemMetrics,
}

var systemDBStatsCmd = &cobra.Command{
	Use:   "db-stats",
	Short: "Show on-disk storage breakdown",
	Long:  "Print orva.db / WAL / functions tree sizes plus the SQLite page-level breakdown the Settings UI uses to decide whether VACUUM would reclaim anything.",
	Run:   runSystemDBStats,
}

var systemVacuumCmd = &cobra.Command{
	Use:   "vacuum",
	Short: "Compact the SQLite database (VACUUM)",
	Long:  "Run PRAGMA wal_checkpoint(TRUNCATE) followed by VACUUM on orva.db. Holds an exclusive lock for the duration; writes are blocked until it returns. Prints freed bytes on success.",
	Run:   runSystemVacuum,
}

func init() {
	systemCmd.AddCommand(systemHealthCmd)
	systemCmd.AddCommand(systemMetricsCmd)
	systemCmd.AddCommand(systemDBStatsCmd)
	systemCmd.AddCommand(systemVacuumCmd)
	rootCmd.AddCommand(systemCmd)
}

func runSystemHealth(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	resp, err := client.Get("/api/v1/system/health")
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

func runSystemMetrics(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	resp, err := client.Get("/api/v1/system/metrics.json")
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

// runSystemDBStats prints the storage breakdown in a readable table-ish
// format. We hit the JSON endpoint and reformat — keeps the CLI output
// pretty without making the API endpoint do double duty.
func runSystemDBStats(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	resp, err := client.Get("/api/v1/system/storage")
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var s struct {
		DBBytes        int64 `json:"db_bytes"`
		DBPages        int64 `json:"db_pages"`
		DBPageSize     int64 `json:"db_page_size"`
		DBFreePages    int64 `json:"db_free_pages"`
		WALBytes       int64 `json:"wal_bytes"`
		FunctionsBytes int64 `json:"functions_bytes"`
		TotalBytes     int64 `json:"total_bytes"`
	}
	if err := decodeJSON(resp, &s); err != nil {
		exitError("decode response: %v", err)
	}

	fmt.Printf("orva.db        %s  (%d pages x %d bytes; %d free pages reclaimable)\n",
		humanBytes(s.DBBytes), s.DBPages, s.DBPageSize, s.DBFreePages)
	if s.WALBytes > 0 {
		fmt.Printf("orva.db-wal    %s\n", humanBytes(s.WALBytes))
	}
	fmt.Printf("functions/     %s\n", humanBytes(s.FunctionsBytes))
	fmt.Printf("---------------------------------\n")
	fmt.Printf("total          %s\n", humanBytes(s.TotalBytes))
}

// runSystemVacuum issues the VACUUM, prints freed bytes + duration.
// Worth noting: this is not a dry-run; the call holds an exclusive
// lock on the live DB until the server-side handler returns.
func runSystemVacuum(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fmt.Println("Running VACUUM (writes will block briefly)...")
	resp, err := client.Post("/api/v1/system/vacuum", nil)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var r struct {
		BeforeBytes int64 `json:"before_bytes"`
		AfterBytes  int64 `json:"after_bytes"`
		FreedBytes  int64 `json:"freed_bytes"`
		DurationMS  int64 `json:"duration_ms"`
	}
	if err := decodeJSON(resp, &r); err != nil {
		exitError("decode response: %v", err)
	}

	fmt.Printf("VACUUM complete in %d ms\n", r.DurationMS)
	fmt.Printf("  before: %s\n", humanBytes(r.BeforeBytes))
	fmt.Printf("  after:  %s\n", humanBytes(r.AfterBytes))
	fmt.Printf("  freed:  %s\n", humanBytes(r.FreedBytes))
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
