package firewall

// Daemon-side enforcement of the same egress policy the sandboxes get.
//
// The deleted nftables implementation hooked `output` in orvad's own network
// namespace, so every process in the container — orvad included — was filtered.
// The NSTUN policy that replaced it is loaded per sandbox, so without this file
// the daemon's own outbound calls would be the one unfiltered way out of the
// box. handlers.validateWebhookURL depends on it explicitly: it deliberately
// accepts private-range URLs because the operator's policy is what decides
// whether a delivery may leave, and that decision now happens here.
//
// The check runs in net.Dialer.Control, which fires AFTER name resolution with
// the concrete address the kernel is about to connect to. That ordering is the
// point: a hostname that resolved to an allowed address when the operator saved
// it and to a blocked one at connect time is still caught, because what gets
// evaluated is the address being dialled, not the name it came from.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"syscall"
	"time"
)

// Subsystem labels for daemon-side callers. A refused connection names the
// feature that attempted it, so an operator sees which subsystem is blocked
// instead of a mystery timeout.
const (
	SubsystemWebhook     = "webhook-delivery"
	SubsystemAIProviders = "ai-provider-catalog"
	SubsystemAIGateway   = "ai-gateway"
)

// Guard is the policy source consulted before every outbound connection. Both
// *Manager and *Policy implement it, so production code passes the live manager
// (which tracks generations) and tests pass a compiled policy directly. Taking
// an interface rather than reaching for the manager keeps this package free of
// a global and its callers free of an import cycle.
//
// A nil Guard blocks nothing — same posture as Manager.Blocks before the first
// policy compiles: the daemon's own traffic predates the policy and must not be
// severed by its absence.
type Guard interface {
	Blocks(addr netip.Addr, port uint16) bool
}

// GuardFunc adapts a plain function to Guard.
type GuardFunc func(netip.Addr, uint16) bool

// Blocks implements Guard.
func (f GuardFunc) Blocks(a netip.Addr, p uint16) bool { return f(a, p) }

// BlockedError names the refused destination and the subsystem that tried it.
// It unwraps to ErrBlockedByEgressPolicy so callers can errors.Is it even
// through the *url.Error that net/http wraps a dial failure in.
type BlockedError struct {
	Subsystem string
	Network   string
	Address   string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%s: %s: %s %s", e.Subsystem, ErrBlockedByEgressPolicy, e.Network, e.Address)
}

func (e *BlockedError) Unwrap() error { return ErrBlockedByEgressPolicy }

// NewDialer returns a dialer that refuses destinations the operator's egress
// policy blocks. Timeouts match net/http's DefaultTransport so swapping this in
// changes the guard and nothing else.
func NewDialer(g Guard, subsystem string) *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   control(g, subsystem),
	}
}

// NewTransport clones DefaultTransport (keeping its proxy support, HTTP/2
// upgrade and idle-connection pooling) and swaps in the guarded dialer.
func NewTransport(g Guard, subsystem string) *http.Transport {
	dial := NewDialer(g, subsystem).DialContext
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{DialContext: dial}
	}
	t := base.Clone()
	t.DialContext = dial
	return t
}

// NewHTTPClient is the client every daemon-side caller should use for outbound
// requests. timeout bounds the whole request, as on any http.Client.
func NewHTTPClient(g Guard, subsystem string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: NewTransport(g, subsystem)}
}

// control builds the Control hook. Kept separate from NewDialer so the decision
// is testable without opening a socket.
func control(g Guard, subsystem string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if g == nil {
			return nil
		}
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			// Not an ip:port destination (a unix socket, say). There is no
			// address to evaluate against a packet policy, and failing closed
			// here would break unrelated local dials without tightening
			// anything an operator can express.
			return nil
		}
		if !g.Blocks(ap.Addr().Unmap(), ap.Port()) {
			return nil
		}
		slog.Warn("egress policy refused an outbound connection from orvad",
			"subsystem", subsystem, "network", network, "dest", address)
		return &BlockedError{Subsystem: subsystem, Network: network, Address: address}
	}
}

// CheckEndpoint resolves rawURL and reports a BlockedError when the policy
// refuses any address it answers with.
//
// This is a PRE-FLIGHT gate, deliberately weaker than the Control hook above,
// and exists for the one daemon-side path a dialer cannot reach: the embedded
// Bifrost gateway builds its own fasthttp client per provider and exposes no
// dial hook, so an AI provider stream cannot be filtered at connect time. The
// address can change between this call and that connection (DNS rebinding),
// which Control would have caught and this cannot. Use NewHTTPClient wherever
// the client is ours; reach for this only when it is not.
//
// Any resolved address being blocked refuses the whole endpoint: connecting to
// a sibling A record of a host the operator blocked would be an obvious bypass.
func CheckEndpoint(ctx context.Context, g Guard, subsystem, rawURL string) error {
	if g == nil || rawURL == "" {
		return nil
	}
	host, port := splitEndpoint(rawURL)
	if host == "" {
		return nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		// A destination that does not resolve cannot be dialled either, so the
		// caller's own connection attempt will fail with a better message than
		// anything this gate could invent.
		return nil
	}
	for _, a := range addrs {
		a = a.Unmap()
		if !g.Blocks(a, port) {
			continue
		}
		dest := net.JoinHostPort(a.String(), strconv.Itoa(int(port)))
		slog.Warn("egress policy refused an outbound connection from orvad",
			"subsystem", subsystem, "host", host, "dest", dest, "preflight", true)
		return &BlockedError{Subsystem: subsystem, Network: "tcp", Address: dest}
	}
	return nil
}

// splitEndpoint pulls host and port out of a URL, falling back to a bare
// host[:port] so a schemeless operator base URL (Ollama's "localhost:11434")
// is still evaluated rather than silently skipped.
func splitEndpoint(raw string) (host string, port uint16) {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host = u.Hostname()
		if p, perr := strconv.Atoi(u.Port()); perr == nil && p > 0 && p < 65536 {
			return host, uint16(p)
		}
		if u.Scheme == "https" {
			return host, 443
		}
		return host, 80
	}
	if h, p, err := net.SplitHostPort(raw); err == nil {
		if n, perr := strconv.Atoi(p); perr == nil && n > 0 && n < 65536 {
			return h, uint16(n)
		}
		return h, 0
	}
	// No port anywhere: 0 means "no port constraint", which still matches every
	// blocklist reject (those carry no port) and skips the carve-outs, which do.
	return raw, 0
}
