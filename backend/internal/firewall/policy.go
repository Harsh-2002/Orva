package firewall

// Egress policy compiler. Turns the operator's egress_blocklist rows into
// nsjail NSTUN `user_net { rule4/rule6 }` rules that are loaded per sandbox
// worker via --config.
//
// Why every value is validated here rather than trusted to nsjail: NSTUN's
// rule compilation is fail-OPEN. nsjail's parse_ip() returns void and its
// callers ignore failures, and an out-of-range prefix hits a shift that
// yields mask 0 — and a zero mask means "match any address". So a malformed
// rule does not become a stricter rule, it becomes a *wildcard*. Combined
// with NSTUN's default-ALLOW that is a policy bypass, not a tightening.
// Verified empirically: dst_ip "10.0.0.0/64" matched every destination.
//
// Consequences encoded below:
//   - every address is parsed with netip and re-emitted canonicalised; the
//     operator's raw text never reaches the config file
//   - a rule is routed by address family so a v6 literal can never land in
//     rule4 (where it would parse as nothing and match everything)
//   - an unspecified network address (0.0.0.0/x, ::/x) is refused, because
//     NSTUN cannot distinguish it from "field unset" = match any. This applies
//     to EVERY path that produces a rule target — operator CIDRs, resolver
//     answers, and configured DNS servers — via usableTarget/parseTarget. An
//     earlier revision guarded only the CIDR path; the other two fed
//     already-parsed addresses straight through, so a 0.0.0.0 DNS server
//     compiled to a match-any ALLOW on port 53 and a hostname resolving to
//     0.0.0.0 compiled to a match-any REJECT that severed all egress. Add a
//     new address source only through those helpers.
//   - ports must be 1..65535; nsjail narrows uint32 to uint16 silently, and
//     treats 0 as "unset" = match all ports
//   - there is no way to express "match everything": every emitted rule
//     carries a concrete dst_ip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// ErrPolicyUnavailable means no valid compiled egress policy exists. An
// egress sandbox must not start without one: NSTUN defaults to ALLOW, so a
// missing policy silently means "no filtering at all".
var ErrPolicyUnavailable = errors.New("egress policy unavailable")

// ErrBlockedByEgressPolicy is returned by the daemon-side dialer when orvad's
// own outbound connection targets an address the operator has blocked.
var ErrBlockedByEgressPolicy = errors.New("destination blocked by egress policy")

// nsjail NSTUN guest addressing (config.proto UserNet defaults). The v4
// gateway lives inside 10.0.0.0/8, which Orva ships as a `suggested` rule —
// so it must be allowed ahead of any private-network reject or enabling that
// suggestion would sever the guest's own gateway.
const (
	defaultGuestIP4 = "10.255.255.2"
	defaultGuestGW4 = "10.255.255.1"
	defaultGuestIP6 = "fc00::2"
	defaultGuestGW6 = "fc00::1"
)

// GuestNet mirrors the addresses nsjail assigns inside the sandbox.
type GuestNet struct{ IP4, GW4, IP6, GW6 netip.Addr }

// DefaultGuestNet returns nsjail's built-in addressing.
func DefaultGuestNet() GuestNet {
	return GuestNet{
		IP4: netip.MustParseAddr(defaultGuestIP4),
		GW4: netip.MustParseAddr(defaultGuestGW4),
		IP6: netip.MustParseAddr(defaultGuestIP6),
		GW6: netip.MustParseAddr(defaultGuestGW6),
	}
}

// ControlPlane is the narrow carve-out the guest needs to reach orvad's
// internal SDK endpoints (kv, jobs, function-to-function). Supplied by the
// server from its single startup probe — the compiler never probes.
//
// This exists because detectInternalAPIBase deliberately hands sandboxes a
// NON-loopback address (from inside the jail, 127.0.0.1 is the jail's own
// loopback). That address is normally RFC1918, so enabling any shipped
// private-network suggestion would otherwise break every SDK call.
type ControlPlane struct {
	Addrs []netip.Addr
	Port  int
}

// UnenforcedRule is a stored rule deliberately excluded from the compiled
// policy, surfaced through the API so the UI never implies it is in force.
type UnenforcedRule struct {
	ID     int64  `json:"id"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// Policy is an immutable published snapshot. Path names a file that is never
// rewritten, so a worker that captured it keeps exactly that policy for its
// whole life even across generation swaps.
type Policy struct {
	Gen        string    `json:"generation"`
	Path       string    `json:"-"`
	Rules4     int       `json:"rules_v4"`
	Rules6     int       `json:"rules_v6"`
	Allows     int       `json:"allow_rules"`
	Rejects    int       `json:"reject_rules"`
	CompiledAt time.Time `json:"compiled_at"`

	// rules backs Blocks() for daemon-side enforcement. Same rule set and
	// same first-match-wins order the sandbox gets.
	rules4, rules6 []nstunRule
	// exemptAddrs/exemptPort are never blocked daemon-side. They mirror the
	// compiled control-plane carve-out EXACTLY, including its port: the
	// sandbox rule is ALLOW TCP <addr> dport=<port>, so exempting the whole
	// address here would make the daemon strictly less restricted than a
	// sandbox. On a single-node install the control-plane address is the
	// box's own LAN IP, so that gap was an SSRF hole — an operator who
	// blocklists their own host still had webhook deliveries reaching every
	// other port on it.
	exemptAddrs []netip.Addr
	exemptPort  uint16
}

type ruleAction uint8

const (
	actionReject ruleAction = iota
	actionAllow
)

func (a ruleAction) String() string {
	if a == actionAllow {
		return "ALLOW"
	}
	return "REJECT"
}

type ruleProto uint8

const (
	protoAny ruleProto = iota
	protoTCP
	protoUDP
)

func (p ruleProto) String() string {
	switch p {
	case protoTCP:
		return "TCP"
	case protoUDP:
		return "UDP"
	default:
		return "ANY"
	}
}

// nstunRule is one compiled rule. dst is always a canonical, masked prefix:
// this package has no way to express "every address", because NSTUN treats an
// absent or all-zero dst_ip as match-any and an accidental one would silently
// turn a narrow rule into a wildcard. Validation refuses unspecified addresses
// for exactly that reason.
type nstunRule struct {
	act   ruleAction
	pr    ruleProto
	dst   netip.Prefix
	dport uint16 // 0 = no port constraint
}

// compiled is the intermediate result: ordered rules plus what was dropped.
type compiled struct {
	rules4, rules6  []nstunRule
	unenforced      []UnenforcedRule
	allows, rejects int
}

// compile builds the ordered rule set. Order is a security control, not a
// style choice: NSTUN is first-match-wins, so every carve-out must precede
// the blocklist or a broad reject will shadow it.
//
// resolve is injected so hostname expansion is testable without DNS.
func compile(
	rules []*database.BlocklistRule,
	resolve func(host string) []string,
	cp ControlPlane,
	gn GuestNet,
	dnsServers []netip.Addr,
) (compiled, error) {
	var c compiled

	// 1. NSTUN's own gateway. Inside 10.0.0.0/8, so it must outrank any
	//    private-network reject.
	if usableTarget(gn.GW4) {
		c.add4(nstunRule{act: actionAllow, pr: protoAny, dst: hostPrefix(gn.GW4)})
	}
	if usableTarget(gn.GW6) {
		c.add6(nstunRule{act: actionAllow, pr: protoAny, dst: hostPrefix(gn.GW6)})
	}

	// 2. Control plane: exact host, exact port, TCP only. A missing carve-out
	//    is a compile failure — never a broad allow and never a silent
	//    omission, which would break the SDK in a maximally confusing way.
	if len(cp.Addrs) == 0 {
		return compiled{}, errors.New("control-plane address is unknown: refusing to compile a policy that could block orvad's internal SDK")
	}
	if cp.Port < 1 || cp.Port > 65535 {
		return compiled{}, fmt.Errorf("control-plane port %d out of range", cp.Port)
	}
	for _, a := range cp.Addrs {
		if !usableTarget(a) {
			return compiled{}, errors.New("control-plane address is invalid or unspecified")
		}
		port, ok := portToUint16(cp.Port)
		if !ok {
			return compiled{}, fmt.Errorf("control-plane port %d out of range", cp.Port)
		}
		c.addByFamily(nstunRule{
			act: actionAllow, pr: protoTCP,
			dst: hostPrefix(a), dport: port,
		})
	}

	// 3. DNS resolvers: exact host, port 53, UDP and TCP.
	for _, s := range dnsServers {
		if !usableTarget(s) {
			continue
		}
		for _, p := range []ruleProto{protoUDP, protoTCP} {
			c.addByFamily(nstunRule{
				act: actionAllow, pr: p, dst: hostPrefix(s), dport: 53,
			})
		}
	}

	// 4. The operator blocklist.
	var v4Blocks, v6Blocks []netip.Prefix
	for _, r := range rules {
		if r == nil || !r.Enabled {
			continue
		}
		switch r.RuleType {
		case database.BlocklistTypeCIDR:
			p, err := parseTarget(r.Value)
			if err != nil {
				// A malformed row is skipped, not fatal: dropping one reject
				// is safer than refusing the whole policy and taking every
				// egress function offline. The API rejects these on write, so
				// this only fires for hand-edited rows.
				c.unenforced = append(c.unenforced, UnenforcedRule{
					ID: r.ID, Value: r.Value, Reason: err.Error(),
				})
				continue
			}
			if p.Addr().Is4() {
				v4Blocks = append(v4Blocks, p)
			} else {
				v6Blocks = append(v6Blocks, p)
			}

		case database.BlocklistTypeHostname:
			// A hostname that resolves to nothing usable contributes NO rules.
			// Silently emitting nothing would render in the dashboard as an
			// ordinary active block — the UI builds its entire "not enforced"
			// surface from unenforced_rules — so the operator would believe a
			// destination was blocked while it was fully reachable. Causes:
			// NXDOMAIN, a typo, or the resolver being unavailable at boot
			// before any answer has been cached.
			var resolved int
			for _, ipStr := range resolve(r.Value) {
				a, err := netip.ParseAddr(ipStr)
				if err != nil || !usableTarget(a) {
					continue
				}
				a = a.Unmap()
				resolved++
				if a.Is4() {
					v4Blocks = append(v4Blocks, hostPrefix(a))
				} else {
					v6Blocks = append(v6Blocks, hostPrefix(a))
				}
			}
			if resolved == 0 {
				c.unenforced = append(c.unenforced, UnenforcedRule{
					ID:    r.ID,
					Value: r.Value,
					Reason: "hostname did not resolve to any usable address, so it " +
						"blocks nothing right now. Check the name and that the " +
						"resolver is reachable; this clears itself once resolution " +
						"succeeds.",
				})
			}

		case database.BlocklistTypeWildcard:
			// Never enforced. Packets carry addresses, not names, so a
			// wildcard cannot be expressed as a packet rule. Previously this
			// resolved only the wildcard's apex, which blocked the bare
			// domain and none of its subdomains while the UI claimed
			// otherwise. Reported instead of half-enforced.
			c.unenforced = append(c.unenforced, UnenforcedRule{
				ID:    r.ID,
				Value: r.Value,
				Reason: "wildcard hostnames are not enforceable: egress policy matches " +
					"IP/CIDR, not DNS names. Use a CIDR or an exact hostname.",
			})

		default:
			c.unenforced = append(c.unenforced, UnenforcedRule{
				ID: r.ID, Value: r.Value,
				Reason: "unknown rule_type " + r.RuleType,
			})
		}
	}

	for _, p := range canonPrefixes(v4Blocks) {
		c.add4(nstunRule{act: actionReject, pr: protoAny, dst: p})
	}
	for _, p := range canonPrefixes(v6Blocks) {
		c.add6(nstunRule{act: actionReject, pr: protoAny, dst: p})
	}

	return c, nil
}

func (c *compiled) add4(r nstunRule) { c.rules4 = append(c.rules4, r); c.count(r) }
func (c *compiled) add6(r nstunRule) { c.rules6 = append(c.rules6, r); c.count(r) }

func (c *compiled) addByFamily(r nstunRule) {
	if r.dst.Addr().Is4() {
		c.add4(r)
		return
	}
	c.add6(r)
}

func (c *compiled) count(r nstunRule) {
	if r.act == actionAllow {
		c.allows++
	} else {
		c.rejects++
	}
}

// hostPrefix returns the single-address prefix for a (/32 or /128).
func hostPrefix(a netip.Addr) netip.Prefix {
	return netip.PrefixFrom(a, a.BitLen())
}

// portToUint16 converts a port with the range check local to the conversion.
//
// compile rejects an out-of-range control-plane port before any policy is
// built, so this cannot currently fail — but the conversion sits on a security
// boundary, and a bare uint16() truncates silently: 70000 becomes 4464, and
// 65536+8443 becomes 8443. Either would scope the daemon's exemption to a port
// the operator never exempted, which is precisely the defect the port-scoping
// fix closed. ok=false yields 0, which matches no real port, so an invalid
// value produces NO exemption rather than the wrong one.
func portToUint16(p int) (uint16, bool) {
	if p < 1 || p > 65535 {
		return 0, false
	}
	return uint16(p), true
}

// usableTarget reports whether a resolved address may be turned into a rule.
//
// NSTUN cannot distinguish an all-zero dst_ip from an absent one, so an
// unspecified address does not produce the narrow rule it looks like — it
// matches EVERY destination. As an ALLOW that silently voids the blocklist for
// whatever ports the rule covers; as a REJECT it silently severs all egress.
// Neither is reported as unenforced, because from the compiler's point of view
// the rule compiled fine.
//
// parseTarget applies this to operator-typed CIDRs. This is the same gate for
// addresses that arrive already parsed — resolver answers and configured DNS
// servers — which previously bypassed it entirely.
func usableTarget(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && !a.IsUnspecified()
}

// parseTarget accepts a bare address or a CIDR and returns a canonical masked
// prefix. It rejects everything NSTUN would silently turn into a wildcard.
func parseTarget(raw string) (netip.Prefix, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return netip.Prefix{}, errors.New("empty value")
	}

	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		p, err = netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("not a valid CIDR: %s", raw)
		}
		// ParsePrefix already bounds Bits() to the family width, which is the
		// nsjail bug this guards: an over-wide prefix there yields mask 0 and
		// matches every address.
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("not a valid IP or CIDR: %s", raw)
		}
		p = hostPrefix(a)
	}

	if p.Addr().Is4In6() {
		// A v4-mapped v6 literal would be routed to rule6 and never match the
		// v4 traffic the operator meant. Force the operator to be explicit.
		return netip.Prefix{}, fmt.Errorf("v4-mapped IPv6 address is ambiguous: %s", raw)
	}
	if p.Addr().IsUnspecified() {
		// NSTUN cannot tell an all-zero dst_ip from an absent one, so this
		// would become match-any rather than the narrow rule it looks like.
		return netip.Prefix{}, fmt.Errorf("unspecified address is not allowed: %s", raw)
	}
	return p.Masked(), nil
}

// canonPrefixes sorts and dedupes so rule order — and therefore the policy
// hash — is stable regardless of DB row order or DNS answer order.
func canonPrefixes(in []netip.Prefix) []netip.Prefix {
	seen := make(map[string]struct{}, len(in))
	out := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		k := p.String()
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// render emits the nsjail textproto. mount_proc is mandatory: loading any
// config without it clears nsjail's proc_path and /proc silently vanishes
// from the jail, which breaks both runtimes. Verified empirically.
func render(c compiled, gn GuestNet) []byte {
	var b strings.Builder
	b.WriteString("# Generated by orva. Do not edit — regenerated on every policy change.\n")
	b.WriteString("# mount_proc is required: without it nsjail clears proc_path and /proc\n")
	b.WriteString("# is silently absent inside the jail.\n")
	b.WriteString("mount_proc: true\n\n")
	b.WriteString("user_net {\n")
	b.WriteString("  backend: NSTUN\n")
	fmt.Fprintf(&b, "  ip4: %q\n", gn.IP4.String())
	fmt.Fprintf(&b, "  gw4: %q\n", gn.GW4.String())
	fmt.Fprintf(&b, "  ip6: %q\n", gn.IP6.String())
	fmt.Fprintf(&b, "  gw6: %q\n", gn.GW6.String())
	b.WriteString("  ns_iface: \"eth0\"\n")
	for _, r := range c.rules4 {
		renderRule(&b, "rule4", r, true)
	}
	for _, r := range c.rules6 {
		renderRule(&b, "rule6", r, false)
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

func renderRule(b *strings.Builder, field string, r nstunRule, v4 bool) {
	fmt.Fprintf(b, "  %s {\n", field)
	// Every field is explicit. NstunRule.action defaults to ALLOW, so an
	// omitted action is not a stricter rule — it is no rule at all.
	b.WriteString("    direction: GUEST_TO_HOST\n")
	fmt.Fprintf(b, "    action: %s\n", r.act)
	fmt.Fprintf(b, "    proto: %s\n", r.pr)
	fmt.Fprintf(b, "    dst_ip: %q\n", r.dst.String())
	if r.dport != 0 {
		// Both ends set: an omitted dport_end matches every port.
		fmt.Fprintf(b, "    dport: %d\n", r.dport)
		fmt.Fprintf(b, "    dport_end: %d\n", r.dport)
	}
	if v4 {
		// nsjail parses redirect_ip unconditionally for every rule4 regardless
		// of action, logging an error per rule when it is absent. Inert here.
		b.WriteString("    redirect_ip: \"0.0.0.0\"\n")
	}
	b.WriteString("  }\n")
}

// genOf is the policy generation: a hash of the rendered bytes, i.e. of
// exactly what nsjail will enforce. Hashing the output rather than the DB
// rows means a reordered table or a no-op timestamp bump does not churn pools.
func genOf(rendered []byte) string {
	sum := sha256.Sum256(rendered)
	return hex.EncodeToString(sum[:])[:16]
}

// countRuleBlocks counts emitted rule blocks so a render bug cannot ship a
// policy with fewer rules than intended. There is no `nsjail --check-config`.
func countRuleBlocks(rendered []byte) (v4, v6 int) {
	for _, line := range strings.Split(string(rendered), "\n") {
		switch strings.TrimSpace(line) {
		case "rule4 {":
			v4++
		case "rule6 {":
			v6++
		}
	}
	return v4, v6
}

// Blocks reports whether orvad's own outbound connection to addr:port must be
// refused. It walks the same rules in the same first-match-wins order the
// sandbox gets, so the daemon and the sandboxes cannot drift apart.
//
// Loopback and the control plane are always exempt: orvad must never be able
// to cut off its own internal calls.
func (p *Policy) Blocks(addr netip.Addr, port uint16) bool {
	if p == nil || !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	// Loopback stays unconditionally exempt: orvad must never be able to cut
	// off calls to itself, and a sandbox cannot reach the host's loopback
	// anyway (from inside the jail 127.0.0.1 is the jail's own).
	if addr.IsLoopback() {
		return false
	}
	for _, e := range p.exemptAddrs {
		if e.Unmap() == addr && port == p.exemptPort {
			return false
		}
	}
	rules := p.rules4
	if addr.Is6() {
		rules = p.rules6
	}
	for _, r := range rules {
		if !r.dst.Contains(addr) {
			continue
		}
		if r.dport != 0 && r.dport != port {
			continue
		}
		return r.act == actionReject
	}
	return false // NSTUN's no-match default
}
