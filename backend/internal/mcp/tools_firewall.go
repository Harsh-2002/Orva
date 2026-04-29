package mcp

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/firewall"
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
	Value    string `json:"value" jsonschema:"CIDR (10.0.0.0/8), hostname (example.com), or wildcard (*.example.com)"`
	RuleType string `json:"rule_type,omitempty" jsonschema:"cidr, hostname, or wildcard — auto-detected from value if omitted"`
	Label    string `json:"label,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty" jsonschema:"defaults to true"`
}

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

func registerFirewallTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_firewall_rules",
				Description: "List all egress firewall rules — both built-in defaults and operator-added customs. Each rule is a CIDR, hostname, or wildcard pattern; enabled rules block matching outbound traffic from sandboxes with network_mode=egress.",
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
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "add_firewall_rule",
				Description: "Add a custom egress firewall rule. Value can be a CIDR (10.0.0.0/8), hostname (example.com), or wildcard (*.example.com) — type is auto-detected. Takes effect immediately for new sandbox spawns.",
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
					return nil, FirewallRuleView{}, errors.New("invalid rule_type (allowed: cidr, hostname, wildcard)")
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
					_ = deps.Firewall.ForceRefresh()
				}
				return nil, toFirewallRuleView(rule), nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_firewall_rule",
				Description: "Delete a custom firewall rule by id. Built-in (default/suggested) rules can't be deleted — disable them via add_firewall_rule's enabled flag instead. Pass confirm=true.",
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
					_ = deps.Firewall.ForceRefresh()
				}
				return nil, DeletedOutput{DeletedID: ""}, nil
			},
		)
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "get_dns_config",
				Description: "Get the operator-managed DNS configuration: upstream resolver IPs, optional search domain, and host→IP overrides. Sandboxes with network_mode=egress see this as their /etc/resolv.conf and /etc/hosts.",
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
	})

	gatedAdd(perms, permAdmin, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "set_dns_config",
				Description: "Update DNS settings. Servers must be literal IPs (not hostnames). Records (max 64) override DNS for specific hostnames. Idempotent — pass the desired full state.",
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
					_ = deps.Firewall.ForceRefresh()
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
	})
}
