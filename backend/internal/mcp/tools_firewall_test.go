package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestAddFirewallRuleRefusesWildcard: the MCP surface must refuse a wildcard
// exactly like POST /api/v1/firewall/rules. Both the explicit rule_type and the
// '*.'-prefix auto-detect path are covered; the refusal fires before any DB
// call, which is why an empty Deps is enough here.
func TestAddFirewallRuleRefusesWildcard(t *testing.T) {
	reg := BuildAgentRegistry(Deps{}, allPerms())

	for _, tc := range []struct {
		name string
		args string
	}{
		{"explicit rule_type", `{"value":"*.corp.internal","rule_type":"wildcard"}`},
		{"auto-detected from value", `{"value":"*.metadata.google.internal"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := reg.Dispatch(context.Background(), "add_firewall_rule", []byte(tc.args))
			if err == nil {
				t.Fatal("wildcard was accepted; it can never be compiled into a packet rule")
			}
			if !strings.Contains(err.Error(), "IP/CIDR") {
				t.Errorf("error should explain that the policy matches IP/CIDR, got: %v", err)
			}
		})
	}
}

// TestEgressPolicyStatusToolRegistered pins the read-only status tool: an agent
// asking "is this actually blocked?" must have a tool that answers from the
// compiled policy, not from the stored rule table.
func TestEgressPolicyStatusToolRegistered(t *testing.T) {
	reg := BuildAgentRegistry(Deps{}, allPerms())

	tool := reg.Get("get_egress_policy_status")
	if tool == nil {
		t.Fatal("get_egress_policy_status missing from the agent registry")
	}
	if !tool.ReadOnly {
		t.Error("status tool must be marked read-only")
	}
	if tool.Destructive {
		t.Error("status tool must not be marked destructive")
	}
	if tool.Group != "firewall" {
		t.Errorf("group = %q, want firewall", tool.Group)
	}

	// A read-only principal still gets nothing: the whole firewall group is
	// admin-gated, so this must not become a read-perm information leak.
	if BuildAgentRegistry(Deps{}, permSet{permRead: true}).Get("get_egress_policy_status") != nil {
		t.Error("status tool leaked to a read-only principal")
	}

	// Without a manager it must error rather than answer "nothing is blocked".
	if _, err := reg.Dispatch(context.Background(), "get_egress_policy_status", nil); err == nil {
		t.Error("expected an error when the egress policy manager is absent")
	}
}
