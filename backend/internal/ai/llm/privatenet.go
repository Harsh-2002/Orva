package llm

import (
	"context"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/network"
)

// Reaching a model endpoint on the LAN.
//
// Bifrost installs its own dialer on every provider client
// (providers/utils.ConfigureDialer): it resolves the host itself, then refuses
// to dial any RFC1918 address unless NetworkConfig.AllowPrivateNetwork is set.
// Orva never set it, so a LAN-hosted endpoint — ollama, vLLM, an internal
// gateway, the single most common reason a self-hosted install brings its own
// provider — could not be reached at all. Worse, the refusal surfaced as
// "failed to execute HTTP request to provider API", because Bifrost's
// GetErrorString drops the wrapped cause (see bifrostErr).
//
// That default guards against a threat Orva does not have. A provider base URL
// is admin-configured through /api/v1/ai/providers, not attacker-supplied, and
// Orva already governs its own outbound traffic with the operator's egress
// blocklist — enforced for this subsystem by the CheckEndpoint preflight that
// runs before every turn. Two overlapping controls with different opinions
// meant the operator's explicit configuration silently lost to a library
// default.
//
// So the flag is delegated to what the operator actually configured, and only
// that: it is enabled when the configured base URL is ITSELF a private
// destination. For a public provider (api.openai.com) it stays false, so
// Bifrost keeps its resolve-then-dial guard fully intact there and a public
// hostname can never be rebound onto a private address mid-flight. Link-local
// (169.254/16 — the cloud metadata address) is refused by Bifrost
// unconditionally and is not reachable through this either way.

// privateHostTTL bounds how long a hostname's resolution is trusted. Short
// enough that moving an internal endpoint takes effect on its own, long enough
// that the lookup never lands on the request path.
const privateHostTTL = 5 * time.Minute

// privateHostResolveTimeout keeps a wedged internal resolver from stalling the
// first request to a provider. On timeout the answer is "not private", which
// leaves Bifrost's guard in place rather than widening it on a failed lookup.
const privateHostResolveTimeout = 2 * time.Second

type privateHostEntry struct {
	allow bool
	at    time.Time
}

// privateHostCache memoises the decision per base URL.
//
// Bifrost calls GetConfigForProvider on every request (core/bifrost.go), so an
// uncached DNS lookup there would put a resolution in front of every single
// model call. IP literals never touch the resolver at all.
type privateHostCache struct {
	mu sync.Mutex
	m  map[string]privateHostEntry
}

func (c *privateHostCache) allow(baseURL string) bool {
	host := hostFromBaseURL(baseURL)
	if host == "" {
		return false
	}

	// An IP literal has no rebinding window: what the operator typed is exactly
	// what gets dialled, so this is decided without DNS and without caching.
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateTarget(ip)
	}

	c.mu.Lock()
	if e, ok := c.m[host]; ok && time.Since(e.at) < privateHostTTL {
		c.mu.Unlock()
		return e.allow
	}
	c.mu.Unlock()

	allow := resolvesEntirelyPrivate(host)

	c.mu.Lock()
	if c.m == nil {
		c.m = make(map[string]privateHostEntry)
	}
	c.m[host] = privateHostEntry{allow: allow, at: time.Now()}
	c.mu.Unlock()
	return allow
}

// resolvesEntirelyPrivate requires EVERY address to be private. A mixed result
// means the name also answers publicly, and widening the dialer on the strength
// of one private answer is exactly the rebinding shape the guard exists to stop.
func resolvesEntirelyPrivate(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), privateHostResolveTimeout)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !isPrivateTarget(ip) {
			return false
		}
	}
	return true
}

// isPrivateTarget uses Bifrost's own predicate rather than a reimplementation,
// so this decision cannot drift from the check that actually runs at dial time.
// Unspecified addresses are excluded explicitly: IsPrivateIP answers true for
// them, and 0.0.0.0 is not a destination an operator ever means to configure.
// Link-local is excluded because Bifrost refuses it regardless of this flag —
// claiming otherwise here would be a lie about what is reachable.
func isPrivateTarget(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || network.IsLinkLocal(ip) {
		return false
	}
	return network.IsPrivateIP(ip)
}

// hostFromBaseURL uses url.Hostname rather than splitting u.Host by hand.
// SplitHostPort fails when there is no port, and the fallback of returning
// u.Host verbatim keeps the brackets on an IPv6 literal ("[fc00::1]"), which
// net.ParseIP rejects — so a bracketed v6 literal with no port was treated as a
// hostname, took the full resolver timeout on first use, and was then judged on
// a lookup that could never succeed. Hostname strips the port and the brackets.
func hostFromBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
