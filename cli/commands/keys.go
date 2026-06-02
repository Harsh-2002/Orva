package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	Long: `Create a new API key and print the secret once.

The plaintext key is shown only at creation time — store it immediately.
Permissions are comma-separated (invoke, read, write, admin); the default
grants all four. Use --expires-in-days to make the key auto-expire.

Examples:
  orva keys create --name ci
  orva keys create --name deploy-bot --permissions invoke,read
  orva keys create --name temp --expires-in-days 30
  orva keys create --name ci -o json | jq -r .key`,
	RunE: runKeysCreate,
}

var keysListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all API keys",
	Long: `List API keys (secrets are never shown — only id, name, and metadata).

Examples:
  orva keys list
  orva keys list -o json | jq '.keys[].name'`,
	RunE: runKeysList,
}

var keysRevokeCmd = &cobra.Command{
	Use:   "revoke [key-id]",
	Short: "Revoke an API key",
	Long: `Revoke an API key by id. Prompts for confirmation unless --yes is set.

A revoked key stops authenticating immediately.

Examples:
  orva keys revoke <id>
  orva keys revoke <id> --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runKeysRevoke,
}

func init() {
	keysCreateCmd.Flags().String("name", "", "key name (required)")
	keysCreateCmd.Flags().String("permissions", "", "comma-separated permissions (invoke,read,write,admin); default grants all")
	keysCreateCmd.Flags().Int("expires-in-days", 0, "auto-expire the key after N days (0 = never)")
	keysCreateCmd.MarkFlagRequired("name")

	keysCmd.AddCommand(keysCreateCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysRevokeCmd)
}

func runKeysCreate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	name, _ := cmd.Flags().GetString("name")
	permissions, _ := cmd.Flags().GetString("permissions")
	expiresInDays, _ := cmd.Flags().GetInt("expires-in-days")

	body := map[string]any{"name": name}
	if permissions != "" {
		var perms []string
		for _, p := range strings.Split(permissions, ",") {
			if p = strings.TrimSpace(p); p != "" {
				perms = append(perms, p)
			}
		}
		body["permissions"] = perms
	}
	if expiresInDays > 0 {
		body["expires_in_days"] = expiresInDays
	}

	resp, err := client.Post("/api/v1/keys", body)
	if err != nil {
		return fmt.Errorf("create: %w", err)
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
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	json.Unmarshal(raw, &result)
	okf(cmd, "Created API key %s", result.ID)
	if result.Key != "" {
		// The secret is data — send it to stdout so it can be piped/captured,
		// with the warning on stderr.
		infof(cmd, "Save this key now — it will not be shown again:")
		fmt.Println(result.Key)
	}
	return nil
}

func runKeysList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	resp, err := client.Get("/api/v1/keys")
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
		Keys []struct {
			ID          string     `json:"id"`
			Name        string     `json:"name"`
			Permissions []string   `json:"permissions"`
			CreatedAt   time.Time  `json:"created_at"`
			LastUsedAt  *time.Time `json:"last_used_at"`
			ExpiresAt   *time.Time `json:"expires_at"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("ID", "NAME", "PERMISSIONS", "CREATED", "LAST USED", "EXPIRES")
	for _, key := range result.Keys {
		lastUsed := "never"
		if key.LastUsedAt != nil {
			lastUsed = key.LastUsedAt.Format(time.DateTime)
		}
		expires := "never"
		if key.ExpiresAt != nil {
			expires = key.ExpiresAt.Format(time.DateTime)
		}
		t.row(key.ID, dash(key.Name), dash(strings.Join(key.Permissions, ",")),
			key.CreatedAt.Format(time.DateTime), lastUsed, expires)
	}
	t.flush()
	infof(cmd, "\nTotal: %d", len(result.Keys))
	return nil
}

func runKeysRevoke(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	keyID := args[0]
	if err := confirm(cmd, fmt.Sprintf("Revoke API key %s?", keyID)); err != nil {
		return err
	}

	resp, err := client.Delete("/api/v1/keys/" + keyID)
	if err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	resp.Body.Close()

	okf(cmd, "Revoked API key %s", keyID)
	return nil
}
