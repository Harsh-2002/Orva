package commands

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var webhooksCmd = &cobra.Command{
	Use:   "webhooks",
	Short: "Manage system-event webhook subscriptions",
	Long: `List, create, test, and delete outbound webhook subscriptions for system
events (deployment.failed, job.failed, cron.failed, …), inspect their delivery
history, and manage inbound webhook triggers.

  orva webhooks create --name ci --url https://example.com/hook --events deployment.failed
  orva webhooks deliveries <id> -o json | jq .`,
}

var webhooksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List webhook subscriptions",
	Long: `List outbound webhook subscriptions with their URL, subscribed events, and
last delivery status.

  orva webhooks list
  orva webhooks list -o json | jq '.subscriptions[].url'`,
	RunE: runWebhooksList,
}

var webhooksCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a webhook subscription",
	Long: `Create an outbound webhook subscription. The signing secret is returned ONCE
on create — store it. Events default to '*' (all).

  orva webhooks create --name ci --url https://example.com/hook --events deployment.failed
  orva webhooks create --name all --url https://example.com/hook`,
	RunE: runWebhooksCreate,
}

var webhooksTestCmd = &cobra.Command{
	Use:   "test <id>",
	Short: "Send a synthetic test event to a webhook",
	Long: `Deliver a synthetic test event to a subscription so you can verify the
endpoint receives and accepts it.

  orva webhooks test <id>`,
	Args: cobra.ExactArgs(1),
	RunE: runWebhooksTest,
}

var webhooksGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get a webhook subscription",
	Long: `Print a single webhook subscription as JSON (the plaintext secret is never
returned — only a preview).

  orva webhooks get <id>
  orva webhooks get <id> | jq .events`,
	Args: cobra.ExactArgs(1),
	RunE: runWebhooksGet,
}

var webhooksUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a webhook subscription",
	Long: `Update a subscription in place (partial — only the flags you pass change).
The secret cannot be rotated here; delete and recreate to rotate it.

  orva webhooks update <id> --enabled=false
  orva webhooks update <id> --url https://new.example.com/hook
  orva webhooks update <id> --events deployment.failed,job.failed`,
	Args: cobra.ExactArgs(1),
	RunE: runWebhooksUpdate,
}

var webhooksDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a webhook subscription",
	Long: `Delete an outbound webhook subscription. Prompts for confirmation unless
--yes is passed.

  orva webhooks delete <id>
  orva webhooks delete <id> --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runWebhooksDelete,
}

var webhooksDeliveriesCmd = &cobra.Command{
	Use:   "deliveries <id>",
	Short: "List recent delivery attempts for a subscription",
	Long: `Show recent delivery attempts (status, response code, error) for a
subscription — the first stop when a webhook isn't arriving.

  orva webhooks deliveries <id>
  orva webhooks deliveries <id> -o json | jq .`,
	Args: cobra.ExactArgs(1),
	RunE: runWebhooksDeliveries,
}

var webhooksRetryCmd = &cobra.Command{
	Use:   "retry <delivery-id>",
	Short: "Retry a failed delivery",
	Long: `Re-attempt a single failed delivery by its delivery id (from
'orva webhooks deliveries <id>').

  orva webhooks retry <delivery-id>`,
	Args: cobra.ExactArgs(1),
	RunE: runWebhooksRetry,
}

func init() {
	webhooksCreateCmd.Flags().String("name", "", "subscription name (required)")
	webhooksCreateCmd.Flags().String("url", "", "delivery URL (required)")
	webhooksCreateCmd.Flags().String("events", "*", "comma-separated event names (default '*')")
	webhooksCreateCmd.MarkFlagRequired("name")
	webhooksCreateCmd.MarkFlagRequired("url")

	webhooksUpdateCmd.Flags().String("name", "", "rename the subscription")
	webhooksUpdateCmd.Flags().String("url", "", "delivery URL")
	webhooksUpdateCmd.Flags().String("events", "", "comma-separated event names (replaces the set)")
	webhooksUpdateCmd.Flags().Bool("enabled", true, "enable or disable delivery")

	webhooksCmd.AddCommand(webhooksListCmd)
	webhooksCmd.AddCommand(webhooksCreateCmd)
	webhooksCmd.AddCommand(webhooksGetCmd)
	webhooksCmd.AddCommand(webhooksUpdateCmd)
	webhooksCmd.AddCommand(webhooksTestCmd)
	webhooksCmd.AddCommand(webhooksDeleteCmd)
	webhooksCmd.AddCommand(webhooksDeliveriesCmd)
	webhooksCmd.AddCommand(webhooksRetryCmd)
	webhooksCmd.AddCommand(inboundWebhooksCmd)

	// Inbound subcommand tree.
	inboundWebhooksCmd.AddCommand(inboundListCmd)
	inboundWebhooksCmd.AddCommand(inboundCreateCmd)
	inboundWebhooksCmd.AddCommand(inboundDeleteCmd)
	inboundWebhooksCmd.AddCommand(inboundTestCmd)

	inboundCreateCmd.Flags().String("name", "", "subscription name (required)")
	inboundCreateCmd.Flags().String("format", "hmac_sha256_hex",
		"signature format: hmac_sha256_hex|hmac_sha256_base64|github|stripe|slack")
	inboundCreateCmd.MarkFlagRequired("name")

	inboundTestCmd.Flags().String("data", `{"hello":"orva"}`, "JSON payload to sign and POST (inline, @file, or @-)")
	inboundTestCmd.Flags().String("secret", "",
		"plaintext secret captured at create time (required — server can't recover it)")
	inboundTestCmd.Flags().String("format", "",
		"override signature format (hmac_sha256_hex|hmac_sha256_base64|github|stripe|slack); default: read from server")
	inboundTestCmd.MarkFlagRequired("secret")
}

func runWebhooksList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/webhooks")
	if err != nil {
		return fmt.Errorf("list: %w", err)
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
	var result struct {
		Subscriptions []struct {
			ID         string    `json:"id"`
			Name       string    `json:"name"`
			URL        string    `json:"url"`
			Events     []string  `json:"events"`
			Enabled    bool      `json:"enabled"`
			LastStatus string    `json:"last_status"`
			CreatedAt  time.Time `json:"created_at"`
		} `json:"subscriptions"`
	}
	if err := jsonUnmarshal(raw, &result); err != nil {
		return err
	}
	t := newTable("ID", "NAME", "URL", "EVENTS", "ENABLED", "LAST STATUS", "CREATED")
	for _, s := range result.Subscriptions {
		t.row(s.ID, s.Name, s.URL, dash(strings.Join(s.Events, ",")), s.Enabled,
			dash(s.LastStatus), s.CreatedAt.Format(time.DateTime))
	}
	t.flush()
	infof(cmd, "\nTotal: %d", len(result.Subscriptions))
	return nil
}

func runWebhooksCreate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	name, _ := cmd.Flags().GetString("name")
	url, _ := cmd.Flags().GetString("url")
	eventsStr, _ := cmd.Flags().GetString("events")

	events := []string{}
	for _, e := range strings.Split(eventsStr, ",") {
		if e = strings.TrimSpace(e); e != "" {
			events = append(events, e)
		}
	}

	resp, err := client.Post("/api/v1/webhooks", map[string]any{
		"name": name, "url": url, "events": events,
	})
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if err := emitRaw(raw); err != nil {
		return err
	}
	infof(cmd, "\nNote: the plaintext secret above is shown ONCE — store it now.")
	return nil
}

func runWebhooksGet(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/webhooks/" + args[0])
	if err != nil {
		return fmt.Errorf("get: %w", err)
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

func runWebhooksUpdate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	// Partial update — only send fields whose flag was explicitly set.
	body := map[string]any{}
	f := cmd.Flags()
	if f.Changed("name") {
		v, _ := f.GetString("name")
		body["name"] = v
	}
	if f.Changed("url") {
		v, _ := f.GetString("url")
		body["url"] = v
	}
	if f.Changed("events") {
		v, _ := f.GetString("events")
		events := []string{}
		for _, e := range strings.Split(v, ",") {
			if e = strings.TrimSpace(e); e != "" {
				events = append(events, e)
			}
		}
		body["events"] = events
	}
	if f.Changed("enabled") {
		v, _ := f.GetBool("enabled")
		body["enabled"] = v
	}
	if len(body) == 0 {
		return fmt.Errorf("update: nothing to change — pass at least one of --name/--url/--events/--enabled")
	}

	resp, err := client.Put("/api/v1/webhooks/"+args[0], body)
	if err != nil {
		return fmt.Errorf("update: %w", err)
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
	okf(cmd, "Updated webhook %s", args[0])
	return nil
}

func runWebhooksTest(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Post("/api/v1/webhooks/"+args[0]+"/test", nil)
	if err != nil {
		return fmt.Errorf("test: %w", err)
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

func runWebhooksDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	if err := confirm(cmd, fmt.Sprintf("Delete webhook subscription %q?", args[0])); err != nil {
		return err
	}
	resp, err := client.Delete("/api/v1/webhooks/" + args[0])
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitJSON(map[string]any{"deleted": true, "id": args[0]})
	}
	okf(cmd, "Webhook %s deleted", args[0])
	return nil
}

func runWebhooksDeliveries(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/webhooks/" + args[0] + "/deliveries")
	if err != nil {
		return fmt.Errorf("deliveries: %w", err)
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
	var result struct {
		Deliveries []struct {
			ID             string    `json:"id"`
			EventName      string    `json:"event_name"`
			Status         string    `json:"status"`
			Attempts       int       `json:"attempts"`
			MaxAttempts    int       `json:"max_attempts"`
			ResponseStatus int       `json:"response_status"`
			CreatedAt      time.Time `json:"created_at"`
		} `json:"deliveries"`
	}
	if err := jsonUnmarshal(raw, &result); err != nil {
		return err
	}
	t := newTable("ID", "EVENT", "STATUS", "ATTEMPTS", "CODE", "CREATED")
	for _, d := range result.Deliveries {
		code := "-"
		if d.ResponseStatus != 0 {
			code = strconv.Itoa(d.ResponseStatus)
		}
		t.row(d.ID, d.EventName, d.Status, fmt.Sprintf("%d/%d", d.Attempts, d.MaxAttempts),
			code, d.CreatedAt.Format(time.DateTime))
	}
	t.flush()
	infof(cmd, "\nTotal: %d", len(result.Deliveries))
	return nil
}

func runWebhooksRetry(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Post("/api/v1/webhooks/deliveries/"+args[0]+"/retry", nil)
	if err != nil {
		return fmt.Errorf("retry: %w", err)
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
	okf(cmd, "Delivery %s re-queued", args[0])
	return nil
}

// ── Inbound webhook triggers ────────────────────────────────────────

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
	RunE:  runInboundList,
}

var inboundCreateCmd = &cobra.Command{
	Use:   "create [function-name-or-id]",
	Short: "Create an inbound webhook trigger",
	Long: "Returns the trigger URL and the plaintext secret. The secret is " +
		"shown ONCE — store it now; subsequent list/get only show the preview.",
	Args: cobra.ExactArgs(1),
	RunE: runInboundCreate,
}

var inboundDeleteCmd = &cobra.Command{
	Use:   "delete [function-name-or-id] [trigger-id]",
	Short: "Delete an inbound webhook trigger",
	Args:  cobra.ExactArgs(2),
	RunE:  runInboundDelete,
}

var inboundTestCmd = &cobra.Command{
	Use:   "test [function-name-or-id] [trigger-id]",
	Short: "Sign a payload locally with --secret and POST it to the trigger URL",
	Long: "The server cannot show the secret again, so you must pass --secret " +
		"yourself. Useful as a smoke test from the operator's machine before " +
		"pointing a real upstream at the URL.",
	Args: cobra.ExactArgs(2),
	RunE: runInboundTest,
}

func runInboundList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/functions/" + fnID + "/inbound-webhooks")
	if err != nil {
		return fmt.Errorf("list: %w", err)
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
	if err := jsonUnmarshal(raw, &result); err != nil {
		return err
	}
	t := newTable("ID", "NAME", "FORMAT", "HEADER", "SECRET", "ACTIVE", "CREATED")
	for _, h := range result.InboundWebhooks {
		t.row(h.ID, h.Name, h.SignatureFormat, h.SignatureHeader, h.SecretPreview,
			h.Active, h.CreatedAt.Format(time.DateTime))
	}
	t.flush()
	infof(cmd, "\nTotal: %d", len(result.InboundWebhooks))
	return nil
}

func runInboundCreate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	name, _ := cmd.Flags().GetString("name")
	format, _ := cmd.Flags().GetString("format")

	resp, err := client.Post("/api/v1/functions/"+fnID+"/inbound-webhooks", map[string]any{
		"name": name, "signature_format": format,
	})
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if err := emitRaw(raw); err != nil {
		return err
	}
	infof(cmd, "\nNote: the plaintext secret above is shown ONCE — store it now.")
	return nil
}

func runInboundDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	if err := confirm(cmd, fmt.Sprintf("Delete inbound webhook %q?", args[1])); err != nil {
		return err
	}
	resp, err := client.Delete("/api/v1/functions/" + fnID + "/inbound-webhooks/" + args[1])
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitJSON(map[string]any{"deleted": true, "id": args[1]})
	}
	okf(cmd, "Inbound webhook %s deleted", args[1])
	return nil
}

// runInboundTest signs the payload locally with --secret in whatever format the
// trigger row declares (or --format override) and POSTs it to the trigger URL.
// Mirrors the verifier in internal/server/handlers/inbound_webhook_trigger.go
// so all five supported formats round-trip.
func runInboundTest(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnID, err := resolveFnID(client, args[0])
	if err != nil {
		return err
	}
	id := args[1]

	dataStr, _ := cmd.Flags().GetString("data")
	secret, _ := cmd.Flags().GetString("secret")
	formatOverride, _ := cmd.Flags().GetString("format")

	body, err := readBodyArg(dataStr)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	getResp, err := client.Get("/api/v1/functions/" + fnID + "/inbound-webhooks/" + id)
	if err != nil {
		return fmt.Errorf("lookup: %w", err)
	}
	if err := checkResponse(getResp); err != nil {
		return err
	}
	var hook struct {
		ID              string `json:"id"`
		SignatureFormat string `json:"signature_format"`
		SignatureHeader string `json:"signature_header"`
	}
	if err := decodeJSON(getResp, &hook); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	format := hook.SignatureFormat
	if formatOverride != "" {
		format = formatOverride
	}

	header := hook.SignatureHeader
	var value string
	extraHeaders := map[string]string{}

	switch format {
	case "hmac_sha256_hex":
		value = hex.EncodeToString(hmacSHA256(secret, body))
	case "hmac_sha256_base64":
		value = base64.StdEncoding.EncodeToString(hmacSHA256(secret, body))
	case "github":
		value = "sha256=" + hex.EncodeToString(hmacSHA256(secret, body))
	case "stripe":
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		signed := []byte(ts + "." + string(body))
		value = "t=" + ts + ",v1=" + hex.EncodeToString(hmacSHA256(secret, signed))
	case "slack":
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		signed := []byte("v0:" + ts + ":" + string(body))
		value = "v0=" + hex.EncodeToString(hmacSHA256(secret, signed))
		extraHeaders["X-Slack-Request-Timestamp"] = ts
	default:
		return fmt.Errorf("unknown signature format %q (expected hmac_sha256_hex|hmac_sha256_base64|github|stripe|slack)", format)
	}

	url := client.BaseURL + "/webhook/" + hook.ID
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(header, value)
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := client.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if outputJSON(cmd) {
		out := map[string]any{
			"status":  resp.StatusCode,
			"headers": flattenHeaders(resp.Header),
		}
		var parsed any
		if jsonUnmarshal(respBody, &parsed) == nil {
			out["body"] = parsed
		} else {
			out["body"] = string(respBody)
		}
		if err := emitJSON(out); err != nil {
			return err
		}
		return exitForStatus(resp.StatusCode)
	}
	infof(cmd, "HTTP %d", resp.StatusCode)
	fmt.Println(string(respBody))
	return exitForStatus(resp.StatusCode)
}

func hmacSHA256(secret string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return mac.Sum(nil)
}
