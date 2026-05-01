package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Harsh-2002/Orva/internal/cli"
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
	logsCmd.Flags().Bool("tail", false, "follow new executions for this function via SSE (Ctrl-C to stop)")
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
	tail, _ := cmd.Flags().GetBool("tail")

	if tail {
		runLogsTail(client, fnID)
		return
	}

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

// runLogsTail subscribes to /api/v1/events and pretty-prints every
// `execution` event whose function_id matches fnID. The server emits all
// types on one stream (see internal/server/events/handler.go); the
// `?type=...&function=...` query params are forward-compatibility hints
// and are filtered client-side here.
func runLogsTail(client *cli.Client, fnID string) {
	path := fmt.Sprintf("/api/v1/events?type=execution&function=%s", fnID)
	resp, err := streamSSE(client, path)
	if err != nil {
		exitError("tail: %v", err)
	}
	defer resp.Body.Close()

	fmt.Fprintf(os.Stderr, "Tailing executions for %s — Ctrl-C to stop.\n", fnID)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var curType, curData string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, ":"):
			// SSE comment / heartbeat — ignore.
		case strings.HasPrefix(line, "event:"):
			curType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			curData = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "":
			if curType == "execution" && curData != "" {
				printExecutionEvent(curData, fnID)
			}
			curType, curData = "", ""
		}
	}
	if err := scanner.Err(); err != nil {
		exitError("tail: scanner: %v", err)
	}
}

func printExecutionEvent(data, wantFnID string) {
	var ev struct {
		ID           string `json:"id"`
		FunctionID   string `json:"function_id"`
		FunctionName string `json:"function_name"`
		Status       string `json:"status"`
		StatusCode   int    `json:"status_code"`
		DurationMS   int64  `json:"duration_ms"`
		ColdStart    bool   `json:"cold_start"`
		StartedAt    string `json:"started_at"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		fmt.Println(data)
		return
	}
	// Filter client-side: the unified hub doesn't honour ?function=.
	if wantFnID != "" && ev.FunctionID != wantFnID {
		return
	}
	cold := "warm"
	if ev.ColdStart {
		cold = "cold"
	}
	ts := ev.StartedAt
	if t, err := time.Parse(time.RFC3339Nano, ev.StartedAt); err == nil {
		ts = t.Format(time.TimeOnly)
	}
	fmt.Printf("%s  %s  %-8s  %3d  %5dms  %s  %s\n",
		ts, ev.ID, ev.Status, ev.StatusCode, ev.DurationMS, cold, ev.FunctionName,
	)
}
