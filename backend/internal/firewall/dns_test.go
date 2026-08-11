package firewall

import "testing"

type dnsConfigStub map[string]string

func (s dnsConfigStub) GetSystemConfigString(key, fallback string) string {
	if value, ok := s[key]; ok {
		return value
	}
	return fallback
}

func TestLoadDNSConfigFiltersLegacyUnusableResolvers(t *testing.T) {
	cfg := LoadDNSConfig(dnsConfigStub{
		"dns_servers": "0.0.0.0,::,::ffff:192.0.2.1,1.1.1.1,2001:4860:4860::8888",
	})
	want := []string{"1.1.1.1", "2001:4860:4860::8888"}
	if len(cfg.Servers) != len(want) {
		t.Fatalf("servers = %v, want %v", cfg.Servers, want)
	}
	for i := range want {
		if cfg.Servers[i] != want[i] {
			t.Fatalf("servers = %v, want %v", cfg.Servers, want)
		}
	}
}
