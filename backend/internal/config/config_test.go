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
	const expected = 10
	if len(SupportedEnvVars) != expected {
		t.Errorf("SupportedEnvVars: want %d entries, got %d (%v)", expected, len(SupportedEnvVars), SupportedEnvVars)
	}
	want := map[string]bool{
		"ORVA_CORS_ORIGINS":      true,
		"ORVA_DATA_DIR":          true,
		"ORVA_HOST":              true,
		"ORVA_LOG_LEVEL":         true,
		"ORVA_MAX_BODY_BYTES":    true,
		"ORVA_PORT":              true,
		"ORVA_SECCOMP_POLICY":    true,
		"ORVA_SECURE_COOKIES":    true,
		"ORVA_SESSION_DAYS":      true,
		"ORVA_WRITE_TIMEOUT_SEC": true,
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

// validSample is one legal value per supported env var. Kept beside
// SupportedEnvVars so adding a knob without a loader block fails the build's
// tests rather than shipping a name operators can set to no effect.
var validSample = map[string]string{
	"ORVA_CORS_ORIGINS":      "https://a.example",
	"ORVA_DATA_DIR":          "/custom/data",
	"ORVA_HOST":              "127.0.0.1",
	"ORVA_LOG_LEVEL":         "debug",
	"ORVA_MAX_BODY_BYTES":    "1048576",
	"ORVA_PORT":              "7070",
	"ORVA_SECCOMP_POLICY":    "strict",
	"ORVA_SECURE_COOKIES":    "true",
	"ORVA_SESSION_DAYS":      "30",
	"ORVA_WRITE_TIMEOUT_SEC": "120",
}

// TestEverySupportedEnvVarIsRead guards the phantom-knob regression: a name
// advertised in SupportedEnvVars (and therefore in docs/CONFIG.md) that
// applyEnvOverrides never reads is a knob an operator can set with no effect.
// TestEverySupportedEnvVarIsAppliedByTheLoader checks that every advertised
// variable is actually consumed by applyEnvOverrides — i.e. that a supported
// name is not simply ignored.
//
// It does NOT prove the variable has a runtime effect, and it would NOT have
// caught the three phantom knobs this package just deleted
// (ORVA_DEFAULT_MEMORY_MB, ORVA_DEFAULT_TIMEOUT_MS, ORVA_LOG_RETENTION_DAYS).
// Those were read by the loader and recorded in ActiveEnvVars exactly like a
// working knob; what made them phantoms is that nothing OUTSIDE this package
// ever read the struct field they set. That is a property of the whole
// codebase, not of the loader, so no unit test here can assert it — the guard
// is the rule stated at the top of docs/CONFIG.md ("every variable below has
// an observable runtime effect") plus review of the consuming call site.
//
// Do not describe this test as a phantom-knob guard. It is not one.
func TestEverySupportedEnvVarIsAppliedByTheLoader(t *testing.T) {
	for _, name := range SupportedEnvVars {
		t.Run(name, func(t *testing.T) {
			sample, ok := validSample[name]
			if !ok {
				t.Fatalf("%s has no entry in validSample — add one alongside the loader block", name)
			}
			clearOrvaEnv(t)
			t.Setenv(name, sample)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			for _, active := range cfg.ActiveEnvVars {
				if active == name {
					return
				}
			}
			t.Errorf("%s=%q is declared supported but applyEnvOverrides never read it (this checks parsing only, not runtime effect)", name, sample)
		})
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
	if !cfg.Security.SecureCookies {
		t.Error("secure cookies: want true, got false")
	}
	if cfg.Security.SessionDays != 30 {
		t.Errorf("session days: want 30, got %d", cfg.Security.SessionDays)
	}
	if len(cfg.ActiveEnvVars) != 5 {
		t.Errorf("ActiveEnvVars: want 5 entries, got %d (%v)", len(cfg.ActiveEnvVars), cfg.ActiveEnvVars)
	}
}

func TestLoadHardeningOverrides(t *testing.T) {
	clearOrvaEnv(t)
	t.Setenv("ORVA_HOST", "127.0.0.1")
	t.Setenv("ORVA_MAX_BODY_BYTES", "12582912") // 12MB
	t.Setenv("ORVA_CORS_ORIGINS", "https://a.example, https://b.example")
	t.Setenv("ORVA_SECCOMP_POLICY", "strict")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("host: want 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Server.MaxBodyBytes != 12582912 {
		t.Errorf("max body: want 12582912, got %d", cfg.Server.MaxBodyBytes)
	}
	if got := cfg.Security.CORSOrigins; len(got) != 2 || got[0] != "https://a.example" || got[1] != "https://b.example" {
		t.Errorf("cors origins: want [a b], got %v", got)
	}
	if cfg.Sandbox.SeccompPolicy != "strict" {
		t.Errorf("seccomp: want strict, got %s", cfg.Sandbox.SeccompPolicy)
	}

	// An invalid seccomp policy must be ignored (fall back to the default),
	// never silently disable the sandbox.
	clearOrvaEnv(t)
	t.Setenv("ORVA_SECCOMP_POLICY", "totally-bogus")
	cfg2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Sandbox.SeccompPolicy != "default" {
		t.Errorf("invalid seccomp policy should fall back to default, got %s", cfg2.Sandbox.SeccompPolicy)
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
	t.Setenv("ORVA_WRITE_TIMEOUT_SEC", "abc")
	t.Setenv("ORVA_MAX_BODY_BYTES", "-1") // non-positive is rejected
	t.Setenv("ORVA_SESSION_DAYS", "0")    // zero is rejected: must be >0

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Port != 8443 {
		t.Errorf("port: invalid value should preserve default, got %d", cfg.Server.Port)
	}
	if cfg.Server.WriteTimeoutSec != 60 {
		t.Errorf("write timeout: invalid value should preserve default 60, got %d", cfg.Server.WriteTimeoutSec)
	}
	if cfg.Server.MaxBodyBytes != 6*1024*1024 {
		t.Errorf("max body: negative value should preserve default, got %d", cfg.Server.MaxBodyBytes)
	}
	if cfg.Security.SessionDays != 7 {
		t.Errorf("session days: zero should preserve default 7, got %d", cfg.Security.SessionDays)
	}
	for _, v := range cfg.ActiveEnvVars {
		switch v {
		case "ORVA_PORT", "ORVA_WRITE_TIMEOUT_SEC", "ORVA_MAX_BODY_BYTES", "ORVA_SESSION_DAYS":
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
