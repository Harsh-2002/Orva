package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/Harsh-2002/Orva/cli/commands/theme"
	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

// diffCmd exposes the dashboard's FunctionDiff view to the terminal.
// Hits the same /functions/{fn}/diff endpoint, but asks for the
// pre-computed `unified` format so the slim CLI doesn't need a
// diff library of its own — the server already wraps the source
// trees with gotextdiff.
var diffCmd = &cobra.Command{
	Use:   "diff <function>",
	Short: "Compare two past deployments of a function",
	Long: `Show a git-style unified diff between two past successful deployments.

Each side is identified by deployment ID (` + "`dep_…`" + `). When --from or
--to are omitted, defaults are picked from the function's deployment
history: --to defaults to the currently-active deployment, and --from
defaults to the most recent earlier deployment whose code_hash differs
from the active one. That makes "orva diff <fn>" useful out of the box —
a no-arg invocation almost always shows the last real code change.

The dashboard's deep-link UI exposes deployment IDs alongside each
version (Settings → Compare). Copy them from there if you want to pin
the comparison to specific historical points.

Pipe through pagers (` + "`less -R`" + ` for ANSI) for large diffs. Use the global
--no-color to disable terminal escapes even when stdout is a TTY (useful for
CI captures); -o json dumps the structured response for scripted post-processing.`,
	Args: cobra.ExactArgs(1),
	RunE: runDiff,
}

func init() {
	diffCmd.Flags().String("from", "", "deployment id to compare FROM (default: previous distinct code_hash)")
	diffCmd.Flags().String("to", "", "deployment id to compare TO (default: active deployment)")
}

func runDiff(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnName := args[0]

	from, _ := cmd.Flags().GetString("from")
	to, _ := cmd.Flags().GetString("to")
	wantJSON := outputJSON(cmd)

	// Fill in defaults when the operator didn't pin a specific range.
	if from == "" || to == "" {
		defFrom, defTo, err := resolveDefaultDiffRange(client, fnName)
		if err != nil {
			return fmt.Errorf("resolve default range: %w (specify --from and --to explicitly)", err)
		}
		if from == "" {
			from = defFrom
		}
		if to == "" {
			to = defTo
		}
	}
	if from == to {
		return fmt.Errorf("--from and --to are the same deployment (%s)", from)
	}

	format := "unified"
	if wantJSON {
		format = "json"
	}
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)
	q.Set("format", format)
	path := "/api/v1/functions/" + url.PathEscape(fnName) + "/diff?" + q.Encode()

	resp, err := client.Get(path)
	if err != nil {
		return fmt.Errorf("diff request: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	defer resp.Body.Close()

	if wantJSON {
		var v any
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		fmt.Fprintln(os.Stderr, "(no code differences between these versions)")
		return nil
	}
	if !colorEnabled(cmd) {
		_, err = os.Stdout.Write(body)
		return err
	}
	return writeColorizedDiff(os.Stdout, string(body), styles(cmd))
}

// writeColorizedDiff applies the Orva theme to the unified-diff bytes:
// bold for the +++/--- file headers, cyan for @@ hunk headers, red for
// removed lines, green for added lines. Untouched context lines pass
// through plain so the output stays scannable.
func writeColorizedDiff(w io.Writer, body string, s *theme.Styles) error {
	var sb strings.Builder
	for i, line := range strings.Split(body, "\n") {
		// strings.Split with a trailing newline yields a final empty
		// element — emit it as a bare newline so we don't append two.
		if i > 0 {
			sb.WriteByte('\n')
		}
		switch {
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			sb.WriteString(s.DiffMeta.Render(line))
		case strings.HasPrefix(line, "@@"):
			sb.WriteString(s.DiffHunk.Render(line))
		case strings.HasPrefix(line, "-"):
			sb.WriteString(s.DiffDel.Render(line))
		case strings.HasPrefix(line, "+"):
			sb.WriteString(s.DiffAdd.Render(line))
		default:
			sb.WriteString(line)
		}
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

// resolveDefaultDiffRange picks sensible defaults for --from / --to when
// the operator runs `orva diff <fn>` with no explicit IDs. Strategy:
// `to` = active deployment (the row whose code_hash matches the
// function's current code_hash); `from` = the most recent earlier
// succeeded row with a *different* code_hash, so a no-op redeploy that
// landed on the same hash doesn't produce an empty diff by default.
func resolveDefaultDiffRange(client *cli.Client, fnName string) (string, string, error) {
	// Resolve name → ID. GET /api/v1/functions/{id} doesn't accept names
	// (it bypasses the shared resolveFnID helper that the diff endpoint
	// uses), so we look the name up via the list endpoint first.
	fnID, err := resolveFnID(client, fnName)
	if err != nil {
		return "", "", err
	}
	fnResp, err := client.Get("/api/v1/functions/" + url.PathEscape(fnID))
	if err != nil {
		return "", "", err
	}
	if err := checkResponse(fnResp); err != nil {
		return "", "", err
	}
	var fn struct {
		ID       string `json:"id"`
		CodeHash string `json:"code_hash"`
	}
	if err := decodeJSON(fnResp, &fn); err != nil {
		return "", "", err
	}
	if fn.CodeHash == "" {
		return "", "", fmt.Errorf("function %q has no active code hash yet", fnName)
	}

	depResp, err := client.Get("/api/v1/functions/" + url.PathEscape(fn.ID) + "/deployments?limit=100")
	if err != nil {
		return "", "", err
	}
	if err := checkResponse(depResp); err != nil {
		return "", "", err
	}
	var deps struct {
		Deployments []struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			CodeHash string `json:"code_hash"`
		} `json:"deployments"`
	}
	if err := decodeJSON(depResp, &deps); err != nil {
		return "", "", err
	}

	var to, from string
	for _, d := range deps.Deployments {
		if d.Status != "succeeded" || d.CodeHash == "" {
			continue
		}
		if to == "" && d.CodeHash == fn.CodeHash {
			to = d.ID
			continue
		}
		if to != "" && d.CodeHash != fn.CodeHash {
			from = d.ID
			break
		}
	}
	if to == "" {
		return "", "", fmt.Errorf("no succeeded deployment matches the function's current code_hash")
	}
	if from == "" {
		return "", "", fmt.Errorf("only one distinct code_hash in deployment history — nothing earlier to compare against")
	}
	return from, to, nil
}
