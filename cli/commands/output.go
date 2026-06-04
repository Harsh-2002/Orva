package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Harsh-2002/Orva/cli/commands/theme"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Global flag names. These are persistent flags on the root command, so every
// subcommand inherits a consistent --output / --quiet / --no-color / --yes
// surface. They are the backbone of the CLI's scripting story.
const (
	flagOutput  = "output"
	flagQuiet   = "quiet"
	flagNoColor = "no-color"
	flagYes     = "yes"
)

// registerGlobalFlags wires the shared persistent flags onto root. Called once
// from newRootEmpty so both the slim CLI and the server binary expose them.
func registerGlobalFlags(root *cobra.Command) {
	pf := root.PersistentFlags()
	pf.StringP(flagOutput, "o", "table", "output format: table | json")
	pf.BoolP(flagQuiet, "q", false, "suppress status messages on stderr (data still goes to stdout)")
	pf.Bool(flagNoColor, false, "disable ANSI color even when stdout is a TTY")
	pf.BoolP(flagYes, "y", false, "skip confirmation prompts (required for non-interactive destructive ops)")
}

// outputJSON reports whether the user asked for machine-readable output.
func outputJSON(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetString(flagOutput)
	return strings.EqualFold(strings.TrimSpace(v), "json")
}

// isQuiet reports whether status chatter should be suppressed.
func isQuiet(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool(flagQuiet)
	return v
}

// assumeYes reports whether destructive confirmations should be skipped.
func assumeYes(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool(flagYes)
	return v
}

// colorEnabled decides whether ANSI escapes are safe to emit. It honours
// --no-color, the NO_COLOR convention (https://no-color.org), JSON output
// mode, and falls back to a TTY check on stdout.
func colorEnabled(cmd *cobra.Command) bool {
	if noColor, _ := cmd.Flags().GetBool(flagNoColor); noColor {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if outputJSON(cmd) {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// stdoutIsTerminal reports whether stdout is an interactive terminal. Used to
// decide between pretty (human) and raw (pipe-friendly) rendering.
func stdoutIsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// styles resolves the Orva color palette for this command, honoring the same
// gate as colorEnabled (--no-color / NO_COLOR / JSON mode / non-TTY). Every
// command and the chat renderer style through the returned set so the look is
// consistent and color control stays in one place.
func styles(cmd *cobra.Command) *theme.Styles {
	return theme.New(colorEnabled(cmd))
}

// emitJSON writes v as indented JSON to stdout. This is the canonical machine
// output path: clean, parseable, nothing else on stdout.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// emitRaw pretty-prints raw JSON bytes to stdout, preserving every field and
// the server's exact value formatting (ISO timestamps, numbers). Falls back to
// the raw bytes if they aren't valid JSON. Use this for --output json on list
// and get commands so machine output is faithful to the API response.
func emitRaw(b []byte) error {
	var v any
	if json.Unmarshal(b, &v) != nil {
		_, err := os.Stdout.Write(b)
		return err
	}
	return emitJSON(v)
}

// jsonUnmarshal decodes JSON bytes into v with a consistent error message.
func jsonUnmarshal(b []byte, v any) error {
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// infof prints a status/diagnostic line to stderr unless --quiet is set. Status
// never goes to stdout, so `orva ... | jq` always sees clean data.
func infof(cmd *cobra.Command, format string, a ...any) {
	if isQuiet(cmd) {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}

// okf prints a success line to stderr (a green check when color is enabled),
// suppressed by --quiet. In JSON mode it is suppressed entirely so stdout holds
// only the JSON document.
func okf(cmd *cobra.Command, format string, a ...any) {
	if isQuiet(cmd) || outputJSON(cmd) {
		return
	}
	msg := fmt.Sprintf(format, a...)
	s := styles(cmd)
	if s.Enabled() {
		fmt.Fprintf(os.Stderr, "%s %s\n", s.Success.Render("✓"), msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", msg)
	}
}

// confirm gates a destructive action. It returns nil to proceed. With --yes it
// proceeds silently. Otherwise it prompts on an interactive terminal; on a
// non-TTY (CI, pipes) it refuses and tells the caller to pass --yes.
func confirm(cmd *cobra.Command, prompt string) error {
	if assumeYes(cmd) {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("%s\nrefusing without confirmation — re-run with --yes for non-interactive use", prompt)
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("aborted")
	}
}

// table is a thin wrapper over tabwriter that standardises column spacing
// across every list command.
type table struct {
	w *tabwriter.Writer
}

// newTable starts a table on stdout with the given header columns.
func newTable(headers ...string) *table {
	t := &table{w: tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)}
	fmt.Fprintln(t.w, strings.Join(headers, "\t"))
	return t
}

// row appends a row. Each cell is formatted with %v and joined by tabs.
func (t *table) row(cells ...any) {
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = fmt.Sprintf("%v", c)
	}
	fmt.Fprintln(t.w, strings.Join(parts, "\t"))
}

// flush renders the accumulated rows.
func (t *table) flush() { t.w.Flush() }

// dash returns "-" for empty strings, keeping table cells aligned and obvious.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// readBodyArg resolves a body/value argument that may be inline text, @file to
// read from a file, or @- to read from stdin. This is the curl-style input
// convention used by invoke, kv put, secrets set, and job/cron payloads.
func readBodyArg(v string) ([]byte, error) {
	switch {
	case v == "@-":
		return io.ReadAll(os.Stdin)
	case strings.HasPrefix(v, "@"):
		return os.ReadFile(v[1:])
	default:
		return []byte(v), nil
	}
}

// consumeSSE reads a text/event-stream response frame-by-frame, calling onFrame
// for each complete `event:`/`data:` frame (terminated by a blank line). onFrame
// returns stop=true to end consumption (e.g. on a terminal event) or a non-nil
// error to abort. consumeSSE returns nil on a clean stream close (EOF).
//
// It uses a bufio.Reader (ReadString), not a bufio.Scanner, so a single very
// long line — a big build-log line or a large JSON blob — is read in full
// rather than tripping bufio's max-token limit. This is the one place the four
// CLI streamers (deploy --watch, deployments logs, activity, logs --follow)
// parse the SSE wire format.
func consumeSSE(resp *http.Response, onFrame func(event, data string) (stop bool, err error)) error {
	reader := bufio.NewReader(resp.Body)
	var event, data string
	for {
		line, rerr := reader.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(trimmed, ":"):
			// SSE comment / heartbeat — ignore.
		case strings.HasPrefix(trimmed, "event:"):
			event = strings.TrimSpace(trimmed[len("event:"):])
		case strings.HasPrefix(trimmed, "data:"):
			data = strings.TrimSpace(trimmed[len("data:"):])
		case trimmed == "" && line != "":
			// Blank line = end of frame.
			stop, ferr := onFrame(event, data)
			if ferr != nil {
				return ferr
			}
			if stop {
				return nil
			}
			event, data = "", ""
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return rerr
		}
	}
}
