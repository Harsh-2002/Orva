package llm

import (
	"testing"
	"time"
)

// TestAllowPrivateNetworkDecidesFromTheConfiguredBaseURL is the guard on the
// one thing this flag must never do: widen Bifrost's dialer for a public
// provider. It is enabled only when the operator pointed the provider at a
// private destination themselves.
func TestAllowPrivateNetworkDecidesFromTheConfiguredBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    bool
		why     string
	}{
		// The case this whole change exists for: a LAN-hosted model endpoint.
		{"rfc1918 class A", "http://10.1.1.20:11434/v1", true, "a LAN ollama must be reachable"},
		{"rfc1918 class B", "http://172.16.4.4:8000/v1", true, "a LAN vLLM must be reachable"},
		{"rfc1918 class C", "http://192.168.1.50:11434/v1", true, "the common homelab address"},
		{"ipv6 unique-local", "http://[fc00::1]:11434/v1", true, "the v6 equivalent of RFC1918"},

		// Public providers keep Bifrost's resolve-then-dial rebinding guard.
		{"public v4 literal", "https://1.1.1.1/v1", false, "a public literal must not widen the dialer"},
		{"public v6 literal", "https://[2606:4700::1111]/v1", false, "same, over v6"},

		// Never, under any configuration.
		{"link-local metadata", "http://169.254.169.254/v1", false,
			"the cloud metadata address; Bifrost refuses it regardless, so claiming " +
				"otherwise here would misreport what is reachable"},
		{"ipv6 link-local", "http://[fe80::1]/v1", false, "the v6 metadata/link-local range"},
		{"unspecified", "http://0.0.0.0:11434/v1", false,
			"IsPrivateIP answers true for 0.0.0.0, so this must be excluded explicitly"},

		// Nothing configured, or nothing parseable, means no widening.
		{"empty", "", false, "no base URL configured"},
		{"no host", "notaurl", false, "unparseable"},
		{"scheme only", "http://", false, "no host to judge"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c privateHostCache
			if got := c.allow(tc.baseURL); got != tc.want {
				t.Errorf("allow(%q) = %v, want %v — %s", tc.baseURL, got, tc.want, tc.why)
			}
		})
	}
}

// TestAllowPrivateNetworkFailsClosedOnResolutionFailure pins the direction of
// the failure. A name that will not resolve must leave Bifrost's guard in
// place, never widen it.
func TestAllowPrivateNetworkFailsClosedOnResolutionFailure(t *testing.T) {
	var c privateHostCache
	// .invalid is reserved by RFC 2606 and is guaranteed never to resolve, so
	// this stays offline and deterministic.
	if c.allow("http://orva-no-such-host.invalid:11434/v1") {
		t.Fatal("an unresolvable host must not enable private-network dialing")
	}
}

// TestAllowPrivateNetworkResolvesHostnames covers the ollama.lan shape: an
// operator names their internal box instead of typing its address. localhost is
// used because it resolves without a network and only to loopback.
func TestAllowPrivateNetworkResolvesHostnames(t *testing.T) {
	var c privateHostCache
	if !c.allow("http://localhost:11434/v1") {
		t.Fatal("a hostname resolving only to a private address must be allowed")
	}
}

// TestAllowPrivateNetworkCachesHostnameLookups proves the resolution stays off
// the request path. Bifrost calls GetConfigForProvider on every request, so an
// uncached lookup here would sit in front of every model call.
func TestAllowPrivateNetworkCachesHostnameLookups(t *testing.T) {
	var c privateHostCache
	const url = "http://localhost:11434/v1"

	if !c.allow(url) {
		t.Fatal("precondition: localhost should be allowed")
	}
	c.mu.Lock()
	entry, ok := c.m["localhost"]
	c.mu.Unlock()
	if !ok {
		t.Fatal("hostname decision was not cached; every request would re-resolve")
	}
	if !entry.allow {
		t.Fatal("cached the wrong answer")
	}

	// Poison the cache with the opposite answer. A second call returning the
	// poisoned value is the proof that the cache was consulted rather than the
	// resolver being hit again.
	c.mu.Lock()
	c.m["localhost"] = privateHostEntry{allow: false, at: time.Now()}
	c.mu.Unlock()
	if c.allow(url) {
		t.Fatal("second call re-resolved instead of using the cached decision")
	}
}

// TestAllowPrivateNetworkSkipsTheCacheForIPLiterals — an IP literal has no
// rebinding window and needs no resolver, so it must never occupy the cache.
func TestAllowPrivateNetworkSkipsTheCacheForIPLiterals(t *testing.T) {
	// Both forms, and both with and without a port: a bracketed IPv6 literal
	// with no port used to miss the literal path entirely, fall through to the
	// resolver, and land in the cache after a full DNS timeout.
	for _, u := range []string{
		"http://10.1.1.20:11434/v1",
		"http://10.1.1.20/v1",
		"http://[fc00::1]:11434/v1",
		"http://[fc00::1]/v1",
		"https://[2606:4700::1111]/v1",
	} {
		var c privateHostCache
		start := time.Now()
		c.allow(u)
		elapsed := time.Since(start)
		c.mu.Lock()
		n := len(c.m)
		c.mu.Unlock()
		if n != 0 {
			t.Errorf("%s: IP literals must not be cached (no lookup happens); cache holds %d entries", u, n)
		}
		if elapsed > time.Second {
			t.Errorf("%s: took %s — an IP literal must be decided without touching the resolver", u, elapsed)
		}
	}
}

// TestExpiredCacheEntryIsRefreshed keeps the TTL honest: an operator moving an
// internal endpoint must not be stuck with the old decision forever.
func TestExpiredCacheEntryIsRefreshed(t *testing.T) {
	var c privateHostCache
	c.m = map[string]privateHostEntry{
		"localhost": {allow: false, at: time.Now().Add(-2 * privateHostTTL)},
	}
	if !c.allow("http://localhost:11434/v1") {
		t.Fatal("an expired entry must be re-resolved, not trusted")
	}
}
