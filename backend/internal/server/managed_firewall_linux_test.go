//go:build linux

package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
)

// newManagedFirewallFixture rebuilds the fixture's router over the same
// database with a real egress manager wired in. newManagedOpsFixture's tune
// callback only sees managedRouteDeps, and the manager needs the database, so
// the router is composed here rather than widening that callback for every
// caller.
func newManagedFirewallFixture(t *testing.T) *managedOpsFixture {
	t.Helper()
	f := newManagedOpsFixture(t)
	ops := f.runtime.routeDeps("/usr/local/bin/nsjail")
	ops.Firewall = firewall.NewManager(f.db, t.TempDir())
	router, err := managedRoutesWithAsync(f.db, f.reg, f.authority, f.runtime.pool, nil, nil,
		filepath.Dir(f.db.Path()), f.runtime.invoke, nil, nil, nil, ops)
	managedMust(t, err)
	f.router = router
	return f
}

type managedFirewallRoute struct {
	method, path, body string
	admin              int
}

// Every route is admin, as on the legacy router: one rule change widens or
// narrows what every egress function can reach. A read or write key must be
// refused before the handler sees the request.
func TestManagedFirewallRequiresAdminForEveryRoute(t *testing.T) {
	f := newManagedFirewallFixture(t)
	reader, _ := f.key("reader", `["read"]`)
	writer, _ := f.key("writer", `["read","write"]`)
	admin, _ := f.key("admin", `["read","write","admin"]`)

	seeded := f.expect(http.MethodPost, "/api/v1/firewall/rules", admin,
		`{"rule_type":"cidr","value":"10.9.0.0/16","label":"seed"}`, http.StatusCreated)
	var rule database.BlocklistRule
	managedMust(t, json.Unmarshal(seeded.Body.Bytes(), &rule))
	if rule.ID == 0 {
		t.Fatalf("seeded rule has no id: %s", seeded.Body.String())
	}
	rulePath := "/api/v1/firewall/rules/" + strconv.FormatInt(rule.ID, 10)

	routes := []managedFirewallRoute{
		{http.MethodGet, "/api/v1/firewall/rules", "", http.StatusOK},
		{http.MethodPost, "/api/v1/firewall/rules", `{"rule_type":"cidr","value":"10.8.0.0/16"}`, http.StatusCreated},
		{http.MethodPut, rulePath, `{"label":"changed"}`, http.StatusOK},
		{http.MethodGet, "/api/v1/firewall/status", "", http.StatusOK},
		{http.MethodPost, "/api/v1/firewall/resolve", "", http.StatusOK},
		{http.MethodGet, "/api/v1/firewall/dns", "", http.StatusOK},
		{http.MethodPut, "/api/v1/firewall/dns", `{"servers":["1.1.1.1"],"search":"corp.local"}`, http.StatusOK},
		{http.MethodDelete, rulePath, "", http.StatusOK},
	}
	for _, route := range routes {
		for name, bearer := range map[string]string{"": "", "reader": reader, "writer": writer} {
			if rec := f.call(route.method, route.path, bearer, route.body); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with %q key: got %d, want 401/403", route.method, route.path, name, rec.Code)
			}
		}
		f.expect(route.method, route.path, admin, route.body, route.admin)
	}
	// The first check on each route must be an unauthenticated one, and the
	// reader/writer refusals must be 403 rather than a login challenge.
	if rec := f.call(http.MethodGet, "/api/v1/firewall/rules", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key: got %d want 401", rec.Code)
	}
	for _, bearer := range []string{reader, writer} {
		if rec := f.call(http.MethodGet, "/api/v1/firewall/rules", bearer, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin key: got %d want 403", rec.Code)
		}
	}
}

// An unmounted surface must 404 for an admin too: answering with an empty rule
// table would read as "nothing is blocked" from a manager that does not exist.
func TestManagedFirewallUnmountedWithoutManager(t *testing.T) {
	f := newManagedOpsFixture(t)
	admin, _ := f.key("admin", `["read","write","admin"]`)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/firewall/rules"},
		{http.MethodPost, "/api/v1/firewall/rules"},
		{http.MethodPut, "/api/v1/firewall/rules/1"},
		{http.MethodDelete, "/api/v1/firewall/rules/1"},
		{http.MethodGet, "/api/v1/firewall/status"},
		{http.MethodPost, "/api/v1/firewall/resolve"},
		{http.MethodGet, "/api/v1/firewall/dns"},
		{http.MethodPut, "/api/v1/firewall/dns"},
	} {
		f.expect(route.method, route.path, admin, "{}", http.StatusNotFound)
	}
}

func TestManagedFirewallRuleLifecyclePublishesGeneration(t *testing.T) {
	f := newManagedFirewallFixture(t)
	admin, _ := f.key("admin", `["read","write","admin"]`)

	var first struct {
		Status firewall.Snapshot `json:"status"`
	}
	managedMust(t, json.Unmarshal(f.expect(http.MethodPost, "/api/v1/firewall/resolve", admin, "", http.StatusOK).Body.Bytes(), &first))
	if !first.Status.Enforced || first.Status.PolicyGeneration == "" {
		t.Fatalf("no policy after resolve: %+v", first.Status)
	}

	created := f.expect(http.MethodPost, "/api/v1/firewall/rules", admin,
		`{"rule_type":"cidr","value":"10.9.0.0/16","label":"lifecycle"}`, http.StatusCreated)
	var rule database.BlocklistRule
	managedMust(t, json.Unmarshal(created.Body.Bytes(), &rule))

	var list struct {
		Rules  []database.BlocklistRule `json:"rules"`
		Status firewall.Snapshot        `json:"status"`
	}
	managedMust(t, json.Unmarshal(f.expect(http.MethodGet, "/api/v1/firewall/rules", admin, "", http.StatusOK).Body.Bytes(), &list))
	found := false
	for _, row := range list.Rules {
		found = found || row.ID == rule.ID
	}
	if !found {
		t.Fatalf("created rule %d missing from list", rule.ID)
	}
	if !list.Status.Enforced || list.Status.PolicyGeneration == "" || list.Status.PolicyGeneration == first.Status.PolicyGeneration {
		t.Fatalf("rule change did not publish a new generation: before %q after %q",
			first.Status.PolicyGeneration, list.Status.PolicyGeneration)
	}

	// The mutation must be in force, not merely stored.
	var status firewall.Snapshot
	managedMust(t, json.Unmarshal(f.expect(http.MethodGet, "/api/v1/firewall/status", admin, "", http.StatusOK).Body.Bytes(), &status))
	if !status.Enforced || status.PolicyGeneration != list.Status.PolicyGeneration {
		t.Fatalf("status disagrees with the list: %+v vs %+v", status, list.Status)
	}

	f.expect(http.MethodDelete, "/api/v1/firewall/rules/"+strconv.FormatInt(rule.ID, 10), admin, "", http.StatusOK)
	managedMust(t, json.Unmarshal(f.expect(http.MethodGet, "/api/v1/firewall/rules", admin, "", http.StatusOK).Body.Bytes(), &list))
	for _, row := range list.Rules {
		if row.ID == rule.ID {
			t.Fatalf("deleted rule %d still listed", rule.ID)
		}
	}
}

func TestManagedFirewallDNSRoundTrip(t *testing.T) {
	f := newManagedFirewallFixture(t)
	admin, _ := f.key("admin", `["read","write","admin"]`)

	f.expect(http.MethodPut, "/api/v1/firewall/dns", admin,
		`{"servers":["1.1.1.1"],"search":"corp.local","records":[{"host":"db.corp.local","ip":"10.1.1.5"}]}`, http.StatusOK)
	var cfg firewall.DNSConfig
	managedMust(t, json.Unmarshal(f.expect(http.MethodGet, "/api/v1/firewall/dns", admin, "", http.StatusOK).Body.Bytes(), &cfg))
	if len(cfg.Servers) != 1 || cfg.Servers[0] != "1.1.1.1" || cfg.Search != "corp.local" ||
		len(cfg.Records) != 1 || cfg.Records[0].Host != "db.corp.local" || cfg.Records[0].IP != "10.1.1.5" {
		t.Fatalf("dns round trip lost data: %+v", cfg)
	}
	if len(cfg.Defaults) == 0 {
		t.Fatal("dns config did not expose the shipped defaults")
	}
}
