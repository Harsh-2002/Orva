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
  orva kv incr greeter visits --by 1
  orva kv cas greeter lock --expected null --new '"held"'
  orva kv delete greeter visits`,
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

var kvIncrCmd = &cobra.Command{
	Use:   "incr <fn> <key>",
	Short: "Atomically increment an integer KV entry",
	Long: `Atomically add a delta (default 1) to an integer-valued key and print the
new value. Useful for counters and rate limiters from CI or an agent. The
key is created at the delta if it does not yet exist. Fails if the stored
value is not an integer.

Examples:
  orva kv incr greeter visits
  orva kv incr greeter visits --by 5
  orva kv incr greeter remaining --by -1
  orva kv incr greeter window --by 1 --ttl 60`,
	Args: cobra.ExactArgs(2),
	RunE: runKVIncr,
}

var kvCASCmd = &cobra.Command{
	Use:   "cas <fn> <key>",
	Short: "Compare-and-swap a KV entry",
	Long: `Atomically set a key to --new only if its current value equals --expected.
Both values are JSON. Use --expected null to require that the key does not
yet exist (insert-if-absent) — the building block for distributed locks and
optimistic concurrency.

On success prints the new value and exits 0. On a precondition mismatch it
prints the current value and exits non-zero, so scripts can branch or retry.

Examples:
  orva kv cas greeter lock --expected null --new '"held"'
  orva kv cas greeter counter --expected 4 --new 5
  orva kv cas greeter lock --expected '"held"' --new null --ttl 0`,
	Args: cobra.ExactArgs(2),
	RunE: runKVCAS,
}

func init() {
	kvListCmd.Flags().String("prefix", "", "filter entries by key prefix")
	kvListCmd.Flags().Int("limit", 200, "maximum number of entries to return (max 1000)")

	kvPutCmd.Flags().String("value", "", "JSON value to store: inline, @file, or @- for stdin (required)")
	kvPutCmd.Flags().Int("ttl", 0, "TTL in seconds (0 = no expiry)")
	kvPutCmd.MarkFlagRequired("value")

	kvIncrCmd.Flags().Int64("by", 1, "amount to add (may be negative)")
	kvIncrCmd.Flags().Int("ttl", 0, "TTL in seconds to (re)set on the key (0 = preserve existing)")

	kvCASCmd.Flags().String("expected", "", "expected current JSON value; 'null' means the key must not exist (required)")
	kvCASCmd.Flags().String("new", "", "new JSON value to store if the precondition holds (required)")
	kvCASCmd.Flags().Int("ttl", 0, "TTL in seconds to set on the new value (0 = no expiry)")
	kvCASCmd.MarkFlagRequired("expected")
	kvCASCmd.MarkFlagRequired("new")

	kvCmd.AddCommand(kvListCmd)
	kvCmd.AddCommand(kvGetCmd)
	kvCmd.AddCommand(kvPutCmd)
	kvCmd.AddCommand(kvDeleteCmd)
	kvCmd.AddCommand(kvIncrCmd)
	kvCmd.AddCommand(kvCASCmd)
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

func runKVIncr(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	key := args[1]
	by, _ := cmd.Flags().GetInt64("by")
	ttl, _ := cmd.Flags().GetInt("ttl")

	body := map[string]any{"delta": by}
	if ttl > 0 {
		body["ttl_seconds"] = ttl
	}

	resp, err := client.Post("/api/v1/functions/"+fnID+"/kv/"+url.PathEscape(key)+"/incr", body)
	if err != nil {
		return fmt.Errorf("incr: %w", err)
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
		Value int64 `json:"value"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	// The new value is data → stdout (pipes into scripts); status stays on stderr.
	fmt.Println(result.Value)
	return nil
}

func runKVCAS(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	key := args[1]
	expectedArg, _ := cmd.Flags().GetString("expected")
	newArg, _ := cmd.Flags().GetString("new")
	ttl, _ := cmd.Flags().GetInt("ttl")

	var expected json.RawMessage
	if err := json.Unmarshal([]byte(expectedArg), &expected); err != nil {
		return fmt.Errorf("cas: --expected must be valid JSON (use 'null' for insert-if-absent): %w", err)
	}
	var newVal json.RawMessage
	if err := json.Unmarshal([]byte(newArg), &newVal); err != nil {
		return fmt.Errorf("cas: --new must be valid JSON: %w", err)
	}

	body := map[string]any{"expected": expected, "new": newVal}
	if ttl > 0 {
		body["ttl_seconds"] = ttl
	}

	resp, err := client.Post("/api/v1/functions/"+fnID+"/kv/"+url.PathEscape(key)+"/cas", body)
	if err != nil {
		return fmt.Errorf("cas: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		// JSON mode prints the server envelope verbatim to stdout; still exit
		// non-zero on a precondition miss so `... cas -o json && echo won` works.
		if err := emitRaw(raw); err != nil {
			return err
		}
		var r struct {
			OK bool `json:"ok"`
		}
		_ = json.Unmarshal(raw, &r)
		if !r.OK {
			return fmt.Errorf("CAS precondition not met for %q", key)
		}
		return nil
	}

	var result struct {
		OK      bool            `json:"ok"`
		Current json.RawMessage `json:"current"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if result.OK {
		okf(cmd, "swapped %q", key)
		return emitRaw(newVal)
	}
	// Precondition mismatch: print the current value (data → stdout) so a script
	// can read it, then exit non-zero with a clear message on stderr.
	if len(result.Current) > 0 {
		_ = emitRaw(result.Current)
	}
	return fmt.Errorf("CAS precondition not met for %q", key)
}
