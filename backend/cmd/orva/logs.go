package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [name-or-id]",
	Short: "View function execution logs",
	Long:  "List recent executions for a function or view logs for a specific execution.",
	Args:  cobra.ExactArgs(1),
	Run:   runLogs,
}

func init() {
	logsCmd.Flags().String("exec-id", "", "specific execution ID to view logs for")
	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	nameOrID := args[0]
	fnID := resolveFunctionID(client, nameOrID)
	execID, _ := cmd.Flags().GetString("exec-id")

	if execID != "" {
		// Get specific execution logs.
		resp, err := client.Get("/api/v1/executions/" + execID + "/logs")
		if err != nil {
			exitError("request failed: %v", err)
		}
		if err := checkResponse(resp); err != nil {
			exitError("%v", err)
		}

		var logs struct {
			ExecutionID string `json:"execution_id"`
			Stdout      string `json:"stdout"`
			Stderr      string `json:"stderr"`
		}
		if err := decodeJSON(resp, &logs); err != nil {
			exitError("decode response: %v", err)
		}

		fmt.Printf("Execution: %s\n", logs.ExecutionID)
		if logs.Stdout != "" {
			fmt.Printf("\n--- stdout ---\n%s\n", logs.Stdout)
		}
		if logs.Stderr != "" {
			fmt.Printf("\n--- stderr ---\n%s\n", logs.Stderr)
		}
		if logs.Stdout == "" && logs.Stderr == "" {
			fmt.Println("(no logs)")
		}
		return
	}

	// List recent executions for this function.
	resp, err := client.Get("/api/v1/executions?function_id=" + fnID)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var result struct {
		Executions []struct {
			ID         string     `json:"id"`
			Status     string     `json:"status"`
			ColdStart  bool       `json:"cold_start"`
			DurationMS *int64     `json:"duration_ms"`
			StatusCode *int       `json:"status_code"`
			StartedAt  time.Time  `json:"started_at"`
			FinishedAt *time.Time `json:"finished_at"`
			ErrorMsg   string     `json:"error_message"`
		} `json:"executions"`
		Total int `json:"total"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		// Try raw JSON output as fallback.
		var raw json.RawMessage
		fmt.Fprintf(os.Stderr, "Warning: could not parse as expected format: %v\n", err)
		_ = raw
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tCOLD START\tDURATION\tCODE\tSTARTED")
	for _, exec := range result.Executions {
		dur := "-"
		if exec.DurationMS != nil {
			dur = fmt.Sprintf("%dms", *exec.DurationMS)
		}
		code := "-"
		if exec.StatusCode != nil {
			code = fmt.Sprintf("%d", *exec.StatusCode)
		}
		cold := "no"
		if exec.ColdStart {
			cold = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			exec.ID, exec.Status, cold, dur, code,
			exec.StartedAt.Format(time.DateTime),
		)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", result.Total)
}
