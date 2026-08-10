package firewall

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// These tests assert on the RENDERED bytes, not on intermediate structs.
// nsjail only ever sees the bytes, and the failure modes being guarded here
// (a wildcard where a narrow rule was intended) are invisible at the struct
// level.

func testCP() ControlPlane {
	return ControlPlane{Addrs: []netip.Addr{netip.MustParseAddr("172.17.0.2")}, Port: 8443}
}

func noResolve(string) []string { return nil }

func cidrRule(id int64, v string) *database.BlocklistRule {
	return &database.BlocklistRule{ID: id, Kind: "custom",
		RuleType: database.BlocklistTypeCIDR, Value: v, Enabled: true}
}

// mustCompile compiles and renders in one step, since every assertion here is
// on the rendered bytes.
func mustCompile(t *testing.T, rules []*database.BlocklistRule, dns []netip.Addr) (compiled, []byte) {
	t.Helper()
	c, err := compile(rules, noResolve, testCP(), DefaultGuestNet(), dns)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return c, render(c, DefaultGuestNet())
}

// indexOf returns the line index of the first line containing sub, or -1.
func indexOf(lines []string, sub string) int {
	for i, l := range lines {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

// ── ordering: first-match-wins makes these security properties ──────────

func TestCompileEmitsControlPlaneAllowBeforeAnyReject(t *testing.T) {
	// The shipped RFC1918 "suggested" rules cover the control-plane address,
	// so without this ordering every orva.kv / orva.jobs / F2F call breaks.
	_, out := mustCompile(t, []*database.BlocklistRule{
		cidrRule(1, "172.16.0.0/12"),
	}, nil)
	lines := strings.Split(string(out), "\n")

	allow := indexOf(lines, `dst_ip: "172.17.0.2/32"`)
	reject := indexOf(lines, `dst_ip: "172.16.0.0/12"`)
	if allow < 0 || reject < 0 {
		t.Fatalf("missing rules; got:\n%s", out)
	}
	if allow > reject {
		t.Fatalf("control-plane ALLOW (line %d) must precede the covering REJECT (line %d)", allow, reject)
	}
}

func TestCompileEmitsGuestGatewayAllowBeforePrivateReject(t *testing.T) {
	// nsjail's own gw4 is 10.255.255.1 — inside the shipped 10.0.0.0/8 rule.
	_, out := mustCompile(t, []*database.BlocklistRule{
		cidrRule(1, "10.0.0.0/8"),
	}, nil)
	lines := strings.Split(string(out), "\n")

	gw := indexOf(lines, `dst_ip: "10.255.255.1/32"`)
	reject := indexOf(lines, `dst_ip: "10.0.0.0/8"`)
	if gw < 0 || reject < 0 {
		t.Fatalf("missing rules; got:\n%s", out)
	}
	if gw > reject {
		t.Fatalf("gateway ALLOW (line %d) must precede REJECT 10.0.0.0/8 (line %d)", gw, reject)
	}
}

func TestCompileEmitsDNSAllowBeforeAnyReject(t *testing.T) {
	_, out := mustCompile(t, []*database.BlocklistRule{
		cidrRule(1, "1.0.0.0/8"),
	}, []netip.Addr{netip.MustParseAddr("1.1.1.1")})
	lines := strings.Split(string(out), "\n")

	dns := indexOf(lines, `dst_ip: "1.1.1.1/32"`)
	reject := indexOf(lines, `dst_ip: "1.0.0.0/8"`)
	if dns < 0 || reject < 0 {
		t.Fatalf("missing rules; got:\n%s", out)
	}
	if dns > reject {
		t.Fatalf("DNS ALLOW (line %d) must precede the covering REJECT (line %d)", dns, reject)
	}
}

func TestControlPlaneAllowIsExactHostExactPortTCPOnly(t *testing.T) {
	_, out := mustCompile(t, nil, nil)
	block := ruleBlockContaining(t, out, `dst_ip: "172.17.0.2/32"`)

	for _, want := range []string{
		"action: ALLOW", "proto: TCP", "dport: 8443", "dport_end: 8443",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("control-plane rule missing %q; block:\n%s", want, block)
		}
	}
	if strings.Contains(block, "proto: ANY") {
		t.Errorf("control-plane rule must not be proto ANY; block:\n%s", block)
	}
}

// ── the fail-open traps: a malformed value must never become a wildcard ──

func TestCompileRejectsOverWidePrefix(t *testing.T) {
	// Verified against nsjail: dst_ip "10.0.0.0/64" yields mask 0 and matches
	// EVERY destination. It must never reach the config file.
	c, out := mustCompile(t, []*database.BlocklistRule{
		cidrRule(7, "10.0.0.0/64"),
	}, nil)
	if strings.Contains(string(out), "10.0.0.0") {
		t.Fatalf("over-wide prefix leaked into the policy:\n%s", out)
	}
	if len(c.unenforced) != 1 || c.unenforced[0].ID != 7 {
		t.Fatalf("expected the bad row reported as unenforced, got %+v", c.unenforced)
	}
}

func TestCompileRejectsUnspecifiedAddress(t *testing.T) {
	// NSTUN cannot distinguish an all-zero dst_ip from an absent one, so
	// 0.0.0.0/8 silently becomes match-any.
	for _, bad := range []string{"0.0.0.0/8", "0.0.0.0", "::/0", "::"} {
		c, out := mustCompile(t, []*database.BlocklistRule{cidrRule(1, bad)}, nil)
		if len(c.unenforced) != 1 {
			t.Errorf("%s: expected rejection, got %+v", bad, c.unenforced)
		}
		if strings.Contains(string(out), `dst_ip: "0.0.0.0`) ||
			strings.Contains(string(out), `dst_ip: "::`) {
			t.Errorf("%s: unspecified address leaked into policy:\n%s", bad, out)
		}
	}
}

func TestCompileRoutesFamiliesSeparately(t *testing.T) {
	// A v6 literal in rule4 parses as nothing in nsjail and matches
	// everything, so families must never cross lists.
	_, out := mustCompile(t, []*database.BlocklistRule{
		cidrRule(1, "2001:db8::/32"),
		cidrRule(2, "203.0.113.0/24"),
	}, nil)

	v4Body, v6Body := splitRuleSections(t, out)
	if strings.Contains(v4Body, "2001:db8") {
		t.Errorf("IPv6 prefix landed in rule4:\n%s", out)
	}
	if strings.Contains(v6Body, "203.0.113") {
		t.Errorf("IPv4 prefix landed in rule6:\n%s", out)
	}
}

func TestCompileRejectsV4MappedIPv6(t *testing.T) {
	c, _ := mustCompile(t, []*database.BlocklistRule{
		cidrRule(1, "::ffff:203.0.113.1"),
	}, nil)
	if len(c.unenforced) != 1 {
		t.Fatalf("expected v4-mapped address to be refused, got %+v", c.unenforced)
	}
}

func TestEveryRenderedRuleHasExplicitAction(t *testing.T) {
	// NstunRule.action defaults to ALLOW: an omitted action is not a stricter
	// rule, it is no rule at all.
	_, out := mustCompile(t, []*database.BlocklistRule{
		cidrRule(1, "203.0.113.0/24"), cidrRule(2, "2001:db8::/32"),
	}, []netip.Addr{netip.MustParseAddr("8.8.8.8")})

	blocks := allRuleBlocks(string(out))
	if len(blocks) == 0 {
		t.Fatal("no rule blocks rendered")
	}
	for i, b := range blocks {
		if !strings.Contains(b, "action: ") {
			t.Errorf("rule block %d has no explicit action:\n%s", i, b)
		}
		if !strings.Contains(b, "direction: GUEST_TO_HOST") {
			t.Errorf("rule block %d has no explicit direction:\n%s", i, b)
		}
		if !strings.Contains(b, "proto: ") {
			t.Errorf("rule block %d has no explicit proto:\n%s", i, b)
		}
	}
}

func TestEveryRule4CarriesRedirectIP(t *testing.T) {
	// nsjail parses redirect_ip unconditionally for every rule4 regardless of
	// action, logging one error per rule when absent.
	_, out := mustCompile(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24")}, nil)
	v4Body, _ := splitRuleSections(t, out)
	for i, b := range allRuleBlocks(v4Body) {
		if !strings.Contains(b, `redirect_ip: "0.0.0.0"`) {
			t.Errorf("rule4 block %d missing redirect_ip:\n%s", i, b)
		}
	}
}

func TestRenderAlwaysSetsMountProc(t *testing.T) {
	// Verified against nsjail: without this, /proc is silently absent inside
	// the jail (0 entries) and both runtimes break.
	_, out := mustCompile(t, nil, nil)
	if !strings.Contains(string(out), "mount_proc: true") {
		t.Fatalf("mount_proc: true missing:\n%s", out)
	}
}

func TestCompileEmitsNoTrailingCatchAllForV4(t *testing.T) {
	// Orva's model is a blocklist, not an allowlist: everything not blocked
	// must stay reachable via NSTUN's default-allow.
	_, out := mustCompile(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24")}, nil)
	v4Body, _ := splitRuleSections(t, out)
	for _, b := range allRuleBlocks(v4Body) {
		if !strings.Contains(b, "dst_ip:") {
			t.Errorf("rule4 has no dst_ip — that is a catch-all:\n%s", b)
		}
	}
}

func TestRuleBlockCountMatchesCompiled(t *testing.T) {
	c, out := mustCompile(t, []*database.BlocklistRule{
		cidrRule(1, "203.0.113.0/24"), cidrRule(2, "198.51.100.0/24"),
		cidrRule(3, "2001:db8::/32"),
	}, []netip.Addr{netip.MustParseAddr("8.8.8.8")})

	v4, v6 := countRuleBlocks(out)
	if v4 != len(c.rules4) || v6 != len(c.rules6) {
		t.Fatalf("rendered %d/%d rule blocks, compiled %d/%d", v4, v6, len(c.rules4), len(c.rules6))
	}
}

// ── control-plane failure must be fatal, never a broad allow ────────────

func TestCompileFailsWhenControlPlaneUnknown(t *testing.T) {
	if _, err := compile(nil, noResolve, ControlPlane{Port: 8443},
		DefaultGuestNet(), nil); err == nil {
		t.Fatal("expected compile to fail with no control-plane address")
	}
}

func TestCompileFailsOnBadControlPlanePort(t *testing.T) {
	cp := ControlPlane{Addrs: []netip.Addr{netip.MustParseAddr("10.1.2.3")}, Port: 0}
	if _, err := compile(nil, noResolve, cp, DefaultGuestNet(), nil); err == nil {
		t.Fatal("expected compile to fail on out-of-range port")
	}
}

func TestCompileNeverEmitsBroadAllow(t *testing.T) {
	// No ALLOW may cover more than a single host: a private-range ALLOW would
	// be an escape hatch for every function on the box.
	_, out := mustCompile(t, []*database.BlocklistRule{cidrRule(1, "10.0.0.0/8")},
		[]netip.Addr{netip.MustParseAddr("1.1.1.1")})

	for _, b := range allRuleBlocks(string(out)) {
		if !strings.Contains(b, "action: ALLOW") {
			continue
		}
		dst := extractField(b, "dst_ip")
		if dst == "" {
			t.Errorf("ALLOW rule with no dst_ip is a blanket allow:\n%s", b)
			continue
		}
		p, err := netip.ParsePrefix(dst)
		if err != nil {
			t.Errorf("unparseable dst_ip %q", dst)
			continue
		}
		if p.Bits() != p.Addr().BitLen() {
			t.Errorf("ALLOW must be a single host, got %s:\n%s", dst, b)
		}
	}
}

// ── wildcards: reported, never half-enforced ────────────────────────────

func TestWildcardIsUnenforcedNotApexOnly(t *testing.T) {
	rules := []*database.BlocklistRule{{
		ID: 9, Kind: "custom", RuleType: database.BlocklistTypeWildcard,
		Value: "*.corp.internal", Enabled: true,
	}}
	// A resolver that WOULD answer for the apex: the old code resolved it and
	// emitted an apex-only rule, which blocked the bare domain and none of its
	// subdomains while the UI claimed the whole domain was covered.
	c, err := compile(rules, func(string) []string { return []string{"203.0.113.9"} },
		testCP(), DefaultGuestNet(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := render(c, DefaultGuestNet())

	if strings.Contains(string(out), "203.0.113.9") {
		t.Fatalf("wildcard apex leaked into the policy:\n%s", out)
	}
	if len(c.unenforced) != 1 || c.unenforced[0].ID != 9 {
		t.Fatalf("wildcard must be reported unenforced, got %+v", c.unenforced)
	}
}

// ── hostnames ───────────────────────────────────────────────────────────

func TestHostnameSnapshotsEveryResolvedAddress(t *testing.T) {
	rules := []*database.BlocklistRule{{
		ID: 3, Kind: "custom", RuleType: database.BlocklistTypeHostname,
		Value: "bad.example", Enabled: true,
	}}
	resolve := func(string) []string { return []string{"203.0.113.1", "203.0.113.2", "2001:db8::1"} }
	c, err := compile(rules, resolve, testCP(), DefaultGuestNet(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := string(render(c, DefaultGuestNet()))
	for _, want := range []string{"203.0.113.1/32", "203.0.113.2/32", "2001:db8::1/128"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}

// ── generation stability: controls pool churn ───────────────────────────

func TestGenerationStableUnderRuleReordering(t *testing.T) {
	a := []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24"), cidrRule(2, "198.51.100.0/24")}
	b := []*database.BlocklistRule{cidrRule(2, "198.51.100.0/24"), cidrRule(1, "203.0.113.0/24")}

	_, outA := mustCompile(t, a, nil)
	_, outB := mustCompile(t, b, nil)
	if genOf(outA) != genOf(outB) {
		t.Fatalf("generation changed on row reorder: %s vs %s", genOf(outA), genOf(outB))
	}
}

func TestGenerationChangesWhenRulesChange(t *testing.T) {
	_, outA := mustCompile(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24")}, nil)
	_, outB := mustCompile(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/25")}, nil)
	if genOf(outA) == genOf(outB) {
		t.Fatal("generation must change when the compiled policy changes")
	}
}

func TestDisabledRulesAreNotCompiled(t *testing.T) {
	r := cidrRule(1, "203.0.113.0/24")
	r.Enabled = false
	_, out := mustCompile(t, []*database.BlocklistRule{r}, nil)
	if strings.Contains(string(out), "203.0.113") {
		t.Fatalf("disabled rule was compiled:\n%s", out)
	}
}

// ── IPv6 posture ────────────────────────────────────────────────────────

func TestExplicitV6RuleCompilesToRule6Reject(t *testing.T) {
	// fd00:ec2::254/128 is the GCP IPv6 metadata endpoint and ships as an
	// ENABLED `default` rule, so dropping v6 rule compilation would silently
	// remove cloud-metadata protection rather than merely narrowing scope.
	_, out := mustCompile(t, []*database.BlocklistRule{
		cidrRule(1, "fd00:ec2::254/128"),
	}, nil)

	_, v6Body := splitRuleSections(t, out)
	block := ruleBlockContaining(t, []byte(v6Body), `dst_ip: "fd00:ec2::254/128"`)
	if !strings.Contains(block, "action: REJECT") {
		t.Fatalf("v6 metadata rule must compile to a rule6 REJECT; block:\n%s", block)
	}
}

func TestCompileEmitsNoBlanketV6Reject(t *testing.T) {
	// The policy is a blocklist in both families: a REJECT with no dst_ip is a
	// deny-all, which would take IPv6 egress offline wholesale.
	for _, rules := range [][]*database.BlocklistRule{
		nil,
		{cidrRule(1, "203.0.113.0/24")},
		{cidrRule(1, "2001:db8::/32")},
	} {
		_, out := mustCompile(t, rules, nil)
		_, v6Body := splitRuleSections(t, out)
		for _, b := range allRuleBlocks(v6Body) {
			if strings.Contains(b, "action: REJECT") && !strings.Contains(b, "dst_ip:") {
				t.Fatalf("rule6 REJECT with no dst_ip is a deny-all:\n%s", v6Body)
			}
		}
	}
}

// ── daemon-side Blocks() ────────────────────────────────────────────────

func TestBlocksMatchesCompiledRules(t *testing.T) {
	p := buildPolicy(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24")}, nil)

	if !p.Blocks(netip.MustParseAddr("203.0.113.7"), 443) {
		t.Error("address inside a blocked prefix must be blocked")
	}
	if p.Blocks(netip.MustParseAddr("198.51.100.7"), 443) {
		t.Error("address outside every rule must be allowed (default-allow)")
	}
}

func TestBlocksNeverBlocksLoopbackOrControlPlane(t *testing.T) {
	// 172.16.0.0/12 covers the control-plane address; orvad must still be able
	// to reach its own API.
	p := buildPolicy(t, []*database.BlocklistRule{
		cidrRule(1, "172.16.0.0/12"), cidrRule(2, "127.0.0.0/8"),
	}, nil)

	if p.Blocks(netip.MustParseAddr("127.0.0.1"), 8443) {
		t.Error("loopback must never be blocked daemon-side")
	}
	if p.Blocks(netip.MustParseAddr("172.17.0.2"), 8443) {
		t.Error("control-plane address must never be blocked daemon-side")
	}
	if !p.Blocks(netip.MustParseAddr("172.20.1.1"), 443) {
		t.Error("other addresses in the blocked range must still be blocked")
	}
}

func TestBlocksEmptyPolicyBlocksNothing(t *testing.T) {
	p := buildPolicy(t, nil, nil)
	if p.Blocks(netip.MustParseAddr("203.0.113.7"), 443) {
		t.Error("an empty blocklist must block nothing")
	}
}

func TestBlocksNilPolicyIsSafe(t *testing.T) {
	var p *Policy
	if p.Blocks(netip.MustParseAddr("203.0.113.7"), 443) {
		t.Error("nil policy must not block")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

func buildPolicy(t *testing.T, rules []*database.BlocklistRule, dns []netip.Addr) *Policy {
	t.Helper()
	c, err := compile(rules, noResolve, testCP(), DefaultGuestNet(), dns)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return &Policy{
		rules4: c.rules4, rules6: c.rules6,
		exemptAddrs: testCP().Addrs, exemptPort: uint16(testCP().Port),
	}
}

// allRuleBlocks splits rendered output into individual "ruleN { ... }" blocks.
func allRuleBlocks(s string) []string {
	var out []string
	var cur []string
	in := false
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == "rule4 {" || t == "rule6 {":
			in, cur = true, nil
		case in && t == "}":
			out = append(out, strings.Join(cur, "\n"))
			in = false
		case in:
			cur = append(cur, t)
		}
	}
	return out
}

// ruleBlockContaining returns the single rule block containing sub.
func ruleBlockContaining(t *testing.T, rendered []byte, sub string) string {
	t.Helper()
	for _, b := range allRuleBlocks(string(rendered)) {
		if strings.Contains(b, sub) {
			return b
		}
	}
	t.Fatalf("no rule block contains %q; rendered:\n%s", sub, rendered)
	return ""
}

// splitRuleSections returns the rule4 region and the rule6 region separately,
// so a family-crossing bug is detectable.
func splitRuleSections(t *testing.T, rendered []byte) (v4, v6 string) {
	t.Helper()
	var b4, b6 []string
	var cur *[]string
	for _, l := range strings.Split(string(rendered), "\n") {
		switch strings.TrimSpace(l) {
		case "rule4 {":
			cur = &b4
		case "rule6 {":
			cur = &b6
		}
		if cur != nil {
			*cur = append(*cur, l)
		}
		if strings.TrimSpace(l) == "}" {
			// keep appending to the same section until the next rule header
		}
	}
	return strings.Join(b4, "\n"), strings.Join(b6, "\n")
}

func extractField(block, name string) string {
	for _, l := range strings.Split(block, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, name+":") {
			v := strings.TrimSpace(strings.TrimPrefix(l, name+":"))
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// ── unspecified addresses must never reach a rule, from ANY input path ──
//
// These cover a real defect found in adversarial review. parseTarget guarded
// operator-typed CIDRs against unspecified addresses, but the DNS-server and
// hostname paths fed already-parsed addresses straight into rules, bypassing
// it. NSTUN reads an all-zero dst_ip as "unset" = match-any, so:
//   - a 0.0.0.0 DNS server became ALLOW match-any on port 53, silently opening
//     every blocked destination on that port
//   - a hostname resolving to 0.0.0.0 became REJECT match-any, silently
//     severing all egress
// Both were reachable with a non-admin write-scoped key and both reported
// enforced:true with nothing in unenforced_rules.

func TestUnspecifiedDNSServerNeverBecomesAWildcardAllow(t *testing.T) {
	for _, bad := range []string{"0.0.0.0", "::"} {
		dns := []netip.Addr{netip.MustParseAddr(bad)}
		_, out := mustCompile(t, []*database.BlocklistRule{cidrRule(1, "1.1.1.0/24")}, dns)

		for _, b := range allRuleBlocks(string(out)) {
			if !strings.Contains(b, "action: ALLOW") {
				continue
			}
			dst := extractField(b, "dst_ip")
			if dst == "" {
				t.Errorf("%s: ALLOW with no dst_ip is match-any:\n%s", bad, b)
				continue
			}
			p, err := netip.ParsePrefix(dst)
			if err != nil {
				t.Errorf("%s: unparseable dst_ip %q", bad, dst)
				continue
			}
			if p.Addr().IsUnspecified() {
				t.Errorf("%s: unspecified DNS server compiled to a match-any ALLOW "+
					"(every blocked destination reachable on that port):\n%s", bad, b)
			}
		}
	}
}

func TestUnspecifiedHostnameResolutionNeverBecomesAWildcardReject(t *testing.T) {
	rules := []*database.BlocklistRule{{
		ID: 5, Kind: "custom", RuleType: database.BlocklistTypeHostname,
		Value: "0.0.0.0", Enabled: true,
	}}
	// net.LookupHost("0.0.0.0") really does return ["0.0.0.0"], which is how
	// this reached the compiler in the first place.
	c, err := compile(rules, func(string) []string { return []string{"0.0.0.0", "::"} },
		testCP(), DefaultGuestNet(), nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := render(c, DefaultGuestNet())

	for _, b := range allRuleBlocks(string(out)) {
		dst := extractField(b, "dst_ip")
		if dst == "" {
			t.Errorf("rule with no dst_ip is match-any:\n%s", b)
			continue
		}
		if p, err := netip.ParsePrefix(dst); err == nil && p.Addr().IsUnspecified() {
			t.Errorf("a hostname resolving to an unspecified address compiled to a "+
				"match-any rule (severs ALL egress):\n%s", b)
		}
	}
}

// TestDaemonExemptionIsPortScopedLikeTheSandboxRule pins the other half of the
// review finding. The compiled carve-out is ALLOW TCP <addr> dport=<port>, so
// exempting the whole address daemon-side made orvad strictly less restricted
// than a sandbox — on a single-node install the control-plane address is the
// host's own LAN IP, so an operator who blocklisted it still had webhook
// deliveries reaching every other port on that host.
func TestDaemonExemptionIsPortScopedLikeTheSandboxRule(t *testing.T) {
	cp := testCP()
	addr := cp.Addrs[0]
	// Blocklist the control-plane host itself.
	p := buildPolicy(t, []*database.BlocklistRule{cidrRule(1, addr.String()+"/32")}, nil)

	if p.Blocks(addr, uint16(cp.Port)) {
		t.Error("the SDK port must stay reachable or orvad cannot serve its own internal API")
	}
	for _, other := range []uint16{80, 443, 22, 9200} {
		if !p.Blocks(addr, other) {
			t.Errorf("port %d on a blocklisted control-plane host must be blocked "+
				"daemon-side; exempting the whole address is an SSRF hole", other)
		}
	}
}
