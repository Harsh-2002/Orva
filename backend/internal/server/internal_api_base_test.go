package server

import (
	"net"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/config"
)

func TestInternalAPIBaseSelectsLocalInterfaceNotGateway(t *testing.T) {
	route := "Iface\tDestination\tGateway\tFlags\neth0\t00000000\t01006064\t0003\n"
	if got := parseDefaultRouteInterface(route); got != "eth0" {
		t.Fatalf("default route interface = %q, want eth0", got)
	}
	// The gateway is 100.96.0.1 and may serve a different Orva. Only an
	// address assigned to this process's interface is eligible.
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("100.96.0.2"), Mask: net.CIDRMask(30, 32)},
	}
	if got, ok := localInternalAPIBase(addrs, 8443); !ok || got != "http://100.96.0.2:8443" {
		t.Fatalf("local SDK base = %q, found=%v", got, ok)
	}
}

func TestInternalAPIBaseDoesNotUseLoopbackOrLinkLocal(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("169.254.1.2"), Mask: net.CIDRMask(16, 32)},
		&net.IPNet{IP: net.ParseIP("fd00::1"), Mask: net.CIDRMask(64, 128)},
	}
	if got, ok := localInternalAPIBase(addrs, 8443); ok {
		t.Fatalf("ineligible SDK base = %q", got)
	}
}

func TestInternalAPIBaseExplicitOverride(t *testing.T) {
	t.Setenv("ORVA_INTERNAL_API_BASE", "http://127.0.0.2:9000/")
	if got := detectInternalAPIBase(8443); got != "http://127.0.0.2:9000" {
		t.Fatalf("SDK override = %q", got)
	}
}

func TestSandboxTemplateKeepsConfiguredSeccompPolicy(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sandbox.SeccompPolicy = "strict"
	tmpl := newSandboxTemplate(cfg, nil, nil, "http://192.0.2.10:8443")
	if tmpl.DefaultSeccomp != "strict" {
		t.Fatalf("worker policy = %q, want strict", tmpl.DefaultSeccomp)
	}
	if tmpl.DefaultMaxPids != cfg.Functions.DefaultMaxPids || tmpl.APIBaseURL != "http://192.0.2.10:8443" {
		t.Fatalf("worker limits or SDK base lost: pids=%d base=%q", tmpl.DefaultMaxPids, tmpl.APIBaseURL)
	}
}
