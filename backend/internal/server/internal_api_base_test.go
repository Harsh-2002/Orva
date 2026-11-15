package server

import (
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/config"
)

func TestSandboxTemplateKeepsConfiguredSeccompPolicy(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sandbox.SeccompPolicy = "strict"
	tmpl := newSandboxTemplate(cfg, nil, nil)
	if tmpl.DefaultSeccomp != "strict" {
		t.Fatalf("worker policy = %q, want strict", tmpl.DefaultSeccomp)
	}
	if tmpl.DefaultMaxPids != cfg.Functions.DefaultMaxPids {
		t.Fatalf("worker pids = %d, want %d", tmpl.DefaultMaxPids, cfg.Functions.DefaultMaxPids)
	}
}
