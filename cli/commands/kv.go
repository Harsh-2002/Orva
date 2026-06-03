package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

var kvCmd = &cobra.Command{
	Use:   "kv",
	Short: "Manage per-function key/value state",
	Long: `List, get, put, and delete entries in a function's KV store.

Each function has its own namespaced KV store. Values are JSON and may
carry an optional TTL. Values are capped at 64 KB each.

Examples:
  orva kv list greeter
  orva kv put greeter visits --value 0
  orva kv get greeter visits
  orva kv delete greeter visits

Note: atomic counters (incr) and compare-and-swap (cas) are only exposed
on the internal SDK path, which requires a per-process internal token the
CLI does not hold — use them from inside a function via the runtime SDK.`,
}

var kvListCmd = &cobra.Command{
	Use:   "list <fn>",
	Short: "List KV entries for a function",
	Long: `List a function's KV entries, optionally filtered by key prefix.

Examples:
  orva kv list greeter
  orva kv list greeter --prefix session: --limit 50
  orva kv list greeter -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runKVList,
}

var kvGetCmd = &cobra.Command{
	Use:   "get <fn> <key>",
	Short: "Get a KV entry",
	Long: `Get a single KV entry. In table mode the stored JSON value is printed to
stdout; with -o json the full entry object (value, ttl, timestamps) is
emitted.

Examples:
  orva kv get greeter visits
  orva kv get greeter visits -o json`,
	Args: cobra.ExactArgs(2),
	RunE: runKVGet,
}

var kvPutCmd = &cobra.Command{
	Use:   "put <fn> <key>",
	Short: "Put (create or update) a KV entry",
	Long: `Store a JSON value under a key. The value comes from --value as inline
JSON, @file, or @- (stdin). Optionally expire the key with --ttl seconds.

Examples:
  orva kv put greeter visits --value 0
  orva kv put greeter config --value '{"theme":"dark"}'
  orva kv put greeter blob --value @payload.json
  echo '{"x":1}' | orva kv put greeter blob --value @-
  orva kv put greeter session --value '"abc"' --ttl 3600`,
	Args: cobra.ExactArgs(2),
	RunE: runKVPut,
}

var kvDeleteCmd = &cobra.Command{
	Use:   "delete <fn> <key>",
	Short: "Delete a KV entry",
	Long: `Delete a single KV entry. Destructive; prompts for confirmation unless
--yes is passed. Idempotent on the server — deleting a missing key succeeds.

Examples:
  orva kv delete greeter visits
  orva kv delete greeter visits --yes`,
	Args: cobra.ExactArgs(2),
	RunE: runKVDelete,
}

func init() {
	kvListCmd.Flags().String("prefix", "", "filter entries by key prefix")
	kvListCmd.Flags().Int("limit", 200, "maximum number of entries to return (max 1000)")

	kvPutCmd.Flags().String("value", "", "JSON value to store: inline, @file, or @- for stdin (required)")
	kvPutCmd.Flags().Int("ttl", 0, "TTL in seconds (0 = no expiry)")
	kvPutCmd.MarkFlagRequired("value")

	kvCmd.AddCommand(kvListCmd)
	kvCmd.AddCommand(kvGetCmd)
	kvCmd.AddCommand(kvPutCmd)
	kvCmd.AddCommand(kvDeleteCmd)
}

func runKVList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	prefix, _ := cmd.Flags().GetString("prefix")
	limit, _ := cmd.Flags().GetInt("limit")

	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/api/v1/functions/" + fnID + "/kv"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	resp, err := client.Get(path)
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
		Entries []struct {
			Key       string  `json:"key"`
			ExpiresAt *string `json:"expires_at"`
			UpdatedAt string  `json:"updated_at"`
			SizeBytes int     `json:"size_bytes"`
		} `json:"entries"`
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("KEY", "SIZE", "EXPIRES", "UPDATED")
	for _, e := range result.Entries {
		expires := "-"
		if e.ExpiresAt != nil {
			expires = *e.ExpiresAt
		}
		updated := e.UpdatedAt
		if ts, err := time.Parse(time.RFC3339, e.UpdatedAt); err == nil {
			updated = ts.Format(time.DateTime)
		}
		t.row(e.Key, e.SizeBytes, expires, updated)
	}
	t.flush()

	if result.Truncated {
		infof(cmd, "\nTotal: %d (truncated; narrow --prefix or raise --limit)", result.Total)
	} else {
		infof(cmd, "\nTotal: %d", result.Total)
	}
	return nil
}

func runKVGet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	key := args[1]

	resp, err := client.Get("/api/v1/functions/" + fnID + "/kv/" + url.PathEscape(key))
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	// Table/human mode: print the stored JSON value to stdout so it pipes
	// cleanly into jq. The value field is raw JSON as the server stored it.
	var result struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return emitRaw(result.Value)
}

func runKVPut(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	key := args[1]

	valueArg, _ := cmd.Flags().GetString("value")
	ttl, _ := cmd.Flags().GetInt("ttl")

	data, err := readBodyArg(valueArg)
	if err != nil {
		return fmt.Errorf("put: read --value: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("put: --value must be valid JSON: %w", err)
	}

	body := map[string]any{
		"value": value,
	}
	if ttl > 0 {
		body["ttl_seconds"] = ttl
	}

	resp, err := client.Put("/api/v1/functions/"+fnID+"/kv/"+url.PathEscape(key), body)
	if err != nil {
		return fmt.Errorf("put: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(respBody)
	}
	if ttl > 0 {
		okf(cmd, "KV entry %q saved (ttl %ds)", key, ttl)
	} else {
		okf(cmd, "KV entry %q saved", key)
	}
	return nil
}

func runKVDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	key := args[1]

	if err := confirm(cmd, fmt.Sprintf("Delete KV entry %q from %s?", key, args[0])); err != nil {
		return err
	}

	resp, err := client.Delete("/api/v1/functions/" + fnID + "/kv/" + url.PathEscape(key))
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
	okf(cmd, "KV entry %q deleted", key)
	return nil
}
