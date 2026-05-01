package main

import (
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Manage custom URL → function routes",
	Long:  "List, create, and delete user-defined route mappings.",
}

var routesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all custom routes",
	Run:   runRoutesList,
}

var routesSetCmd = &cobra.Command{
	Use:   "set [path]",
	Short: "Create or update a route mapping",
	Args:  cobra.ExactArgs(1),
	Run:   runRoutesSet,
}

var routesDeleteCmd = &cobra.Command{
	Use:   "delete [path]",
	Short: "Delete a route",
	Args:  cobra.ExactArgs(1),
	Run:   runRoutesDelete,
}

func init() {
	routesSetCmd.Flags().String("fn", "", "function name or ID (required)")
	routesSetCmd.Flags().String("methods", "", "HTTP methods (default '*'); e.g. 'GET,POST'")
	routesSetCmd.MarkFlagRequired("fn")

	routesCmd.AddCommand(routesListCmd)
	routesCmd.AddCommand(routesSetCmd)
	routesCmd.AddCommand(routesDeleteCmd)
	rootCmd.AddCommand(routesCmd)
}

func runRoutesList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	resp, err := client.Get("/api/v1/routes")
	if err != nil {
		exitError("list: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("list: %v", err)
	}

	var result struct {
		Routes []struct {
			Path       string    `json:"path"`
			FunctionID string    `json:"function_id"`
			Methods    string    `json:"methods"`
			CreatedAt  time.Time `json:"created_at"`
		} `json:"routes"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATH\tFUNCTION_ID\tMETHODS\tCREATED")
	for _, r := range result.Routes {
		methods := r.Methods
		if methods == "" {
			methods = "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			r.Path, r.FunctionID, methods,
			r.CreatedAt.Format(time.DateTime),
		)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", len(result.Routes))
}

func runRoutesSet(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	path := args[0]
	fnNameOrID, _ := cmd.Flags().GetString("fn")
	methods, _ := cmd.Flags().GetString("methods")

	fnID := resolveFunctionID(client, fnNameOrID)

	body := map[string]any{
		"path":        path,
		"function_id": fnID,
	}
	if methods != "" {
		body["methods"] = methods
	}

	resp, err := client.Post("/api/v1/routes", body)
	if err != nil {
		exitError("set: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("set: %v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	fmt.Printf("Route %s → %s saved\n", path, fnID)
}

func runRoutesDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	path := args[0]
	// REST shape note: server expects ?path=... query param (handlers/routes.go).
	q := url.Values{}
	q.Set("path", path)

	resp, err := client.Delete("/api/v1/routes?" + q.Encode())
	if err != nil {
		exitError("delete: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("delete: %v", err)
	}

	fmt.Printf("Route %s deleted\n", path)
}
