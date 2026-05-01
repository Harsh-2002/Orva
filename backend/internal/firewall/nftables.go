package firewall

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// nftables-backed enforcement. The Manager calls nftablesApply with the
// effective IPv4 and IPv6 CIDR sets every refresh. We render a single
// `nft -f -` script that idempotently rebuilds our table — that way we
// never leave stale rules around.
//
// Table layout:
//
//   table inet orva_firewall {
//     set orva_egress_v4 {
//       type ipv4_addr
//       flags interval
//       elements = { ... CIDRs ... }
//     }
//     set orva_egress_v6 {
//       type ipv6_addr
//       flags interval
//       elements = { ... CIDRs ... }
//     }
//     chain output {
//       type filter hook output priority 0;
//       ip  daddr @orva_egress_v4 reject with icmp type host-unreachable
//       ip6 daddr @orva_egress_v6 reject with icmpv6 type addr-unreachable
//     }
//   }
//
// We use `reject` instead of `drop` so handlers see EHOSTUNREACH
// immediately rather than hanging until their own timeout. Easier to
// debug and the operator gets a clean error in the function logs.

const tableName = "orva_firewall"

// nftablesAvail caches whether `nft` is on PATH and works; probed lazily
// the first time nftablesAvailable() is called. If unavailable, Apply
// becomes a no-op and the API surfaces a warning via Snapshot.LastError.
//
// Lazy probe — not init() — so that simply importing this package (e.g.,
// by the CLI which transitively pulls in internal/server) does not run
// nft and emit warnings before the user has even invoked a subcommand.
// The probe still runs at most once per process via nftablesProbeOnce.
var (
	nftablesAvail     atomic.Bool
	nftablesProbeOnce sync.Once
)

func nftablesAvailable() bool {
	nftablesProbeOnce.Do(probeNftables)
	return nftablesAvail.Load()
}

func probeNftables() {
	if _, err := exec.LookPath("nft"); err == nil {
		// Probe: try a no-op list. If we don't have CAP_NET_ADMIN this
		// will fail; that's information we want surfaced to the operator.
		out, err := exec.Command("nft", "list", "tables").CombinedOutput()
		if err == nil {
			nftablesAvail.Store(true)
			_ = out
		} else {
			slog.Warn("nft list failed; firewall enforcement will be disabled",
				"err", err, "output", string(out))
		}
	} else {
		slog.Warn("nft binary not on PATH; firewall enforcement will be disabled")
	}
}

// nftablesApply rebuilds the orva_firewall table with the given sets.
// Idempotent — if nothing changed, the operation is harmless. If the
// sets are empty, the table is created with empty sets so a "deny
// everything matching" chain still exists (matching nothing).
func nftablesApply(v4, v6 []string) error {
	if !nftablesAvailable() {
		return nil // best-effort: silently no-op
	}

	var b bytes.Buffer
	// flush + delete is the pattern that makes `nft -f -` re-runnable
	// without leaking old rules.
	fmt.Fprintf(&b, "delete table inet %s\n", tableName)
	fmt.Fprintf(&b, "table inet %s {\n", tableName)
	fmt.Fprintf(&b, "  set orva_egress_v4 {\n")
	fmt.Fprintf(&b, "    type ipv4_addr\n")
	fmt.Fprintf(&b, "    flags interval\n")
	if len(v4) > 0 {
		fmt.Fprintf(&b, "    elements = { %s }\n", strings.Join(v4, ", "))
	}
	fmt.Fprintf(&b, "  }\n")
	fmt.Fprintf(&b, "  set orva_egress_v6 {\n")
	fmt.Fprintf(&b, "    type ipv6_addr\n")
	fmt.Fprintf(&b, "    flags interval\n")
	if len(v6) > 0 {
		fmt.Fprintf(&b, "    elements = { %s }\n", strings.Join(v6, ", "))
	}
	fmt.Fprintf(&b, "  }\n")
	fmt.Fprintf(&b, "  chain output {\n")
	fmt.Fprintf(&b, "    type filter hook output priority 0;\n")
	fmt.Fprintf(&b, "    oif lo accept\n")
	fmt.Fprintf(&b, "    ip  daddr @orva_egress_v4 reject with icmp type host-unreachable\n")
	fmt.Fprintf(&b, "    ip6 daddr @orva_egress_v6 reject with icmpv6 type addr-unreachable\n")
	fmt.Fprintf(&b, "  }\n")
	fmt.Fprintf(&b, "}\n")

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = &b
	out, err := cmd.CombinedOutput()
	if err != nil {
		// If the delete failed because the table didn't exist, that's
		// fine on first apply — retry without the delete prefix.
		errStr := string(out) + " " + err.Error()
		if strings.Contains(errStr, "No such file or directory") ||
			strings.Contains(errStr, "does not exist") {
			b.Reset()
			fmt.Fprintf(&b, "table inet %s {\n", tableName)
			fmt.Fprintf(&b, "  set orva_egress_v4 { type ipv4_addr; flags interval; %s }\n",
				elementsLine(v4))
			fmt.Fprintf(&b, "  set orva_egress_v6 { type ipv6_addr; flags interval; %s }\n",
				elementsLine(v6))
			fmt.Fprintf(&b, "  chain output { type filter hook output priority 0;\n")
			fmt.Fprintf(&b, "    oif lo accept\n")
			fmt.Fprintf(&b, "    ip  daddr @orva_egress_v4 reject with icmp type host-unreachable\n")
			fmt.Fprintf(&b, "    ip6 daddr @orva_egress_v6 reject with icmpv6 type addr-unreachable\n")
			fmt.Fprintf(&b, "  }\n}\n")
			cmd = exec.Command("nft", "-f", "-")
			cmd.Stdin = &b
			out, err = cmd.CombinedOutput()
			if err != nil {
				return errors.New("nft apply failed: " + strings.TrimSpace(string(out)) + ": " + err.Error())
			}
			return nil
		}
		return errors.New("nft apply failed: " + strings.TrimSpace(string(out)) + ": " + err.Error())
	}
	return nil
}

func elementsLine(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return "elements = { " + strings.Join(items, ", ") + " };"
}

// nftablesFlush is called on shutdown to drop our table.
func nftablesFlush() error {
	if !nftablesAvailable() {
		return nil
	}
	cmd := exec.Command("nft", "delete", "table", "inet", tableName)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "does not exist") &&
		!strings.Contains(string(out), "No such file") {
		return errors.New(string(out))
	}
	return nil
}
