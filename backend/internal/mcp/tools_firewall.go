package mcp

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
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
	if deps.ManagedServer && deps.Firewall == nil {
		return
	}
	rc.group = "firewall"
	regAddFirewallTool(rc,
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

	regAddFirewallTool(rc,
		&mcpsdk.Tool{
			Name:  "get_egress_policy_status",
			Title: "Get Egress Policy Status",
			Description: "Report what the sandbox egress policy is actually enforcing right now, as opposed to what list_firewall_rules says is stored. " +
				"`enforced` is false when no policy has compiled — every network_mode=egress invocation then fails closed with EGRESS_POLICY_UNAVAILABLE rather than running unfiltered. " +
				"`policy_stale` means a recompile failed and the last known-good generation is still in force; read `last_compile_error` for why. " +
				"`unenforced_rules` lists stored rules the compiler deliberately dropped (a wildcard, or a malformed value) — check it before telling anyone a destination is blocked. " +
				"SDK calls use a private per-worker channel; there is no HTTP control-plane carve-out.",
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

	regAddFirewallTool(rc,
		&mcpsdk.Tool{
			Name:        "add_firewall_rule",
			Title:       "Add Firewall Rule",
			Description: "Add a custom sandbox egress rule. Value is a CIDR (10.0.0.0/8) or a hostname (example.com); the type is auto-detected. A wildcard (*.example.com) is refused — the policy filters packets by address, so a name pattern can never be enforced; block the CIDR or the exact hostname instead. The rule is compiled into a new policy generation immediately, which recycles the warm egress workers so running functions pick it up.",
			Annotations: &mcpsdk.ToolAnnotations{OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in AddFirewallRuleInput) (*mcpsdk.CallToolResult, FirewallRuleView, error) {
			ruleType, value, err := handlers.NormalizeFirewallRule(in.RuleType, in.Value)
			if err != nil {
				return nil, FirewallRuleView{}, err
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

	regAddFirewallTool(rc,
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

	regAddFirewallTool(rc,
		&mcpsdk.Tool{
			Name:        "get_dns_config",
			Title:       "Get DNS Config",
			Description: "Get the operator-managed DNS configuration: upstream resolver IPs, optional search domain, and host→IP overrides. Sandboxes with network_mode=egress see this as their /etc/resolv.conf and /etc/hosts, mounted per worker at spawn. The configured resolvers are also carved out of the egress policy, so a rule that would otherwise cover them does not break name resolution.",
			Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
		},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, GetDNSConfigOutput, error) {
			return nil, dnsConfigView(deps.DB), nil
		},
	)

	regAddFirewallTool(rc,
		&mcpsdk.Tool{
			Name:        "set_dns_config",
			Title:       "Set DNS Config",
			Description: "Update DNS settings. Servers must be literal IPs (not hostnames). Records (max 64) override DNS for specific hostnames. Idempotent — pass the desired full state. Changing the resolvers also moves the egress policy (resolvers get an explicit allow rule), which recycles the warm egress workers; a search-domain or records-only edit reaches new spawns and lets existing warm workers age out.",
			Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
		},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in SetDNSConfigInput) (*mcpsdk.CallToolResult, GetDNSConfigOutput, error) {
			records := make([]firewall.DNSRecord, 0, len(in.Records))
			for _, r := range in.Records {
				records = append(records, firewall.DNSRecord{Host: r.Host, IP: r.IP})
			}
			if err := handlers.SaveSandboxDNSConfig(ctx, deps.DB, in.Servers, in.Search, records); err != nil {
				return nil, GetDNSConfigOutput{}, err
			}
			if deps.Firewall != nil {
				refreshEgressPolicy(deps)
			}
			return nil, dnsConfigView(deps.DB), nil
		},
	)
}

func regAddFirewallTool[In, Out any](rc *regCtx, def *mcpsdk.Tool, handler mcpsdk.ToolHandlerFor[In, Out]) {
	if !rc.deps.ManagedServer {
		regAddTool(rc, permAdmin, def, handler)
		return
	}
	db := rc.deps.DB
	regAddTool(rc, permAdmin, def, func(ctx context.Context, req *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
		ref, ok := auth.OperatorFromContext(ctx)
		if !ok || db == nil || db.CheckOperatorPermission(ctx, ref, permAdmin) != nil {
			var out Out
			return nil, out, database.ErrOperatorDenied
		}
		return handler(ctx, req, in)
	})
}

func dnsConfigView(db *database.Database) GetDNSConfigOutput {
	cfg := firewall.LoadDNSConfig(db)
	out := GetDNSConfigOutput{
		Servers: cfg.Servers, Search: cfg.Search, Defaults: cfg.Defaults,
		Records: make([]DNSRecordView, 0, len(cfg.Records)),
	}
	for _, r := range cfg.Records {
		out.Records = append(out.Records, DNSRecordView{Host: r.Host, IP: r.IP})
	}
	return out
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
