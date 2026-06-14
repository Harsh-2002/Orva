package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Manage cron schedules",
	Long:  "List, create, update, and delete cron schedules attached to functions.",
}

var cronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List cron schedules",
	Long: `List cron schedules.

With no --fn, lists every schedule across all functions. Pass --fn to
scope the list to a single function.

Examples:
  orva cron list
  orva cron list --fn greeter
  orva cron list -o json | jq '.schedules[].cron_expr'`,
	RunE: runCronList,
}

var cronCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a cron schedule for a function",
	Long: `Create a cron schedule that fires a function on a recurring expression.

The expression is standard 5-field cron. Use --tz for a non-UTC IANA
timezone, and --payload to pass a JSON body to each invocation (inline,
@file, or @- for stdin).

Examples:
  orva cron create --fn greeter --expr '0 9 * * *'
  orva cron create --fn report --expr '*/15 * * * *' --tz Asia/Kolkata
  orva cron create --fn report --expr '0 0 * * *' --payload '{"full":true}'
  orva cron create --fn report --expr '0 0 * * *' --payload @body.json`,
	RunE: runCronCreate,
}

var cronUpdateCmd = &cobra.Command{
	Use:   "update [id]",
	Short: "Update a cron schedule",
	Long: `Update an existing cron schedule. Only the flags you pass are changed.

The owning function is auto-resolved from the schedule id, so --fn is
optional (supply it to skip the lookup).

Examples:
  orva cron update <id> --expr '30 9 * * *'
  orva cron update <id> --enabled false
  orva cron update <id> --tz UTC --payload '{"v":2}'`,
	Args: cobra.ExactArgs(1),
	RunE: runCronUpdate,
}

var cronDeleteCmd = &cobra.Command{
	Use:   "delete [id]",
	Short: "Delete a cron schedule",
	Long: `Delete a cron schedule by id. Prompts for confirmation unless --yes is set.

The owning function is auto-resolved from the schedule id.

Examples:
  orva cron delete <id>
  orva cron delete <id> --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runCronDelete,
}

func init() {
	cronListCmd.Flags().String("fn", "", "scope to a single function (name or ID); omit to list all")

	cronCreateCmd.Flags().String("fn", "", "function name or ID (required)")
	cronCreateCmd.Flags().String("expr", "", "cron expression, e.g. '0 9 * * *' (required)")
	cronCreateCmd.Flags().String("tz", "", "IANA timezone, e.g. 'Asia/Kolkata' (default UTC)")
	cronCreateCmd.Flags().String("payload", "", "JSON payload to send to the function: inline, @file, or @-")
	cronCreateCmd.MarkFlagRequired("fn")
	cronCreateCmd.MarkFlagRequired("expr")

	cronUpdateCmd.Flags().String("fn", "", "function name or ID (optional; auto-resolved from cron id when omitted)")
	cronUpdateCmd.Flags().String("expr", "", "new cron expression")
	cronUpdateCmd.Flags().String("tz", "", "new IANA timezone")
	cronUpdateCmd.Flags().String("payload", "", "new JSON payload: inline, @file, or @-")
	cronUpdateCmd.Flags().String("enabled", "", "enable/disable the schedule (true|false)")

	cronDeleteCmd.Flags().String("fn", "", "function name or ID (optional; auto-resolved from cron id when omitted)")

	cronCmd.AddCommand(cronListCmd)
	cronCmd.AddCommand(cronCreateCmd)
	cronCmd.AddCommand(cronUpdateCmd)
	cronCmd.AddCommand(cronDeleteCmd)
}

func runCronList(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnNameOrID, _ := cmd.Flags().GetString("fn")
	path := "/api/v1/cron"
	if fnNameOrID != "" {
		fnID, err := resolveFnID(client, fnNameOrID)
		if err != nil {
			return err
		}
		path = "/api/v1/functions/" + fnID + "/cron"
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
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("ID", "FUNCTION", "EXPR", "TZ", "ENABLED", "NEXT RUN", "LAST STATUS")
	for _, s := range result.Schedules {
		next := "-"
		if s.NextRunAt != nil {
			next = s.NextRunAt.Format(time.DateTime)
		}
		fnLabel := s.FunctionName
		if fnLabel == "" {
			fnLabel = s.FunctionID
		}
		t.row(s.ID, dash(fnLabel), s.CronExpr, dash(s.Timezone), s.Enabled, next, dash(s.LastStatus))
	}
	t.flush()
	infof(cmd, "\nTotal: %d", len(result.Schedules))
	return nil
}

func runCronCreate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	fnNameOrID, _ := cmd.Flags().GetString("fn")
	expr, _ := cmd.Flags().GetString("expr")
	tz, _ := cmd.Flags().GetString("tz")
	payloadArg, _ := cmd.Flags().GetString("payload")

	fnID, err := resolveFnID(client, fnNameOrID)
	if err != nil {
		return err
	}

	body := map[string]any{"cron_expr": expr}
	if tz != "" {
		body["timezone"] = tz
	}
	if payloadArg != "" {
		raw, err := readBodyArg(payloadArg)
		if err != nil {
			return fmt.Errorf("create: read payload: %w", err)
		}
		var payload any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return fmt.Errorf("create: payload must be valid JSON: %w", err)
		}
		body["payload"] = payload
	}

	resp, err := client.Post("/api/v1/functions/"+fnID+"/cron", body)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(out)
	}

	var sched struct {
		ID       string `json:"id"`
		CronExpr string `json:"cron_expr"`
	}
	json.Unmarshal(out, &sched)
	okf(cmd, "Created cron schedule %s (%s)", sched.ID, sched.CronExpr)
	return nil
}

func runCronUpdate(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	id := args[0]
	fnNameOrID, _ := cmd.Flags().GetString("fn")
	var fnID string
	if fnNameOrID != "" {
		fnID, err = resolveFnID(client, fnNameOrID)
		if err != nil {
			return err
		}
	} else {
		fnID, err = lookupCronFunctionID(client, id)
		if err != nil {
			return err
		}
	}

	body := map[string]any{}
	if expr, _ := cmd.Flags().GetString("expr"); expr != "" {
		body["cron_expr"] = expr
	}
	if tz, _ := cmd.Flags().GetString("tz"); tz != "" {
		body["timezone"] = tz
	}
	if payloadArg, _ := cmd.Flags().GetString("payload"); payloadArg != "" {
		raw, err := readBodyArg(payloadArg)
		if err != nil {
			return fmt.Errorf("update: read payload: %w", err)
		}
		var payload any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return fmt.Errorf("update: payload must be valid JSON: %w", err)
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
			return fmt.Errorf("update: --enabled must be 'true' or 'false'")
		}
	}

	resp, err := client.Put("/api/v1/functions/"+fnID+"/cron/"+id, body)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitRaw(out)
	}
	okf(cmd, "Updated cron schedule %s", id)
	return nil
}

func runCronDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	id := args[0]
	fnNameOrID, _ := cmd.Flags().GetString("fn")
	var fnID string
	if fnNameOrID != "" {
		fnID, err = resolveFnID(client, fnNameOrID)
		if err != nil {
			return err
		}
	} else {
		fnID, err = lookupCronFunctionID(client, id)
		if err != nil {
			return err
		}
	}

	if err := confirm(cmd, fmt.Sprintf("Delete cron schedule %s?", id)); err != nil {
		return err
	}

	resp, err := client.Delete("/api/v1/functions/" + fnID + "/cron/" + id)
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	resp.Body.Close()

	if outputJSON(cmd) {
		return emitJSON(map[string]any{"deleted": true, "id": id})
	}
	okf(cmd, "Deleted cron schedule %s", id)
	return nil
}

// lookupCronFunctionID resolves a cron schedule id to its owning
// function id by querying GET /api/v1/cron and matching on the
// returned schedule rows. Used when the user supplies a cron id
// without a --fn flag (cron ids are globally unique).
func lookupCronFunctionID(client *cli.Client, cronID string) (string, error) {
	resp, err := client.Get("/api/v1/cron")
	if err != nil {
		return "", fmt.Errorf("lookup cron: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return "", err
	}

	var result struct {
		Schedules []struct {
			ID         string `json:"id"`
			FunctionID string `json:"function_id"`
		} `json:"schedules"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		return "", fmt.Errorf("decode cron list: %w", err)
	}

	for _, s := range result.Schedules {
		if s.ID == cronID {
			return s.FunctionID, nil
		}
	}
	return "", fmt.Errorf("cron schedule %s not found", cronID)
}
