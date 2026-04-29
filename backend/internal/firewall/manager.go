// Package firewall enforces the global egress blocklist on top of the
// per-function egress toggle. Every function with network_mode='egress'
// goes through nsjail's --user_net (a userspace TCP/UDP stack); the
// firewall package adds two layers on top:
//
//  1. **Hostname matching**: rules of type 'hostname' or 'wildcard' are
//     resolved on a 5-min ticker into IPs that get appended to the
//     effective set.
//  2. **nftables packet filter**: every enabled rule's IPs/CIDRs go
//     into a dedicated nftables set and a chain that DROPs matching
//     egress packets.
//
// Source of truth is the `egress_blocklist` table — UI-driven, not
// config-file-driven. The Manager polls every 10s for table changes
// and applies them live.
package firewall

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
)

// Manager owns the firewall lifecycle. One instance per orvad process.
// Lives for the duration of the server; Stop() drains gracefully.
type Manager struct {
	db      *database.Database
	dataDir string // where the per-sandbox resolv.conf is written

	// Cached effective set of IPv4/IPv6 CIDRs derived from rules.
	// Read by the API for /resolve introspection.
	mu          sync.RWMutex
	resolvedV4  []string // CIDRs ready for nft set
	resolvedV6  []string
	hostnameMap map[string][]string // rule.value → resolved IPs (for UI display)
	lastError   string              // most recent apply or resolve error, surfaced by /firewall/status

	pollInterval    time.Duration
	resolveInterval time.Duration

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewManager(db *database.Database, dataDir string) *Manager {
	return &Manager{
		db:              db,
		dataDir:         dataDir,
		hostnameMap:     map[string][]string{},
		pollInterval:    10 * time.Second,
		resolveInterval: 5 * time.Minute,
	}
}

// Start kicks off the poll + resolve goroutines and applies the initial
// rule set. Errors during apply are logged but don't stop the manager —
// the API still works; nftables enforcement just won't be in place.
//
// In environments where nftables is unavailable (tests, hosts without
// NET_ADMIN, BSD), Start short-circuits: the API still answers, but no
// background goroutine runs. This avoids leaking polling goroutines
// across the test process lifetime.
func (m *Manager) Start(ctx context.Context) {
	m.stopCh = make(chan struct{})

	// DNS (resolv.conf + hosts file) is independent of nftables — write
	// both on every boot, even if packet filtering is disabled. Otherwise
	// functions with network_mode=egress on hosts without nftables would
	// lose operator-configured DNS resolvers and host overrides.
	if m.dataDir != "" {
		dnsCfg := LoadDNSConfig(m.db)
		if err := WriteResolvConf(m.dataDir, dnsCfg); err != nil {
			slog.Warn("firewall: initial resolv.conf write failed", "err", err)
		}
		if err := WriteHostsFile(m.dataDir, dnsCfg.Records); err != nil {
			slog.Warn("firewall: initial hosts file write failed", "err", err)
		}
	}

	if !nftablesAvailable() {
		m.setLastError("nftables unavailable: install the 'nftables' package, run 'modprobe nf_tables', and ensure the orva process has CAP_NET_ADMIN. Egress filtering is disabled until this is resolved; the per-function egress toggle still works (sandbox isolation only).")
		slog.Warn("firewall: nftables unavailable — egress filtering disabled",
			"hint", "install nftables, modprobe nf_tables, run with CAP_NET_ADMIN")
		return
	}

	// Initial apply on startup.
	if err := m.refresh(); err != nil {
		slog.Warn("firewall initial apply failed", "err", err)
		m.setLastError(err.Error())
	}

	m.wg.Add(1)
	go m.pollLoop(ctx)
}

func (m *Manager) Stop(ctx context.Context) error {
	close(m.stopCh)
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	// Best-effort cleanup of nftables state. Failures here are noise on
	// shutdown so we just log.
	if err := nftablesFlush(); err != nil {
		slog.Debug("firewall flush on shutdown", "err", err)
	}
	return nil
}

// pollLoop ticks the poll interval (DB changes) and the resolve
// interval (re-resolve hostnames). Combining them in one goroutine
// avoids races on m.resolvedV4/V6.
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
				slog.Warn("firewall refresh failed", "err", err)
				m.setLastError(err.Error())
			}
		case <-resolveT.C:
			if err := m.refresh(); err != nil {
				slog.Warn("firewall resolve failed", "err", err)
				m.setLastError(err.Error())
			}
		}
	}
}

// ForceRefresh is the API hook for "Force resolve now" in the UI.
// Returns whatever apply returned so the operator sees errors live.
func (m *Manager) ForceRefresh() error {
	err := m.refresh()
	if err != nil {
		m.setLastError(err.Error())
	}
	return err
}

// refresh: read enabled rules, expand hostnames to IPs, build set,
// apply nftables. Single goroutine path — no need to lock the apply.
func (m *Manager) refresh() error {
	rules, err := m.db.ListEnabledBlocklistRules()
	if err != nil {
		return err
	}

	v4, v6 := []string{}, []string{}
	hostnameMap := map[string][]string{}

	for _, r := range rules {
		switch r.RuleType {
		case database.BlocklistTypeCIDR:
			if isIPv6CIDR(r.Value) {
				v6 = append(v6, r.Value)
			} else {
				v4 = append(v4, r.Value)
			}
		case database.BlocklistTypeHostname, database.BlocklistTypeWildcard:
			ips := resolveHostnameSet(r.Value)
			hostnameMap[r.Value] = ips
			for _, ip := range ips {
				if strings.Contains(ip, ":") {
					v6 = append(v6, ip+"/128")
				} else {
					v4 = append(v4, ip+"/32")
				}
			}
		}
	}

	m.mu.Lock()
	m.resolvedV4 = dedupe(v4)
	m.resolvedV6 = dedupe(v6)
	m.hostnameMap = hostnameMap
	m.mu.Unlock()

	if err := nftablesApply(m.resolvedV4, m.resolvedV6); err != nil {
		return err
	}

	// Regenerate the per-sandbox resolv.conf + /etc/hosts alongside the
	// nft rules. Both come from operator-driven settings; all three should
	// re-apply on the same tick. Failure here is non-fatal — sandboxes
	// fall back to whatever was last on disk (or the host's if we never
	// wrote one).
	if m.dataDir != "" {
		dnsCfg := LoadDNSConfig(m.db)
		if err := WriteResolvConf(m.dataDir, dnsCfg); err != nil {
			slog.Warn("firewall: write resolv.conf failed", "err", err)
		}
		if err := WriteHostsFile(m.dataDir, dnsCfg.Records); err != nil {
			slog.Warn("firewall: write hosts file failed", "err", err)
		}
	}

	m.setLastError("")
	return nil
}

// Snapshot returns a read-only view of the current effective set.
// Used by the /firewall/status endpoint and the UI's "currently
// resolving to" column.
type Snapshot struct {
	IPv4         []string            `json:"ipv4"`
	IPv6         []string            `json:"ipv6"`
	HostnameMap  map[string][]string `json:"hostname_map"`
	LastError    string              `json:"last_error,omitempty"`
	NftablesAvail bool                `json:"nftables_available"`
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := Snapshot{
		IPv4:          append([]string(nil), m.resolvedV4...),
		IPv6:          append([]string(nil), m.resolvedV6...),
		HostnameMap:   map[string][]string{},
		LastError:     m.lastError,
		NftablesAvail: nftablesAvailable(),
	}
	for k, v := range m.hostnameMap {
		out.HostnameMap[k] = append([]string(nil), v...)
	}
	return out
}

func (m *Manager) setLastError(s string) {
	m.mu.Lock()
	m.lastError = s
	m.mu.Unlock()
}

// resolveHostnameSet handles both exact hostnames and *.suffix wildcards.
// Wildcards can't be expanded (we don't enumerate every *.foo.com), so
// for a wildcard we resolve only the suffix's apex — best-effort. The
// nftables layer can't match by hostname, so wildcards primarily protect
// via the DNS layer (future work). Exact hostnames work fully.
func resolveHostnameSet(value string) []string {
	target := value
	if strings.HasPrefix(value, "*.") {
		target = value[2:]
	}
	addrs, err := net.LookupHost(target)
	if err != nil {
		return nil
	}
	return addrs
}

func isIPv6CIDR(s string) bool {
	if !strings.Contains(s, "/") {
		return strings.Contains(s, ":")
	}
	ip, _, err := net.ParseCIDR(s)
	if err != nil {
		return false
	}
	return ip.To4() == nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// ErrManagerClosed is returned from API calls after Stop().
var ErrManagerClosed = errors.New("firewall manager closed")
