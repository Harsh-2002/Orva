package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

var activityCmd = &cobra.Command{
	Use:   "activity",
	Short: "Show recent platform activity",
	Long: `Show paginated activity rows, or follow live activity via SSE with --follow.

  orva activity
  orva activity --source mcp --limit 20
  orva activity -o json | jq '.rows[] | select(.status >= 400)'
  orva activity --follow`,
	Args: cobra.NoArgs,
	RunE: runActivity,
}

func init() {
	activityCmd.Flags().Int("limit", 50, "maximum number of rows to return")
	activityCmd.Flags().String("source", "", "filter by source (web|api|mcp|sdk|webhook|cron|internal)")
	activityCmd.Flags().BoolP("follow", "f", false, "follow new activity via SSE (Ctrl-C to stop)")
}

func runActivity(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	// Read the filters BEFORE the --follow early return. They used to be
	// read after it, so `orva activity --follow --source mcp` silently
	// streamed every source.
	source, _ := cmd.Flags().GetString("source")
	limit, _ := cmd.Flags().GetInt("limit")

	follow, _ := cmd.Flags().GetBool("follow")
	if follow {
		return runActivityTail(cmd, client, source)
	}

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
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	t := newTable("TIME", "SOURCE", "ACTOR", "METHOD", "PATH", "STATUS", "DURATION")
	for _, r := range result.Rows {
		ts := time.UnixMilli(r.TS).Format(time.DateTime)
		t.row(ts, r.Source, dash(r.ActorLabel), r.Method, r.Path, r.Status, fmt.Sprintf("%dms", r.DurationMS))
	}
	t.flush()
	infof(cmd, "\nTotal: %d", result.Count)
	return nil
}

func runActivityTail(cmd *cobra.Command, client *cli.Client, source string) error {
	// Server's /api/v1/events emits all event types on one stream; clients
	// filter by `event:` field (see internal/server/events/handler.go). The
	// query params below are forward-compatibility hints and currently
	// ignored server-side.
	path := "/api/v1/events?type=activity"
	resp, err := streamSSE(client, path)
	if err != nil {
		return fmt.Errorf("follow: %w", err)
	}
	defer resp.Body.Close()

	infof(cmd, "Subscribed to activity stream — Ctrl-C to stop.")

	// The server emits all event types on one stream; print the "activity"
	// ones. --source is applied here rather than server-side because the
	// events endpoint does not filter; previously it was not applied at all,
	// so --follow --source mcp streamed everything.
	if err := consumeSSE(resp, func(event, data string) (bool, error) {
		if event == "activity" && data != "" {
			if source == "" || activityEventSource(data) == source {
				printActivityEvent(data)
			}
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("follow: %w", err)
	}
	return nil
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
// returned http.Response is left open — the caller reads resp.Body. Routed
// through client.Send so every SSE consumer shares one hardened path: no
// total-duration cap (long-lived streams survive) but a 45s idle deadline so
// a stream that goes silent after the headers can't hang the CLI forever.
func streamSSE(client *cli.Client, path string) (*http.Response, error) {
	resp, err := client.Send(cli.Request{
		Path:        path,
		Accept:      "text/event-stream",
		NoTimeout:   true,
		IdleTimeout: cli.DefaultStreamIdleTimeout,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("SSE subscribe failed: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// activityEventSource pulls the source field out of an activity SSE frame
// so --follow can honour --source. Returns "" when the frame cannot be
// parsed, which the caller treats as "do not filter it out".
func activityEventSource(data string) string {
	var row struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(data), &row); err != nil {
		return ""
	}
	return row.Source
}
