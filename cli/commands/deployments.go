package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

// deploymentsCmd surfaces a function's deployment history and the
// per-deployment build logs to the terminal. These are the same async
// build records the dashboard's Deployments view shows: every deploy and
// rollback creates a row, and each carries an append-only build log.
var deploymentsCmd = &cobra.Command{
	Use:   "deployments",
	Short: "Inspect deployment history and build logs",
	Long: `Inspect a function's deployment history and read per-deployment build logs.

Every deploy or rollback creates a deployment record with a status
(queued / building / succeeded / failed), a content-addressed code_hash,
and an append-only build log. Use these subcommands to audit what shipped
when, find a target for ` + "`orva rollback`" + `, and debug build failures.

Examples:
  orva deployments list greeter
  orva deployments get dep_01J...
  orva deployments logs dep_01J... --follow`,
}

var deploymentsListCmd = &cobra.Command{
	Use:   "list <fn>",
	Short: "List a function's deployment history (newest first)",
	Long: `List the deployment history for a function, newest first.

The function may be referenced by name or by UUID. Each row shows the
deployment id (a target for rollback / diff), its terminal status and
build phase, the short code_hash, when it was submitted, and how long the
build took.

Examples:
  orva deployments list greeter
  orva deployments list greeter --limit 100
  orva deployments list greeter -o json | jq '.deployments[].code_hash'`,
	Args: cobra.ExactArgs(1),
	RunE: runDeploymentsList,
}

var deploymentsGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show a single deployment by id",
	Long: `Show the full detail for a single deployment by its id.

Includes status, phase, code_hash, source (deploy / rollback), the parent
deployment for a rollback, timestamps, build duration, and any error
message from a failed build.

Examples:
  orva deployments get dep_01J...
  orva deployments get dep_01J... -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runDeploymentsGet,
}

var deploymentsLogsCmd = &cobra.Command{
	Use:   "logs <id>",
	Short: "Print a deployment's build log",
	Long: `Print the build log for a deployment to stdout.

Each line carries a monotonically-increasing sequence number; the build
log is what to read when a deploy fails. With --follow / -f the log is
streamed live over Server-Sent Events until the build reaches a terminal
state (succeeded / failed), so you can watch an in-flight build.

Examples:
  orva deployments logs dep_01J...
  orva deployments logs dep_01J... --follow`,
	Args: cobra.ExactArgs(1),
	RunE: runDeploymentsLogs,
}

func init() {
	deploymentsListCmd.Flags().Int("limit", 50, "max number of deployments to return (1-500)")
	deploymentsLogsCmd.Flags().BoolP("follow", "f", false, "stream the build log live until the build finishes")

	deploymentsCmd.AddCommand(deploymentsListCmd, deploymentsGetCmd, deploymentsLogsCmd)
}

func runDeploymentsList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	limit, _ := cmd.Flags().GetInt("limit")

	path := fmt.Sprintf("/api/v1/functions/%s/deployments?limit=%d", url.PathEscape(fnID), limit)
	resp, err := client.Get(path)
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
		Deployments []struct {
			ID          string    `json:"id"`
			Status      string    `json:"status"`
			Phase       string    `json:"phase"`
			CodeHash    string    `json:"code_hash"`
			SubmittedAt time.Time `json:"submitted_at"`
			DurationMS  *int64    `json:"duration_ms"`
		} `json:"deployments"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if len(result.Deployments) == 0 {
		infof(cmd, "no deployments for %s", args[0])
		return nil
	}

	t := newTable("ID", "STATUS", "PHASE", "CODE_HASH", "CREATED", "DURATION")
	for _, d := range result.Deployments {
		dur := "-"
		if d.DurationMS != nil {
			dur = fmt.Sprintf("%dms", *d.DurationMS)
		}
		t.row(
			d.ID,
			dash(d.Status),
			dash(d.Phase),
			dash(shortHash(d.CodeHash)),
			d.SubmittedAt.Format(time.DateTime),
			dur,
		)
	}
	t.flush()

	shown := len(result.Deployments)
	if shown < result.Total {
		infof(cmd, "\nShowing %d of %d (raise --limit to see older deployments)", shown, result.Total)
	} else {
		infof(cmd, "\nShowing %d of %d", shown, result.Total)
	}
	return nil
}

func runDeploymentsGet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	id := args[0]

	resp, err := client.Get("/api/v1/deployments/" + url.PathEscape(id))
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

	var d struct {
		ID                 string     `json:"id"`
		FunctionID         string     `json:"function_id"`
		Version            int64      `json:"version"`
		Status             string     `json:"status"`
		Phase              string     `json:"phase"`
		CodeHash           string     `json:"code_hash"`
		Source             string     `json:"source"`
		ParentDeploymentID *string    `json:"parent_deployment_id"`
		ErrorMessage       string     `json:"error_message"`
		SubmittedAt        time.Time  `json:"submitted_at"`
		FinishedAt         *time.Time `json:"finished_at"`
		DurationMS         *int64     `json:"duration_ms"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("FIELD", "VALUE")
	t.row("ID", d.ID)
	t.row("Function", d.FunctionID)
	t.row("Version", d.Version)
	t.row("Status", dash(d.Status))
	t.row("Phase", dash(d.Phase))
	t.row("Code hash", dash(d.CodeHash))
	t.row("Source", dash(d.Source))
	if d.ParentDeploymentID != nil {
		t.row("Parent", *d.ParentDeploymentID)
	}
	t.row("Submitted", d.SubmittedAt.Format(time.DateTime))
	if d.FinishedAt != nil {
		t.row("Finished", d.FinishedAt.Format(time.DateTime))
	}
	if d.DurationMS != nil {
		t.row("Duration", fmt.Sprintf("%dms", *d.DurationMS))
	}
	if d.ErrorMessage != "" {
		t.row("Error", d.ErrorMessage)
	}
	t.flush()
	return nil
}

func runDeploymentsLogs(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	id := args[0]
	follow, _ := cmd.Flags().GetBool("follow")

	if follow {
		return followDeploymentLogs(cmd, client, id)
	}

	resp, err := client.Get("/api/v1/deployments/" + url.PathEscape(id) + "/logs?limit=2000")
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
		Logs []struct {
			Seq    int64  `json:"seq"`
			Stream string `json:"stream"`
			Line   string `json:"line"`
		} `json:"logs"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(result.Logs) == 0 {
		infof(cmd, "(no build log lines)")
		return nil
	}
	for _, ln := range result.Logs {
		fmt.Println(ln.Line)
	}
	return nil
}

// followDeploymentLogs streams the build log live over SSE. The
// /deployments/{id}/stream endpoint emits `log` events (one per line),
// then a terminal `succeeded` / `failed` / `error` event carrying the
// deployment row. We print each log line to stdout and the terminal
// status to stderr, then return.
func followDeploymentLogs(cmd *cobra.Command, client *cli.Client, id string) error {
	resp, err := streamSSE(client, "/api/v1/deployments/"+url.PathEscape(id)+"/stream")
	if err != nil {
		return fmt.Errorf("follow: %w", err)
	}
	defer resp.Body.Close()

	infof(cmd, "following build log for %s — Ctrl-C to stop", id)

	terminal := false
	if err := consumeSSE(resp, func(event, data string) (bool, error) {
		stop, ferr := handleStreamFrame(cmd, event, data)
		if stop {
			terminal = true
		}
		return stop, ferr
	}); err != nil {
		return fmt.Errorf("follow: %w", err)
	}
	// A clean EOF with no terminal event means the build stream was cut before
	// reporting a result — don't pass it off as success (mirrors watchBuild).
	if !terminal {
		return fmt.Errorf("build stream ended before a result was reported; check `orva deployments get %s`", id)
	}
	return nil
}

// handleStreamFrame processes one complete SSE frame. It returns done=true
// when a terminal event arrives (succeeded / failed / error).
func handleStreamFrame(cmd *cobra.Command, event, data string) (bool, error) {
	switch event {
	case "log":
		if data == "" {
			return false, nil
		}
		var ln struct {
			Line string `json:"line"`
		}
		if json.Unmarshal([]byte(data), &ln) == nil {
			fmt.Println(ln.Line)
		} else {
			fmt.Println(data)
		}
		return false, nil
	case "succeeded":
		okf(cmd, "build succeeded")
		return true, nil
	case "failed":
		var d struct {
			ErrorMessage string `json:"error_message"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		if d.ErrorMessage != "" {
			fmt.Fprintf(os.Stderr, "build failed: %s\n", d.ErrorMessage)
		} else {
			fmt.Fprintln(os.Stderr, "build failed")
		}
		return true, fmt.Errorf("deployment failed")
	case "error":
		var d struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(data), &d)
		return true, fmt.Errorf("stream error: %s", dash(d.Message))
	default:
		return false, nil
	}
}

// shortHash trims a content-addressed code_hash to its first 12 chars for
// table display, matching the dashboard's convention.
func shortHash(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
