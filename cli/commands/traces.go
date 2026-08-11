package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// tracesCmd surfaces the dashboard's tracing view to the terminal. A trace
// is a set of executions sharing one trace_id; its local root is the earliest
// span whose parent is absent from the trace. These subcommands hit the same REST endpoints the
// dashboard's waterfall view uses:
//
//	GET /api/v1/traces                       — recent trace summaries
//	GET /api/v1/traces/{trace_id}            — full causal tree
//	GET /api/v1/functions/{id}/baseline      — rolling p50/p95/p99 latency
var tracesCmd = &cobra.Command{
	Use:   "traces",
	Short: "Inspect distributed traces",
	Long: `Inspect distributed traces collected from function invocations.

Every execution is a span; spans sharing a trace_id form a causal tree
(HTTP → function-to-function calls, cron, jobs, inbound webhooks). List
recent traces, expand one into its span tree, or read a function's
rolling latency baseline.

  orva traces list
  orva traces list --fn greeter --limit 20
  orva traces get tr_01h...
  orva traces baseline greeter`,
}

var tracesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent trace summaries",
	Long: `List recent traces, one trace-wide summary per row. Status, duration,
outliers, and counts include all spans. --fn matches any span by exact function
name or id; --before accepts the opaque cursor returned by a previous page.

  orva traces list
  orva traces list --fn greeter --limit 20
  orva traces list -o json`,
	Args: cobra.NoArgs,
	RunE: runTracesList,
}

var tracesGetCmd = &cobra.Command{
	Use:   "get <trace-id>",
	Short: "Show the full span tree for a trace",
	Long: `Show every span in a trace as an indented causal tree (children nested
under their parent span).

  orva traces get tr_01h...
  orva traces get tr_01h... -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runTracesGet,
}

var tracesBaselineCmd = &cobra.Command{
	Use:   "baseline <function>",
	Short: "Show a function's rolling latency baseline",
	Long: `Show the rolling p95/p99/mean latency baseline for a function plus the
current sample count. The baseline drives the outlier flag on each span.

  orva traces baseline greeter
  orva traces baseline greeter -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runTracesBaseline,
}

func init() {
	tracesListCmd.Flags().String("fn", "", "filter to traces containing this function (exact name or id)")
	tracesListCmd.Flags().Int("limit", 50, "max number of traces to return (1-200)")
	tracesListCmd.Flags().String("before", "", "opaque cursor from a prior page's next_cursor")

	tracesCmd.AddCommand(
		tracesListCmd,
		tracesGetCmd,
		tracesBaselineCmd,
	)
}

// rootSpanRow mirrors the server's trace-wide list summary.
type rootSpanRow struct {
	TraceID              string `json:"trace_id"`
	RootSpanID           string `json:"root_span_id"`
	RootFunctionID       string `json:"root_function_id"`
	FunctionName         string `json:"function_name"`
	ExternalParentSpanID string `json:"external_parent_span_id"`
	Trigger              string `json:"trigger"`
	StartedAt            string `json:"started_at"`
	DurationMS           int64  `json:"duration_ms"`
	Status               string `json:"status"`
	StatusCode           int    `json:"status_code"`
	IsOutlier            bool   `json:"is_outlier"`
	SpanCount            int    `json:"span_count"`
	ErrorCount           int    `json:"error_count"`
	ColdStartCount       int    `json:"cold_start_count"`
}

// spanRow mirrors the server's SpanView (handlers/traces.go).
type spanRow struct {
	ExecutionID  string `json:"execution_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id"`
	FunctionID   string `json:"function_id"`
	FunctionName string `json:"function_name"`
	Trigger      string `json:"trigger"`
	Status       string `json:"status"`
	StatusCode   int    `json:"status_code"`
	ColdStart    bool   `json:"cold_start"`
	IsOutlier    bool   `json:"is_outlier"`
	StartedAt    string `json:"started_at"`
	DurationMS   int64  `json:"duration_ms"`
	OffsetMS     int64  `json:"offset_ms"`
	ErrorMessage string `json:"error_message"`
}

// traceView mirrors the server's TraceView (handlers/traces.go).
type traceView struct {
	TraceID              string    `json:"trace_id"`
	RootSpanID           string    `json:"root_span_id"`
	RootFunctionID       string    `json:"root_function_id"`
	ExternalParentSpanID string    `json:"external_parent_span_id"`
	Trigger              string    `json:"trigger"`
	StartedAt            string    `json:"started_at"`
	TotalDurationMS      int64     `json:"total_duration_ms"`
	Status               string    `json:"status"`
	HasOutlier           bool      `json:"has_outlier"`
	SpanCount            int       `json:"span_count"`
	ErrorCount           int       `json:"error_count"`
	ColdStartCount       int       `json:"cold_start_count"`
	Spans                []spanRow `json:"spans"`
}

// baselineSummary mirrors metrics.BaselineSummary.
type baselineSummary struct {
	FunctionID    string `json:"function_id"`
	P95MS         int64  `json:"p95_ms"`
	P99MS         int64  `json:"p99_ms"`
	MeanMS        int64  `json:"mean_ms"`
	SampleCount   int    `json:"sample_count"`
	WindowSize    int    `json:"window_size"`
	LastUpdatedAt int64  `json:"last_updated_at"`
}

func runTracesList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	q := url.Values{}
	if fn, _ := cmd.Flags().GetString("fn"); fn != "" {
		fnID, err := resolveFnID(client, fn)
		if err != nil {
			return err
		}
		q.Set("function_id", fnID)
	}
	if limit, _ := cmd.Flags().GetInt("limit"); limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if before, _ := cmd.Flags().GetString("before"); before != "" {
		q.Set("before", before)
	}

	path := "/api/v1/traces"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	resp, err := client.Get(path)
	if err != nil {
		return fmt.Errorf("list traces: %w", err)
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
		Traces     []rootSpanRow `json:"traces"`
		NextCursor string        `json:"next_cursor"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(result.Traces) == 0 {
		infof(cmd, "no traces found")
		return nil
	}

	t := newTable("TRACE_ID", "ENTRY", "STATUS", "DURATION", "SPANS", "ERRORS", "STARTED")
	for _, tr := range result.Traces {
		root := tr.FunctionName
		if root == "" {
			root = tr.RootFunctionID
		}
		t.row(tr.TraceID, dash(root), dash(tr.Status), formatDuration(tr.DurationMS),
			fmt.Sprintf("%d", tr.SpanCount), fmt.Sprintf("%d", tr.ErrorCount), dash(tr.StartedAt))
	}
	t.flush()

	if result.NextCursor != "" {
		infof(cmd, "\nMore available; next page: --before %s", result.NextCursor)
	}
	return nil
}

func runTracesGet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	traceID := args[0]

	resp, err := client.Get("/api/v1/traces/" + url.PathEscape(traceID))
	if err != nil {
		return fmt.Errorf("get trace: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var tv traceView
	if err := json.Unmarshal(raw, &tv); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	infof(cmd, "trace %s · %s · %s · %d span(s)",
		tv.TraceID, dash(tv.Status), formatDuration(tv.TotalDurationMS), tv.SpanCount)

	// Render the span tree indented by parent/child depth. Build a
	// children index keyed by parent_span_id, then walk from the root(s).
	childrenOf := map[string][]spanRow{}
	bySpanID := map[string]bool{}
	for _, sp := range tv.Spans {
		childrenOf[sp.ParentSpanID] = append(childrenOf[sp.ParentSpanID], sp)
		bySpanID[sp.SpanID] = true
	}

	t := newTable("SPAN", "STATUS", "DURATION", "OFFSET", "TRIGGER")
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, sp := range childrenOf[parent] {
			name := sp.FunctionName
			if name == "" {
				name = sp.FunctionID
			}
			label := strings.Repeat("  ", depth) + dash(name)
			status := sp.Status
			if sp.IsOutlier {
				status += " (outlier)"
			}
			t.row(label, dash(status), formatDuration(sp.DurationMS), formatDuration(sp.OffsetMS), dash(sp.Trigger))
			walk(sp.SpanID, depth+1)
		}
	}
	// Roots are spans whose parent isn't present in this trace (true root has
	// an empty parent_span_id; an orphaned child still renders at depth 0).
	walk("", 0)
	for _, sp := range tv.Spans {
		if sp.ParentSpanID != "" && !bySpanID[sp.ParentSpanID] {
			name := sp.FunctionName
			if name == "" {
				name = sp.FunctionID
			}
			t.row(dash(name), dash(sp.Status), formatDuration(sp.DurationMS), formatDuration(sp.OffsetMS), dash(sp.Trigger))
			walk(sp.SpanID, 1)
		}
	}
	t.flush()
	return nil
}

func runTracesBaseline(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/functions/" + url.PathEscape(fnID) + "/baseline")
	if err != nil {
		return fmt.Errorf("get baseline: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var b baselineSummary
	if err := json.Unmarshal(raw, &b); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if b.SampleCount == 0 {
		infof(cmd, "no samples yet for %q (baseline fills from live traffic)", args[0])
		return nil
	}

	t := newTable("METRIC", "VALUE")
	t.row("mean", formatDuration(b.MeanMS))
	t.row("p95", formatDuration(b.P95MS))
	t.row("p99", formatDuration(b.P99MS))
	t.row("samples", fmt.Sprintf("%d", b.SampleCount))
	t.row("window", fmt.Sprintf("%d", b.WindowSize))
	t.flush()
	return nil
}

// formatDuration renders a millisecond count as a human-readable cell.
func formatDuration(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.2fs", float64(ms)/1000)
}
