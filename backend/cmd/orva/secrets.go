package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage per-function encrypted secrets",
	Long:  "List, set, and delete encrypted secrets attached to a function.",
}

var secretsListCmd = &cobra.Command{
	Use:   "list [fn]",
	Short: "List secret keys for a function",
	Args:  cobra.ExactArgs(1),
	Run:   runSecretsList,
}

var secretsSetCmd = &cobra.Command{
	Use:   "set [fn] [key]",
	Short: "Set a secret value",
	Args:  cobra.ExactArgs(2),
	Run:   runSecretsSet,
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete [fn] [key]",
	Short: "Delete a secret",
	Args:  cobra.ExactArgs(2),
	Run:   runSecretsDelete,
}

func init() {
	secretsSetCmd.Flags().String("value", "", "secret value (use --value-file for file input)")
	secretsSetCmd.Flags().String("value-file", "", "path to file containing the secret value")

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
	rootCmd.AddCommand(secretsCmd)
}

func runSecretsList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnID := resolveFunctionID(client, args[0])

	resp, err := client.Get("/api/v1/functions/" + fnID + "/secrets")
	if err != nil {
		exitError("list: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("list: %v", err)
	}

	var result struct {
		Secrets []string `json:"secrets"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY")
	for _, k := range result.Secrets {
		fmt.Fprintf(w, "%s\n", k)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", len(result.Secrets))
}

func runSecretsSet(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnID := resolveFunctionID(client, args[0])
	key := args[1]

	value, _ := cmd.Flags().GetString("value")
	valueFile, _ := cmd.Flags().GetString("value-file")

	if value == "" && valueFile == "" {
		exitError("set: --value or --value-file is required")
	}
	if value != "" && valueFile != "" {
		exitError("set: --value and --value-file are mutually exclusive")
	}
	if valueFile != "" {
		data, err := os.ReadFile(valueFile)
		if err != nil {
			exitError("set: read --value-file: %v", err)
		}
		// Trim trailing newline so editors that auto-add one don't poison the value.
		value = strings.TrimRight(string(data), "\r\n")
	}

	// REST shape note: the server exposes Upsert at POST /api/v1/functions/{id}/secrets
	// with body {"key": "...", "value": "..."} — there is no PUT-by-key route.
	body := map[string]any{
		"key":   key,
		"value": value,
	}
	resp, err := client.Post("/api/v1/functions/"+fnID+"/secrets", body)
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
	fmt.Printf("Secret %q saved\n", key)
}

func runSecretsDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnID := resolveFunctionID(client, args[0])
	key := args[1]

	resp, err := client.Delete("/api/v1/functions/" + fnID + "/secrets/" + key)
	if err != nil {
		exitError("delete: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("delete: %v", err)
	}

	fmt.Printf("Secret %q deleted\n", key)
}
