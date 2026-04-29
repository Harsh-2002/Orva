package config

import (
	"runtime"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Server.Port != 8443 {
		t.Errorf("expected default port 8443, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected default host 0.0.0.0, got %s", cfg.Server.Host)
	}
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
	if cfg.Logging.Format != "json" {
		t.Errorf("expected log format json, got %s", cfg.Logging.Format)
	}
	if cfg.Sandbox.NsjailBin != "/usr/local/bin/nsjail" {
		t.Errorf("expected nsjail bin path, got %s", cfg.Sandbox.NsjailBin)
	}
}

func TestSupportedEnvVars(t *testing.T) {
	const expected = 9
	if len(SupportedEnvVars) != expected {
		t.Errorf("SupportedEnvVars: want %d entries, got %d (%v)", expected, len(SupportedEnvVars), SupportedEnvVars)
	}
	want := map[string]bool{
		"ORVA_DATA_DIR":           true,
		"ORVA_DEFAULT_MEMORY_MB":  true,
		"ORVA_DEFAULT_TIMEOUT_MS": true,
		"ORVA_LOG_LEVEL":          true,
		"ORVA_LOG_RETENTION_DAYS": true,
		"ORVA_PORT":               true,
		"ORVA_SECURE_COOKIES":     true,
		"ORVA_SESSION_DAYS":       true,
		"ORVA_WRITE_TIMEOUT_SEC":  true,
	}
	for _, v := range SupportedEnvVars {
		if !want[v] {
			t.Errorf("unexpected env var in SupportedEnvVars: %s", v)
		}
		delete(want, v)
	}
	for v := range want {
		t.Errorf("missing env var from SupportedEnvVars: %s", v)
	}
}

// clearOrvaEnv ensures none of the supported env vars leak in from the test
// process environment. t.Setenv("X","") sets X to empty string, which the
// loader treats as unset.
func clearOrvaEnv(t *testing.T) {
	t.Helper()
	for _, v := range SupportedEnvVars {
		t.Setenv(v, "")
	}
}

func TestLoadNoEnv(t *testing.T) {
	clearOrvaEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 8443 {
		t.Errorf("port: want 8443, got %d", cfg.Server.Port)
	}
	if len(cfg.ActiveEnvVars) != 0 {
		t.Errorf("ActiveEnvVars: want empty, got %v", cfg.ActiveEnvVars)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	clearOrvaEnv(t)
	t.Setenv("ORVA_PORT", "7070")
	t.Setenv("ORVA_LOG_LEVEL", "debug")
	t.Setenv("ORVA_DEFAULT_MEMORY_MB", "128")
	t.Setenv("ORVA_DEFAULT_TIMEOUT_MS", "45000")
	t.Setenv("ORVA_LOG_RETENTION_DAYS", "30")
	t.Setenv("ORVA_WRITE_TIMEOUT_SEC", "120")
	t.Setenv("ORVA_SECURE_COOKIES", "true")
	t.Setenv("ORVA_SESSION_DAYS", "30")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Port != 7070 {
		t.Errorf("port: want 7070, got %d", cfg.Server.Port)
	}
	if cfg.Server.WriteTimeoutSec != 120 {
		t.Errorf("write timeout: want 120, got %d", cfg.Server.WriteTimeoutSec)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("log level: want debug, got %s", cfg.Logging.Level)
	}
	if cfg.Logging.RetentionDays != 30 {
		t.Errorf("log retention: want 30, got %d", cfg.Logging.RetentionDays)
	}
	if cfg.Functions.DefaultMemoryMB != 128 {
		t.Errorf("memory: want 128, got %d", cfg.Functions.DefaultMemoryMB)
	}
	if cfg.Functions.DefaultTimeoutMS != 45000 {
		t.Errorf("timeout: want 45000, got %d", cfg.Functions.DefaultTimeoutMS)
	}
	if !cfg.Security.SecureCookies {
		t.Error("secure cookies: want true, got false")
	}
	if cfg.Security.SessionDays != 30 {
		t.Errorf("session days: want 30, got %d", cfg.Security.SessionDays)
	}
	if len(cfg.ActiveEnvVars) != 8 {
		t.Errorf("ActiveEnvVars: want 8 entries, got %d (%v)", len(cfg.ActiveEnvVars), cfg.ActiveEnvVars)
	}
}

func TestLoadDataDirDerivedPaths(t *testing.T) {
	clearOrvaEnv(t)
	t.Setenv("ORVA_DATA_DIR", "/custom/data")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Data.Dir != "/custom/data" {
		t.Errorf("data dir: want /custom/data, got %s", cfg.Data.Dir)
	}
	if cfg.Database.Path != "/custom/data/orva.db" {
		t.Errorf("db path: want /custom/data/orva.db, got %s", cfg.Database.Path)
	}
	if cfg.Sandbox.RootfsDir != "/custom/data/rootfs" {
		t.Errorf("rootfs dir: want /custom/data/rootfs, got %s", cfg.Sandbox.RootfsDir)
	}
}

// Invalid numeric env values must be ignored — the default stays in place
// and the var is NOT reported as active in the startup log.
func TestLoadIgnoresInvalidNumericEnv(t *testing.T) {
	clearOrvaEnv(t)
	t.Setenv("ORVA_PORT", "not-a-number")
	t.Setenv("ORVA_DEFAULT_MEMORY_MB", "abc")
	t.Setenv("ORVA_SESSION_DAYS", "0") // zero is rejected: must be >0

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Port != 8443 {
		t.Errorf("port: invalid value should preserve default, got %d", cfg.Server.Port)
	}
	if cfg.Functions.DefaultMemoryMB != 64 {
		t.Errorf("memory: invalid value should preserve default 64, got %d", cfg.Functions.DefaultMemoryMB)
	}
	if cfg.Security.SessionDays != 7 {
		t.Errorf("session days: zero should preserve default 7, got %d", cfg.Security.SessionDays)
	}
	for _, v := range cfg.ActiveEnvVars {
		switch v {
		case "ORVA_PORT", "ORVA_DEFAULT_MEMORY_MB", "ORVA_SESSION_DAYS":
			t.Errorf("invalid env var leaked into ActiveEnvVars: %s", v)
		}
	}
}

func TestSecureCookiesAcceptsTrueAnd1(t *testing.T) {
	for _, v := range []string{"true", "1"} {
		t.Run(v, func(t *testing.T) {
			clearOrvaEnv(t)
			t.Setenv("ORVA_SECURE_COOKIES", v)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.Security.SecureCookies {
				t.Errorf("ORVA_SECURE_COOKIES=%q: want true, got false", v)
			}
		})
	}
}
