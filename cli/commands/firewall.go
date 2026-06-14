package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// firewallCmd exposes the dashboard's egress firewall (Settings → Firewall)
// to the terminal. Rules are CIDRs, hostnames, or wildcard patterns; enabled
// rules block matching outbound traffic from sandboxes running with
// network_mode=egress. It hits the same /api/v1/firewall/* REST endpoints
// the dashboard uses, so behavior stays byte-faithful between the two.
var firewallCmd = &cobra.Command{
	Use:   "firewall",
	Short: "Manage egress firewall rules",
	Long: `Manage the egress firewall — the allow/block list applied to sandboxes
running with network_mode=egress.

Each rule is a CIDR (10.0.0.0/8), a hostname (example.com), or a wildcard
(*.example.com). Built-in (default/suggested) rules ship with Orva and can
be enabled or disabled but not deleted; custom rules you add can be edited
and deleted freely. Mutations take effect on the next sandbox spawn.`,
	Example: `  # List every rule, built-in and custom
  orva firewall list

  # Block a CIDR and a wildcard domain
  orva firewall add 10.0.0.0/8 --label "internal net"
  orva firewall add '*.metadata.google.internal'

  # Toggle a rule on or off by id
  orva firewall disable 7
  orva firewall enable 7

  # Remove a custom rule
  orva firewall delete 7`,
}

var firewallListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all egress firewall rules",
	Long: `List every egress firewall rule — built-in defaults and custom additions.

The SCOPE column shows whether a rule is a built-in (default/suggested) or a
custom rule you added. Only custom rules can be deleted.`,
	Example: `  orva firewall list
  orva firewall list -o json | jq '.rules[] | select(.enabled)'`,
	Args: cobra.NoArgs,
	RunE: runFirewallList,
}

var firewallAddCmd = &cobra.Command{
	Use:   "add <value>",
	Short: "Add a custom egress firewall rule",
	Long: `Add a custom egress firewall rule.

The value is a CIDR (10.0.0.0/8), a hostname (example.com), or a wildcard
(*.example.com). The rule type is auto-detected from the value, or pin it
explicitly with --type. New rules are enabled by default.`,
	Example: `  orva firewall add 192.168.0.0/16
  orva firewall add example.com --type hostname --label "vendor api"
  orva firewall add '*.corp.internal'
  orva firewall add 10.1.2.3 --disabled`,
	Args: cobra.ExactArgs(1),
	RunE: runFirewallAdd,
}

var firewallEnableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Enable a firewall rule",
	Long: `Enable a firewall rule by id so it starts blocking matching traffic.

Works for both built-in and custom rules.`,
	Example: `  orva firewall enable 7`,
	Args:    cobra.ExactArgs(1),
	RunE:    runFirewallToggle(true),
}

var firewallDisableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Disable a firewall rule",
	Long: `Disable a firewall rule by id so it stops blocking matching traffic.

Works for both built-in and custom rules — disabling is how you neutralize a
built-in rule you don't want (they can't be deleted).`,
	Example: `  orva firewall disable 7`,
	Args:    cobra.ExactArgs(1),
	RunE:    runFirewallToggle(false),
}

var firewallDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a custom firewall rule",
	Long: `Delete a custom firewall rule by id.

Built-in (default/suggested) rules can't be deleted — disable them with
"orva firewall disable <id>" instead.`,
	Example: `  orva firewall delete 7
  orva firewall delete 7 --yes`,
	Args: cobra.ExactArgs(1),
	RunE: runFirewallDelete,
}

var firewallResolveCmd = &cobra.Command{
	Use:   "resolve <hostname>",
	Short: "Force-resolve hostname rules and print the firewall status",
	Long: `Force the firewall manager to re-resolve hostname/wildcard rules to IPs
and re-apply the nftables set, then print the resulting status snapshot.

The hostname argument is informational — the manager re-resolves all of its
configured hostname rules, not just the one named. Use this to refresh the
applied IP set immediately instead of waiting for the next poll tick.`,
	Example: `  orva firewall resolve example.com
  orva firewall resolve example.com -o json`,
	Args: cobra.ExactArgs(1),
	RunE: runFirewallResolve,
}

func init() {
	firewallAddCmd.Flags().String("type", "", "rule type: cidr | hostname | wildcard (default: auto-detect from value)")
	firewallAddCmd.Flags().String("label", "", "human-readable label for the rule")
	firewallAddCmd.Flags().Bool("disabled", false, "add the rule in a disabled state")

	firewallCmd.AddCommand(
		firewallListCmd,
		firewallAddCmd,
		firewallEnableCmd,
		firewallDisableCmd,
		firewallDeleteCmd,
		firewallResolveCmd,
	)
}

func runFirewallList(cmd *cobra.Command, _ []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/firewall/rules")
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
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
		Rules []struct {
			ID       int64  `json:"id"`
			Kind     string `json:"kind"`
			RuleType string `json:"rule_type"`
			Value    string `json:"value"`
			Label    string `json:"label"`
			Enabled  bool   `json:"enabled"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	t := newTable("ID", "TYPE", "VALUE", "ENABLED", "SCOPE", "LABEL")
	for _, r := range result.Rules {
		scope := "custom"
		if r.Kind != "custom" {
			scope = "built-in"
		}
		t.row(r.ID, r.RuleType, r.Value, r.Enabled, scope, dash(r.Label))
	}
	t.flush()
	return nil
}

func runFirewallAdd(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	value := strings.TrimSpace(args[0])
	if value == "" {
		return fmt.Errorf("value is required")
	}
	ruleType, _ := cmd.Flags().GetString("type")
	label, _ := cmd.Flags().GetString("label")
	disabled, _ := cmd.Flags().GetBool("disabled")

	body := map[string]any{
		"value": value,
	}
	if ruleType != "" {
		body["rule_type"] = ruleType
	}
	if label != "" {
		body["label"] = label
	}
	if disabled {
		body["enabled"] = false
	}

	resp, err := client.Post("/api/v1/firewall/rules", body)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var rule struct {
		ID       int64  `json:"id"`
		RuleType string `json:"rule_type"`
		Value    string `json:"value"`
	}
	_ = json.Unmarshal(raw, &rule)
	okf(cmd, "added firewall rule %d (%s %s)", rule.ID, rule.RuleType, rule.Value)
	return nil
}

// runFirewallToggle builds the RunE for enable/disable, which differ only in
// the boolean they PUT to the rule's enabled flag.
func runFirewallToggle(enabled bool) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		client, err := getClient(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid rule id %q (expected a number)", args[0])
		}
		resp, err := client.Put(
			fmt.Sprintf("/api/v1/firewall/rules/%d", id),
			map[string]any{"enabled": enabled},
		)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		if err := checkResponse(resp); err != nil {
			return err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if outputJSON(cmd) {
			return emitRaw(raw)
		}
		state := "enabled"
		if !enabled {
			state = "disabled"
		}
		okf(cmd, "rule %d %s", id, state)
		return nil
	}
}

func runFirewallDelete(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	id, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid rule id %q (expected a number)", args[0])
	}
	if err := confirm(cmd, fmt.Sprintf("Delete custom firewall rule %d?", id)); err != nil {
		return err
	}
	resp, err := client.Delete(fmt.Sprintf("/api/v1/firewall/rules/%d", id))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitRaw(raw)
	}
	okf(cmd, "deleted firewall rule %d", id)
	return nil
}

func runFirewallResolve(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	hostname := strings.TrimSpace(args[0])
	resp, err := client.Post("/api/v1/firewall/resolve", nil)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
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
		Refreshed bool   `json:"refreshed"`
		Error     string `json:"error"`
		Status    struct {
			IPv4        []string            `json:"ipv4"`
			IPv6        []string            `json:"ipv6"`
			HostnameMap map[string][]string `json:"hostname_map"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !result.Refreshed {
		return fmt.Errorf("refresh failed: %s", dash(result.Error))
	}
	infof(cmd, "firewall refreshed")

	// Prefer the IPs the named hostname resolved to; fall back to the full
	// applied set when the host isn't a configured hostname rule.
	if ips, ok := result.Status.HostnameMap[hostname]; ok {
		if len(ips) == 0 {
			infof(cmd, "%s resolved to no addresses", hostname)
			return nil
		}
		for _, ip := range ips {
			fmt.Println(ip)
		}
		return nil
	}

	infof(cmd, "%q is not a configured hostname rule; showing the full applied set:", hostname)
	all := append(append([]string{}, result.Status.IPv4...), result.Status.IPv6...)
	if len(all) == 0 {
		infof(cmd, "no resolved IPs in the applied set")
		return nil
	}
	for _, ip := range all {
		fmt.Println(ip)
	}
	return nil
}
