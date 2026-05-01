package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Harsh-2002/Orva/internal/cli"
	"github.com/spf13/cobra"
)

var activityCmd = &cobra.Command{
	Use:   "activity",
	Short: "Show recent platform activity",
	Long:  "Show paginated activity rows or follow live activity via SSE.",
	Run:   runActivity,
}

func init() {
	activityCmd.Flags().Int("limit", 50, "maximum number of rows to return")
	activityCmd.Flags().String("source", "", "filter by source (web|api|mcp|sdk|webhook|cron|internal)")
	activityCmd.Flags().Bool("tail", false, "follow new activity via SSE (Ctrl-C to stop)")
	rootCmd.AddCommand(activityCmd)
}

func runActivity(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	tail, _ := cmd.Flags().GetBool("tail")
	if tail {
		runActivityTail(client)
		return
	}

	source, _ := cmd.Flags().GetString("source")
	limit, _ := cmd.Flags().GetInt("limit")

	q := url.Values{}
	if source != "" {
		q.Set("source", source)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}

	path := "/api/v1/activity"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	resp, err := client.Get(path)
	if err != nil {
		exitError("activity: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("activity: %v", err)
	}

	var result struct {
		Rows []struct {
			TS         int64  `json:"ts"`
			Source     string `json:"source"`
			ActorLabel string `json:"actor_label"`
			Method     string `json:"method"`
			Path       string `json:"path"`
			Status     int    `json:"status"`
			DurationMS int64  `json:"duration_ms"`
			Summary    string `json:"summary"`
		} `json:"rows"`
		Count int `json:"count"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tSOURCE\tACTOR\tMETHOD\tPATH\tSTATUS\tDURATION")
	for _, r := range result.Rows {
		ts := time.UnixMilli(r.TS).Format(time.DateTime)
		actor := r.ActorLabel
		if actor == "" {
			actor = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%dms\n",
			ts, r.Source, actor, r.Method, r.Path, r.Status, r.DurationMS,
		)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", result.Count)
}

func runActivityTail(client *cli.Client) {
	// Server's /api/v1/events emits all event types on one stream; clients
	// filter by `event:` field (see internal/server/events/handler.go). The
	// query params below are forward-compatibility hints and currently
	// ignored server-side.
	path := "/api/v1/events?type=activity"
	resp, err := streamSSE(client, path)
	if err != nil {
		exitError("tail: %v", err)
	}
	defer resp.Body.Close()

	fmt.Fprintln(os.Stderr, "Subscribed to activity stream — Ctrl-C to stop.")

	// Parse SSE: each frame is `event: <type>\ndata: <json>\n\n`. We read
	// line-by-line, track the current event's type, and pretty-print each
	// "activity" event as it arrives.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var curType, curData string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, ":"):
			// Comment / heartbeat — ignore.
		case strings.HasPrefix(line, "event:"):
			curType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			curData = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "":
			// Frame boundary — emit if it's an activity event.
			if curType == "activity" && curData != "" {
				printActivityEvent(curData)
			}
			curType, curData = "", ""
		}
	}
	if err := scanner.Err(); err != nil {
		exitError("tail: scanner: %v", err)
	}
}

func printActivityEvent(data string) {
	var row struct {
		TS         int64  `json:"ts"`
		Source     string `json:"source"`
		ActorLabel string `json:"actor_label"`
		Method     string `json:"method"`
		Path       string `json:"path"`
		Status     int    `json:"status"`
		DurationMS int64  `json:"duration_ms"`
	}
	if err := json.Unmarshal([]byte(data), &row); err != nil {
		// Couldn't parse — still print raw so nothing is silently dropped.
		fmt.Println(data)
		return
	}
	ts := time.UnixMilli(row.TS).Format(time.TimeOnly)
	actor := row.ActorLabel
	if actor == "" {
		actor = "-"
	}
	fmt.Printf("%s  %-8s  %-20s  %-6s %-40s  %3d  %dms\n",
		ts, row.Source, actor, row.Method, row.Path, row.Status, row.DurationMS,
	)
}

// streamSSE issues a GET that expects a text/event-stream response. The
// returned http.Response is left open — the caller reads resp.Body.
func streamSSE(client *cli.Client, path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, client.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if client.APIKey != "" {
		req.Header.Set("X-Orva-API-Key", client.APIKey)
	}
	// SSE streams are long-lived; bypass the client's default 120s timeout
	// by using a fresh client with no timeout for streaming reads.
	streamingClient := &http.Client{Timeout: 0}
	resp, err := streamingClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("SSE subscribe failed: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}
