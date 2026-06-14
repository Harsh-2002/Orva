package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// dnsCmd exposes the sandbox DNS configuration (Settings → Firewall → DNS in
// the dashboard) to the terminal. Sandboxes running with network_mode=egress
// see this config as their /etc/resolv.conf (servers + search domain) and
// /etc/hosts (host→IP overrides). It hits the same /api/v1/firewall/dns
// endpoints the dashboard uses.
var dnsCmd = &cobra.Command{
	Use:   "dns",
	Short: "View or set sandbox DNS config",
	Long: `View or set the DNS configuration handed to egress-enabled sandboxes.

Servers are literal upstream resolver IPs (never hostnames). An optional
search domain is appended to bare names. Host overrides map a hostname to a
fixed IP and beat upstream DNS — they're written into the sandbox /etc/hosts.

An empty server list falls back to Orva's shipped default resolvers.`,
	Example: `  # Show the current config
  orva dns get

  # Point sandboxes at specific resolvers with a search domain
  orva dns set --server 1.1.1.1 --server 8.8.8.8 --search corp.internal

  # Pin a hostname to an IP (repeatable) and clear it back to defaults
  orva dns set --host db.internal=10.0.0.5 --host cache=10.0.0.6
  orva dns set --server "" --search ""`,
}

var dnsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the current sandbox DNS config",
	Long:  `Show the upstream resolver IPs, search domain, host overrides, and the shipped default resolvers.`,
	Example: `  orva dns get
  orva dns get -o json`,
	Args: cobra.NoArgs,
	RunE: runDNSGet,
}

var dnsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set the sandbox DNS config",
	Long: `Set the DNS configuration for egress-enabled sandboxes.

This endpoint is idempotent and overwrites the full state from the flags you
pass: omitting --server clears the server list (falling back to defaults),
and omitting --host clears all host overrides. Pass the complete desired
state in a single call.`,
	Example: `  orva dns set --server 1.1.1.1 --server 1.0.0.1 --search corp.lan
  orva dns set --host api.internal=10.0.0.10
  orva dns set --server ""   # reset to default resolvers`,
	Args: cobra.NoArgs,
	RunE: runDNSSet,
}

func init() {
	dnsSetCmd.Flags().StringArray("server", nil, "upstream resolver IP (repeatable); pass \"\" to reset to defaults")
	dnsSetCmd.Flags().String("search", "", "DNS search domain (empty clears it)")
	dnsSetCmd.Flags().StringArray("host", nil, "host override 'name=ip' (repeatable)")

	dnsCmd.AddCommand(dnsGetCmd, dnsSetCmd)
}

// dnsRecord mirrors firewall.DNSRecord's JSON shape.
type dnsRecord struct {
	Host string `json:"host"`
	IP   string `json:"ip"`
}

type dnsConfig struct {
	Servers  []string    `json:"servers"`
	Search   string      `json:"search"`
	Records  []dnsRecord `json:"records"`
	Defaults []string    `json:"defaults"`
}

func runDNSGet(cmd *cobra.Command, _ []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.Get("/api/v1/firewall/dns")
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var cfg dnsConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	printDNSConfig(cfg)
	return nil
}

func runDNSSet(cmd *cobra.Command, _ []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	servers, _ := cmd.Flags().GetStringArray("server")
	search, _ := cmd.Flags().GetString("search")
	hosts, _ := cmd.Flags().GetStringArray("host")

	// Drop empty server entries (the "" sentinel used to reset to defaults).
	cleanServers := []string{}
	for _, s := range servers {
		if s = strings.TrimSpace(s); s != "" {
			cleanServers = append(cleanServers, s)
		}
	}

	records := []dnsRecord{}
	for _, h := range hosts {
		name, ip, ok := strings.Cut(h, "=")
		if !ok {
			return fmt.Errorf("invalid --host %q (expected 'name=ip')", h)
		}
		name = strings.TrimSpace(name)
		ip = strings.TrimSpace(ip)
		if name == "" || ip == "" {
			return fmt.Errorf("invalid --host %q (both name and ip are required)", h)
		}
		records = append(records, dnsRecord{Host: name, IP: ip})
	}

	// This replaces the entire DNS config — omitted flags clear their section
	// (e.g. passing no --server wipes all servers). Confirm so it can't silently
	// nuke the config; --yes skips the prompt and is required on a non-TTY.
	if err := confirm(cmd, "Replace the sandbox DNS config? (omitted flags clear their section)"); err != nil {
		return err
	}

	body := map[string]any{
		"servers": cleanServers,
		"search":  strings.TrimSpace(search),
		"records": records,
	}
	resp, err := client.Put("/api/v1/firewall/dns", body)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var cfg dnsConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	okf(cmd, "DNS config updated")
	printDNSConfig(cfg)
	return nil
}

func printDNSConfig(cfg dnsConfig) {
	servers := "(defaults)"
	if len(cfg.Servers) > 0 {
		servers = strings.Join(cfg.Servers, ", ")
	}
	fmt.Printf("Servers:  %s\n", servers)
	fmt.Printf("Search:   %s\n", dash(cfg.Search))
	if len(cfg.Defaults) > 0 {
		fmt.Printf("Defaults: %s\n", strings.Join(cfg.Defaults, ", "))
	}
	if len(cfg.Records) == 0 {
		fmt.Println("Records:  (none)")
		return
	}
	fmt.Println("Records:")
	t := newTable("HOST", "IP")
	for _, r := range cfg.Records {
		t.row(r.Host, r.IP)
	}
	t.flush()
}
