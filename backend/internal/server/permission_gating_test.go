package server

import "testing"

// TestSecurityPolicySurfacesRequireAdmin — both of these mint or control
// instance-wide security state and were reachable with a "write"-scoped key.
//
// Channels: a channel token is a long-lived bearer credential whose tools
// route into invokeFunction, which bypasses the function's auth_mode. At
// "write" a CI key deliberately scoped without "invoke" could create a
// channel over a payments function and call it unsigned — issuing itself a
// credential that outranked it. Expiry is opt-in, so it also outlived the
// key that minted it.
//
// Firewall: egress blocklists and the DNS every sandbox resolves through.
func TestSecurityPolicySurfacesRequireAdmin(t *testing.T) {
	cases := []struct{ method, path string }{
		{"POST", "/api/v1/channels"},
		{"GET", "/api/v1/channels"},
		{"PATCH", "/api/v1/channels/abc"},
		{"DELETE", "/api/v1/channels/abc"},
		{"PUT", "/api/v1/firewall/dns"},
		{"POST", "/api/v1/firewall/rules"},
		{"GET", "/api/v1/firewall/status"},
		{"DELETE", "/api/v1/firewall/rules/1"},
	}
	for _, c := range cases {
		if got := requiredPermission(c.method, c.path); got != "admin" {
			t.Errorf("requiredPermission(%s %s) = %q, want admin", c.method, c.path, got)
		}
	}
}

// TestOrdinaryResourcesKeepTheirPermissions — the gating change must not
// escalate everything else.
func TestOrdinaryResourcesKeepTheirPermissions(t *testing.T) {
	cases := []struct{ method, path, want string }{
		{"GET", "/api/v1/functions", "read"},
		{"POST", "/api/v1/functions", "write"},
		{"GET", "/api/v1/executions", "read"},
		{"DELETE", "/api/v1/routes/1", "write"},
		{"GET", "/api/v1/keys", "admin"},
		{"POST", "/api/v1/backup", "admin"},
	}
	for _, c := range cases {
		if got := requiredPermission(c.method, c.path); got != c.want {
			t.Errorf("requiredPermission(%s %s) = %q, want %q",
				c.method, c.path, got, c.want)
		}
	}
}
