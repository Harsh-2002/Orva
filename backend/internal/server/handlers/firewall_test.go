package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// errCode pulls error.code out of the standard envelope.
func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v (raw: %s)", err, body)
	}
	return env.Error.Code
}

func postRule(t *testing.T, h *FirewallHandler, body map[string]any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/firewall/rules", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.Create(w, req)
	return w.Code, w.Body.Bytes()
}

func putRule(t *testing.T, h *FirewallHandler, id int64, body map[string]any) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/api/v1/firewall/rules/%d", id), bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.Update(w, req)
	return w.Code, w.Body.Bytes()
}

// TestCreateFirewallRuleRejectsWildcard locks the product decision: a wildcard
// can never be compiled into a packet policy, so accepting one would store a
// rule that reads as armed and blocks nothing. Both the explicit rule_type and
// the '*.'-prefix auto-detect path must refuse.
func TestCreateFirewallRuleRejectsWildcard(t *testing.T) {
	db := newTestDB(t)
	h := &FirewallHandler{DB: db}

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"explicit rule_type", map[string]any{"rule_type": "wildcard", "value": "*.corp.internal"}},
		{"auto-detected from value", map[string]any{"value": "*.metadata.google.internal"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := postRule(t, h, tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", code, body)
			}
			if got := errCode(t, body); got != "VALIDATION" {
				t.Errorf("code = %q, want VALIDATION", got)
			}
			if !bytes.Contains(body, []byte("IP/CIDR")) {
				t.Errorf("message should explain that the policy matches IP/CIDR: %s", body)
			}
		})
	}

	// Nothing may have landed in the table.
	rules, err := db.ListBlocklistRules()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.Kind == database.BlocklistKindCustom {
			t.Fatalf("a refused wildcard was still inserted: %+v", r)
		}
	}
}

// TestCreateFirewallRuleAcceptsCIDRAndHostname guards against the wildcard
// refusal being over-broad.
func TestCreateFirewallRuleAcceptsCIDRAndHostname(t *testing.T) {
	h := &FirewallHandler{DB: newTestDB(t)}

	for _, tc := range []struct {
		name     string
		body     map[string]any
		wantType string
	}{
		{"cidr", map[string]any{"value": "192.168.7.0/24"}, database.BlocklistTypeCIDR},
		{"bare ip becomes /32", map[string]any{"value": "10.1.2.3"}, database.BlocklistTypeCIDR},
		{"hostname", map[string]any{"value": "vendor.example.com"}, database.BlocklistTypeHostname},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := postRule(t, h, tc.body)
			if code != http.StatusCreated {
				t.Fatalf("status = %d, want 201 (body: %s)", code, body)
			}
			var rule database.BlocklistRule
			if err := json.Unmarshal(body, &rule); err != nil {
				t.Fatalf("decode: %v (raw: %s)", err, body)
			}
			if rule.RuleType != tc.wantType {
				t.Errorf("rule_type = %q, want %q", rule.RuleType, tc.wantType)
			}
		})
	}
}

func TestCreateFirewallRuleRejectsUnenforceableTargets(t *testing.T) {
	db := newTestDB(t)
	h := &FirewallHandler{DB: db}

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"unspecified IPv4 CIDR", map[string]any{"rule_type": "cidr", "value": "0.0.0.0/0"}},
		{"unspecified IPv6 CIDR", map[string]any{"rule_type": "cidr", "value": "::/0"}},
		{"over-wide CIDR", map[string]any{"rule_type": "cidr", "value": "10.0.0.0/64"}},
		{"v4-mapped IPv6", map[string]any{"rule_type": "cidr", "value": "::ffff:192.0.2.1"}},
		{"literal IP as hostname", map[string]any{"rule_type": "hostname", "value": "0.0.0.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := postRule(t, h, tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", code, body)
			}
			if got := errCode(t, body); got != "VALIDATION" {
				t.Errorf("code = %q, want VALIDATION", got)
			}
		})
	}

	rules, err := db.ListBlocklistRules()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.Kind == database.BlocklistKindCustom {
			t.Fatalf("a refused target was still inserted: %+v", rule)
		}
	}
}

func TestUpdateLegacyUnenforceableRule(t *testing.T) {
	db := newTestDB(t)
	h := &FirewallHandler{DB: db}
	legacy, err := db.InsertCustomBlocklistRule(
		database.BlocklistTypeCIDR, "0.0.0.0/0", "legacy", false)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("enable refused without partial mutation", func(t *testing.T) {
		code, body := putRule(t, h, legacy.ID, map[string]any{
			"enabled": true,
			"value":   "::/0",
		})
		if code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body: %s)", code, body)
		}
		row, err := db.GetBlocklistRule(legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.Enabled || row.Value != "0.0.0.0/0" {
			t.Fatalf("refused update partially mutated row: %+v", row)
		}
	})

	t.Run("repair to valid target is allowed", func(t *testing.T) {
		code, body := putRule(t, h, legacy.ID, map[string]any{
			"enabled": true,
			"value":   "203.0.113.0/24",
		})
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", code, body)
		}
		row, err := db.GetBlocklistRule(legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !row.Enabled || row.Value != "203.0.113.0/24" {
			t.Fatalf("valid repair did not apply: %+v", row)
		}
	})
}

func TestPutDNSRejectsUnspecifiedResolvers(t *testing.T) {
	db := newTestDB(t)
	h := &FirewallHandler{DB: db}
	if err := db.SetSystemConfig("dns_servers", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}

	for _, resolver := range []string{"0.0.0.0", "::", "::ffff:192.0.2.1"} {
		t.Run(resolver, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"servers": []string{resolver}})
			req := httptest.NewRequest(http.MethodPut, "/api/v1/firewall/dns", bytes.NewReader(raw))
			w := httptest.NewRecorder()
			h.PutDNS(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			if got := db.GetSystemConfigString("dns_servers", ""); got != "1.1.1.1" {
				t.Fatalf("refused resolver overwrote stored config: %q", got)
			}
		})
	}
}

// TestUpdateLegacyWildcardRule covers the deliberate asymmetry for rows that
// predate the refusal: they are never rewritten behind the operator's back, so
// enabling and editing are closed off while disabling stays open.
func TestUpdateLegacyWildcardRule(t *testing.T) {
	db := newTestDB(t)
	h := &FirewallHandler{DB: db}

	// Seed through the DAO — the handler no longer has a path that creates one.
	legacy, err := db.InsertCustomBlocklistRule(
		database.BlocklistTypeWildcard, "*.legacy.internal", "predates the refusal", true)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("enable refused", func(t *testing.T) {
		// Flip it off first so the enable attempt is a real transition.
		if err := db.SetBlocklistRuleEnabled(legacy.ID, false); err != nil {
			t.Fatal(err)
		}
		code, body := putRule(t, h, legacy.ID, map[string]any{"enabled": true})
		if code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body: %s)", code, body)
		}
		if got := errCode(t, body); got != "VALIDATION" {
			t.Errorf("code = %q, want VALIDATION", got)
		}
		row, err := db.GetBlocklistRule(legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.Enabled {
			t.Error("refused enable still wrote enabled=1")
		}
	})

	t.Run("value edit refused", func(t *testing.T) {
		code, body := putRule(t, h, legacy.ID, map[string]any{"value": "*.other.internal"})
		if code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body: %s)", code, body)
		}
		row, err := db.GetBlocklistRule(legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.Value != "*.legacy.internal" {
			t.Errorf("value = %q, want it untouched", row.Value)
		}
	})

	t.Run("disable allowed", func(t *testing.T) {
		if err := db.SetBlocklistRuleEnabled(legacy.ID, true); err != nil {
			t.Fatal(err)
		}
		code, body := putRule(t, h, legacy.ID, map[string]any{"enabled": false})
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", code, body)
		}
		row, err := db.GetBlocklistRule(legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		if row.Enabled {
			t.Error("disable did not take: retiring a legacy wildcard must stay possible")
		}
	})
}

// TestUpdateRuleTypeToWildcardRefused: the same rule cannot be smuggled in by
// re-typing an existing CIDR row.
func TestUpdateRuleTypeToWildcardRefused(t *testing.T) {
	db := newTestDB(t)
	h := &FirewallHandler{DB: db}

	rule, err := db.InsertCustomBlocklistRule(
		database.BlocklistTypeCIDR, "203.0.113.0/24", "", true)
	if err != nil {
		t.Fatal(err)
	}
	code, body := putRule(t, h, rule.ID,
		map[string]any{"rule_type": "wildcard", "value": "*.example.com"})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", code, body)
	}
	row, err := db.GetBlocklistRule(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.RuleType != database.BlocklistTypeCIDR || row.Value != "203.0.113.0/24" {
		t.Errorf("row mutated: %+v", row)
	}
}

// TestFirewallStatusEndpoint checks the additive GET /firewall/status: it must
// report the backend and enforcement state, and must NOT answer with an empty
// snapshot when the manager is absent (that would read as "nothing blocked").
func TestFirewallStatusEndpoint(t *testing.T) {
	db := newTestDB(t)

	t.Run("no manager", func(t *testing.T) {
		h := &FirewallHandler{DB: db}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/status", nil)
		w := httptest.NewRecorder()
		h.Status(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body: %s)", w.Code, w.Body.String())
		}
		if got := errCode(t, w.Body.Bytes()); got != "FIREWALL_DISABLED" {
			t.Errorf("code = %q, want FIREWALL_DISABLED", got)
		}
	})

	t.Run("with manager", func(t *testing.T) {
		// Not Start()ed: no policy has been published, so this is exactly the
		// fail-closed state EGRESS_POLICY_UNAVAILABLE points operators at.
		mgr := firewall.NewManager(db, t.TempDir(), firewall.ControlPlane{Port: 8443})
		h := &FirewallHandler{DB: db, Manager: mgr}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/status", nil)
		w := httptest.NewRecorder()
		h.Status(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		var snap firewall.Snapshot
		if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
			t.Fatalf("decode: %v (raw: %s)", err, w.Body.String())
		}
		if snap.Backend != "nstun" {
			t.Errorf("backend = %q, want nstun", snap.Backend)
		}
		if snap.Enforced {
			t.Error("enforced = true with no published policy")
		}
		if snap.ControlPlane.Port != 8443 {
			t.Errorf("control_plane_allow.port = %d, want 8443", snap.ControlPlane.Port)
		}
	})
}

// TestInvokeErrorEgressPolicyFailsClosed: both spawn-refusal sentinels — the
// pool's (no compiled policy) and buildArgs' (no --config path) — must surface
// as one retryable 503 pointing at the status endpoint. A generic
// SANDBOX_ERROR here would hide a security-relevant refusal.
func TestInvokeErrorEgressPolicyFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"firewall.ErrPolicyUnavailable", firewall.ErrPolicyUnavailable},
		{"sandbox.ErrEgressPolicyMissing", sandbox.ErrEgressPolicyMissing},
		{"wrapped", fmt.Errorf("spawn worker: %w", firewall.ErrPolicyUnavailable)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, opts := invokeError(tc.err, nil, "req_1")
			if status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", status)
			}
			if opts.Code != "EGRESS_POLICY_UNAVAILABLE" {
				t.Errorf("code = %q, want EGRESS_POLICY_UNAVAILABLE", opts.Code)
			}
			if opts.RetryAfterS <= 0 {
				t.Errorf("retry_after_s = %d, want > 0", opts.RetryAfterS)
			}
			if !bytes.Contains([]byte(opts.Hint), []byte("/api/v1/firewall/status")) {
				t.Errorf("hint should point at the status endpoint, got %q", opts.Hint)
			}
		})
	}
}
