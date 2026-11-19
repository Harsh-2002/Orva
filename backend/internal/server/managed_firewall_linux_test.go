//go:build linux

package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedFirewallPublicationPreservesInstalledFactories(t *testing.T) {
	f := newManagedOpsFixture(t)
	ctx := t.Context()
	limits := artifactfs.Limits{Generations: 4, Leases: 8, Staging: 1, Entries: 8,
		PathBytes: 256, LinkBytes: 256, Bytes: 8192, FileBytes: 1024, ManifestBytes: 8192}
	store, err := artifactfs.New(t.TempDir(), limits)
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, store.Close()) })
	runtime := sealFirewallFixture(t, store, limits, artifactfs.KindRuntime, "node", "runtime")
	fw := firewall.NewManager(f.db, t.TempDir())
	r := managedRuntime{hub: f.runtime.hub, pool: pool.NewManager(
		pool.ManagerConfig{ManagedAuthority: f.authority},
		pool.SandboxTemplate{ManagedSpawnReady: func() bool { return false }, EgressPolicy: fw.EgressPolicy}, f.db, f.reg, nil)}
	defer func() { managedMust(t, r.pool.Shutdown(context.Background())) }()
	factories := make(map[string]*workloadspawn.Factory)
	for _, mode := range []string{"none", "egress"} {
		owner := ids.New()
		code := sealFirewallFixture(t, store, limits, artifactfs.KindCode, owner, "handler.js")
		factory, err := workloadspawn.NewFactory(ctx, &secrets.PreparedArtifactSnapshot{
			Function: &database.Function{ID: owner, Name: mode, Runtime: "node", Entrypoint: "handler.js",
				NetworkMode: mode, Version: 1, Status: "active", CodeHash: strings.Repeat("a", 64),
				MemoryMB: 64, CPUs: 1, TimeoutMS: 1000, AuthMode: "none", ConcurrencyPolicy: "queue"},
			Artifact: database.WorkloadArtifactRecord{OwnerID: owner, Generation: ids.New(), Revision: 1, Epoch: 1},
		}, store, code, store, runtime, workloadspawn.FactoryLimits{
			MaxPreparedPlans: 1, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20,
		})
		managedMust(t, err)
		t.Cleanup(factory.Close)
		managedMust(t, r.pool.InstallFactory(ctx, factory))
		factories[mode] = factory
	}
	assertInstalled := func() {
		t.Helper()
		for mode, factory := range factories {
			owner := factory.Owner()
			if r.pool.CurrentFactory(owner) != factory || factory.Retired() ||
				r.pool.CurrentArtifact(owner) != factory.ArtifactRevision() {
				t.Fatalf("firewall publication changed the installed %s Factory", mode)
			}
		}
	}

	r.startFirewall(ctx, fw)
	defer func() { managedMust(t, fw.Stop(context.Background())) }()
	_, initial, err := fw.EgressPolicy()
	managedMust(t, err)
	assertInstalled()

	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	rec := &firewallEventRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{}, 2)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.runtime.hub.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(requestCtx, http.MethodGet, "/api/v1/events", nil))
	}()
	defer func() { cancel(); <-done }()
	for f.runtime.hub.SubscriberCount() == 0 {
		select {
		case <-requestCtx.Done():
			t.Fatal("event subscriber did not start")
		case <-time.After(time.Millisecond):
		}
	}

	_, err = f.db.InsertCustomBlocklistRule("cidr", "192.0.2.77/32", "fixture", true)
	managedMust(t, err)
	managedMust(t, fw.ForceRefresh())
	_, updated, err := fw.EgressPolicy()
	managedMust(t, err)
	if initial == "" || updated == "" || updated == initial {
		t.Fatalf("expected a new compiled generation, got %q → %q", initial, updated)
	}
	assertInstalled()
	for range 2 {
		select {
		case <-rec.flushed:
		case <-requestCtx.Done():
			t.Fatal("expected firewall.policy_applied event")
		}
	}
	cancel()
	<-done
	want := fmt.Sprintf("event: firewall.policy_applied\ndata: {\"generation\":%q,\"retired_pools\":1}\n\n", updated)
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("expected one egress rotation and no offline rotation, got %q", rec.Body.String())
	}
}

type firewallEventRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func (r *firewallEventRecorder) Flush() {
	r.ResponseRecorder.Flush()
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}

func sealFirewallFixture(t *testing.T, store *artifactfs.Store, limits artifactfs.Limits, kind artifactfs.Kind, owner, filename string) string {
	t.Helper()
	stage, err := store.Begin(t.Context(), 1024)
	managedMust(t, err)
	var generation string
	t.Cleanup(func() {
		if generation == "" {
			managedMust(t, stage.Abort())
			return
		}
		managedMust(t, store.Retire(generation))
		managedMust(t, store.Reclaim(generation))
	})
	root, err := stage.Path()
	managedMust(t, err)
	body := []byte("fixture only")
	managedMust(t, os.Chmod(root, 0700))
	managedMust(t, os.WriteFile(filepath.Join(root, filename), body, 0600))
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	manifest := artifactfs.Manifest{Version: 1, Entries: []artifactfs.Entry{
		{Path: ".", Type: artifactfs.TypeDirectory, Mode: 0700, UID: uid, GID: gid},
		{Path: filename, Type: artifactfs.TypeFile, Mode: 0600, UID: uid, GID: gid,
			Size: int64(len(body)), SHA256: fmt.Sprintf("%x", sha256.Sum256(body))},
	}}
	digest, err := artifactfs.Digest(manifest, limits)
	managedMust(t, err)
	generation, err = stage.Seal(t.Context(), kind, owner, manifest, digest)
	managedMust(t, err)
	return generation
}

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
