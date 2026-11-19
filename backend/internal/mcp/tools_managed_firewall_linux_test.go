//go:build linux

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/internal/ids"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestManagedFirewallToolsRequireLiveAdmin(t *testing.T) {
	db := newStatelessDatabase(t)
	deps := Deps{ManagedServer: true, DB: db}
	withoutManager := BuildAgentRegistry(deps, allPerms())
	deps.Firewall = firewall.NewManager(db, t.TempDir())
	reg := BuildAgentRegistry(deps, allPerms())
	handler := NewHandler(deps)

	type tier struct{ name, id, key, perms string }
	tiers := []tier{
		{"firewall-read", ids.New(), "orva_firewall_read_fixture", `["read"]`},
		{"firewall-write", ids.New(), "orva_firewall_write_fixture", `["write"]`},
		{"firewall-revoked", ids.New(), "orva_firewall_revoked_fixture", `["admin"]`},
	}
	for _, p := range tiers {
		seedStatelessAPIKey(t, db, p.key, p.id, p.name, p.perms)
	}
	// Use a retained registry after revocation, like a suspended Chat turn.
	if err := db.DeleteAPIKey(tiers[2].id); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"list_firewall_rules":      `{}`,
		"get_egress_policy_status": `{}`,
		"add_firewall_rule":        `{"value":"192.0.2.11/32"}`,
		"delete_firewall_rule":     `{"rule_id":1,"confirm":true}`,
		"get_dns_config":           `{}`,
		"set_dns_config":           `{"servers":["192.0.2.53"]}`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if withoutManager.Get(name) != nil || reg.Get(name) == nil {
				t.Fatal("manager did not gate registration")
			}
			if _, err := reg.Dispatch(t.Context(), name, []byte(args)); !errors.Is(err, database.ErrOperatorDenied) {
				t.Fatalf("missing operator: %v", err)
			}
			for _, p := range tiers {
				ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: p.id})
				if _, err := reg.Dispatch(ctx, name, []byte(args)); !errors.Is(err, database.ErrOperatorDenied) {
					t.Fatalf("%s retained registry dispatch: %v", p.name, err)
				}
				w := callManagedFirewallMCP(t, handler, p.key, name, args)
				if p.name == "firewall-revoked" {
					if w.Code != http.StatusUnauthorized {
						t.Fatalf("revoked MCP HTTP status %d", w.Code)
					}
				} else {
					res := decodeRPC(t, w)
					if res.Error == nil && !decodeResult[mcpsdk.CallToolResult](t, res).IsError {
						t.Fatalf("%s unexpectedly called tool", p.name)
					}
				}
			}
		})
	}
}

func TestManagedFirewallToolPublication(t *testing.T) {
	db := newStatelessDatabase(t)
	const key = "orva_firewall_admin_fixture"
	id := ids.New()
	seedStatelessAPIKey(t, db, key, id, "firewall-admin", `["admin"]`)
	ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: id})
	fw := firewall.NewManager(db, t.TempDir())
	var rotated []string
	fw.SetOnPolicyChange(func(gen string) { rotated = append(rotated, gen) })
	deps := Deps{ManagedServer: true, DB: db, Firewall: fw}
	reg := BuildAgentRegistry(deps, permSet{permAdmin: true})
	handler := NewHandler(deps)

	apply := func(name, args string) json.RawMessage {
		t.Helper()
		w := callManagedFirewallMCP(t, handler, key, name, args)
		result := decodeResult[mcpsdk.CallToolResult](t, decodeRPC(t, w))
		if result.IsError {
			for _, content := range result.Content {
				if text, ok := content.(*mcpsdk.TextContent); ok {
					t.Fatalf("%s failed: %s", name, text.Text)
				}
			}
			t.Fatalf("%s failed without a text result", name)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var rule FirewallRuleView
	if err := json.Unmarshal(apply("add_firewall_rule", `{"value":"192.0.2.31/32"}`), &rule); err != nil || rule.ID == 0 {
		t.Fatalf("add result: %+v, %v", rule, err)
	}
	first := fw.Snapshot()
	if !first.Enforced || first.PolicyGeneration == "" || !reflect.DeepEqual(rotated, []string{first.PolicyGeneration}) {
		t.Fatalf("manager did not publish and notify: %+v, %v", first, rotated)
	}
	var listed ListFirewallRulesOutput
	if err := json.Unmarshal(apply("list_firewall_rules", `{}`), &listed); err != nil || !slices.Contains(listed.Rules, rule) {
		t.Fatalf("list result: %+v, %v", listed, err)
	}
	if _, err := reg.Dispatch(ctx, "delete_firewall_rule", fmt.Appendf(nil, `{"rule_id":%d,"confirm":false}`, rule.ID)); err == nil {
		t.Fatal("delete did not require confirmation")
	}
	if _, err := reg.Dispatch(ctx, "delete_firewall_rule", fmt.Appendf(nil, `{"rule_id":%d,"confirm":true}`, rule.ID)); err != nil {
		t.Fatal(err)
	}
	var status firewall.Snapshot
	if err := json.Unmarshal(apply("get_egress_policy_status", `{}`), &status); err != nil ||
		!status.Enforced || status.PolicyGeneration == first.PolicyGeneration || status.PolicyGeneration != fw.Snapshot().PolicyGeneration {
		t.Fatalf("delete did not publish through the manager: %+v, %v", status, err)
	}

	apply("set_dns_config", `{"servers":["192.0.2.53"],"search":"site.test","records":[{"host":"db","ip":"192.0.2.20"}]}`)
	var dns GetDNSConfigOutput
	if err := json.Unmarshal(apply("get_dns_config", `{}`), &dns); err != nil ||
		!reflect.DeepEqual(dns.Servers, []string{"192.0.2.53"}) || dns.Search != "site.test" ||
		!reflect.DeepEqual(dns.Records, []DNSRecordView{{Host: "db", IP: "192.0.2.20"}}) {
		t.Fatalf("DNS result: %+v, %v", dns, err)
	}
	dnsGen := fw.Snapshot().PolicyGeneration
	if dnsGen == status.PolicyGeneration {
		t.Fatal("resolver change did not publish a generation")
	}
	durableDNS := func() [3]string {
		return [3]string{db.GetSystemConfigString("dns_servers", ""), db.GetSystemConfigString("dns_search", ""), db.GetSystemConfigString("dns_records", "")}
	}
	current := durableDNS()
	for name, args := range map[string]string{
		"resolver": `{"servers":["0.0.0.0"],"search":"other.test"}`,
		"search":   `{"servers":["192.0.2.54"],"search":"other.test\ninvalid"}`,
		"record":   `{"servers":["192.0.2.54"],"search":"other.test","records":[{"host":"invalid host","ip":"192.0.2.21"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := callManagedFirewallMCP(t, handler, key, "set_dns_config", args)
			if !decodeResult[mcpsdk.CallToolResult](t, decodeRPC(t, w)).IsError {
				t.Fatal("invalid DNS was accepted")
			}
			if after := durableDNS(); after != current || fw.Snapshot().PolicyGeneration != dnsGen {
				t.Fatalf("invalid request changed durable DNS or manager generation: %q -> %q", current, after)
			}
		})
	}
}

func callManagedFirewallMCP(t *testing.T, handler http.Handler, key, name, args string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(rpcBody(t, 1, "tools/call", map[string]any{
		"_meta": newProtocolMeta(true), "name": name, "arguments": json.RawMessage(args),
	})))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
	req.Header.Set(hdrMethod, "tools/call")
	req.Header.Set("Mcp-Name", name)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}
