package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Harsh-2002/Orva/internal/cli"
	"github.com/spf13/cobra"
)

var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Manage cron schedules",
	Long:  "List, create, update, and delete cron schedules attached to functions.",
}

var cronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all cron schedules across functions",
	Run:   runCronList,
}

var cronCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new cron schedule for a function",
	Run:   runCronCreate,
}

var cronUpdateCmd = &cobra.Command{
	Use:   "update [id]",
	Short: "Update a cron schedule",
	Args:  cobra.ExactArgs(1),
	Run:   runCronUpdate,
}

var cronDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a cron schedule",
	Args:  cobra.ExactArgs(1),
	Run:   runCronDelete,
}

func init() {
	cronCreateCmd.Flags().String("fn", "", "function name or ID (required)")
	cronCreateCmd.Flags().String("expr", "", "cron expression, e.g. '0 9 * * *' (required)")
	cronCreateCmd.Flags().String("tz", "", "IANA timezone, e.g. 'Asia/Kolkata' (default UTC)")
	cronCreateCmd.Flags().String("payload", "", "optional JSON payload to send to the function")
	cronCreateCmd.MarkFlagRequired("fn")
	cronCreateCmd.MarkFlagRequired("expr")

	cronUpdateCmd.Flags().String("fn", "", "function name or ID (optional; auto-resolved from cron id when omitted)")
	cronUpdateCmd.Flags().String("expr", "", "new cron expression")
	cronUpdateCmd.Flags().String("tz", "", "new IANA timezone")
	cronUpdateCmd.Flags().String("payload", "", "new JSON payload")
	cronUpdateCmd.Flags().String("enabled", "", "enable/disable the schedule (true|false)")

	cronDeleteCmd.Flags().String("fn", "", "function name or ID (optional; auto-resolved from cron id when omitted)")

	cronCmd.AddCommand(cronListCmd)
	cronCmd.AddCommand(cronCreateCmd)
	cronCmd.AddCommand(cronUpdateCmd)
	cronCmd.AddCommand(cronDeleteCmd)
	rootCmd.AddCommand(cronCmd)
}

func runCronList(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	resp, err := client.Get("/api/v1/cron")
	if err != nil {
		exitError("list: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("list: %v", err)
	}

	var result struct {
		Schedules []struct {
			ID           string     `json:"id"`
			FunctionID   string     `json:"function_id"`
			FunctionName string     `json:"function_name"`
			CronExpr     string     `json:"cron_expr"`
			Timezone     string     `json:"timezone"`
			Enabled      bool       `json:"enabled"`
			NextRunAt    *time.Time `json:"next_run_at"`
			LastStatus   string     `json:"last_status"`
		} `json:"schedules"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode response: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tFUNCTION\tEXPR\tTZ\tENABLED\tNEXT RUN\tLAST STATUS")
	for _, s := range result.Schedules {
		next := "-"
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format(time.DateTime)
		}
		fnLabel := s.FunctionName
		if fnLabel == "" {
			fnLabel = s.FunctionID
		}
		lastStatus := s.LastStatus
		if lastStatus == "" {
			lastStatus = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			s.ID, fnLabel, s.CronExpr, s.Timezone, s.Enabled, next, lastStatus,
		)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d\n", len(result.Schedules))
}

func runCronCreate(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	fnNameOrID, _ := cmd.Flags().GetString("fn")
	expr, _ := cmd.Flags().GetString("expr")
	tz, _ := cmd.Flags().GetString("tz")
	payloadStr, _ := cmd.Flags().GetString("payload")

	fnID := resolveFunctionID(client, fnNameOrID)

	body := map[string]any{
		"cron_expr": expr,
	}
	if tz != "" {
		body["timezone"] = tz
	}
	if payloadStr != "" {
		var payload any
		if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
			exitError("create: payload must be valid JSON: %v", err)
		}
		body["payload"] = payload
	}

	resp, err := client.Post("/api/v1/functions/"+fnID+"/cron", body)
	if err != nil {
		exitError("create: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("create: %v", err)
	}

	var sched map[string]any
	if err := decodeJSON(resp, &sched); err != nil {
		exitError("decode response: %v", err)
	}
	out, _ := json.MarshalIndent(sched, "", "  ")
	fmt.Println(string(out))
}

func runCronUpdate(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	id := args[0]
	fnNameOrID, _ := cmd.Flags().GetString("fn")
	var fnID string
	if fnNameOrID != "" {
		fnID = resolveFunctionID(client, fnNameOrID)
	} else {
		fnID = lookupCronFunctionID(client, id)
	}

	body := map[string]any{}
	if expr, _ := cmd.Flags().GetString("expr"); expr != "" {
		body["cron_expr"] = expr
	}
	if tz, _ := cmd.Flags().GetString("tz"); tz != "" {
		body["timezone"] = tz
	}
	if payloadStr, _ := cmd.Flags().GetString("payload"); payloadStr != "" {
		var payload any
		if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
			exitError("update: payload must be valid JSON: %v", err)
		}
		body["payload"] = payload
	}
	if enabledStr, _ := cmd.Flags().GetString("enabled"); enabledStr != "" {
		switch enabledStr {
		case "true":
			body["enabled"] = true
		case "false":
			body["enabled"] = false
		default:
			exitError("update: --enabled must be 'true' or 'false'")
		}
	}

	resp, err := client.Put("/api/v1/functions/"+fnID+"/cron/"+id, body)
	if err != nil {
		exitError("update: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("update: %v", err)
	}

	var sched map[string]any
	if err := decodeJSON(resp, &sched); err != nil {
		exitError("decode response: %v", err)
	}
	out, _ := json.MarshalIndent(sched, "", "  ")
	fmt.Println(string(out))
}

func runCronDelete(cmd *cobra.Command, args []string) {
	client, err := getClient(cmd)
	if err != nil {
		exitError("%v", err)
	}

	id := args[0]
	fnNameOrID, _ := cmd.Flags().GetString("fn")
	var fnID string
	if fnNameOrID != "" {
		fnID = resolveFunctionID(client, fnNameOrID)
	} else {
		fnID = lookupCronFunctionID(client, id)
	}

	resp, err := client.Delete("/api/v1/functions/" + fnID + "/cron/" + id)
	if err != nil {
		exitError("delete: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("delete: %v", err)
	}

	fmt.Printf("Cron schedule %s deleted\n", id)
}

// lookupCronFunctionID resolves a cron schedule id to its owning
// function id by querying GET /api/v1/cron and matching on the
// returned schedule rows. Used when the user supplies a cron id
// without a --fn flag (cron ids are globally unique).
func lookupCronFunctionID(client *cli.Client, cronID string) string {
	resp, err := client.Get("/api/v1/cron")
	if err != nil {
		exitError("lookup cron: %v", err)
	}
	if err := checkResponse(resp); err != nil {
		exitError("lookup cron: %v", err)
	}

	var result struct {
		Schedules []struct {
			ID         string `json:"id"`
			FunctionID string `json:"function_id"`
		} `json:"schedules"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		exitError("decode cron list: %v", err)
	}

	for _, s := range result.Schedules {
		if s.ID == cronID {
			return s.FunctionID
		}
	}

	exitError("cron schedule %s not found", cronID)
	return "" // unreachable
}
