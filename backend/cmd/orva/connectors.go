package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Harsh-2002/Orva/internal/cli"
	"github.com/spf13/cobra"
)

var connectorsCmd = &cobra.Command{
	Use:     "connectors",
	Aliases: []string{"connector"},
	Short:   "Manage agent connectors (function bundles exposed as MCP tools)",
	Long: `Agent connectors group N deployed functions under a name and a static
bearer token. Presenting that token at /mcp exposes ONLY those functions
as MCP tools (invoke-only). Use this to ship a curated MCP toolbox to
an agentic workflow without giving it Orva management.`,
}

var connectorsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent connectors",
	Run:   runConnectorsList,
}

var connectorsCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new connector",
	Long:  "Create a connector. The token plaintext is printed once and never shown again.",
	Args:  cobra.ExactArgs(1),
	Run:   runConnectorsCreate,
}

var connectorsShowCmd = &cobra.Command{
	Use:   "show [id|name]",
	Short: "Show a connector + its function set",
	Args:  cobra.ExactArgs(1),
	Run:   runConnectorsShow,
}

var connectorsAddFunctionsCmd = &cobra.Command{
	Use:   "add-functions [id|name] [fn1] [fn2] ...",
	Short: "Add functions to a connector",
	Args:  cobra.MinimumNArgs(2),
	Run:   runConnectorsAddFunctions,
}

var connectorsRemoveFunctionsCmd = &cobra.Command{
	Use:   "remove-functions [id|name] [fn1] [fn2] ...",
	Short: "Remove functions from a connector",
	Args:  cobra.MinimumNArgs(2),
	Run:   runConnectorsRemoveFunctions,
}

var connectorsRotateCmd = &cobra.Command{
	Use:   "rotate [id|name]",
	Short: "Rotate the connector's token (invalidates the old one)",
	Args:  cobra.ExactArgs(1),
	Run:   runConnectorsRotate,
}

var connectorsDeleteCmd = &cobra.Command{
	Use:     "delete [id|name]",
	Aliases: []string{"rm"},
	Short:   "Delete a connector",
	Args:    cobra.ExactArgs(1),
	Run:     runConnectorsDelete,
}

func init() {
	connectorsCreateCmd.Flags().StringSlice("functions", nil, "comma-separated function ids/names (required)")
	connectorsCreateCmd.Flags().String("description", "", "human-readable description")
	connectorsCreateCmd.Flags().Int("expires-in-days", 0, "token expiry in days (0 = no expiry)")
	connectorsCreateCmd.MarkFlagRequired("functions")

	connectorsCmd.AddCommand(connectorsListCmd)
	connectorsCmd.AddCommand(connectorsCreateCmd)
	connectorsCmd.AddCommand(connectorsShowCmd)
	connectorsCmd.AddCommand(connectorsAddFunctionsCmd)
	connectorsCmd.AddCommand(connectorsRemoveFunctionsCmd)
	connectorsCmd.AddCommand(connectorsRotateCmd)
	connectorsCmd.AddCommand(connectorsDeleteCmd)
	rootCmd.AddCommand(connectorsCmd)
}

func runConnectorsList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	resp, err := client.Get("/api/v1/connectors")
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}
	var out struct {
		Connectors []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			Prefix        string `json:"prefix"`
			FunctionCount int    `json:"function_count"`
			ExpiresAt     string `json:"expires_at"`
			LastUsedAt    string `json:"last_used_at"`
		} `json:"connectors"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		exitError("decode response: %v", err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tFUNCTIONS\tPREFIX\tLAST USED\tEXPIRES")
	for _, c := range out.Connectors {
		last := c.LastUsedAt
		if last == "" {
			last = "never"
		}
		exp := c.ExpiresAt
		if exp == "" {
			exp = "never"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", c.ID, c.Name, c.FunctionCount, c.Prefix, last, exp)
	}
	tw.Flush()
}

func runConnectorsCreate(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	name := args[0]
	fns, _ := cmd.Flags().GetStringSlice("functions")
	desc, _ := cmd.Flags().GetString("description")
	days, _ := cmd.Flags().GetInt("expires-in-days")

	// Resolve names → ids via /api/v1/functions/<nameOrId>.
	fnIDs := make([]string, 0, len(fns))
	for _, f := range fns {
		id := resolveFunctionID(client, strings.TrimSpace(f))
		if id == "" {
			exitError("function not found: %s", f)
		}
		fnIDs = append(fnIDs, id)
	}

	body := map[string]any{
		"name":         name,
		"description":  desc,
		"function_ids": fnIDs,
	}
	if days > 0 {
		body["expires_in_days"] = days
	}
	resp, err := client.Post("/api/v1/connectors", body)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}
	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	if tok, ok := result["token"].(string); ok {
		fmt.Printf("\nSave this token — it will not be shown again:\n  %s\n", tok)
	}
}

func runConnectorsShow(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	id := resolveConnectorIDByName(client, args[0])
	if id == "" {
		exitError("connector not found: %s", args[0])
	}
	resp, err := client.Get("/api/v1/connectors/" + id)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}
	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

func runConnectorsAddFunctions(cmd *cobra.Command, args []string) {
	mutateConnectorFunctions(cmd, args, true)
}

func runConnectorsRemoveFunctions(cmd *cobra.Command, args []string) {
	mutateConnectorFunctions(cmd, args, false)
}

// mutateConnectorFunctions does GET then PUT to add/remove from the
// function set. The REST API only supports replace-set; we read the
// current list, compute the new list, and PUT it back.
func mutateConnectorFunctions(cmd *cobra.Command, args []string, add bool) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	id := resolveConnectorIDByName(client, args[0])
	if id == "" {
		exitError("connector not found: %s", args[0])
	}
	// Read current set.
	resp, err := client.Get("/api/v1/connectors/" + id)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}
	var current struct {
		FunctionIDs []string `json:"function_ids"`
	}
	if err := decodeJSON(resp, &current); err != nil {
		exitError("decode response: %v", err)
	}
	have := make(map[string]bool, len(current.FunctionIDs))
	for _, fnID := range current.FunctionIDs {
		have[fnID] = true
	}
	for _, f := range args[1:] {
		fnID := resolveFunctionID(client, strings.TrimSpace(f))
		if fnID == "" {
			exitError("function not found: %s", f)
		}
		if add {
			have[fnID] = true
		} else {
			delete(have, fnID)
		}
	}
	newIDs := make([]string, 0, len(have))
	for fnID := range have {
		newIDs = append(newIDs, fnID)
	}
	body := map[string]any{"function_ids": newIDs}
	resp, err = client.Put("/api/v1/connectors/"+id+"/functions", body)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}
	fmt.Printf("Connector %s now has %d function(s).\n", args[0], len(newIDs))
}

func runConnectorsRotate(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	id := resolveConnectorIDByName(client, args[0])
	if id == "" {
		exitError("connector not found: %s", args[0])
	}
	resp, err := client.Post("/api/v1/connectors/"+id+"/rotate", nil)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}
	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
	if tok, ok := result["token"].(string); ok {
		fmt.Printf("\nSave this token — it will not be shown again:\n  %s\n", tok)
	}
}

func runConnectorsDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	id := resolveConnectorIDByName(client, args[0])
	if id == "" {
		exitError("connector not found: %s", args[0])
	}
	resp, err := client.Delete("/api/v1/connectors/" + id)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}
	fmt.Printf("Connector %s deleted.\n", args[0])
}

// resolveConnectorIDByName lists connectors and returns the id matching
// the supplied UUID OR name. Behaves like resolveFunctionID's pattern.
func resolveConnectorIDByName(client *cli.Client, idOrName string) string {
	resp, err := client.Get("/api/v1/connectors")
	if err != nil {
		return ""
	}
	if err := checkResponse(resp); err != nil {
		return ""
	}
	var out struct {
		Connectors []struct {
			ID, Name string
		} `json:"connectors"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		return ""
	}
	for _, c := range out.Connectors {
		if c.ID == idOrName || c.Name == idOrName {
			return c.ID
		}
	}
	return ""
}
