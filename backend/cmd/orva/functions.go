package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Harsh-2002/Orva/internal/cli"
	"github.com/Harsh-2002/Orva/internal/ids"
	"github.com/spf13/cobra"
)

var functionsCmd = &cobra.Command{
	Use:   "functions",
	Short: "Manage functions",
	Long:  "Create, list, get, and delete serverless functions.",
}

var functionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all functions",
	Run:   runFunctionsList,
}

var functionsCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new function",
	Run:   runFunctionsCreate,
}

var functionsGetCmd = &cobra.Command{
	Use:   "get [name-or-id]",
	Short: "Get function details",
	Args:  cobra.ExactArgs(1),
	Run:   runFunctionsGet,
}

var functionsDeleteCmd = &cobra.Command{
	Use:   "delete [name-or-id]",
	Short: "Delete a function",
	Args:  cobra.ExactArgs(1),
	Run:   runFunctionsDelete,
}

func init() {
	functionsCreateCmd.Flags().String("name", "", "function name (required)")
	functionsCreateCmd.Flags().String("runtime", "", "runtime (node24, node22, python314, python313) (required)")
	functionsCreateCmd.MarkFlagRequired("name")
	functionsCreateCmd.MarkFlagRequired("runtime")

	functionsCmd.AddCommand(functionsListCmd)
	functionsCmd.AddCommand(functionsCreateCmd)
	functionsCmd.AddCommand(functionsGetCmd)
	functionsCmd.AddCommand(functionsDeleteCmd)
	rootCmd.AddCommand(functionsCmd)
}

func runFunctionsList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	resp, err := client.Get("/api/v1/functions")
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var result struct {
		Functions []struct {
			ID        string    `json:"id"`
			Name      string    `json:"name"`
			Runtime   string    `json:"runtime"`
			Status    string    `json:"status"`
			Version   int       `json:"version"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"functions"`
		Total int `json:"total"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tRUNTIME\tSTATUS\tVERSION\tCREATED")
	for _, fn := range result.Functions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			fn.ID, fn.Name, fn.Runtime, fn.Status, fn.Version,
			fn.CreatedAt.Format(time.DateTime),
		)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", result.Total)
}

func runFunctionsCreate(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	name, _ := cmd.Flags().GetString("name")
	runtime, _ := cmd.Flags().GetString("runtime")

	body := map[string]string{
		"name":    name,
		"runtime": runtime,
	}

	resp, err := client.Post("/api/v1/functions", body)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var fn struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Runtime string `json:"runtime"`
		Status  string `json:"status"`
	}
	if err := decodeJSON(resp, &fn); err != nil {
		exitError("decode response: %v", err)
	}

	fmt.Printf("Function created:\n")
	fmt.Printf("  ID:      %s\n", fn.ID)
	fmt.Printf("  Name:    %s\n", fn.Name)
	fmt.Printf("  Runtime: %s\n", fn.Runtime)
	fmt.Printf("  Status:  %s\n", fn.Status)
}

func runFunctionsGet(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	nameOrID := args[0]
	fnID := resolveFunctionID(client, nameOrID)

	resp, err := client.Get("/api/v1/functions/" + fnID)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var fn map[string]any
	if err := decodeJSON(resp, &fn); err != nil {
		exitError("decode response: %v", err)
	}

	data, _ := json.MarshalIndent(fn, "", "  ")
	fmt.Println(string(data))
}

func runFunctionsDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	nameOrID := args[0]
	fnID := resolveFunctionID(client, nameOrID)

	resp, err := client.Delete("/api/v1/functions/" + fnID)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	fmt.Printf("Function %s deleted\n", fnID)
}

// resolveFunctionID resolves a name-or-id to a function ID.
// If the input parses as a UUID, it is treated as an ID directly.
// Otherwise, it lists functions and finds one matching by name.
func resolveFunctionID(client *cli.Client, nameOrID string) string {
	if ids.IsUUID(nameOrID) {
		return nameOrID
	}

	// Try to find by name via listing.
	resp, err := client.Get("/api/v1/functions")
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var result struct {
		Functions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"functions"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	for _, fn := range result.Functions {
		if fn.Name == nameOrID {
			return fn.ID
		}
	}

	// If not found by name, use as-is (server will return 404 if invalid).
	return nameOrID
}
