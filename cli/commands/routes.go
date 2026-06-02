package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Manage custom URL → function routes",
	Long: `List, create, and delete user-defined route mappings that give functions
pretty URLs (e.g. /webhooks/stripe) instead of /fn/<id>.

A route maps a path to a function and an optional set of HTTP methods.
Prefix routes end in /* (e.g. /shortener/*).

Examples:
  orva routes list
  orva routes set /webhooks/stripe --fn payments
  orva routes set /api-proxy/* --fn proxy --methods GET,POST
  orva routes delete /webhooks/stripe`,
}

var routesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all custom routes",
	Long: `List every user-defined route mapping.

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

Examples:
  orva routes set /webhooks/stripe --fn payments
  orva routes set /api-proxy/* --fn proxy --methods GET,POST`,
	Args: cobra.ExactArgs(1),
	RunE: runRoutesSet,
}

var routesDeleteCmd = &cobra.Command{
	Use:   "delete <path>",
	Short: "Delete a route",
	Long: `Delete a route by its path. Destructive; prompts for confirmation unless
--yes is passed.

Examples:
  orva routes delete /webhooks/stripe
  orva routes delete /webhooks/stripe --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runRoutesDelete,
}

func init() {
	routesSetCmd.Flags().String("fn", "", "function name or ID (required)")
	routesSetCmd.Flags().String("methods", "", "HTTP methods (default '*'); e.g. 'GET,POST'")
	routesSetCmd.MarkFlagRequired("fn")

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

	resp, err := client.Delete("/api/v1/routes?" + q.Encode())
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
