package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage per-function encrypted secrets",
	Long: `List, set, and delete encrypted secrets attached to a function.

Secrets are stored AES-256-GCM encrypted and injected into the function's
environment at invoke time. Values are write-only: once set, the API only
ever returns key names, never the plaintext.

Examples:
  orva secrets list greeter
  orva secrets set greeter STRIPE_KEY --value sk_live_123
  orva secrets set greeter STRIPE_KEY --value @key.txt
  orva secrets delete greeter STRIPE_KEY`,
}

var secretsListCmd = &cobra.Command{
	Use:   "list <fn>",
	Short: "List secret key names for a function",
	Long: `List the secret key names attached to a function. Values are never returned.

Examples:
  orva secrets list greeter
  orva secrets list greeter -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretsList,
}

var secretsSetCmd = &cobra.Command{
	Use:   "set <fn> <key>",
	Short: "Set (create or update) a secret value",
	Long: `Set a secret value on a function. Provide the value with --value (inline,
or @file / @- to read from a file or stdin) or with --value-file. The two
flags are mutually exclusive and exactly one is required.

A trailing newline is trimmed so editors that auto-append one don't poison
the value.

Examples:
  orva secrets set greeter STRIPE_KEY --value sk_live_123
  orva secrets set greeter STRIPE_KEY --value @key.txt
  printf 'sk_live_123' | orva secrets set greeter STRIPE_KEY --value @-
  orva secrets set greeter STRIPE_KEY --value-file ./key.txt`,
	Args: cobra.ExactArgs(2),
	RunE: runSecretsSet,
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete <fn> <key>",
	Short: "Delete a secret",
	Long: `Delete a secret from a function. This is destructive and prompts for
confirmation unless --yes is passed.

Examples:
  orva secrets delete greeter STRIPE_KEY
  orva secrets delete greeter STRIPE_KEY --yes`,
	Args: cobra.ExactArgs(2),
	RunE: runSecretsDelete,
}

func init() {
	secretsSetCmd.Flags().String("value", "", "secret value: inline string, @file, or @- for stdin")
	secretsSetCmd.Flags().String("value-file", "", "path to a file containing the secret value")

	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
}

func runSecretsList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/functions/" + fnID + "/secrets")
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
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("KEY")
	for _, k := range result.Secrets {
		t.row(k)
	}
	t.flush()
	infof(cmd, "\nTotal: %d", len(result.Secrets))
	return nil
}

func runSecretsSet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	key := args[1]

	value, _ := cmd.Flags().GetString("value")
	valueFile, _ := cmd.Flags().GetString("value-file")

	if value == "" && valueFile == "" {
		return fmt.Errorf("set: provide a value with --value or --value-file")
	}
	if value != "" && valueFile != "" {
		return fmt.Errorf("set: --value and --value-file are mutually exclusive")
	}

	// --value accepts inline text, @file, or @- (stdin); --value-file is a
	// plain path. Both routes trim a trailing newline.
	var raw []byte
	if valueFile != "" {
		raw, err = readBodyArg("@" + valueFile)
		if err != nil {
			return fmt.Errorf("set: read --value-file: %w", err)
		}
	} else {
		raw, err = readBodyArg(value)
		if err != nil {
			return fmt.Errorf("set: read --value: %w", err)
		}
	}
	secretValue := strings.TrimRight(string(raw), "\r\n")

	// REST shape note: the server exposes Upsert at POST /api/v1/functions/{id}/secrets
	// with body {"key": "...", "value": "..."} — there is no PUT-by-key route.
	body := map[string]any{
		"key":   key,
		"value": secretValue,
	}
	resp, err := client.Post("/api/v1/functions/"+fnID+"/secrets", body)
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
	okf(cmd, "Secret %q saved on %s", key, args[0])
	return nil
}

func runSecretsDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	key := args[1]

	if err := confirm(cmd, fmt.Sprintf("Delete secret %q from %s?", key, args[0])); err != nil {
		return err
	}

	resp, err := client.Delete("/api/v1/functions/" + fnID + "/secrets/" + key)
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
	okf(cmd, "Secret %q deleted", key)
	return nil
}
