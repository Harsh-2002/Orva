package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var kvCmd = &cobra.Command{
	Use:   "kv",
	Short: "Manage per-function key/value state",
	Long:  "List, get, put, and delete entries in a function's KV store.",
}

var kvListCmd = &cobra.Command{
	Use:   "list [fn]",
	Short: "List KV entries for a function",
	Args:  cobra.ExactArgs(1),
	Run:   runKVList,
}

var kvGetCmd = &cobra.Command{
	Use:   "get [fn] [key]",
	Short: "Get a KV entry",
	Args:  cobra.ExactArgs(2),
	Run:   runKVGet,
}

var kvPutCmd = &cobra.Command{
	Use:   "put [fn] [key]",
	Short: "Put a KV entry",
	Args:  cobra.ExactArgs(2),
	Run:   runKVPut,
}

var kvDeleteCmd = &cobra.Command{
	Use:   "delete [fn] [key]",
	Short: "Delete a KV entry",
	Args:  cobra.ExactArgs(2),
	Run:   runKVDelete,
}

func init() {
	kvListCmd.Flags().String("prefix", "", "filter entries by key prefix")
	kvListCmd.Flags().Int("limit", 200, "maximum number of entries to return (max 1000)")

	kvPutCmd.Flags().String("data", "", "JSON value to store (required)")
	kvPutCmd.Flags().Int("ttl", 0, "TTL in seconds (0 = no expiry)")
	kvPutCmd.MarkFlagRequired("data")

	kvCmd.AddCommand(kvListCmd)
	kvCmd.AddCommand(kvGetCmd)
	kvCmd.AddCommand(kvPutCmd)
	kvCmd.AddCommand(kvDeleteCmd)
	rootCmd.AddCommand(kvCmd)
}

func runKVList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnID := resolveFunctionID(client, args[0])
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
		exitError("list: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("list: %v", err)
	}

	var result struct {
		Entries []struct {
			Key       string          `json:"key"`
			Value     json.RawMessage `json:"value"`
			ExpiresAt *string         `json:"expires_at"`
			UpdatedAt string          `json:"updated_at"`
			SizeBytes int             `json:"size_bytes"`
		} `json:"entries"`
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tSIZE\tEXPIRES\tUPDATED")
	for _, e := range result.Entries {
		expires := "-"
		if e.ExpiresAt != nil {
			expires = *e.ExpiresAt
		}
		updated := e.UpdatedAt
		if t, err := time.Parse(time.RFC3339, e.UpdatedAt); err == nil {
			updated = t.Format(time.DateTime)
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", e.Key, e.SizeBytes, expires, updated)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d", result.Total)
	if result.Truncated {
		fmt.Printf(" (truncated; narrow the prefix or raise --limit)")
	}
	fmt.Println()
}

func runKVGet(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnID := resolveFunctionID(client, args[0])
	key := args[1]

	resp, err := client.Get("/api/v1/functions/" + fnID + "/kv/" + url.PathEscape(key))
	if err != nil {
		exitError("get: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("get: %v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}

func runKVPut(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnID := resolveFunctionID(client, args[0])
	key := args[1]

	dataStr, _ := cmd.Flags().GetString("data")
	ttl, _ := cmd.Flags().GetInt("ttl")

	var value any
	if err := json.Unmarshal([]byte(dataStr), &value); err != nil {
		exitError("put: --data must be valid JSON: %v", err)
	}

	body := map[string]any{
		"value": value,
	}
	if ttl > 0 {
		body["ttl_seconds"] = ttl
	}

	resp, err := client.Put("/api/v1/functions/"+fnID+"/kv/"+url.PathEscape(key), body)
	if err != nil {
		exitError("put: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("put: %v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}

func runKVDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnID := resolveFunctionID(client, args[0])
	key := args[1]

	resp, err := client.Delete("/api/v1/functions/" + fnID + "/kv/" + url.PathEscape(key))
	if err != nil {
		exitError("delete: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("delete: %v", err)
	}

	fmt.Printf("KV entry %q deleted\n", key)
}
