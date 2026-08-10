// Package firewall owns the operator's sandbox egress policy.
//
// Every function with network_mode='egress' runs under nsjail's --user_net
// (the NSTUN userspace network stack). This package turns the egress_blocklist
// table into an NSTUN rule set, publishes it as an immutable nsjail --config
// generation, and retires warm egress workers when it changes so the next
// spawn picks the new one up.
//
// It also exposes the same rule set to orvad's own outbound clients via
// Policy.Blocks, so the daemon and the sandboxes are filtered by one policy
// rather than drifting apart.
//
// Two layers sit on top of the raw table:
//
//  1. Hostname matching: 'hostname' rules are resolved to addresses on a
//     ticker and unioned with recently-seen answers, so a CDN rotating its A
//     records neither loses coverage nor churns the policy.
//  2. Packet policy: every enabled rule's addresses become NSTUN REJECT rules,
//     scoped to the individual sandbox rather than the whole host.
//
// Source of truth is the `egress_blocklist` table — UI-driven, not
// config-file-driven. The Manager polls for table changes and applies them.
package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

const (
	// hostnameTTL is how long a resolved address stays in the policy after it
	// was last seen. Replace-semantics would flip the policy hash every time a
	// CDN answered with a different address, retiring every warm egress pool
	// on each tick; union-with-decay keeps coverage stable.
	hostnameTTL = 30 * time.Minute

	// minRetireInterval bounds how often a policy change may recycle warm
	// workers. Without it, a flapping DNS answer becomes a cold-start machine
	// gun. A change arriving inside the window is published immediately (so
	// new spawns are correct) and the recycle is coalesced to the boundary.
	minRetireInterval = 60 * time.Second
)

// Manager owns the egress policy lifecycle. One instance per orvad process.
type Manager struct {
	db      *database.Database
	dataDir string // where resolv.conf, hosts and policy generations are written

	cp    ControlPlane
	guest GuestNet

	mu          sync.RWMutex
	resolvedV4  []string            // effective blocked v4 prefixes (for the API)
	resolvedV6  []string            // effective blocked v6 prefixes (for the API)
	hostnameMap map[string][]string // rule.value → resolved IPs (UI display)
	hostSeen    map[string]map[string]time.Time
	// unenforced describes STORED rows that are deliberately not in the
	// compiled policy. It lives here, not on Policy, because it is status
	// about the table rather than part of the enforced artifact: a wildcard
	// row contributes no rules, so the policy hash does not move and the
	// Policy object is not republished — reporting it from there meant a
	// wildcard added to a running instance was never surfaced at all.
	unenforced  []UnenforcedRule
	lastError   string
	compileErr  string
	lastSuccess time.Time
	stale       bool

	policy         atomic.Pointer[Policy]
	onPolicyChange func(gen string)
	lastRetire     time.Time
	pendingRetire  bool

	pollInterval    time.Duration
	resolveInterval time.Duration

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewManager builds the policy manager. cp is required: a policy compiled
// without knowing orvad's own reachable address could block the internal SDK,
// so compilation fails rather than guessing.
func NewManager(db *database.Database, dataDir string, cp ControlPlane) *Manager {
	return &Manager{
		db:              db,
		dataDir:         dataDir,
		cp:              cp,
		guest:           DefaultGuestNet(),
		hostnameMap:     map[string][]string{},
		hostSeen:        map[string]map[string]time.Time{},
		pollInterval:    10 * time.Second,
		resolveInterval: 5 * time.Minute,
	}
}

// SetOnPolicyChange registers the callback fired when a NEW policy generation
// is published. Late-bound by the server so this package never imports pool.
// It fires only on an actual generation change, never on an identical
// recompile — otherwise the poll interval would recycle pools continuously.
func (m *Manager) SetOnPolicyChange(fn func(gen string)) {
	m.mu.Lock()
	m.onPolicyChange = fn
	m.mu.Unlock()
}

// Start writes the DNS files, compiles the initial policy, and begins polling.
//
// Unlike the previous nftables implementation there is no availability gate
// here: the poll loop always runs. That gate used to return early on hosts
// without nft, which silently stopped operator DNS changes from ever reaching
// sandboxes — the boot-time write below made it look like they were applied.
func (m *Manager) Start(ctx context.Context) {
	m.stopCh = make(chan struct{})

	m.writeDNSFiles()

	if err := m.refresh(); err != nil {
		slog.Error("egress policy: initial compile failed; egress functions will refuse to start",
			"err", err)
		m.setCompileError(err.Error())
	}

	m.wg.Add(1)
	go m.pollLoop(ctx)
}

func (m *Manager) Stop(ctx context.Context) error {
	if m.stopCh == nil {
		return nil
	}
	close(m.stopCh)
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	// Nothing to tear down: the policy is per-sandbox and dies with each
	// worker. No host firewall state is ever created, so there is none to
	// clean up on the way out.
	return nil
}

// pollLoop ticks the DB poll and the DNS re-resolve. They are deliberately
// distinct now: the fast tick picks up operator edits, the slow one refreshes
// hostname answers.
func (m *Manager) pollLoop(ctx context.Context) {
	defer m.wg.Done()
	pollT := time.NewTicker(m.pollInterval)
	defer pollT.Stop()
	resolveT := time.NewTicker(m.resolveInterval)
	defer resolveT.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-pollT.C:
			if err := m.refresh(); err != nil {
				slog.Warn("egress policy refresh failed", "err", err)
				m.setCompileError(err.Error())
			}
			m.drainPendingRetire()
		case <-resolveT.C:
			if err := m.refresh(); err != nil {
				slog.Warn("egress policy resolve failed", "err", err)
				m.setCompileError(err.Error())
			}
		}
	}
}

// ForceRefresh is the API hook behind "Force resolve now". Returns whatever
// compilation returned so the operator sees failures live.
func (m *Manager) ForceRefresh() error {
	err := m.refresh()
	if err != nil {
		m.setCompileError(err.Error())
	}
	return err
}

// refresh reads the enabled rules, resolves hostnames, compiles, and publishes
// a new generation when the result differs from the current one.
func (m *Manager) refresh() error {
	rules, err := m.db.ListEnabledBlocklistRules()
	if err != nil {
		return fmt.Errorf("read blocklist: %w", err)
	}

	dnsCfg := LoadDNSConfig(m.db)
	dnsAddrs := dnsServerAddrs(dnsCfg)

	c, err := compile(rules, m.resolveHostname, m.cp, m.guest, dnsAddrs)
	if err != nil {
		// Keep the last known-good policy rather than degrading to NSTUN's
		// default-allow. If there is no known-good policy, egress spawns will
		// refuse — see CurrentPolicy.
		m.markStale(err.Error())
		return err
	}

	rendered := render(c, m.guest)
	gen := genOf(rendered)

	// Guard against a render bug shipping fewer rules than were compiled.
	// nsjail has no --check-config, so this is the only structural check.
	if v4, v6 := countRuleBlocks(rendered); v4 != len(c.rules4) || v6 != len(c.rules6) {
		err := fmt.Errorf("policy render mismatch: emitted %d/%d rules, compiled %d/%d",
			v4, v6, len(c.rules4), len(c.rules6))
		m.markStale(err.Error())
		return err
	}

	prev := m.policy.Load()
	changed := prev == nil || prev.Gen != gen

	if changed {
		path, err := publish(m.dataDir, gen, rendered)
		if err != nil {
			m.markStale(err.Error())
			return err
		}
		p := &Policy{
			Gen: gen, Path: path,
			Rules4: len(c.rules4), Rules6: len(c.rules6),
			Allows: c.allows, Rejects: c.rejects,
			CompiledAt: time.Now().UTC(),
			rules4:     c.rules4, rules6: c.rules6,
			exempt: m.cp.Addrs,
		}
		m.policy.Store(p)
		slog.Info("egress policy published",
			"generation", gen, "rules_v4", p.Rules4, "rules_v6", p.Rules6,
			"allow", p.Allows, "reject", p.Rejects, "unenforced", len(c.unenforced))
	}

	// Cache the effective blocked set for the API/UI.
	v4, v6 := effectiveBlocked(c)
	m.mu.Lock()
	m.resolvedV4, m.resolvedV6 = v4, v6
	m.unenforced = c.unenforced
	m.lastError, m.compileErr, m.stale = "", "", false
	m.lastSuccess = time.Now().UTC()
	m.mu.Unlock()

	// DNS files re-render on the same tick: both come from operator settings
	// and both are consumed at spawn.
	m.writeDNSFiles()

	if changed {
		m.notifyPolicyChange(gen)
	}
	return nil
}

// notifyPolicyChange recycles warm egress workers, rate-limited. NSTUN loads
// its rules once at worker start, so a running warm worker keeps the policy it
// was spawned with until it is retired.
func (m *Manager) notifyPolicyChange(gen string) {
	m.mu.Lock()
	fn := m.onPolicyChange
	since := time.Since(m.lastRetire)
	if fn == nil {
		m.mu.Unlock()
		return
	}
	if !m.lastRetire.IsZero() && since < minRetireInterval {
		m.pendingRetire = true
		m.mu.Unlock()
		slog.Debug("egress policy: recycle coalesced", "generation", gen,
			"retry_in", (minRetireInterval - since).String())
		return
	}
	m.lastRetire = time.Now()
	m.pendingRetire = false
	m.mu.Unlock()
	fn(gen)
}

// drainPendingRetire performs a recycle that was coalesced away earlier once
// the rate-limit window has passed.
func (m *Manager) drainPendingRetire() {
	m.mu.Lock()
	if !m.pendingRetire || time.Since(m.lastRetire) < minRetireInterval {
		m.mu.Unlock()
		return
	}
	fn := m.onPolicyChange
	m.pendingRetire = false
	m.lastRetire = time.Now()
	m.mu.Unlock()
	if fn != nil {
		if p := m.policy.Load(); p != nil {
			fn(p.Gen)
		}
	}
}

// CurrentPolicy returns the published policy, or ErrPolicyUnavailable when
// none exists. Callers must treat the error as fail-closed: NSTUN defaults to
// allow, so running an egress sandbox without a policy means no filtering.
func (m *Manager) CurrentPolicy() (Policy, error) {
	p := m.policy.Load()
	if p == nil {
		return Policy{}, ErrPolicyUnavailable
	}
	return *p, nil
}

// EgressPolicy is the accessor the pool consults at every egress spawn. It
// returns the concrete generation path — never the `current` symlink — so the
// file backing a running worker cannot change underneath it.
func (m *Manager) EgressPolicy() (path, gen string, err error) {
	p := m.policy.Load()
	if p == nil {
		return "", "", ErrPolicyUnavailable
	}
	return p.Path, p.Gen, nil
}

// Blocks reports whether orvad's own outbound connection must be refused.
// Open (allow-everything) when no policy has compiled yet: the daemon's own
// traffic predates the policy and must not be cut off by its absence.
func (m *Manager) Blocks(addr netip.Addr, port uint16) bool {
	return m.policy.Load().Blocks(addr, port)
}

func (m *Manager) writeDNSFiles() {
	if m.dataDir == "" {
		return
	}
	dnsCfg := LoadDNSConfig(m.db)
	if err := WriteResolvConf(m.dataDir, dnsCfg); err != nil {
		slog.Warn("egress policy: write resolv.conf failed", "err", err)
	}
	if err := WriteHostsFile(m.dataDir, dnsCfg.Records); err != nil {
		slog.Warn("egress policy: write hosts file failed", "err", err)
	}
}

// Snapshot is the read-only view behind /firewall/status and the UI.
//
// `nftables_available` is deliberately gone rather than kept as a hardcoded
// alias: the field described a mechanism that no longer exists, and reporting
// it as permanently true would be a lie in the API.
type Snapshot struct {
	IPv4        []string            `json:"ipv4"`
	IPv6        []string            `json:"ipv6"`
	HostnameMap map[string][]string `json:"hostname_map"`
	LastError   string              `json:"last_error,omitempty"`

	Backend          string           `json:"backend"`  // always "nstun"
	Enforced         bool             `json:"enforced"` // a policy is compiled and in use
	PolicyGeneration string           `json:"policy_generation,omitempty"`
	PolicyRuleCounts RuleCounts       `json:"policy_rule_counts"`
	PolicyStale      bool             `json:"policy_stale"`
	LastCompileError string           `json:"last_compile_error,omitempty"`
	LastSuccessAt    string           `json:"last_success_at,omitempty"`
	ControlPlane     ControlPlaneInfo `json:"control_plane_allow"`
	Unenforced       []UnenforcedRule `json:"unenforced_rules,omitempty"`
}

// RuleCounts breaks the compiled policy down for the operator.
type RuleCounts struct {
	V4     int `json:"v4"`
	V6     int `json:"v6"`
	Allow  int `json:"allow"`
	Reject int `json:"reject"`
}

// ControlPlaneInfo is the carve-out that keeps the internal SDK reachable,
// exposed so an operator can see exactly what is permitted and why.
type ControlPlaneInfo struct {
	Addrs []string `json:"addrs"`
	Port  int      `json:"port"`
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := Snapshot{
		IPv4:        append([]string(nil), m.resolvedV4...),
		IPv6:        append([]string(nil), m.resolvedV6...),
		HostnameMap: map[string][]string{},
		LastError:   m.lastError,
		Backend:     "nstun",
		PolicyStale: m.stale,
		ControlPlane: ControlPlaneInfo{
			Addrs: addrsToStrings(m.cp.Addrs),
			Port:  m.cp.Port,
		},
		LastCompileError: m.compileErr,
	}
	if !m.lastSuccess.IsZero() {
		out.LastSuccessAt = m.lastSuccess.Format(time.RFC3339)
	}
	for k, v := range m.hostnameMap {
		out.HostnameMap[k] = append([]string(nil), v...)
	}
	out.Unenforced = append([]UnenforcedRule(nil), m.unenforced...)
	if p := m.policy.Load(); p != nil {
		out.Enforced = true
		out.PolicyGeneration = p.Gen
		out.PolicyRuleCounts = RuleCounts{
			V4: p.Rules4, V6: p.Rules6, Allow: p.Allows, Reject: p.Rejects,
		}
	}
	return out
}

func (m *Manager) setCompileError(s string) {
	m.mu.Lock()
	m.compileErr = s
	m.lastError = s
	m.mu.Unlock()
}

// markStale records a failure while a previous good policy remains in force.
func (m *Manager) markStale(reason string) {
	m.mu.Lock()
	m.compileErr = reason
	m.lastError = reason
	m.stale = m.policy.Load() != nil
	m.mu.Unlock()
}

// resolveHostname returns the union of addresses seen for host within
// hostnameTTL. A lookup failure retains what was previously known instead of
// silently dropping the rule to unenforced.
func (m *Manager) resolveHostname(host string) []string {
	addrs, err := net.LookupHost(host)
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	seen := m.hostSeen[host]
	if seen == nil {
		seen = map[string]time.Time{}
		m.hostSeen[host] = seen
	}
	if err != nil {
		slog.Warn("egress policy: hostname lookup failed; retaining previous addresses",
			"host", host, "err", err, "retained", len(seen))
	}
	for _, a := range addrs {
		seen[a] = now
	}
	out := make([]string, 0, len(seen))
	for a, t := range seen {
		if now.Sub(t) > hostnameTTL {
			delete(seen, a)
			continue
		}
		out = append(out, a)
	}
	sortStrings(out)
	m.hostnameMap[host] = append([]string(nil), out...)
	return out
}

// effectiveBlocked renders the compiled REJECT rules back into strings for the
// API, so what the UI shows is derived from what is actually enforced.
func effectiveBlocked(c compiled) (v4, v6 []string) {
	for _, r := range c.rules4 {
		if r.act == actionReject {
			v4 = append(v4, r.dst.String())
		}
	}
	for _, r := range c.rules6 {
		if r.act == actionReject {
			v6 = append(v6, r.dst.String())
		}
	}
	return v4, v6
}

func dnsServerAddrs(cfg DNSConfig) []netip.Addr {
	servers := cfg.Servers
	if len(servers) == 0 {
		servers = DefaultDNSServers
	}
	out := make([]netip.Addr, 0, len(servers))
	for _, s := range servers {
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a.Unmap())
		}
	}
	return out
}

func addrsToStrings(in []netip.Addr) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		out = append(out, a.String())
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ParseControlPlane derives the carve-out from the internal API base URL the
// server hands to sandboxes. A hostname is resolved here, at startup, so a
// failure is loud rather than becoming a silently missing allow rule.
func ParseControlPlane(apiBase string, fallbackPort int) (ControlPlane, error) {
	cp := ControlPlane{Port: fallbackPort}

	u, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || u.Host == "" {
		return cp, fmt.Errorf("internal API base %q is not a URL", apiBase)
	}
	host := u.Hostname()
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return cp, fmt.Errorf("internal API base %q has an invalid port", apiBase)
		}
		cp.Port = n
	}

	if a, err := netip.ParseAddr(host); err == nil {
		cp.Addrs = []netip.Addr{a.Unmap()}
		return cp, nil
	}

	ips, err := net.LookupHost(host)
	if err != nil || len(ips) == 0 {
		return cp, fmt.Errorf("internal API base host %q did not resolve: %w", host, err)
	}
	for _, s := range ips {
		if a, perr := netip.ParseAddr(s); perr == nil {
			cp.Addrs = append(cp.Addrs, a.Unmap())
		}
	}
	if len(cp.Addrs) == 0 {
		return cp, fmt.Errorf("internal API base host %q resolved to no usable address", host)
	}
	return cp, nil
}
