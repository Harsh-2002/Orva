package handlers

import (
	"strings"
	"testing"
)

// TestDNSSearchRejectsNewlineInjection — firewall/dns.go renders the search
// domain as "search %s\n" BEFORE the nameserver lines, and resolv.conf takes
// the first nameserver it sees. A newline in this field therefore installs a
// resolver of the caller's choosing for every egress-enabled sandbox, and it
// is persisted in system_config. Servers and record hosts were validated;
// this field only had TrimSpace.
func TestDNSSearchRejectsNewlineInjection(t *testing.T) {
	bad := []string{
		"corp.local\nnameserver 203.0.113.9",
		"corp.local\r\nnameserver 203.0.113.9",
		"corp.local nameserver\n203.0.113.9",
		"a\tb\nnameserver 1.1.1.1",
	}
	for _, in := range bad {
		t.Run(strings.ReplaceAll(in, "\n", "\\n"), func(t *testing.T) {
			got, err := sanitizeDNSSearch(in)
			if err == nil {
				t.Errorf("search %q accepted as %q; it carries a line break "+
					"that would inject a nameserver line", in, got)
			}
		})
	}
}

// TestDNSSearchAcceptsRealSearchLists — the field legitimately holds several
// space-separated domains, so the fix must not break ordinary use.
func TestDNSSearchAcceptsRealSearchLists(t *testing.T) {
	good := []string{
		"corp.local",
		"corp.local internal.example.com",
		"svc.cluster.local cluster.local",
		"db",
	}
	for _, in := range good {
		t.Run(in, func(t *testing.T) {
			got, err := sanitizeDNSSearch(in)
			if err != nil {
				t.Errorf("legitimate search list %q rejected: %v", in, err)
			}
			if got != in {
				t.Errorf("sanitize(%q) = %q, want it unchanged", in, got)
			}
		})
	}
}
