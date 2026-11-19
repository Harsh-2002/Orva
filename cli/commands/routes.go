package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Manage custom URL → function routes",
	Long: `List, create, and delete user-defined route mappings that give functions
pretty URLs (e.g. /webhooks/stripe) instead of /fn/<id>.

A route maps a path to a function and an optional set of HTTP methods.
Prefix routes end in /* (e.g. /shortener/*).

On a managed server every write carries the mapping version and owner
authority read immediately before it, so a concurrent edit returns 409
instead of overwriting it. Managed owners must be exact lowercase UUIDs;
names are not resolved there.

Examples:
  orva routes list
  orva routes set /webhooks/stripe --fn payments
  orva routes set /api-proxy/* --fn proxy --methods GET,POST
  orva routes delete /webhooks/stripe`,
}

var routesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all custom routes",
	Long: `List every user-defined route mapping. On a managed server each row also
shows its version token and whether the mapping is published for dispatch.

Examples:
  orva routes list
  orva routes list -o json`,
	RunE: runRoutesList,
}

var routesSetCmd = &cobra.Command{
	Use:   "set <path>",
	Short: "Create or update a route mapping",
	Long: `Map a URL path to a function. The path is positional; the target function
is given with --fn (name or ID). Restrict methods with --methods (defaults
to all methods).

Managed servers require --fn to be the exact lowercase function UUID and
inspect the mapping before writing. --expected-version overrides the version
token that inspection returned; the owner authority tokens always come from
the inspection. A 409 means the mapping changed and is never retried
automatically.

Examples:
  orva routes set /webhooks/stripe --fn payments
  orva routes set /api-proxy/* --fn proxy --methods GET,POST
  orva routes set /hooks/x --fn 0190000a-0000-7000-8000-000000000001 --expected-version 7`,
	Args: cobra.ExactArgs(1),
	RunE: runRoutesSet,
}

var routesDeleteCmd = &cobra.Command{
	Use:   "delete <path>",
	Short: "Delete a route",
	Long: `Delete a route by its path. Destructive; prompts for confirmation unless
--yes is passed. On a managed server the current version and owner authority
are read first and sent with the delete.

Examples:
  orva routes delete /webhooks/stripe
  orva routes delete /webhooks/stripe --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runRoutesDelete,
}

func init() {
	routesSetCmd.Flags().String("fn", "", "function name or ID (required)")
	routesSetCmd.Flags().String("methods", "", "HTTP methods (default '*'); e.g. 'GET,POST'")
	routesSetCmd.Flags().String("expected-version", "", "managed only: use this version token instead of the one just read")
	routesSetCmd.MarkFlagRequired("fn")
	routesDeleteCmd.Flags().String("expected-version", "", "managed only: use this version token instead of the one just read")

	routesCmd.AddCommand(routesListCmd)
	routesCmd.AddCommand(routesSetCmd)
	routesCmd.AddCommand(routesDeleteCmd)
}

func runRoutesList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/routes")
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if managedServer(client) {
		var result struct {
			Routes []struct {
				Path       string    `json:"path"`
				FunctionID string    `json:"function_id"`
				Methods    string    `json:"methods"`
				CreatedAt  time.Time `json:"created_at"`
				Version    string    `json:"version"`
				Published  bool      `json:"published"`
			} `json:"routes"`
			Truncated bool `json:"truncated"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		if outputJSON(cmd) {
			return emitRaw(raw)
		}
		t := newTable("PATH", "FUNCTION_ID", "METHODS", "VERSION", "PUBLISHED", "CREATED")
		for _, r := range result.Routes {
			methods := r.Methods
			if methods == "" {
				methods = "*"
			}
			t.row(r.Path, r.FunctionID, methods, r.Version, r.Published, r.CreatedAt.Format(time.DateTime))
		}
		t.flush()
		infof(cmd, "\nTotal: %d", len(result.Routes))
		if result.Truncated {
			infof(cmd, "Showing the first 256 mappings; delete one to see the rest.")
		}
		return nil
	}

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var result struct {
		Routes []struct {
			Path       string    `json:"path"`
			FunctionID string    `json:"function_id"`
			Methods    string    `json:"methods"`
			CreatedAt  time.Time `json:"created_at"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("PATH", "FUNCTION_ID", "METHODS", "CREATED")
	for _, r := range result.Routes {
		methods := r.Methods
		if methods == "" {
			methods = "*"
		}
		t.row(r.Path, r.FunctionID, methods, r.CreatedAt.Format(time.DateTime))
	}
	t.flush()
	infof(cmd, "\nTotal: %d", len(result.Routes))
	return nil
}

func runRoutesSet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	path := args[0]
	fnNameOrID, _ := cmd.Flags().GetString("fn")
	methods, _ := cmd.Flags().GetString("methods")

	if managedServer(client) {
		fnID, err := managedOwnerUUID(fnNameOrID)
		if err != nil {
			return err
		}
		inspection, err := inspectManagedExposure(client, "route", path, fnID)
		if err != nil {
			return err
		}
		version, err := managedVersion(cmd, inspection)
		if err != nil {
			return err
		}
		body := managedCASBody(inspection)
		body["version"] = version
		body["path"] = path
		body["function_id"] = fnID
		if methods != "" {
			body["methods"] = methods
		}
		resp, err := client.Post("/api/v1/routes", body)
		if err != nil {
			return fmt.Errorf("set: %w", err)
		}
		if err := checkManagedResponse(resp); err != nil {
			return err
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if outputJSON(cmd) {
			return emitRaw(respBody)
		}
		okf(cmd, "Route %s → %s saved (version %s)", path, fnNameOrID, version)
		return nil
	}

	fnID, err := resolveFnID(client, fnNameOrID)
	if err != nil {
		return err
	}

	body := map[string]any{
		"path":        path,
		"function_id": fnID,
	}
	if methods != "" {
		body["methods"] = methods
	}

	resp, err := client.Post("/api/v1/routes", body)
	if err != nil {
		return fmt.Errorf("set: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(respBody)
	}
	okf(cmd, "Route %s → %s saved", path, fnNameOrID)
	return nil
}

func runRoutesDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	path := args[0]

	if err := confirm(cmd, fmt.Sprintf("Delete route %s?", path)); err != nil {
		return err
	}

	// REST shape note: server expects ?path=... query param (handlers/routes.go).
	q := url.Values{}
	q.Set("path", path)

	managed := managedServer(client)
	var body any
	if managed {
		inspection, err := inspectManagedExposure(client, "route", path, "")
		if err != nil {
			return err
		}
		version, err := managedVersion(cmd, inspection)
		if err != nil {
			return err
		}
		cas := managedCASBody(inspection)
		cas["version"] = version
		cas["confirm"] = true
		body = cas
	}

	resp, err := client.Do(http.MethodDelete, "/api/v1/routes?"+q.Encode(), body)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(respBody)
	}
	okf(cmd, "Route %s deleted", path)
	return nil
}

// ── Managed exposure support ────────────────────────────────────────
//
// A managed server accepts an exposure write only with the exact mapping
// version and per-owner authority tokens read from it. Counters travel as
// decimal strings; decoding them into a struct of strings keeps them that way,
// where map[string]any would turn them into float64 and corrupt large values.

type managedOwnerTokens struct {
	PolicyRevision string `json:"policy_revision"`
	AuthorityEpoch string `json:"authority_epoch"`
}

type managedInspection struct {
	Version          string                        `json:"version"`
	Expected         map[string]managedOwnerTokens `json:"expected"`
	Route            map[string]any                `json:"route"`
	RoutePublished   bool                          `json:"route_published"`
	InboundWebhook   map[string]any                `json:"inbound_webhook"`
	InboundPublished bool                          `json:"inbound_published"`
}

// managedServer reports whether the endpoint is a managed instance. A legacy
// server has no capabilities route, so anything but an explicit true is legacy.
func managedServer(c *client.Client) bool {
	resp, err := c.Get("/api/v1/iam/capabilities")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var out struct {
		Managed bool `json:"managed"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return false
	}
	return out.Managed
}

// managedOwnerUUID accepts only the exact lowercase RFC 4122 form the managed
// API stores and matches. A name is refused rather than resolved: managed
// selectors are never guessed.
func managedOwnerUUID(value string) (string, error) {
	if !canonicalManagedUUID(value) {
		return "", fmt.Errorf("managed mode needs the function's exact lowercase UUID, got %q — `orva functions list -o json` shows ids", value)
	}
	return value, nil
}

func canonicalManagedUUID(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	if s[14] < '1' || s[14] > '8' {
		return false
	}
	return strings.IndexByte("89ab", s[19]) >= 0
}

func isDecimalString(s string) bool {
	if s == "" || len(s) > 19 {
		return false
	}
	if s == "0" {
		return true
	}
	if s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func inspectManagedExposure(c *client.Client, kind, key, owner string) (*managedInspection, error) {
	q := url.Values{}
	q.Set("kind", kind)
	if key != "" {
		q.Set("key", key)
	}
	if owner != "" {
		q.Set("function_id", owner)
	}
	resp, err := c.Get("/api/v1/exposures?" + q.Encode())
	if err != nil {
		return nil, fmt.Errorf("inspect: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	var out managedInspection
	if err := decodeJSON(resp, &out); err != nil {
		return nil, fmt.Errorf("decode inspection: %w", err)
	}
	if !isDecimalString(out.Version) {
		return nil, fmt.Errorf("inspect: the server returned no usable version token; nothing was written")
	}
	if len(out.Expected) < 1 || len(out.Expected) > 2 {
		return nil, fmt.Errorf("inspect: the server returned no owner authority tokens; nothing was written")
	}
	for owner, tokens := range out.Expected {
		if !canonicalManagedUUID(owner) {
			return nil, fmt.Errorf("inspect: the server returned a non-canonical owner %q; nothing was written", owner)
		}
		if !isDecimalString(tokens.PolicyRevision) || tokens.PolicyRevision == "0" ||
			!isDecimalString(tokens.AuthorityEpoch) || tokens.AuthorityEpoch == "0" {
			return nil, fmt.Errorf("inspect: the server returned unusable authority tokens for %s; nothing was written", owner)
		}
	}
	return &out, nil
}

// managedVersion is the version token a write will send. --expected-version
// replaces the one just read, deliberately failing the CAS when it is stale.
func managedVersion(cmd *cobra.Command, inspection *managedInspection) (string, error) {
	if cmd.Flags().Changed("expected-version") {
		value, _ := cmd.Flags().GetString("expected-version")
		if !isDecimalString(value) {
			return "", fmt.Errorf("--expected-version must be a decimal string, got %q", value)
		}
		return value, nil
	}
	return inspection.Version, nil
}

func managedCASBody(inspection *managedInspection) map[string]any {
	expected := make(map[string]map[string]string, len(inspection.Expected))
	for owner, tokens := range inspection.Expected {
		expected[owner] = map[string]string{
			"policy_revision": tokens.PolicyRevision,
			"authority_epoch": tokens.AuthorityEpoch,
		}
	}
	return map[string]any{"version": inspection.Version, "expected": expected}
}

// checkManagedResponse adds the one instruction a 409 needs: re-read, then
// decide. checkResponse reports 409 as a plain error, so the status is tested
// here rather than through the error chain.
func checkManagedResponse(resp *http.Response) error {
	if resp.StatusCode != http.StatusConflict {
		return checkResponse(resp)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	message := string(body)
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		message = envelope.Error.Code + ": " + envelope.Error.Message
	}
	return fmt.Errorf("exposure changed since it was read (HTTP 409, %s)\nnothing was sent again: inspect the current mapping and run the command again", message)
}
