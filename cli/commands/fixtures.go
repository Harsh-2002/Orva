package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

// fixturesCmd surfaces the dashboard's Test pane "Saved" fixtures and the
// test_function_with_fixture MCP tool to the terminal. Fixtures are
// reusable, Postman-style request presets (method, path, headers, body)
// scoped per-function. All subcommands hit the same authenticated REST
// endpoints the dashboard uses:
//
//	GET    /api/v1/functions/{fn_id}/fixtures
//	POST   /api/v1/functions/{fn_id}/fixtures
//	GET    /api/v1/functions/{fn_id}/fixtures/{name}
//	PUT    /api/v1/functions/{fn_id}/fixtures/{name}   (upsert)
//	DELETE /api/v1/functions/{fn_id}/fixtures/{name}
//
// `fixtures test` runs the fixture against the function exactly the way the
// MCP test_function_with_fixture tool does: load the saved fixture, then
// replay its method/path/headers/body at /fn/<id><path> and print the
// function's response to stdout.
var fixturesCmd = &cobra.Command{
	Use:   "fixtures",
	Short: "Manage reusable request fixtures for a function",
	Long: `Manage saved, Postman-style request fixtures for a function.

A fixture bundles a method, sub-path, headers, and body under a name so
you can replay a request without retyping it. Fixtures are the same
presets the dashboard's Test pane "Saved" popover stores, and the ones
the test_function_with_fixture MCP tool reads.

Functions can be referenced by name or by UUID.

  orva fixtures list greeter
  orva fixtures save greeter happy-path --method POST --body '{"name":"ada"}'
  orva fixtures test greeter happy-path | jq .`,
}

var fixturesListCmd = &cobra.Command{
	Use:   "list <function>",
	Short: "List saved fixtures for a function",
	Long: `List every saved request fixture for one function.

  orva fixtures list greeter
  orva fixtures list greeter -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runFixturesList,
}

var fixturesGetCmd = &cobra.Command{
	Use:   "get <function> <name>",
	Short: "Show one saved fixture",
	Long: `Show the full method/path/headers/body of one saved fixture.

  orva fixtures get greeter happy-path
  orva fixtures get greeter happy-path -o json`,
	Args: cobra.ExactArgs(2),
	RunE: runFixturesGet,
}

var fixturesSaveCmd = &cobra.Command{
	Use:   "save <function> <name>",
	Short: "Create or update a fixture (upsert)",
	Long: `Create or update a saved request fixture. Idempotent on (function, name) —
re-running with the same name overwrites the stored method/path/headers/body.

The body can be inline, read from a file with @file, or read from stdin
with @-. Repeat --header to add multiple headers.

  orva fixtures save greeter happy-path --method POST --body '{"name":"ada"}'
  orva fixtures save greeter from-file --body @payload.json
  echo '{"name":"ada"}' | orva fixtures save greeter from-stdin --body @-
  orva fixtures save api ping --method GET --path /health \
    --header 'X-Trace: 1' --query 'verbose=1'`,
	Args: cobra.ExactArgs(2),
	RunE: runFixturesSave,
}

var fixturesDeleteCmd = &cobra.Command{
	Use:   "delete <function> <name>",
	Short: "Delete a saved fixture",
	Long: `Delete a saved fixture by name. Prompts for confirmation unless --yes is
given (required for non-interactive use).

  orva fixtures delete greeter happy-path
  orva fixtures delete greeter happy-path --yes`,
	Args: cobra.ExactArgs(2),
	RunE: runFixturesDelete,
}

var fixturesTestCmd = &cobra.Command{
	Use:   "test <function> <name>",
	Short: "Run a saved fixture against the function",
	Long: `Replay a saved fixture against the live function and print its response.

This mirrors the test_function_with_fixture MCP tool: it loads the saved
fixture, then invokes the function with the fixture's method, sub-path,
headers, and body. The function's response body goes to stdout; status
and timing go to stderr, so you can pipe cleanly.

  orva fixtures test greeter happy-path
  orva fixtures test greeter happy-path | jq .`,
	Args: cobra.ExactArgs(2),
	RunE: runFixturesTest,
}

func init() {
	fixturesSaveCmd.Flags().StringP("method", "X", "POST", "HTTP method (GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS)")
	fixturesSaveCmd.Flags().String("path", "/", "sub-path passed to the handler")
	fixturesSaveCmd.Flags().StringP("body", "d", "", "request body: inline string, @file, or @- for stdin")
	fixturesSaveCmd.Flags().StringArrayP("header", "H", nil, "add a request header 'Key: Value' (repeatable)")
	fixturesSaveCmd.Flags().String("query", "", "raw query string appended to the path (e.g. 'a=1&b=2')")

	fixturesCmd.AddCommand(
		fixturesListCmd,
		fixturesGetCmd,
		fixturesSaveCmd,
		fixturesDeleteCmd,
		fixturesTestCmd,
	)
}

// fixtureView mirrors the server's fixtureView (handlers/fixtures.go) so the
// CLI decodes the same field names the API emits.
type cliFixtureView struct {
	ID         string            `json:"id"`
	FunctionID string            `json:"function_id"`
	Name       string            `json:"name"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
}

func runFixturesList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/functions/" + url.PathEscape(fnID) + "/fixtures")
	if err != nil {
		return fmt.Errorf("list fixtures: %w", err)
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
		Fixtures []cliFixtureView `json:"fixtures"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(result.Fixtures) == 0 {
		infof(cmd, "no fixtures saved for %q", args[0])
		return nil
	}

	t := newTable("NAME", "METHOD", "PATH", "BODY", "UPDATED")
	for _, f := range result.Fixtures {
		t.row(f.Name, f.Method, dash(f.Path), bodyPreview(f.Body), dash(f.UpdatedAt))
	}
	t.flush()
	return nil
}

func runFixturesGet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	name := args[1]

	resp, err := client.Get("/api/v1/functions/" + url.PathEscape(fnID) + "/fixtures/" + url.PathEscape(name))
	if err != nil {
		return fmt.Errorf("get fixture: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var f cliFixtureView
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("FIELD", "VALUE")
	t.row("name", f.Name)
	t.row("method", f.Method)
	t.row("path", dash(f.Path))
	t.row("created", dash(f.CreatedAt))
	t.row("updated", dash(f.UpdatedAt))
	t.flush()

	if len(f.Headers) > 0 {
		fmt.Println()
		ht := newTable("HEADER", "VALUE")
		for k, v := range f.Headers {
			ht.row(k, v)
		}
		ht.flush()
	}
	if f.Body != "" {
		infof(cmd, "\nbody:") // label → stderr; the body itself stays on stdout for piping
		fmt.Println(f.Body)
	}
	return nil
}

func runFixturesSave(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	name := args[1]

	method, _ := cmd.Flags().GetString("method")
	path, _ := cmd.Flags().GetString("path")
	bodyArg, _ := cmd.Flags().GetString("body")
	headerArgs, _ := cmd.Flags().GetStringArray("header")
	query, _ := cmd.Flags().GetString("query")

	headers, err := parseHeaderArgs(headerArgs)
	if err != nil {
		return err
	}

	var body []byte
	if bodyArg != "" {
		body, err = readBodyArg(bodyArg)
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}
	}

	// The fixture's path field stores the sub-path; fold any --query into it
	// since the server persists path verbatim and replays it on invoke.
	if query != "" {
		if path == "" {
			path = "/"
		}
		if strings.Contains(path, "?") {
			path += "&" + query
		} else {
			path += "?" + query
		}
	}

	// Map to the server's fixtureRequest body shape (handlers/fixtures.go).
	reqBody := map[string]any{
		"name":    name,
		"method":  strings.ToUpper(strings.TrimSpace(method)),
		"path":    path,
		"headers": headers,
		"body":    string(body),
	}

	// PUT /fixtures/{name} is the upsert path — idempotent on (function, name).
	resp, err := client.Put("/api/v1/functions/"+url.PathEscape(fnID)+"/fixtures/"+url.PathEscape(name), reqBody)
	if err != nil {
		return fmt.Errorf("save fixture: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(raw)
	}
	okf(cmd, "saved fixture %q for %q", name, args[0])
	return nil
}

func runFixturesDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	name := args[1]

	if err := confirm(cmd, fmt.Sprintf("Delete fixture %q for function %q?", name, args[0])); err != nil {
		return err
	}

	resp, err := client.Delete("/api/v1/functions/" + url.PathEscape(fnID) + "/fixtures/" + url.PathEscape(name))
	if err != nil {
		return fmt.Errorf("delete fixture: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitJSON(map[string]any{"deleted": true, "name": name})
	}
	okf(cmd, "deleted fixture %q", name)
	return nil
}

func runFixturesTest(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	name := args[1]

	// Load the saved fixture, then replay its request envelope against the
	// function — the same behavior as the test_function_with_fixture MCP tool.
	getResp, err := client.Get("/api/v1/functions/" + url.PathEscape(fnID) + "/fixtures/" + url.PathEscape(name))
	if err != nil {
		return fmt.Errorf("load fixture: %w", err)
	}
	if err := checkResponse(getResp); err != nil {
		return err
	}
	var fx cliFixtureView
	if err := decodeJSON(getResp, &fx); err != nil {
		return fmt.Errorf("decode fixture: %w", err)
	}

	// Build the invoke path: /fn/<id> + the fixture's sub-path (which may
	// already carry a query string).
	invokePath := "/fn/" + fnID
	if p := fx.Path; p != "" && p != "/" {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		invokePath += p
	}

	headers := map[string]string{}
	for k, v := range fx.Headers {
		headers[k] = v
	}
	if headerLookup(headers, "Content-Type") == "" && fx.Body != "" {
		// Match invoke.go: default JSON bodies to application/json.
		var tmp any
		if json.Unmarshal([]byte(fx.Body), &tmp) == nil {
			headers["Content-Type"] = "application/json"
		}
	}

	method := strings.ToUpper(strings.TrimSpace(fx.Method))
	if method == "" {
		method = "POST"
	}

	req := cli.Request{
		Method:  method,
		Path:    invokePath,
		Headers: headers,
		Accept:  "*/*",
	}
	if fx.Body != "" {
		req.Body = bytes.NewReader([]byte(fx.Body))
	}

	resp, err := client.Send(req)
	if err != nil {
		return fmt.Errorf("invoke failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if outputJSON(cmd) {
		out := map[string]any{
			"fixture": fx.Name,
			"method":  method,
			"status":  resp.StatusCode,
			"headers": flattenHeaders(resp.Header),
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

	infof(cmd, "%s %s [fixture %q] · %d", method, invokePath, fx.Name, resp.StatusCode)

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

// parseHeaderArgs turns repeated 'Key: Value' flags into a map. Empty input
// yields an empty (non-nil) map so JSON marshals to {} not null.
func parseHeaderArgs(headerArgs []string) (map[string]string, error) {
	headers := map[string]string{}
	for _, h := range headerArgs {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("invalid --header %q (expected 'Key: Value')", h)
		}
		headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return headers, nil
}

// bodyPreview renders a short, single-line preview of a fixture body for the
// list table.
func bodyPreview(body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\n", " "), "\t", " ")
	body = strings.TrimSpace(body)
	if body == "" {
		return "-"
	}
	const max = 40
	if len(body) > max {
		return body[:max-1] + "…"
	}
	return body
}
