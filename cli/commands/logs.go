package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [name-or-id]",
	Short: "View or follow function execution logs",
	Long: `List recent executions for a function, read one execution's logs, or follow
new executions live.

  orva logs greeter                       # recent executions (table)
  orva logs greeter -o json | jq .        # machine-readable
  orva logs greeter --exec-id 019df...    # stdout/stderr for one execution
  orva logs greeter --follow              # stream executions as they happen`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().String("exec-id", "", "show stdout/stderr for a specific execution ID")
	logsCmd.Flags().BoolP("follow", "f", false, "follow new executions for this function via SSE (Ctrl-C to stop)")
}

func runLogs(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	execID, _ := cmd.Flags().GetString("exec-id")
	follow, _ := cmd.Flags().GetBool("follow")

	if follow {
		return runLogsFollow(cmd, client, fnID)
	}

	if execID != "" {
		return showExecutionLogs(cmd, client, execID)
	}

	return listExecutions(cmd, client, fnID)
}

func showExecutionLogs(cmd *cobra.Command, client *cli.Client, execID string) error {
	resp, err := client.Get("/api/v1/executions/" + execID + "/logs")
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
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

	var logs struct {
		ExecutionID string `json:"execution_id"`
		Stdout      string `json:"stdout"`
		Stderr      string `json:"stderr"`
	}
	if err := json.Unmarshal(raw, &logs); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	infof(cmd, "Execution: %s", logs.ExecutionID)
	if logs.Stdout != "" {
		fmt.Print(logs.Stdout)
		if !strings.HasSuffix(logs.Stdout, "\n") {
			fmt.Println()
		}
	}
	if logs.Stderr != "" {
		fmt.Fprint(os.Stderr, logs.Stderr)
		if !strings.HasSuffix(logs.Stderr, "\n") {
			fmt.Fprintln(os.Stderr)
		}
	}
	if logs.Stdout == "" && logs.Stderr == "" {
		infof(cmd, "(no logs)")
	}
	return nil
}

func listExecutions(cmd *cobra.Command, client *cli.Client, fnID string) error {
	resp, err := client.Get("/api/v1/executions?function_id=" + fnID)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
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

	var result struct {
		Executions []struct {
			ID         string    `json:"id"`
			Status     string    `json:"status"`
			ColdStart  bool      `json:"cold_start"`
			DurationMS *int64    `json:"duration_ms"`
			StatusCode *int      `json:"status_code"`
			StartedAt  time.Time `json:"started_at"`
		} `json:"executions"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("ID", "STATUS", "COLD START", "DURATION", "CODE", "STARTED")
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
		t.row(exec.ID, exec.Status, cold, dur, code, exec.StartedAt.Format(time.DateTime))
	}
	t.flush()
	infof(cmd, "\nTotal: %d", result.Total)
	return nil
}

// runLogsFollow subscribes to /api/v1/events and pretty-prints every
// `execution` event whose function_id matches fnID. The server emits all
// types on one stream; we filter client-side.
func runLogsFollow(cmd *cobra.Command, client *cli.Client, fnID string) error {
	path := fmt.Sprintf("/api/v1/events?type=execution&function=%s", fnID)
	resp, err := streamSSE(client, path)
	if err != nil {
		return fmt.Errorf("follow: %w", err)
	}
	defer resp.Body.Close()

	infof(cmd, "Following executions for %s — Ctrl-C to stop.", fnID)

	if err := consumeSSE(resp, func(event, data string) (bool, error) {
		if event == "execution" && data != "" {
			printExecutionEvent(data, fnID)
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("follow: %w", err)
	}
	return nil
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
