package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage API keys",
	Long:  "Create, list, and revoke API keys.",
}

var keysCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new API key",
	Run:   runKeysCreate,
}

var keysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all API keys",
	Run:   runKeysList,
}

var keysRevokeCmd = &cobra.Command{
	Use:   "revoke [key-id]",
	Short: "Revoke an API key",
	Args:  cobra.ExactArgs(1),
	Run:   runKeysRevoke,
}

func init() {
	keysCreateCmd.Flags().String("name", "", "key name (required)")
	keysCreateCmd.Flags().String("permissions", "invoke", "comma-separated permissions (invoke,read,write,admin)")
	keysCreateCmd.MarkFlagRequired("name")

	keysCmd.AddCommand(keysCreateCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysRevokeCmd)
	rootCmd.AddCommand(keysCmd)
}

func runKeysCreate(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	name, _ := cmd.Flags().GetString("name")
	permissions, _ := cmd.Flags().GetString("permissions")

	body := map[string]string{
		"name":        name,
		"permissions": permissions,
	}

	resp, err := client.Post("/api/v1/keys", body)
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

	if key, ok := result["api_key"].(string); ok {
		fmt.Printf("\nSave this key - it will not be shown again: %s\n", key)
	}
}

func runKeysList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	resp, err := client.Get("/api/v1/keys")
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	var result struct {
		Keys []struct {
			ID          string     `json:"id"`
			Name        string     `json:"name"`
			Permissions string     `json:"permissions"`
			CreatedAt   time.Time  `json:"created_at"`
			LastUsedAt  *time.Time `json:"last_used_at"`
		} `json:"keys"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tPERMISSIONS\tCREATED\tLAST USED")
	for _, key := range result.Keys {
		lastUsed := "never"
		if key.LastUsedAt != nil {
			lastUsed = key.LastUsedAt.Format(time.DateTime)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			key.ID, key.Name, key.Permissions,
			key.CreatedAt.Format(time.DateTime), lastUsed,
		)
	}
	w.Flush()
}

func runKeysRevoke(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	keyID := args[0]
	resp, err := client.Delete("/api/v1/keys/" + keyID)
	if err != nil {
		exitError("request failed: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("%v", err)
	}

	fmt.Printf("Key %s revoked\n", keyID)
}
