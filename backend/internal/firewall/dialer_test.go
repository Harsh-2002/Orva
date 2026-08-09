package firewall

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// The dialer is the daemon-side half of the policy: these tests assert the
// decision a real net.Dialer would make, so they never need the network.
// Nothing here contacts anything but a local httptest server.

func TestDialerBlocksBlockedDestination(t *testing.T) {
	p := buildPolicy(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24")}, nil)
	err := control(p, SubsystemWebhook)("tcp", "203.0.113.7:443", nil)
	if err == nil {
		t.Fatal("expected a blocked destination to be refused")
	}
	if !errors.Is(err, ErrBlockedByEgressPolicy) {
		t.Fatalf("error must unwrap to ErrBlockedByEgressPolicy, got %v", err)
	}
	// An operator reading the log or the delivery row must be able to tell
	// which subsystem was blocked and where it was headed.
	if !strings.Contains(err.Error(), SubsystemWebhook) ||
		!strings.Contains(err.Error(), "203.0.113.7:443") {
		t.Fatalf("error must name the subsystem and the destination, got %q", err)
	}
}

func TestDialerBlockedErrorSurvivesHTTPClient(t *testing.T) {
	// net/http wraps a dial failure in *url.Error; the whole point of
	// BlockedError.Unwrap is that callers can still classify it. TEST-NET-3 is
	// never routed, and Control rejects before any packet is sent anyway.
	p := buildPolicy(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24")}, nil)
	c := NewHTTPClient(p, SubsystemWebhook, 0)

	_, err := c.Get("http://203.0.113.7/hook")
	if err == nil {
		t.Fatal("expected the request to be refused")
	}
	if !errors.Is(err, ErrBlockedByEgressPolicy) {
		t.Fatalf("errors.Is must see through *url.Error, got %v", err)
	}
	var be *BlockedError
	if !errors.As(err, &be) || be.Subsystem != SubsystemWebhook {
		t.Fatalf("expected a *BlockedError naming the subsystem, got %v", err)
	}
}

func TestDialerNeverBlocksLoopbackOrControlPlane(t *testing.T) {
	// Both rules cover an address orvad must keep reaching: 127.0.0.0/8 its own
	// loopback, 172.16.0.0/12 the control plane the SDK talks to.
	p := buildPolicy(t, []*database.BlocklistRule{
		cidrRule(1, "127.0.0.0/8"), cidrRule(2, "172.16.0.0/12"),
	}, nil)
	ctl := control(p, SubsystemWebhook)

	for _, dest := range []string{"127.0.0.1:8443", "172.17.0.2:8443"} {
		if err := ctl("tcp", dest, nil); err != nil {
			t.Errorf("%s must never be refused daemon-side: %v", dest, err)
		}
	}
	// The rest of the covering range is still enforced.
	if err := ctl("tcp", "172.20.1.1:443", nil); err == nil {
		t.Error("other addresses in the blocked range must still be refused")
	}
}

func TestDialerReachesLoopbackServerThroughCoveringReject(t *testing.T) {
	// End-to-end through a real dialer: a policy that blocks all of 127.0.0.0/8
	// must not stop orvad from completing a loopback request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := buildPolicy(t, []*database.BlocklistRule{cidrRule(1, "127.0.0.0/8")}, nil)
	resp, err := NewHTTPClient(p, SubsystemWebhook, 0).Get(srv.URL)
	if err != nil {
		t.Fatalf("loopback request must succeed through the guarded dialer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got status %d, want 204", resp.StatusCode)
	}
}

func TestDialerAllowsUnblockedDestination(t *testing.T) {
	p := buildPolicy(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24")}, nil)
	ctl := control(p, SubsystemWebhook)

	// Outside every rule: NSTUN's default-allow, mirrored daemon-side.
	if err := ctl("tcp", "198.51.100.7:443", nil); err != nil {
		t.Errorf("unblocked destination must dial: %v", err)
	}
	// A unix socket has no address a packet policy can evaluate.
	if err := ctl("unix", "/run/orva.sock", nil); err != nil {
		t.Errorf("non-IP network must not be refused: %v", err)
	}
}

func TestDialerWithoutPolicyBlocksNothing(t *testing.T) {
	cases := map[string]Guard{
		"nil guard":            nil,
		"nil policy":           (*Policy)(nil),
		"manager, no compile":  &Manager{},
		"guard func says open": GuardFunc(func(netip.Addr, uint16) bool { return false }),
	}
	for name, g := range cases {
		t.Run(name, func(t *testing.T) {
			if err := control(g, SubsystemWebhook)("tcp", "203.0.113.7:443", nil); err != nil {
				t.Fatalf("daemon egress must stay open when no policy is in force: %v", err)
			}
		})
	}
}

func TestCheckEndpointRefusesBlockedEndpoint(t *testing.T) {
	// An IP literal resolves without touching DNS.
	p := buildPolicy(t, []*database.BlocklistRule{cidrRule(1, "203.0.113.0/24")}, nil)

	err := CheckEndpoint(context.Background(), p, SubsystemAIGateway, "https://203.0.113.9")
	if err == nil {
		t.Fatal("expected the blocked provider endpoint to be refused")
	}
	if !errors.Is(err, ErrBlockedByEgressPolicy) {
		t.Fatalf("error must unwrap to ErrBlockedByEgressPolicy, got %v", err)
	}
	if !strings.Contains(err.Error(), SubsystemAIGateway) {
		t.Fatalf("error must name the subsystem, got %q", err)
	}

	if err := CheckEndpoint(context.Background(), p, SubsystemAIGateway, "https://198.51.100.9"); err != nil {
		t.Errorf("unblocked provider endpoint must pass: %v", err)
	}
	if err := CheckEndpoint(context.Background(), nil, SubsystemAIGateway, "https://203.0.113.9"); err != nil {
		t.Errorf("nil guard must allow: %v", err)
	}
}

func TestCheckEndpointHonoursPortCarveOuts(t *testing.T) {
	// The control-plane carve-out is port-scoped, so the port parsed out of the
	// endpoint has to reach Blocks or the exemption silently stops matching.
	p := buildPolicy(t, []*database.BlocklistRule{cidrRule(1, "172.16.0.0/12")}, nil)

	if err := CheckEndpoint(context.Background(), p, SubsystemAIGateway, "http://172.17.0.2:8443"); err != nil {
		t.Errorf("control-plane endpoint must never be refused: %v", err)
	}
	if err := CheckEndpoint(context.Background(), p, SubsystemAIGateway, "http://172.20.1.1:11434"); err == nil {
		t.Error("blocked endpoint must be refused")
	}
}

func TestSplitEndpointFormats(t *testing.T) {
	cases := []struct {
		raw  string
		host string
		port uint16
	}{
		{"https://api.example.com", "api.example.com", 443},
		{"http://api.example.com", "api.example.com", 80},
		{"http://api.example.com:9000/v1", "api.example.com", 9000},
		{"localhost:11434", "localhost", 11434}, // schemeless Ollama base URL
		{"api.example.com", "api.example.com", 0},
	}
	for _, c := range cases {
		host, port := splitEndpoint(c.raw)
		if host != c.host || port != c.port {
			t.Errorf("splitEndpoint(%q) = %q,%d; want %q,%d", c.raw, host, port, c.host, c.port)
		}
	}
}
