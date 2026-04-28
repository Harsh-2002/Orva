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

func init() {
	systemCmd.AddCommand(systemHealthCmd)
	systemCmd.AddCommand(systemMetricsCmd)
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

	resp, err := client.Get("/api/v1/system/metrics")
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
