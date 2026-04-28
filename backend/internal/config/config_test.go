package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Server.Port != 8443 {
		t.Errorf("expected default port 8443, got %d", cfg.Server.Port)
	}
	// MaxConcurrent scales by CPU count (NumCPU * 64, floored at 200)
	// so the expected value depends on the test host.
	wantConc := runtime.NumCPU() * 64
	if wantConc < 200 {
		wantConc = 200
	}
	if cfg.Sandbox.MaxConcurrent != wantConc {
		t.Errorf("expected max concurrent %d (NumCPU=%d), got %d", wantConc, runtime.NumCPU(), cfg.Sandbox.MaxConcurrent)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected log level info, got %s", cfg.Logging.Level)
	}
	if cfg.Sandbox.NsjailBin != "/usr/local/bin/nsjail" {
		t.Errorf("expected nsjail bin path, got %s", cfg.Sandbox.NsjailBin)
	}
}

func TestLoadYAMLOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(cfgPath, []byte(`
server:
  port: 9090
  host: "127.0.0.1"
logging:
  level: debug
  format: text
sandbox:
  max_concurrent: 50
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("ORVA_CONFIG", cfgPath)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected log level debug, got %s", cfg.Logging.Level)
	}
	if cfg.Sandbox.MaxConcurrent != 50 {
		t.Errorf("expected max concurrent 50, got %d", cfg.Sandbox.MaxConcurrent)
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(cfgPath, []byte(`
server:
  port: 9090
logging:
  level: debug
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("ORVA_CONFIG", cfgPath)
	t.Setenv("ORVA_PORT", "7070")
	t.Setenv("ORVA_LOG_LEVEL", "error")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 7070 {
		t.Errorf("expected port 7070 from env, got %d", cfg.Server.Port)
	}
	if cfg.Logging.Level != "error" {
		t.Errorf("expected log level error from env, got %s", cfg.Logging.Level)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("ORVA_CONFIG", "/nonexistent/config.yaml")
	cfg, err := Load()
	if err != nil {
		t.Fatal("should not error on missing file")
	}
	if cfg.Server.Port != 8443 {
		t.Errorf("expected default port, got %d", cfg.Server.Port)
	}
}
