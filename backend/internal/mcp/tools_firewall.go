package mcp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type FirewallRuleView struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	RuleType string `json:"rule_type"`
	Value    string `json:"value"`
	Label    string `json:"label,omitempty"`
	Enabled  bool   `json:"enabled"`
}

func toFirewallRuleView(r *database.BlocklistRule) FirewallRuleView {
	return FirewallRuleView{
		ID: r.ID, Kind: r.Kind, RuleType: r.RuleType,
		Value: r.Value, Label: r.Label, Enabled: r.Enabled,
	}
}

type ListFirewallRulesOutput struct {
	Rules []FirewallRuleView `json:"rules"`
}

type AddFirewallRuleInput struct {
	Value    string `json:"value" jsonschema:"CIDR (10.0.0.0/8) or hostname (example.com). A wildcard (*.example.com) is rejected — see rule_type"`
	RuleType string `json:"rule_type,omitempty" jsonschema:"cidr or hostname — auto-detected from value if omitted. wildcard is refused: the policy matches packet addresses, not DNS names"`
	Label    string `json:"label,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty" jsonschema:"defaults to true"`
}

// wildcardUnenforceable matches the REST handler's refusal text and the
// compiler's own `unenforced_rules` reason, so an agent that tries a wildcard
// is told the same thing an operator reading the dashboard is.
const wildcardUnenforceable = "wildcard hostnames cannot be enforced: the egress policy matches IP/CIDR, not DNS names. Use a CIDR or an exact hostname."

type DeleteFirewallRuleInput struct {
	RuleID  int64 `json:"rule_id"`
	Confirm bool  `json:"confirm"`
}

type DNSRecordView struct {
	Host string `json:"host"`
	IP   string `json:"ip"`
}

type GetDNSConfigOutput struct {
	Servers  []string        `json:"servers"`
	Search   string          `json:"search,omitempty"`
	Records  []DNSRecordView `json:"records"`
	Defaults []string        `json:"defaults"`
}

type SetDNSConfigInput struct {
	Servers []string        `json:"servers,omitempty" jsonschema:"upstream resolver IPs (no hostnames). Empty list = use defaults"`
	Search  string          `json:"search,omitempty" jsonschema:"DNS search domain"`
	Records []DNSRecordView `json:"records,omitempty" jsonschema:"host→IP overrides (max 64) — beats upstream DNS"`
}

func registerFirewallTools(rc *regCtx) {
	deps := rc.deps
	rc.group = "firewall"
	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:        "list_firewall_rules",
			Title:       "List Firewall Rules",
			Description: "List the stored sandbox egress rules — built-in defaults and operator-added customs. Each rule is a CIDR or hostname; enabled rules are compiled into a per-sandbox nsjail NSTUN policy that rejects matching outbound traffic from every function with network_mode=egress. This returns what is STORED, not what is in force: use get_egress_policy_status to see the compiled generation and any rule the compiler could not enforce.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, ListFirewallRulesOutput, error) {
			rows, err := deps.DB.ListBlocklistRules()
			if err != nil {
				return nil, ListFirewallRulesOutput{}, err
			}
			out := ListFirewallRulesOutput{Rules: make([]FirewallRuleView, 0, len(rows))}
			for _, r := range rows {
				out.Rules = append(out.Rules, toFirewallRuleView(r))
			}
			return nil, out, nil
		},
	)

	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:  "get_egress_policy_status",
			Title: "Get Egress Policy Status",
			Description: "Report what the sandbox egress policy is actually enforcing right now, as opposed to what list_firewall_rules says is stored. " +
				"`enforced` is false when no policy has compiled — every network_mode=egress invocation then fails closed with EGRESS_POLICY_UNAVAILABLE rather than running unfiltered. " +
				"`policy_stale` means a recompile failed and the last known-good generation is still in force; read `last_compile_error` for why. " +
				"`unenforced_rules` lists stored rules the compiler deliberately dropped (a wildcard, or a malformed value) — check it before telling anyone a destination is blocked. " +
				"`control_plane_allow` is the carve-out that keeps orvad's internal SDK reachable from inside the jail.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, firewall.Snapshot, error) {
			if deps.Firewall == nil {
				// Not "nothing is blocked" — enforcement state is unknowable
				// without the manager, and saying otherwise would be a lie.
				return nil, firewall.Snapshot{}, errors.New("egress policy manager is not initialized on this instance")
			}
			return nil, deps.Firewall.Snapshot(), nil
		},
	)

	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:        "add_firewall_rule",
			Title:       "Add Firewall Rule",
			Description: "Add a custom sandbox egress rule. Value is a CIDR (10.0.0.0/8) or a hostname (example.com); the type is auto-detected. A wildcard (*.example.com) is refused — the policy filters packets by address, so a name pattern can never be enforced; block the CIDR or the exact hostname instead. The rule is compiled into a new policy generation immediately, which recycles the warm egress workers so running functions pick it up.",
			Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in AddFirewallRuleInput) (*mcpsdk.CallToolResult, FirewallRuleView, error) {
			value := strings.TrimSpace(in.Value)
			if value == "" {
				return nil, FirewallRuleView{}, errors.New("value is required")
			}
			ruleType := in.RuleType
			if ruleType == "" {
				switch {
				case strings.Contains(value, "/"):
					ruleType = database.BlocklistTypeCIDR
				case strings.HasPrefix(value, "*."):
					ruleType = database.BlocklistTypeWildcard
				default:
					ruleType = database.BlocklistTypeHostname
				}
			}
			if !database.ValidBlocklistRuleType(ruleType) {
				return nil, FirewallRuleView{}, errors.New("invalid rule_type (allowed: cidr, hostname)")
			}
			// Same refusal as POST /api/v1/firewall/rules: a stored wildcard
			// would show up as a rule and block nothing.
			if ruleType == database.BlocklistTypeWildcard {
				return nil, FirewallRuleView{}, errors.New(wildcardUnenforceable)
			}
			enabled := true
			if in.Enabled != nil {
				enabled = *in.Enabled
			}
			rule, err := deps.DB.InsertCustomBlocklistRule(ruleType, value, in.Label, enabled)
			if err != nil {
				return nil, FirewallRuleView{}, err
			}
			if deps.Firewall != nil {
				refreshEgressPolicy(deps)
			}
			return nil, toFirewallRuleView(rule), nil
		},
	)

	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:        "delete_firewall_rule",
			Title:       "Delete Firewall Rule",
			Description: "Delete a custom sandbox egress rule by id. Built-in (default/suggested) rules can't be deleted — disable them from the dashboard or REST instead. Deleting recompiles the policy and recycles the warm egress workers, so traffic the rule was blocking is reachable again within seconds. Pass confirm=true.",
			Annotations: &mcpsdk.ToolAnnotations{DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteFirewallRuleInput) (*mcpsdk.CallToolResult, DeletedOutput, error) {
			if !in.Confirm {
				return nil, DeletedOutput{}, errors.New("delete refused: pass confirm=true")
			}
			if err := deps.DB.DeleteCustomBlocklistRule(in.RuleID); err != nil {
				return nil, DeletedOutput{}, err
			}
			if deps.Firewall != nil {
				refreshEgressPolicy(deps)
			}
			return nil, DeletedOutput{DeletedID: ""}, nil
		},
	)

	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:        "get_dns_config",
			Title:       "Get DNS Config",
			Description: "Get the operator-managed DNS configuration: upstream resolver IPs, optional search domain, and host→IP overrides. Sandboxes with network_mode=egress see this as their /etc/resolv.conf and /etc/hosts, mounted per worker at spawn. The configured resolvers are also carved out of the egress policy, so a rule that would otherwise cover them does not break name resolution.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, GetDNSConfigOutput, error) {
			cfg := firewall.LoadDNSConfig(deps.DB)
			out := GetDNSConfigOutput{
				Servers: cfg.Servers, Search: cfg.Search, Defaults: cfg.Defaults,
				Records: make([]DNSRecordView, 0, len(cfg.Records)),
			}
			for _, r := range cfg.Records {
				out.Records = append(out.Records, DNSRecordView{Host: r.Host, IP: r.IP})
			}
			return nil, out, nil
		},
	)

	regAddTool(rc, permAdmin,
		&mcpsdk.Tool{
			Name:        "set_dns_config",
			Title:       "Set DNS Config",
			Description: "Update DNS settings. Servers must be literal IPs (not hostnames). Records (max 64) override DNS for specific hostnames. Idempotent — pass the desired full state. Changing the resolvers also moves the egress policy (resolvers get an explicit allow rule), which recycles the warm egress workers; a search-domain or records-only edit reaches new spawns and lets existing warm workers age out.",
			Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in SetDNSConfigInput) (*mcpsdk.CallToolResult, GetDNSConfigOutput, error) {
			// Validate servers — must be IPs.
			cleanServers := []string{}
			for _, sIP := range in.Servers {
				sIP = strings.TrimSpace(sIP)
				if sIP == "" {
					continue
				}
				if net.ParseIP(sIP) == nil {
					return nil, GetDNSConfigOutput{}, errors.New("dns server must be a literal IP: " + sIP)
				}
				cleanServers = append(cleanServers, sIP)
			}
			if err := deps.DB.SetSystemConfig("dns_servers", strings.Join(cleanServers, ",")); err != nil {
				return nil, GetDNSConfigOutput{}, err
			}
			if err := deps.DB.SetSystemConfig("dns_search", strings.TrimSpace(in.Search)); err != nil {
				return nil, GetDNSConfigOutput{}, err
			}
			// Validate records.
			if len(in.Records) > 64 {
				return nil, GetDNSConfigOutput{}, errors.New("too many DNS records (max 64)")
			}
			records := make([]firewall.DNSRecord, 0, len(in.Records))
			for _, r := range in.Records {
				r.Host = strings.TrimSpace(r.Host)
				r.IP = strings.TrimSpace(r.IP)
				if r.Host == "" || net.ParseIP(r.IP) == nil {
					return nil, GetDNSConfigOutput{}, errors.New("invalid DNS record (host=" + r.Host + ", ip=" + r.IP + ")")
				}
				records = append(records, firewall.DNSRecord{Host: r.Host, IP: r.IP})
			}
			if err := deps.DB.SetSystemConfig("dns_records", firewall.SerializeDNSRecords(records)); err != nil {
				return nil, GetDNSConfigOutput{}, err
			}
			if deps.Firewall != nil {
				refreshEgressPolicy(deps)
			}
			cfg := firewall.LoadDNSConfig(deps.DB)
			out := GetDNSConfigOutput{
				Servers: cfg.Servers, Search: cfg.Search, Defaults: cfg.Defaults,
				Records: make([]DNSRecordView, 0, len(cfg.Records)),
			}
			for _, r := range cfg.Records {
				out.Records = append(out.Records, DNSRecordView{Host: r.Host, IP: r.IP})
			}
			return nil, out, nil
		},
	)
}

// refreshEgressPolicy recompiles the egress policy after an MCP-driven change.
//
// The DB write has already committed, so the tool result is not wrong about
// the mutation — but a failure here means the change is NOT in force, and
// these tools' own descriptions state the opposite as fact ("recycles the warm
// egress workers, so traffic the rule was blocking is reachable again within
// seconds"). Discarding the error silently let the in-product AI assistant
// report a security change as applied when the previous policy was still
// running. The detail lands on GET /api/v1/firewall/status.
func refreshEgressPolicy(deps Deps) {
	if deps.Firewall == nil {
		return
	}
	if err := deps.Firewall.ForceRefresh(); err != nil {
		slog.Warn("egress policy not refreshed after an MCP change; the change is saved but NOT in force",
			"err", err, "hint", "see GET /api/v1/firewall/status (last_compile_error)")
	}
}
