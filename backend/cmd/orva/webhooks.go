package main

import (
	"encoding/json"
	"fmt"
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
	rootCmd.AddCommand(webhooksCmd)
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
