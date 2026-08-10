package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// Manager-level behaviour: what an operator observes through the API, and the
// two lifecycle rules the compiler alone cannot enforce — DNS generation must
// not depend on policy success, and a failed recompile must never degrade to
// NSTUN's default-allow.

func newManagerTestDB(t *testing.T) *database.Database {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "firewall.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// uncompilableCP is a control plane with no address. compile refuses it rather
// than guessing, which is the cheapest way to drive the failure paths here.
func uncompilableCP() ControlPlane { return ControlPlane{Port: 8443} }

func TestStartWritesDNSFilesEvenWhenNoPolicyCompiles(t *testing.T) {
	// Regression guard: the nftables implementation gated the whole refresh
	// loop on nft availability and returned early, which silently stopped
	// operator DNS edits from ever reaching sandboxes. DNS generation must be
	// independent of whether a policy compiled.
	db := newManagerTestDB(t)
	if err := db.SetSystemConfig("dns_servers", "9.9.9.9"); err != nil {
		t.Fatalf("set dns_servers: %v", err)
	}
	if err := db.SetSystemConfig("dns_records", "internal-api 10.10.0.7\n"); err != nil {
		t.Fatalf("set dns_records: %v", err)
	}

	dataDir := t.TempDir()
	m := NewManager(db, dataDir, uncompilableCP())
	m.Start(context.Background())
	t.Cleanup(func() {
		if err := m.Stop(context.Background()); err != nil {
			t.Errorf("stop manager: %v", err)
		}
	})

	resolv, err := os.ReadFile(ResolvConfPath(dataDir))
	if err != nil {
		t.Fatalf("resolv.conf was not written when the policy failed to compile: %v", err)
	}
	if !strings.Contains(string(resolv), "nameserver 9.9.9.9") {
		t.Errorf("operator resolver missing from resolv.conf:\n%s", resolv)
	}
	hosts, err := os.ReadFile(HostsFilePath(dataDir))
	if err != nil {
		t.Fatalf("hosts file was not written when the policy failed to compile: %v", err)
	}
	if !strings.Contains(string(hosts), "10.10.0.7   internal-api") {
		t.Errorf("operator host record missing from hosts file:\n%s", hosts)
	}

	// The failure itself must be visible, and must not read as "nothing is
	// blocked": enforced=false is how the API says no policy is in force.
	snap := m.Snapshot()
	if snap.Enforced {
		t.Error("snapshot reports enforcement with no compiled policy")
	}
	if snap.LastCompileError == "" {
		t.Error("snapshot hides the compile failure")
	}
	if snap.PolicyStale {
		t.Error("policy_stale must mean 'a known-good policy is still in force', not 'never compiled'")
	}
}

func TestPolicyAccessorsFailClosedBeforeFirstCompile(t *testing.T) {
	// An egress spawn must abort rather than run unfiltered: NSTUN allows
	// anything no rule matches, so a missing policy is not a strict default.
	m := NewManager(newManagerTestDB(t), t.TempDir(), uncompilableCP())

	if _, err := m.CurrentPolicy(); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("CurrentPolicy: want ErrPolicyUnavailable, got %v", err)
	}
	path, gen, err := m.EgressPolicy()
	if !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("EgressPolicy: want ErrPolicyUnavailable, got %v", err)
	}
	if path != "" || gen != "" {
		t.Fatalf("EgressPolicy must not hand out a path on failure, got %q/%q", path, gen)
	}
}

func TestFailedRecompileKeepsLastKnownGoodPolicy(t *testing.T) {
	dataDir := t.TempDir()
	m := NewManager(newManagerTestDB(t), dataDir, testCP())

	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("initial compile: %v", err)
	}
	good, err := m.CurrentPolicy()
	if err != nil {
		t.Fatalf("CurrentPolicy after a successful compile: %v", err)
	}
	if good.Gen == "" {
		t.Fatal("published policy has no generation")
	}
	if _, err := os.Stat(good.Path); err != nil {
		t.Fatalf("published generation file is missing: %v", err)
	}

	// Break compilation the same way a lost control-plane probe would.
	m.cp = uncompilableCP()
	if err := m.ForceRefresh(); err == nil {
		t.Fatal("expected the recompile to fail")
	}

	stale, err := m.CurrentPolicy()
	if err != nil {
		t.Fatalf("a failed recompile must keep the last known-good policy, got %v", err)
	}
	if stale.Gen != good.Gen {
		t.Fatalf("policy generation changed on a failed recompile: %s -> %s", good.Gen, stale.Gen)
	}
	if _, err := os.Stat(stale.Path); err != nil {
		t.Fatalf("in-use generation file was removed: %v", err)
	}
	path, gen, err := m.EgressPolicy()
	if err != nil || gen != good.Gen || path != good.Path {
		t.Fatalf("spawns must keep loading the known-good policy, got %q/%q err=%v", path, gen, err)
	}

	snap := m.Snapshot()
	if !snap.Enforced || snap.PolicyGeneration != good.Gen {
		t.Errorf("snapshot lost the in-force policy: enforced=%v gen=%q", snap.Enforced, snap.PolicyGeneration)
	}
	if !snap.PolicyStale {
		t.Error("policy_stale must be set while a recompile is failing")
	}
	if snap.LastCompileError == "" {
		t.Error("snapshot must name the recompile failure")
	}
}

func TestPolicyChangeCallbackFiresOnlyOnGenerationChange(t *testing.T) {
	// The poll loop recompiles every 10s. Firing on every recompile rather
	// than on every *change* would recycle warm egress workers continuously.
	db := newManagerTestDB(t)
	m := NewManager(db, t.TempDir(), testCP())

	var fired []string
	m.SetOnPolicyChange(func(gen string) { fired = append(fired, gen) })

	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("initial compile: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("first published generation must fire the callback, got %d calls", len(fired))
	}
	first := fired[0]

	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("identical recompile: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("an identical recompile must not recycle pools, got %d calls", len(fired))
	}

	if _, err := db.InsertCustomBlocklistRule(database.BlocklistTypeCIDR, "203.0.113.0/24", "manager-test", true); err != nil {
		t.Fatalf("insert rule: %v", err)
	}
	// The 60s recycle rate-limit is a separate concern (asserted below in
	// TestCoalescedPolicyChangeIsDrainedAfterTheWindow); clear it so this test
	// observes the change/no-change decision on its own.
	m.lastRetire = time.Time{}

	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("recompile after a rule change: %v", err)
	}
	if len(fired) != 2 {
		t.Fatalf("a changed policy must fire the callback, got %d calls", len(fired))
	}
	if fired[1] == first {
		t.Fatalf("generation did not change after a rule edit: %s", fired[1])
	}
}

func TestCoalescedPolicyChangeIsDrainedAfterTheWindow(t *testing.T) {
	// A flapping DNS answer must not become a cold-start machine gun: a change
	// inside the rate-limit window is published immediately (so new spawns are
	// correct) and the recycle is deferred to the window boundary.
	db := newManagerTestDB(t)
	m := NewManager(db, t.TempDir(), testCP())

	var fired []string
	m.SetOnPolicyChange(func(gen string) { fired = append(fired, gen) })

	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("initial compile: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("first generation must fire, got %d calls", len(fired))
	}

	if _, err := db.InsertCustomBlocklistRule(database.BlocklistTypeCIDR, "198.51.100.0/24", "manager-test", true); err != nil {
		t.Fatalf("insert rule: %v", err)
	}
	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("recompile after a rule change: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("a change inside the rate-limit window must be coalesced, got %d calls", len(fired))
	}
	// Published regardless — the recycle is what waits, not the policy.
	changed, err := m.CurrentPolicy()
	if err != nil {
		t.Fatalf("CurrentPolicy: %v", err)
	}
	if changed.Gen == fired[0] {
		t.Fatal("coalescing must not delay publishing the new generation")
	}

	// Simulate the window elapsing; the poll loop calls this on every tick.
	m.lastRetire = time.Now().Add(-2 * minRetireInterval)
	m.drainPendingRetire()
	if len(fired) != 2 {
		t.Fatalf("coalesced recycle was never drained, got %d calls", len(fired))
	}
	if fired[1] != changed.Gen {
		t.Fatalf("drained recycle carried generation %s, want %s", fired[1], changed.Gen)
	}
}

func TestSnapshotReportsNstunBackendAndNoNftablesKey(t *testing.T) {
	m := NewManager(newManagerTestDB(t), t.TempDir(), testCP())
	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("compile: %v", err)
	}

	snap := m.Snapshot()
	if snap.Backend != "nstun" {
		t.Errorf("backend = %q, want %q", snap.Backend, "nstun")
	}
	if len(snap.PolicyGeneration) != 16 {
		t.Errorf("policy_generation = %q, want 16 hex chars", snap.PolicyGeneration)
	}
	if snap.PolicyRuleCounts.Allow < 1 {
		t.Errorf("policy must carry at least the control-plane allow, got %+v", snap.PolicyRuleCounts)
	}
	if snap.PolicyRuleCounts.V4+snap.PolicyRuleCounts.V6 !=
		snap.PolicyRuleCounts.Allow+snap.PolicyRuleCounts.Reject {
		t.Errorf("rule counts do not add up: %+v", snap.PolicyRuleCounts)
	}
	if len(snap.ControlPlane.Addrs) == 0 || snap.ControlPlane.Port != 8443 {
		t.Errorf("control_plane_allow must show what is permitted, got %+v", snap.ControlPlane)
	}

	// The wire shape matters as much as the struct: the dashboard and the MCP
	// tools read this JSON. nftables_available described a mechanism that no
	// longer exists and was dropped deliberately, with no compat alias.
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if _, ok := wire["nftables_available"]; ok {
		t.Errorf("nftables_available must not be reported: %s", raw)
	}
	for _, key := range []string{
		"backend", "enforced", "policy_generation", "policy_rule_counts",
		"policy_stale", "control_plane_allow", "ipv4", "ipv6", "hostname_map",
	} {
		if _, ok := wire[key]; !ok {
			t.Errorf("snapshot is missing %q: %s", key, raw)
		}
	}
}

func TestSnapshotReportsStoredWildcardRowAsUnenforced(t *testing.T) {
	// The API refuses new wildcards, but a legacy row is left exactly as the
	// operator stored it: excluded from the policy, reported as unenforced, and
	// never silently rewritten to enabled=0 behind their back.
	db := newManagerTestDB(t)
	rule, err := db.InsertCustomBlocklistRule(database.BlocklistTypeWildcard, "*.corp.internal", "legacy", true)
	if err != nil {
		t.Fatalf("insert wildcard row: %v", err)
	}

	m := NewManager(db, t.TempDir(), testCP())
	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("compile: %v", err)
	}

	snap := m.Snapshot()
	var found *UnenforcedRule
	for i := range snap.Unenforced {
		if snap.Unenforced[i].ID == rule.ID {
			found = &snap.Unenforced[i]
		}
	}
	if found == nil {
		t.Fatalf("stored wildcard is not reported as unenforced: %+v", snap.Unenforced)
	}
	if found.Value != "*.corp.internal" || found.Reason == "" {
		t.Errorf("unenforced entry must name the rule and the reason, got %+v", *found)
	}
	for _, blocked := range snap.IPv4 {
		if strings.Contains(blocked, "corp") {
			t.Errorf("wildcard leaked into the effective blocklist: %v", snap.IPv4)
		}
	}

	stored, err := db.GetBlocklistRule(rule.ID)
	if err != nil {
		t.Fatalf("re-read rule: %v", err)
	}
	if !stored.Enabled {
		t.Error("the manager must never rewrite a stored rule's enabled flag")
	}
}

// TestWildcardAddedAfterFirstCompileIsStillReportedUnenforced is the
// regression guard for a bug live testing found.
//
// A wildcard row contributes no rules, so the rendered policy — and therefore
// the generation hash — is unchanged by adding one. Publication is gated on
// that hash (deliberately: an unchanged policy must not recycle warm workers).
// While the unenforced list lived on the published Policy, that gate meant a
// wildcard added to an already-running instance was NEVER surfaced: the API
// reported no unenforced rules and the dashboard showed the row as ordinary
// and active — exactly the false impression this whole mechanism exists to
// prevent. The sibling test above missed it because on a manager's first
// compile there is no previous policy, so publication always happens.
func TestWildcardAddedAfterFirstCompileIsStillReportedUnenforced(t *testing.T) {
	db := newManagerTestDB(t)
	m := NewManager(db, t.TempDir(), testCP())
	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("initial compile: %v", err)
	}
	genBefore := m.Snapshot().PolicyGeneration
	if genBefore == "" {
		t.Fatal("expected a published generation after the first compile")
	}

	// Add the wildcard only AFTER a policy already exists.
	rule, err := db.InsertCustomBlocklistRule(database.BlocklistTypeWildcard, "*.legacy.example", "legacy", true)
	if err != nil {
		t.Fatalf("insert wildcard row: %v", err)
	}
	if err := m.ForceRefresh(); err != nil {
		t.Fatalf("recompile: %v", err)
	}
	snap := m.Snapshot()

	if snap.PolicyGeneration != genBefore {
		t.Fatalf("a wildcard must not move the generation (it compiles to nothing): %s -> %s",
			genBefore, snap.PolicyGeneration)
	}
	var found bool
	for _, u := range snap.Unenforced {
		if u.ID == rule.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("wildcard added after the first compile must still be reported unenforced, got %+v", snap.Unenforced)
	}
}
