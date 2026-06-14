package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

var channelsCmd = &cobra.Command{
	Use:     "channels",
	Aliases: []string{"channel"},
	Short:   "Manage agent channels (function bundles exposed as MCP tools)",
	Long: `Agent channels group N deployed functions under a name and a static
bearer token. Presenting that token at /mcp exposes ONLY those functions
as MCP tools (invoke-only). Use this to ship a curated MCP toolbox to
an agentic workflow without giving it Orva management.

  orva channels create my-bot --functions greeter,echo
  orva channels list -o json | jq '.channels[].name'`,
}

var channelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent channels",
	Long: `List agent channels (function bundles exposed at /mcp under a static token).

  orva channels list
  orva channels list -o json | jq '.[].name'`,
	RunE: runChannelsList,
}

var channelsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new channel",
	Long: `Create a channel bundling one or more functions under a static bearer token.
The token plaintext is printed once and never shown again — store it.

  orva channels create prod --functions greeter,echo
  orva channels create ci --functions deploy-hook --expires-in-days 90`,
	Args: cobra.ExactArgs(1),
	RunE: runChannelsCreate,
}

var channelsShowCmd = &cobra.Command{
	Use:   "show <id|name>",
	Short: "Show a channel + its function set",
	Long: `Show a channel's metadata and the functions it exposes.

  orva channels show prod`,
	Args: cobra.ExactArgs(1),
	RunE: runChannelsShow,
}

var channelsAddFunctionsCmd = &cobra.Command{
	Use:   "add-functions <id|name> <fn>...",
	Short: "Add functions to a channel",
	Long: `Add one or more functions (by id or name) to an existing channel.

  orva channels add-functions prod echo summarize`,
	Args: cobra.MinimumNArgs(2),
	RunE: runChannelsAddFunctions,
}

var channelsRemoveFunctionsCmd = &cobra.Command{
	Use:   "remove-functions <id|name> <fn>...",
	Short: "Remove functions from a channel",
	Long: `Remove one or more functions (by id or name) from a channel.

  orva channels remove-functions prod echo`,
	Args: cobra.MinimumNArgs(2),
	RunE: runChannelsRemoveFunctions,
}

var channelsRotateCmd = &cobra.Command{
	Use:   "rotate <id|name>",
	Short: "Rotate the channel's token (invalidates the old one)",
	Long: `Issue a fresh token for the channel and invalidate the old one. The new
token plaintext is printed once.

  orva channels rotate prod`,
	Args: cobra.ExactArgs(1),
	RunE: runChannelsRotate,
}

var channelsDeleteCmd = &cobra.Command{
	Use:     "delete <id|name>",
	Aliases: []string{"rm"},
	Short:   "Delete a channel",
	Long: `Delete a channel and invalidate its token. Prompts for confirmation unless
--yes is passed.

  orva channels delete prod
  orva channels delete prod --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runChannelsDelete,
}

func init() {
	channelsCreateCmd.Flags().StringSlice("functions", nil, "comma-separated function ids/names (required)")
	channelsCreateCmd.Flags().String("description", "", "human-readable description")
	channelsCreateCmd.Flags().Int("expires-in-days", 0, "token expiry in days (0 = no expiry)")
	channelsCreateCmd.MarkFlagRequired("functions")

	channelsCmd.AddCommand(channelsListCmd)
	channelsCmd.AddCommand(channelsCreateCmd)
	channelsCmd.AddCommand(channelsShowCmd)
	channelsCmd.AddCommand(channelsAddFunctionsCmd)
	channelsCmd.AddCommand(channelsRemoveFunctionsCmd)
	channelsCmd.AddCommand(channelsRotateCmd)
	channelsCmd.AddCommand(channelsDeleteCmd)
}

func runChannelsList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/channels")
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if outputJSON(cmd) {
		return emitRaw(raw)
	}
	var out struct {
		Channels []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Prefix        string `json:"prefix"`
			FunctionCount int    `json:"function_count"`
			ExpiresAt     string `json:"expires_at"`
			LastUsedAt    string `json:"last_used_at"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	t := newTable("ID", "NAME", "FUNCTIONS", "PREFIX", "LAST USED", "EXPIRES")
	for _, c := range out.Channels {
		last := c.LastUsedAt
		if last == "" {
			last = "never"
		}
		exp := c.ExpiresAt
		if exp == "" {
			exp = "never"
		}
		t.row(c.ID, c.Name, c.FunctionCount, c.Prefix, last, exp)
	}
	t.flush()
	return nil
}

func runChannelsCreate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	name := args[0]
	fns, _ := cmd.Flags().GetStringSlice("functions")
	desc, _ := cmd.Flags().GetString("description")
	days, _ := cmd.Flags().GetInt("expires-in-days")

	fnIDs := make([]string, 0, len(fns))
	for _, f := range fns {
		id, err := resolveFnID(client, strings.TrimSpace(f))
		if err != nil {
			return err
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
	resp, err := client.Post("/api/v1/channels", body)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	return emitChannelWithToken(cmd, raw)
}

func runChannelsShow(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	id, err := resolveChannelID(client, args[0])
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/channels/" + id)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	return emitRaw(raw)
}

func runChannelsAddFunctions(cmd *cobra.Command, args []string) error {
	return mutateChannelFunctions(cmd, args, true)
}

func runChannelsRemoveFunctions(cmd *cobra.Command, args []string) error {
	return mutateChannelFunctions(cmd, args, false)
}

// mutateChannelFunctions does GET then PUT to add/remove from the function set.
// The REST API only supports replace-set; we read the current list, compute the
// new list, and PUT it back.
func mutateChannelFunctions(cmd *cobra.Command, args []string, add bool) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	id, err := resolveChannelID(client, args[0])
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/channels/" + id)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	var current struct {
		FunctionIDs []string `json:"function_ids"`
	}
	if err := decodeJSON(resp, &current); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	have := make(map[string]bool, len(current.FunctionIDs))
	for _, fnID := range current.FunctionIDs {
		have[fnID] = true
	}
	for _, f := range args[1:] {
		fnID, err := resolveFnID(client, strings.TrimSpace(f))
		if err != nil {
			return err
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
	resp, err = client.Put("/api/v1/channels/"+id+"/functions", body)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	if outputJSON(cmd) {
		return emitJSON(map[string]any{"id": id, "function_ids": newIDs})
	}
	okf(cmd, "Channel %s now has %d function(s).", args[0], len(newIDs))
	return nil
}

func runChannelsRotate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	id, err := resolveChannelID(client, args[0])
	if err != nil {
		return err
	}
	if err := confirm(cmd, fmt.Sprintf("Rotate the token for channel %q? The current token stops working immediately.", args[0])); err != nil {
		return err
	}
	resp, err := client.Post("/api/v1/channels/"+id+"/rotate", nil)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	return emitChannelWithToken(cmd, raw)
}

func runChannelsDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	id, err := resolveChannelID(client, args[0])
	if err != nil {
		return err
	}
	if err := confirm(cmd, fmt.Sprintf("Delete channel %q?", args[0])); err != nil {
		return err
	}
	resp, err := client.Delete("/api/v1/channels/" + id)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitJSON(map[string]any{"deleted": true, "id": id, "name": args[0]})
	}
	okf(cmd, "Channel %s deleted.", args[0])
	return nil
}

// emitChannelWithToken prints the channel record (machine-readable on stdout)
// and, in human mode, a one-time token reminder on stderr.
func emitChannelWithToken(cmd *cobra.Command, raw []byte) error {
	if err := emitRaw(raw); err != nil {
		return err
	}
	if !outputJSON(cmd) {
		var r struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(raw, &r) == nil && r.Token != "" {
			infof(cmd, "\nSave this token — it will not be shown again.")
		}
	}
	return nil
}

// resolveChannelID lists channels and returns the id matching the supplied
// UUID OR name, with a clear error when none matches.
func resolveChannelID(client *cli.Client, idOrName string) (string, error) {
	resp, err := client.Get("/api/v1/channels")
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return "", err
	}
	var out struct {
		Channels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"channels"`
	}
	if err := decodeJSON(resp, &out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	for _, c := range out.Channels {
		if c.ID == idOrName || c.Name == idOrName {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no channel named %q (run `orva channels list`)", idOrName)
}
