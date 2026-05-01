package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "Manage background jobs",
	Long:  "List, enqueue, retry, and delete jobs in the background queue.",
}

var jobsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List jobs",
	Run:   runJobsList,
}

var jobsEnqueueCmd = &cobra.Command{
	Use:   "enqueue",
	Short: "Enqueue a new job",
	Run:   runJobsEnqueue,
}

var jobsRetryCmd = &cobra.Command{
	Use:   "retry [id]",
	Short: "Retry a job",
	Args:  cobra.ExactArgs(1),
	Run:   runJobsRetry,
}

var jobsDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a job",
	Args:  cobra.ExactArgs(1),
	Run:   runJobsDelete,
}

func init() {
	jobsListCmd.Flags().String("status", "", "filter by status (pending|running|succeeded|failed)")
	jobsListCmd.Flags().String("fn", "", "filter by function name or ID")
	jobsListCmd.Flags().Int("limit", 50, "maximum number of jobs to return")

	jobsEnqueueCmd.Flags().String("fn", "", "function name or ID to invoke (required)")
	jobsEnqueueCmd.Flags().String("data", "", "JSON payload to send to the function")
	jobsEnqueueCmd.Flags().Int("max-attempts", 0, "maximum retry attempts (default 3)")
	jobsEnqueueCmd.Flags().String("at", "",
		"RFC3339 timestamp to fire the job at (e.g. 2026-05-15T09:00:00Z). "+
			"Omit to run on the next scheduler tick (~5s).")
	jobsEnqueueCmd.MarkFlagRequired("fn")

	jobsCmd.AddCommand(jobsListCmd)
	jobsCmd.AddCommand(jobsEnqueueCmd)
	jobsCmd.AddCommand(jobsRetryCmd)
	jobsCmd.AddCommand(jobsDeleteCmd)
	rootCmd.AddCommand(jobsCmd)
}

func runJobsList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	status, _ := cmd.Flags().GetString("status")
	fnNameOrID, _ := cmd.Flags().GetString("fn")
	limit, _ := cmd.Flags().GetInt("limit")

	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if fnNameOrID != "" {
		fnID := resolveFunctionID(client, fnNameOrID)
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
		exitError("list: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("list: %v", err)
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
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tFUNCTION\tSTATUS\tATTEMPTS\tSCHEDULED\tCREATED")
	for _, j := range result.Jobs {
		fnLabel := j.FunctionName
		if fnLabel == "" {
			fnLabel = j.FunctionID
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d/%d\t%s\t%s\n",
			j.ID, fnLabel, j.Status, j.Attempts, j.MaxAttempts,
			j.ScheduledAt.Format(time.DateTime), j.CreatedAt.Format(time.DateTime),
		)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", len(result.Jobs))
}

func runJobsEnqueue(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnNameOrID, _ := cmd.Flags().GetString("fn")
	dataStr, _ := cmd.Flags().GetString("data")
	maxAttempts, _ := cmd.Flags().GetInt("max-attempts")
	atStr, _ := cmd.Flags().GetString("at")

	fnID := resolveFunctionID(client, fnNameOrID)

	body := map[string]any{
		"function_id": fnID,
	}
	if dataStr != "" {
		var payload any
		if err := json.Unmarshal([]byte(dataStr), &payload); err != nil {
			exitError("enqueue: --data must be valid JSON: %v", err)
		}
		body["payload"] = payload
	}
	if maxAttempts > 0 {
		body["max_attempts"] = maxAttempts
	}
	if atStr != "" {
		t, err := time.Parse(time.RFC3339, atStr)
		if err != nil {
			exitError("enqueue: --at must be RFC3339 (e.g. 2026-05-15T09:00:00Z): %v", err)
		}
		body["scheduled_at"] = t.UTC().Format(time.RFC3339)
	}

	resp, err := client.Post("/api/v1/jobs", body)
	if err != nil {
		exitError("enqueue: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("enqueue: %v", err)
	}

	var job map[string]any
	if err := decodeJSON(resp, &job); err != nil {
		exitError("decode response: %v", err)
	}
	out, _ := json.MarshalIndent(job, "", "  ")
	fmt.Println(string(out))
}

func runJobsRetry(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	id := args[0]
	resp, err := client.Post("/api/v1/jobs/"+id+"/retry", nil)
	if err != nil {
		exitError("retry: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("retry: %v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}

func runJobsDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	id := args[0]
	resp, err := client.Delete("/api/v1/jobs/" + id)
	if err != nil {
		exitError("delete: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("delete: %v", err)
	}

	fmt.Printf("Job %s deleted\n", id)
}
