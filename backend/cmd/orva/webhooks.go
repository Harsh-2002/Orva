package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

var webhooksCmd = &cobra.Command{
	Use:   "webhooks",
	Short: "Manage system-event webhook subscriptions",
	Long:  "List, create, test, and delete webhook subscriptions for system events.",
}

var webhooksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List webhook subscriptions",
	Run:   runWebhooksList,
}

var webhooksCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a webhook subscription",
	Run:   runWebhooksCreate,
}

var webhooksTestCmd = &cobra.Command{
	Use:   "test [id]",
	Short: "Send a synthetic test event to a webhook",
	Args:  cobra.ExactArgs(1),
	Run:   runWebhooksTest,
}

var webhooksDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a webhook subscription",
	Args:  cobra.ExactArgs(1),
	Run:   runWebhooksDelete,
}

func init() {
	webhooksCreateCmd.Flags().String("name", "", "subscription name (required)")
	webhooksCreateCmd.Flags().String("url", "", "delivery URL (required)")
	webhooksCreateCmd.Flags().String("events", "*", "comma-separated event names (default '*')")
	webhooksCreateCmd.MarkFlagRequired("name")
	webhooksCreateCmd.MarkFlagRequired("url")

	webhooksCmd.AddCommand(webhooksListCmd)
	webhooksCmd.AddCommand(webhooksCreateCmd)
	webhooksCmd.AddCommand(webhooksTestCmd)
	webhooksCmd.AddCommand(webhooksDeleteCmd)
	webhooksCmd.AddCommand(inboundWebhooksCmd)
	rootCmd.AddCommand(webhooksCmd)

	// Inbound subcommand tree.
	inboundWebhooksCmd.AddCommand(inboundListCmd)
	inboundWebhooksCmd.AddCommand(inboundCreateCmd)
	inboundWebhooksCmd.AddCommand(inboundDeleteCmd)
	inboundWebhooksCmd.AddCommand(inboundTestCmd)

	inboundCreateCmd.Flags().String("name", "", "subscription name (required)")
	inboundCreateCmd.Flags().String("format", "hmac_sha256_hex",
		"signature format: hmac_sha256_hex|hmac_sha256_base64|github|stripe|slack")
	inboundCreateCmd.MarkFlagRequired("name")

	inboundTestCmd.Flags().String("data", `{"hello":"orva"}`, "JSON payload to sign and POST")
	inboundTestCmd.Flags().String("secret", "",
		"plaintext secret captured at create time (required — server can't recover it)")
	inboundTestCmd.MarkFlagRequired("secret")
}

func runWebhooksList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	resp, err := client.Get("/api/v1/webhooks")
	if err != nil {
		exitError("list: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("list: %v", err)
	}

	var result struct {
		Subscriptions []struct {
			ID            string    `json:"id"`
			Name          string    `json:"name"`
			URL           string    `json:"url"`
			Events        []string  `json:"events"`
			Enabled       bool      `json:"enabled"`
			SecretPreview string    `json:"secret_preview"`
			LastStatus    string    `json:"last_status"`
			CreatedAt     time.Time `json:"created_at"`
		} `json:"subscriptions"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tURL\tEVENTS\tENABLED\tLAST STATUS\tCREATED")
	for _, s := range result.Subscriptions {
		evts := strings.Join(s.Events, ",")
		if evts == "" {
			evts = "-"
		}
		last := s.LastStatus
		if last == "" {
			last = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			s.ID, s.Name, s.URL, evts, s.Enabled, last,
			s.CreatedAt.Format(time.DateTime),
		)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", len(result.Subscriptions))
}

func runWebhooksCreate(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	name, _ := cmd.Flags().GetString("name")
	url, _ := cmd.Flags().GetString("url")
	eventsStr, _ := cmd.Flags().GetString("events")

	events := []string{}
	for _, e := range strings.Split(eventsStr, ",") {
		e = strings.TrimSpace(e)
		if e != "" {
			events = append(events, e)
		}
	}

	body := map[string]any{
		"name":   name,
		"url":    url,
		"events": events,
	}

	resp, err := client.Post("/api/v1/webhooks", body)
	if err != nil {
		exitError("create: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("create: %v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
	fmt.Fprintln(os.Stderr, "\nNote: the plaintext secret above is shown ONCE — store it now.")
}

func runWebhooksTest(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	id := args[0]
	resp, err := client.Post("/api/v1/webhooks/"+id+"/test", nil)
	if err != nil {
		exitError("test: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("test: %v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}

func runWebhooksDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	id := args[0]
	resp, err := client.Delete("/api/v1/webhooks/" + id)
	if err != nil {
		exitError("delete: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("delete: %v", err)
	}

	fmt.Printf("Webhook %s deleted\n", id)
}

// ── Inbound webhook triggers (v0.4 C2a) ─────────────────────────────

var inboundWebhooksCmd = &cobra.Command{
	Use:   "inbound",
	Short: "Manage inbound webhook triggers (per-function)",
	Long: "Inbound webhooks fire a function when an external service POSTs " +
		"to /webhook/<id> with a signed body. Each trigger is scoped to one " +
		"function and one signature format.",
}

var inboundListCmd = &cobra.Command{
	Use:   "list [function-name-or-id]",
	Short: "List inbound webhook triggers for a function",
	Args:  cobra.ExactArgs(1),
	Run:   runInboundList,
}

var inboundCreateCmd = &cobra.Command{
	Use:   "create [function-name-or-id]",
	Short: "Create an inbound webhook trigger",
	Long: "Returns the trigger URL and the plaintext secret. The secret is " +
		"shown ONCE — store it now; subsequent list/get only show the preview.",
	Args: cobra.ExactArgs(1),
	Run:  runInboundCreate,
}

var inboundDeleteCmd = &cobra.Command{
	Use:   "delete [function-name-or-id] [trigger-id]",
	Short: "Delete an inbound webhook trigger",
	Args:  cobra.ExactArgs(2),
	Run:   runInboundDelete,
}

var inboundTestCmd = &cobra.Command{
	Use:   "test [function-name-or-id] [trigger-id]",
	Short: "Sign a payload locally with --secret and POST it to the trigger URL",
	Long: "The server cannot show the secret again, so you must pass --secret " +
		"yourself. Useful as a smoke test from the operator's machine before " +
		"pointing a real upstream at the URL.",
	Args: cobra.ExactArgs(2),
	Run:  runInboundTest,
}

func runInboundList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	fnID := resolveFunctionID(client, args[0])

	resp, err := client.Get("/api/v1/functions/" + fnID + "/inbound-webhooks")
	if err != nil {
		exitError("list: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("list: %v", err)
	}
	var result struct {
		InboundWebhooks []struct {
			ID              string    `json:"id"`
			Name            string    `json:"name"`
			SignatureFormat string    `json:"signature_format"`
			SignatureHeader string    `json:"signature_header"`
			SecretPreview   string    `json:"secret_preview"`
			Active          bool      `json:"active"`
			CreatedAt       time.Time `json:"created_at"`
		} `json:"inbound_webhooks"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tFORMAT\tHEADER\tSECRET\tACTIVE\tCREATED")
	for _, h := range result.InboundWebhooks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%t\t%s\n",
			h.ID, h.Name, h.SignatureFormat, h.SignatureHeader,
			h.SecretPreview, h.Active, h.CreatedAt.Format(time.DateTime),
		)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", len(result.InboundWebhooks))
}

func runInboundCreate(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	fnID := resolveFunctionID(client, args[0])

	name, _ := cmd.Flags().GetString("name")
	format, _ := cmd.Flags().GetString("format")

	body := map[string]any{
		"name":             name,
		"signature_format": format,
	}
	resp, err := client.Post("/api/v1/functions/"+fnID+"/inbound-webhooks", body)
	if err != nil {
		exitError("create: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("create: %v", err)
	}

	var result map[string]any
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
	fmt.Fprintln(os.Stderr, "\nNote: the plaintext secret above is shown ONCE — store it now.")
}

func runInboundDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	fnID := resolveFunctionID(client, args[0])
	id := args[1]

	resp, err := client.Delete("/api/v1/functions/" + fnID + "/inbound-webhooks/" + id)
	if err != nil {
		exitError("delete: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("delete: %v", err)
	}
	fmt.Printf("Inbound webhook %s deleted\n", id)
}

// runInboundTest signs the payload locally with --secret using
// hmac_sha256_hex (the "default" format) and POSTs it to the trigger
// URL. For other formats (github/stripe/slack) operators usually use
// `openssl dgst` directly; this CLI shortcut covers the common case.
func runInboundTest(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}
	fnID := resolveFunctionID(client, args[0])
	id := args[1]

	dataStr, _ := cmd.Flags().GetString("data")
	secret, _ := cmd.Flags().GetString("secret")

	// Pull the trigger row so we know which header + format to send.
	getResp, err := client.Get("/api/v1/functions/" + fnID + "/inbound-webhooks/" + id)
	if err != nil {
		exitError("lookup: %v", err)
	}
	if err := checkResponse(getResp); err != nil {
		exitError("lookup: %v", err)
	}
	var hook struct {
		ID              string `json:"id"`
		SignatureFormat string `json:"signature_format"`
		SignatureHeader string `json:"signature_header"`
	}
	if err := decodeJSON(getResp, &hook); err != nil {
		exitError("decode response: %v", err)
	}

	// Compute signature.
	body := []byte(dataStr)
	header := hook.SignatureHeader
	value := ""
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	digest := mac.Sum(nil)
	switch hook.SignatureFormat {
	case "hmac_sha256_hex":
		value = hex.EncodeToString(digest)
	case "github":
		value = "sha256=" + hex.EncodeToString(digest)
	default:
		exitError("CLI test only signs hmac_sha256_hex or github format; "+
			"got %q. Use openssl/curl directly for stripe/slack/base64.", hook.SignatureFormat)
	}

	// POST to the trigger. The CLI client targets /api/v1; build the
	// full URL via base + /webhook/<id> by reusing client's base.
	url := client.BaseURL + "/webhook/" + hook.ID
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		exitError("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(header, value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		exitError("post: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	fmt.Printf("HTTP %d\n%s\n", resp.StatusCode, string(respBody))
}
