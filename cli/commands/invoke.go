package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

var invokeCmd = &cobra.Command{
	Use:   "invoke [name-or-id]",
	Short: "Invoke a function and print its response",
	Long: `Invoke a deployed function over HTTP and print the response body to stdout.

The response body goes to stdout; status and timing go to stderr, so you can
pipe cleanly:

  orva invoke greeter --body '{"name":"world"}' | jq .

Send a body inline, from a file, or from stdin:

  orva invoke greeter --body '{"name":"ada"}'
  orva invoke greeter --body @payload.json
  echo '{"name":"ada"}' | orva invoke greeter --body @-

Use a non-default method or custom headers, or call a custom route instead of
the /fn/<id> path:

  orva invoke greeter -X GET -H 'X-Trace: 1'
  orva invoke --route /webhooks/stripe --body @event.json

Stream responses (generators, long-lived handlers) chunk-by-chunk as they
arrive instead of buffering the whole body:

  orva invoke chat --body @prompt.json --stream`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInvoke,
}

func init() {
	invokeCmd.Flags().StringP("body", "d", "", "request body: inline string, @file, or @- for stdin")
	invokeCmd.Flags().StringP("method", "X", "POST", "HTTP method")
	invokeCmd.Flags().StringArrayP("header", "H", nil, "add a request header 'Key: Value' (repeatable)")
	invokeCmd.Flags().String("route", "", "invoke a custom route path (e.g. /webhooks/stripe) instead of /fn/<id>")
	invokeCmd.Flags().Bool("stream", false, "stream the response to stdout as it arrives (no client timeout)")
	invokeCmd.Flags().BoolP("include", "i", false, "print response status line and headers to stderr")
	invokeCmd.Flags().Int("timeout-ms", 0, "per-call timeout in ms (0 = client default of 120s; ignored with --stream)")
}

func runInvoke(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	route, _ := cmd.Flags().GetString("route")
	method, _ := cmd.Flags().GetString("method")
	bodyArg, _ := cmd.Flags().GetString("body")
	headerArgs, _ := cmd.Flags().GetStringArray("header")
	stream, _ := cmd.Flags().GetBool("stream")
	include, _ := cmd.Flags().GetBool("include")
	timeoutMS, _ := cmd.Flags().GetInt("timeout-ms")

	// Resolve the target path: either a custom route or /fn/<id>.
	var path, label string
	switch {
	case route != "":
		if len(args) > 0 {
			return fmt.Errorf("pass either a function name or --route, not both")
		}
		path = route
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		label = path
	case len(args) == 1:
		fnID, err := resolveFnID(client, args[0])
		if err != nil {
			return err
		}
		path = "/fn/" + fnID
		label = args[0]
	default:
		return fmt.Errorf("specify a function name-or-id, or --route <path>")
	}

	// Parse headers and figure out the effective content type.
	headers := map[string]string{}
	for _, h := range headerArgs {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return fmt.Errorf("invalid --header %q (expected 'Key: Value')", h)
		}
		headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	// Resolve the body (inline / @file / @-).
	var body []byte
	if bodyArg != "" {
		body, err = readBodyArg(bodyArg)
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}
	}

	contentType := headerLookup(headers, "Content-Type")
	if contentType == "" && len(body) > 0 {
		contentType = "application/json"
		headers["Content-Type"] = contentType
	}
	// Validate JSON only when we're actually sending JSON, so non-JSON bodies
	// (form posts, plain text, binary) pass through untouched.
	if len(body) > 0 && strings.Contains(strings.ToLower(contentType), "json") {
		var tmp any
		if json.Unmarshal(body, &tmp) != nil {
			return fmt.Errorf("body is not valid JSON (override with -H 'Content-Type: text/plain' to send raw)")
		}
	}

	req := cli.Request{
		Method:    strings.ToUpper(method),
		Path:      path,
		Headers:   headers,
		Accept:    "*/*",
		NoTimeout: stream,
	}
	if stream {
		// No total cap on a streamed invocation, but an idle deadline so a
		// stalled stream can't hang the CLI forever.
		req.IdleTimeout = cli.DefaultStreamIdleTimeout
	}
	if len(body) > 0 {
		req.Body = bytes.NewReader(body)
	}
	if timeoutMS > 0 && !stream {
		client.HTTP.Timeout = time.Duration(timeoutMS) * time.Millisecond
	}

	start := time.Now()
	resp, err := client.Send(req)
	if err != nil {
		return fmt.Errorf("invoke failed: %w", err)
	}
	defer resp.Body.Close()

	if include {
		fmt.Fprintf(os.Stderr, "%s %d %s\n", resp.Proto, resp.StatusCode, http.StatusText(resp.StatusCode))
		for k, vals := range resp.Header {
			for _, v := range vals {
				fmt.Fprintf(os.Stderr, "%s: %s\n", k, v)
			}
		}
		fmt.Fprintln(os.Stderr)
	}

	if stream {
		// Copy chunks straight through as the server flushes them.
		if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
			return fmt.Errorf("stream: %w", err)
		}
		infof(cmd, "%s %s · %d · %s (streamed)", req.Method, label, resp.StatusCode, time.Since(start).Round(time.Millisecond))
		return exitForStatus(resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	duration := time.Since(start).Round(time.Millisecond)

	if outputJSON(cmd) {
		out := map[string]any{
			"status":      resp.StatusCode,
			"duration_ms": duration.Milliseconds(),
			"headers":     flattenHeaders(resp.Header),
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

	infof(cmd, "%s %s · %d · %s", req.Method, label, resp.StatusCode, duration)

	// Pretty-print JSON only for an interactive terminal; pipes get the exact
	// bytes so downstream tools (jq, files) see unmodified output.
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

// exitForStatus returns a non-nil error for 4xx/5xx responses so scripts can
// detect failures via the process exit code, after the body has been printed.
func exitForStatus(status int) error {
	if status >= 400 {
		return fmt.Errorf("function returned HTTP %d", status)
	}
	return nil
}

func headerLookup(h map[string]string, key string) string {
	for k, v := range h {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		out[k] = strings.Join(vals, ", ")
	}
	return out
}
