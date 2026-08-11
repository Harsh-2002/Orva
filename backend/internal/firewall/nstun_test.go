package firewall

import (
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// TestRenderedConfigIsAcceptedByNsjail is the only guard against shipping a
// textproto typo: nsjail treats an unparseable --config as fatal, so if this
// passes the field names and enum spellings are right.
//
// Skipped when nsjail is unavailable (non-Linux dev boxes, CI lanes without
// the sandbox). The e2e suite covers it where nsjail is mandatory.
func TestRenderedConfigIsAcceptedByNsjail(t *testing.T) {
	bin, err := exec.LookPath("nsjail")
	if err != nil {
		t.Skip("nsjail not on PATH")
	}

	rules := []*database.BlocklistRule{
		{ID: 1, Kind: "default", RuleType: database.BlocklistTypeCIDR,
			Value: "169.254.0.0/16", Enabled: true},
		{ID: 2, Kind: "suggested", RuleType: database.BlocklistTypeCIDR,
			Value: "10.0.0.0/8", Enabled: true},
		{ID: 3, Kind: "custom", RuleType: database.BlocklistTypeCIDR,
			Value: "2001:db8::/32", Enabled: true},
	}
	c, err := compile(rules, noResolve, testCP(), DefaultGuestNet(),
		[]netip.Addr{netip.MustParseAddr("1.1.1.1")})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rendered := render(c, DefaultGuestNet())
	t.Logf("rendered policy:\n%s", rendered)

	path := filepath.Join(t.TempDir(), "egress.cfg")
	if err := os.WriteFile(path, rendered, 0o600); err != nil {
		t.Fatal(err)
	}

	// --config is parsed before anything else runs, so nsjail will reject a
	// malformed policy regardless of whether the jail itself can start here.
	out, _ := exec.Command(bin, "--config", path, "-Mo", "-q", "--", "/bin/true").CombinedOutput()
	got := string(out)

	for _, bad := range []string{
		"Couldn't parse configuration",
		"Couldn't parse the configuration",
		"Failed to parse",
		"Unknown field",
		"unknown enum",
	} {
		if strings.Contains(got, bad) {
			t.Fatalf("nsjail rejected the generated policy (%q):\n%s\n--- policy ---\n%s",
				bad, got, rendered)
		}
	}
}

// TestNsjailRejectsAKnownBadConfig proves the check above can actually fail,
// i.e. that we are not just asserting on output nsjail never produces.
func TestNsjailRejectsAKnownBadConfig(t *testing.T) {
	bin, err := exec.LookPath("nsjail")
	if err != nil {
		t.Skip("nsjail not on PATH")
	}
	path := filepath.Join(t.TempDir(), "bad.cfg")
	if err := os.WriteFile(path, []byte("user_net { rule4 { action: NOPE } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "--config", path, "-Mo", "-q", "--", "/bin/true").CombinedOutput()
	if err == nil {
		t.Fatalf("nsjail accepted an invalid config; output:\n%s", out)
	}
}

func TestPublishIsAtomicAndReReadable(t *testing.T) {
	dir := t.TempDir()
	rendered := []byte("mount_proc: true\nuser_net { backend: NSTUN }\n")
	gen := genOf(rendered)

	path, err := publish(dir, gen, rendered)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(rendered) {
		t.Fatalf("content mismatch:\n%s", got)
	}
	if !strings.Contains(path, gen) {
		t.Errorf("published path %q does not name the generation %q", path, gen)
	}

	// No .tmp residue.
	entries, _ := os.ReadDir(PolicyDir(dir))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	// Mode 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("policy mode = %o, want 600", perm)
	}
}

func TestPublishIsIdempotentForSameGeneration(t *testing.T) {
	dir := t.TempDir()
	rendered := []byte("mount_proc: true\n")
	gen := genOf(rendered)

	p1, err := publish(dir, gen, rendered)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := publish(dir, gen, rendered)
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Fatalf("same generation published to two paths: %s vs %s", p1, p2)
	}
}

func TestPublishGCKeepsRecentGenerations(t *testing.T) {
	dir := t.TempDir()
	var last string
	for i := 0; i < generationsKept+4; i++ {
		rendered := []byte(strings.Repeat("x", i+1) + "\n")
		p, err := publish(dir, genOf(rendered), rendered)
		if err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
		last = p
	}

	entries, err := os.ReadDir(PolicyDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	var cfgs int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".cfg") {
			cfgs++
		}
	}
	if cfgs > generationsKept {
		t.Errorf("kept %d generations, want <= %d", cfgs, generationsKept)
	}
	// The generation currently in use must survive GC unconditionally.
	if _, err := os.Stat(last); err != nil {
		t.Errorf("current generation was garbage collected: %v", err)
	}
}

func TestPublishFailsOnUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	// Make the firewall/ parent unwritable so policy/ cannot be created.
	fw := filepath.Join(dir, "firewall")
	if err := os.MkdirAll(fw, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := publish(dir, "deadbeef", []byte("x")); err == nil {
		t.Fatal("expected publish to fail when the policy dir cannot be created")
	}
}
