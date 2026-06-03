package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Manage background jobs",
	Long:  "List, enqueue, inspect, retry, and delete jobs in the background queue.",
}

var jobsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List jobs",
	Long: `List jobs in the background queue, optionally filtered by status or function.

Examples:
  orva jobs list
  orva jobs list --status failed
  orva jobs list --fn report --limit 100
  orva jobs list -o json | jq '.jobs[].id'`,
	RunE: runJobsList,
}

var jobsGetCmd = &cobra.Command{
	Use:   "get [id]",
	Short: "Show a single job",
	Long: `Show the full record for one job, including payload, attempts, and timing.

Examples:
  orva jobs get <id>
  orva jobs get <id> -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runJobsGet,
}

var jobsEnqueueCmd = &cobra.Command{
	Use:   "enqueue",
	Short: "Enqueue a new job",
	Long: `Enqueue a background job that invokes a function.

Pass a JSON payload with --data (inline, @file, or @-). Use --at to defer
execution, and the idempotency flags to dedupe repeated enqueues.

Examples:
  orva jobs enqueue --fn report
  orva jobs enqueue --fn report --data '{"full":true}'
  orva jobs enqueue --fn report --data @body.json --max-attempts 5
  orva jobs enqueue --fn report --at 2026-06-03T09:00:00Z
  orva jobs enqueue --fn report --idempotency-key daily-2026-06-02`,
	RunE: runJobsEnqueue,
}

var jobsRetryCmd = &cobra.Command{
	Use:   "retry [id]",
	Short: "Retry a job",
	Long: `Reset a terminal job back to pending so the scheduler picks it up again.

Examples:
  orva jobs retry <id>`,
	Args: cobra.ExactArgs(1),
	RunE: runJobsRetry,
}

var jobsDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a job",
	Long: `Delete a job from the queue. Prompts for confirmation unless --yes is set.

Examples:
  orva jobs delete <id>
  orva jobs delete <id> --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runJobsDelete,
}

func init() {
	jobsListCmd.Flags().String("status", "", "filter by status (pending|running|succeeded|failed)")
	jobsListCmd.Flags().String("fn", "", "filter by function name or ID")
	jobsListCmd.Flags().Int("limit", 50, "maximum number of jobs to return")

	jobsEnqueueCmd.Flags().String("fn", "", "function name or ID to invoke (required)")
	jobsEnqueueCmd.Flags().String("data", "", "JSON payload to send to the function: inline, @file, or @-")
	jobsEnqueueCmd.Flags().Int("max-attempts", 0, "maximum retry attempts (default 3)")
	jobsEnqueueCmd.Flags().String("at", "",
		"RFC3339 timestamp to fire the job at (e.g. 2026-05-15T09:00:00Z). "+
			"Omit to run on the next scheduler tick (~5s).")
	jobsEnqueueCmd.Flags().String("idempotency-key", "",
		"dedupe key — repeated enqueues with the same key inside the window "+
			"return the same job id without enqueuing again")
	jobsEnqueueCmd.Flags().Int("idempotency-window", 0,
		"seconds within which the idempotency key dedupes (default 86400 = 24h)")
	jobsEnqueueCmd.MarkFlagRequired("fn")

	jobsCmd.AddCommand(jobsListCmd)
	jobsCmd.AddCommand(jobsGetCmd)
	jobsCmd.AddCommand(jobsEnqueueCmd)
	jobsCmd.AddCommand(jobsRetryCmd)
	jobsCmd.AddCommand(jobsDeleteCmd)
}

func runJobsList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	status, _ := cmd.Flags().GetString("status")
	fnNameOrID, _ := cmd.Flags().GetString("fn")
	limit, _ := cmd.Flags().GetInt("limit")

	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if fnNameOrID != "" {
		fnID, err := resolveFnID(client, fnNameOrID)
		if err != nil {
			return err
		}
		q.Set("function_id", fnID)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}

	path := "/api/v1/jobs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	resp, err := client.Get(path)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var result struct {
		Jobs []struct {
			ID           string    `json:"id"`
			FunctionID   string    `json:"function_id"`
			FunctionName string    `json:"function_name"`
			Status       string    `json:"status"`
			Attempts     int       `json:"attempts"`
			MaxAttempts  int       `json:"max_attempts"`
			ScheduledAt  time.Time `json:"scheduled_at"`
			CreatedAt    time.Time `json:"created_at"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("ID", "FUNCTION", "STATUS", "ATTEMPTS", "SCHEDULED", "CREATED")
	for _, j := range result.Jobs {
		fnLabel := j.FunctionName
		if fnLabel == "" {
			fnLabel = j.FunctionID
		}
		t.row(j.ID, dash(fnLabel), j.Status,
			fmt.Sprintf("%d/%d", j.Attempts, j.MaxAttempts),
			j.ScheduledAt.Format(time.DateTime), j.CreatedAt.Format(time.DateTime))
	}
	t.flush()
	infof(cmd, "\nTotal: %d", len(result.Jobs))
	return nil
}

func runJobsGet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	id := args[0]
	resp, err := client.Get("/api/v1/jobs/" + id)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var job struct {
		ID           string          `json:"id"`
		FunctionID   string          `json:"function_id"`
		FunctionName string          `json:"function_name"`
		Status       string          `json:"status"`
		Attempts     int             `json:"attempts"`
		MaxAttempts  int             `json:"max_attempts"`
		ScheduledAt  time.Time       `json:"scheduled_at"`
		CreatedAt    time.Time       `json:"created_at"`
		LastError    string          `json:"last_error"`
		Payload      json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &job); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fnLabel := job.FunctionName
	if fnLabel == "" {
		fnLabel = job.FunctionID
	}
	t := newTable("FIELD", "VALUE")
	t.row("id", job.ID)
	t.row("function", dash(fnLabel))
	t.row("status", job.Status)
	t.row("attempts", fmt.Sprintf("%d/%d", job.Attempts, job.MaxAttempts))
	t.row("scheduled", job.ScheduledAt.Format(time.DateTime))
	t.row("created", job.CreatedAt.Format(time.DateTime))
	t.row("last_error", dash(job.LastError))
	if len(job.Payload) > 0 {
		t.row("payload", string(job.Payload))
	}
	t.flush()
	return nil
}

func runJobsEnqueue(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnNameOrID, _ := cmd.Flags().GetString("fn")
	dataArg, _ := cmd.Flags().GetString("data")
	maxAttempts, _ := cmd.Flags().GetInt("max-attempts")
	atStr, _ := cmd.Flags().GetString("at")

	fnID, err := resolveFnID(client, fnNameOrID)
	if err != nil {
		return err
	}

	body := map[string]any{"function_id": fnID}
	if dataArg != "" {
		raw, err := readBodyArg(dataArg)
		if err != nil {
			return fmt.Errorf("enqueue: read data: %w", err)
		}
		var payload any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return fmt.Errorf("enqueue: --data must be valid JSON: %w", err)
		}
		body["payload"] = payload
	}
	if maxAttempts > 0 {
		body["max_attempts"] = maxAttempts
	}
	if atStr != "" {
		t, err := time.Parse(time.RFC3339, atStr)
		if err != nil {
			return fmt.Errorf("enqueue: --at must be RFC3339 (e.g. 2026-05-15T09:00:00Z): %w", err)
		}
		body["scheduled_at"] = t.UTC().Format(time.RFC3339)
	}
	if idemKey, _ := cmd.Flags().GetString("idempotency-key"); idemKey != "" {
		body["idempotency_key"] = idemKey
	}
	if idemWindow, _ := cmd.Flags().GetInt("idempotency-window"); idemWindow > 0 {
		body["idempotency_window_seconds"] = idemWindow
	}

	resp, err := client.Post("/api/v1/jobs", body)
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(out)
	}

	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(out, &job)
	okf(cmd, "Enqueued job %s (%s)", job.ID, dash(job.Status))
	return nil
}

func runJobsRetry(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	id := args[0]
	resp, err := client.Post("/api/v1/jobs/"+id+"/retry", nil)
	if err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(out)
	}
	okf(cmd, "Re-queued job %s", id)
	return nil
}

func runJobsDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	id := args[0]
	if err := confirm(cmd, fmt.Sprintf("Delete job %s?", id)); err != nil {
		return err
	}

	resp, err := client.Delete("/api/v1/jobs/" + id)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	resp.Body.Close()

	okf(cmd, "Deleted job %s", id)
	return nil
}
