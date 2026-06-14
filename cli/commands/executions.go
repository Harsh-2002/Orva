package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

var executionsCmd = &cobra.Command{
	Use:     "executions",
	Aliases: []string{"execs", "exec"},
	Short:   "Inspect and manage executions across functions",
	Long: `List, inspect, replay, and clean up executions across all functions.

Complements 'orva logs <fn>' (the per-function view): this is the global,
filterable surface for CI cleanup and cross-function observability.

  orva executions list --status error --limit 100
  orva executions get 019df...
  orva executions logs 019df...
  orva executions replay 019df...
  orva executions prune --older-than 168h --status error --yes`,
}

var executionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List executions across functions",
	Long: `List executions, optionally filtered by function, status, time window, or a
search term. Paginated: the footer reports "Showing N of TOTAL"; raise --limit
or page with --offset so a script never silently misses rows.

  orva executions list
  orva executions list --function greeter --status error
  orva executions list --since 2026-06-01T00:00:00Z --limit 200
  orva executions list --search timeout -o json | jq '.executions[].id'`,
	Args: cobra.NoArgs,
	RunE: runExecutionsList,
}

var executionsGetCmd = &cobra.Command{
	Use:   "get <execution-id>",
	Short: "Get one execution's metadata",
	Long: `Print the full execution record (status, timings, sizes, trace ids) as JSON.

  orva executions get 019df...
  orva executions get 019df... | jq .status`,
	Args: cobra.ExactArgs(1),
	RunE: runExecutionsGet,
}

var executionsLogsCmd = &cobra.Command{
	Use:   "logs <execution-id>",
	Short: "Print one execution's stdout/stderr",
	Long: `Print the captured stdout (to stdout) and stderr (to stderr) for a single
execution. With -o json the full log object is emitted to stdout.

  orva executions logs 019df...
  orva executions logs 019df... -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runExecutionsLogs,
}

var executionsDeleteCmd = &cobra.Command{
	Use:   "delete <execution-id>...",
	Short: "Delete one or more executions by id",
	Long: `Delete executions by id (up to 1000 per call). Destructive; prompts for
confirmation unless --yes is passed.

  orva executions delete 019df...
  orva executions delete 019dfa 019dfb 019dfc --yes`,
	Args: cobra.MinimumNArgs(1),
	RunE: runExecutionsDelete,
}

var executionsPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Bulk-delete executions matching a filter",
	Long: `Delete executions matching a filter — the housekeeping primitive for CI.
Lists the matching rows, reports the count, then deletes them (batched at the
server's 1000-per-call limit). Destructive; prompts unless --yes.

At least one filter (--function, --status, or --older-than) is required so an
unfiltered run can't wipe every execution by accident.

  orva executions prune --older-than 168h
  orva executions prune --status error --function greeter --yes
  orva executions prune --older-than 720h --limit 5000 --yes`,
	Args: cobra.NoArgs,
	RunE: runExecutionsPrune,
}

var executionsReplayCmd = &cobra.Command{
	Use:   "replay <execution-id>",
	Short: "Re-run a captured request against current code",
	Long: `Replay an execution's captured request against the function's current
deployment, recording a new execution. The replayed response body is printed
to stdout (pretty on a TTY, raw when piped) and exits non-zero on a 4xx/5xx —
a useful debug and CI regression primitive.

Requires that the original request was captured (request capture must have
been enabled for the function at the time).

  orva executions replay 019df...
  orva executions replay 019df... | jq .`,
	Args: cobra.ExactArgs(1),
	RunE: runExecutionsReplay,
}

func init() {
	executionsListCmd.Flags().String("function", "", "filter to one function (name or id)")
	executionsListCmd.Flags().String("status", "", "filter by status: success | error")
	executionsListCmd.Flags().String("since", "", "only executions at/after this RFC3339 time")
	executionsListCmd.Flags().String("until", "", "only executions before this RFC3339 time")
	executionsListCmd.Flags().String("search", "", "substring match on error message / container id")
	executionsListCmd.Flags().Int("limit", 50, "maximum number of executions to return")
	executionsListCmd.Flags().Int("offset", 0, "number of executions to skip (pagination)")

	executionsPruneCmd.Flags().String("function", "", "filter to one function (name or id)")
	executionsPruneCmd.Flags().String("status", "", "filter by status: success | error")
	executionsPruneCmd.Flags().String("older-than", "", "delete executions older than this duration (e.g. 24h, 7d, 30m)")
	executionsPruneCmd.Flags().Int("limit", 1000, "maximum number of executions to delete in this run")

	executionsCmd.AddCommand(
		executionsListCmd,
		executionsGetCmd,
		executionsLogsCmd,
		executionsDeleteCmd,
		executionsPruneCmd,
		executionsReplayCmd,
	)
}

type executionRow struct {
	ID         string    `json:"id"`
	FunctionID string    `json:"function_id"`
	Status     string    `json:"status"`
	StatusCode *int      `json:"status_code"`
	DurationMS *int64    `json:"duration_ms"`
	StartedAt  time.Time `json:"started_at"`
	ReplayOf   *string   `json:"replay_of"`
}

// executionListQuery builds the /api/v1/executions query string from the shared
// filter flags. functionName is resolved to an id when set.
func executionListQuery(cmd *cobra.Command, client *cli.Client) (url.Values, error) {
	q := url.Values{}
	if fn, _ := cmd.Flags().GetString("function"); fn != "" {
		fnID, err := resolveFnID(client, fn)
		if err != nil {
			return nil, err
		}
		q.Set("function_id", fnID)
	}
	if status, _ := cmd.Flags().GetString("status"); status != "" {
		q.Set("status", status)
	}
	return q, nil
}

func runExecutionsList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	q, err := executionListQuery(cmd, client)
	if err != nil {
		return err
	}
	if since, _ := cmd.Flags().GetString("since"); since != "" {
		q.Set("since", since)
	}
	if until, _ := cmd.Flags().GetString("until"); until != "" {
		q.Set("until", until)
	}
	if search, _ := cmd.Flags().GetString("search"); search != "" {
		q.Set("q", search)
	}
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}

	path := "/api/v1/executions"
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
		Executions []executionRow `json:"executions"`
		Total      int            `json:"total"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("ID", "FUNCTION", "STATUS", "CODE", "DURATION", "STARTED")
	for _, e := range result.Executions {
		dur := "-"
		if e.DurationMS != nil {
			dur = fmt.Sprintf("%dms", *e.DurationMS)
		}
		code := "-"
		if e.StatusCode != nil {
			code = strconv.Itoa(*e.StatusCode)
		}
		t.row(e.ID, e.FunctionID, e.Status, code, dur, e.StartedAt.Format(time.DateTime))
	}
	t.flush()

	shown := len(result.Executions)
	if offset+shown < result.Total {
		infof(cmd, "\nShowing %d of %d (raise --limit or page with --offset %d)", shown, result.Total, offset+shown)
	} else {
		infof(cmd, "\nShowing %d of %d", shown, result.Total)
	}
	return nil
}

func runExecutionsGet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/executions/" + url.PathEscape(args[0]))
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return emitRaw(raw)
}

func runExecutionsLogs(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	// Reuse the same /logs endpoint + rendering as `orva logs --exec-id`.
	return showExecutionLogs(cmd, client, args[0])
}

func runExecutionsDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	if len(args) > 1000 {
		return fmt.Errorf("delete: at most 1000 ids per call (got %d) — use 'executions prune' for bulk cleanup", len(args))
	}
	if err := confirm(cmd, fmt.Sprintf("Delete %d execution(s)?", len(args))); err != nil {
		return err
	}
	return bulkDeleteExecutions(cmd, client, args)
}

func runExecutionsPrune(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnFlag, _ := cmd.Flags().GetString("function")
	statusFlag, _ := cmd.Flags().GetString("status")
	olderThan, _ := cmd.Flags().GetString("older-than")
	if fnFlag == "" && statusFlag == "" && olderThan == "" {
		return fmt.Errorf("prune: at least one filter is required (--function, --status, or --older-than)")
	}

	q, err := executionListQuery(cmd, client)
	if err != nil {
		return err
	}
	if olderThan != "" {
		d, err := parseLooseDuration(olderThan)
		if err != nil {
			return fmt.Errorf("prune: invalid --older-than %q: %w", olderThan, err)
		}
		cutoff := time.Now().UTC().Add(-d)
		q.Set("until", cutoff.Format(time.RFC3339))
	}
	limit, _ := cmd.Flags().GetInt("limit")
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	// Collect matching ids first so we can confirm the exact count.
	path := "/api/v1/executions?" + q.Encode()
	resp, err := client.Get(path)
	if err != nil {
		return fmt.Errorf("prune: list: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var listed struct {
		Executions []executionRow `json:"executions"`
		Total      int            `json:"total"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return fmt.Errorf("prune: decode: %w", err)
	}
	if len(listed.Executions) == 0 {
		infof(cmd, "No executions match the filter — nothing to prune.")
		return nil
	}
	ids := make([]string, 0, len(listed.Executions))
	for _, e := range listed.Executions {
		ids = append(ids, e.ID)
	}

	more := ""
	if listed.Total > len(ids) {
		more = fmt.Sprintf(" (of %d matching; re-run or raise --limit for the rest)", listed.Total)
	}
	if err := confirm(cmd, fmt.Sprintf("Delete %d execution(s)%s?", len(ids), more)); err != nil {
		return err
	}
	return bulkDeleteExecutions(cmd, client, ids)
}

// bulkDeleteExecutions deletes ids in batches of 1000 (the server cap) and
// reports the aggregate result.
func bulkDeleteExecutions(cmd *cobra.Command, client *cli.Client, ids []string) error {
	const batch = 1000
	totalDeleted, totalFailed := 0, 0
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		resp, err := client.Post("/api/v1/executions/bulk-delete", map[string]any{"ids": ids[start:end]})
		if err != nil {
			return fmt.Errorf("bulk-delete: %w", err)
		}
		if err := checkResponse(resp); err != nil {
			return err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var r struct {
			Deleted int `json:"deleted"`
			Failed  int `json:"failed"`
		}
		_ = json.Unmarshal(raw, &r)
		totalDeleted += r.Deleted
		totalFailed += r.Failed
	}
	if outputJSON(cmd) {
		return emitJSON(map[string]any{"deleted": totalDeleted, "failed": totalFailed})
	}
	if totalFailed > 0 {
		okf(cmd, "deleted %d execution(s), %d failed", totalDeleted, totalFailed)
	} else {
		okf(cmd, "deleted %d execution(s)", totalDeleted)
	}
	return nil
}

func runExecutionsReplay(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Post("/api/v1/executions/"+url.PathEscape(args[0])+"/replay", nil)
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	// Note: a 4xx/5xx here may be the replayed function's own status (the body
	// is still meaningful), so read + print the body before signalling via exit.
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	newID := resp.Header.Get("X-Orva-Execution-ID")
	replayOf := resp.Header.Get("X-Orva-Replay-Of")
	durMS := resp.Header.Get("X-Orva-Duration-MS")

	if outputJSON(cmd) {
		out := map[string]any{
			"status":          resp.StatusCode,
			"execution_id":    newID,
			"replay_of":       replayOf,
			"duration_ms_hdr": durMS,
		}
		var parsed any
		if json.Unmarshal(respBody, &parsed) == nil {
			out["body"] = parsed
		} else {
			out["body"] = string(respBody)
		}
		if err := emitJSON(out); err != nil {
			return err
		}
		return exitForStatus(resp.StatusCode)
	}

	infof(cmd, "replay %s · new execution %s · %d · %sms", replayOf, newID, resp.StatusCode, dash(durMS))
	if stdoutIsTerminal() {
		var parsed any
		if json.Unmarshal(respBody, &parsed) == nil {
			pretty, _ := json.MarshalIndent(parsed, "", "  ")
			fmt.Println(string(pretty))
			return exitForStatus(resp.StatusCode)
		}
	}
	os.Stdout.Write(respBody)
	if len(respBody) > 0 && respBody[len(respBody)-1] != '\n' && stdoutIsTerminal() {
		fmt.Println()
	}
	return exitForStatus(resp.StatusCode)
}

// parseLooseDuration accepts Go durations (h/m/s) plus a "<n>d" days suffix,
// which time.ParseDuration does not support, so `--older-than 7d` works.
func parseLooseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}
	return time.ParseDuration(s)
}
