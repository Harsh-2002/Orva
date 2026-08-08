package config

import (
	"os"
	"strconv"
	"strings"
)

// Supported env vars — printed at startup so operators can confirm their
// environment is being picked up. Alphabetical order.
var SupportedEnvVars = []string{
	"ORVA_CORS_ORIGINS",
	"ORVA_DATA_DIR",
	"ORVA_DEFAULT_MEMORY_MB",
	"ORVA_DEFAULT_TIMEOUT_MS",
	"ORVA_HOST",
	"ORVA_LOG_LEVEL",
	"ORVA_LOG_RETENTION_DAYS",
	"ORVA_MAX_BODY_BYTES",
	"ORVA_PORT",
	"ORVA_SECCOMP_POLICY",
	"ORVA_SECURE_COOKIES",
	"ORVA_SESSION_DAYS",
	"ORVA_WRITE_TIMEOUT_SEC",
}

// validSeccompPolicies are the policy names sandbox.ValidatePolicy accepts.
// Duplicated here (rather than importing the sandbox package) to keep config
// free of a dependency on sandbox — an unknown value is ignored so a typo can
// never silently disable the sandbox.
var validSeccompPolicies = map[string]bool{
	"default": true, "strict": true, "permissive": true, "disabled": true,
}

type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Sandbox   SandboxConfig
	Functions FunctionsConfig
	Logging   LoggingConfig
	Security  SecurityConfig
	Data      DataConfig

	// Populated by Load — names of env vars that were found set.
	ActiveEnvVars []string
}

type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeoutSec  int
	WriteTimeoutSec int
	MaxBodyBytes    int64
}

type DatabaseConfig struct {
	Path string
}

type SandboxConfig struct {
	NsjailBin     string
	RootfsDir     string
	MaxConcurrent int
	SeccompPolicy string
}

type FunctionsConfig struct {
	DefaultTimeoutMS int
	DefaultMemoryMB  int
	DefaultCPUs      float64
	MaxCodeSize      int64
	// DefaultMaxPids caps the number of processes a sandbox may spawn
	// (nsjail --cgroup_pids_max), bounding fork bombs. 0 would disable the
	// cap, so it must always be positive on the warm-pool spawn path.
	DefaultMaxPids int
}

type LoggingConfig struct {
	Level         string
	Format        string
	RetentionDays int
}

type SecurityConfig struct {
	CORSOrigins   []string
	SecureCookies bool
	SessionDays   int
}

type DataConfig struct {
	Dir string
}

func Load() (*Config, error) {
	cfg := Defaults()
	cfg.ActiveEnvVars = applyEnvOverrides(cfg)
	return cfg, nil
}

// applyEnvOverrides applies the 9 supported env vars and returns the names
// of those that were found set (for startup logging).
func applyEnvOverrides(cfg *Config) []string {
	var active []string

	if v := os.Getenv("ORVA_DATA_DIR"); v != "" {
		active = append(active, "ORVA_DATA_DIR")
		cfg.Data.Dir = v
		cfg.Database.Path = v + "/orva.db"
		cfg.Sandbox.RootfsDir = v + "/rootfs"
	}
	if v := os.Getenv("ORVA_HOST"); v != "" {
		// Bind address for the HTTP listener. Default is 0.0.0.0 (all
		// interfaces); set to 127.0.0.1 to bind loopback-only behind a
		// reverse proxy.
		active = append(active, "ORVA_HOST")
		cfg.Server.Host = v
	}
	if v := os.Getenv("ORVA_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			active = append(active, "ORVA_PORT")
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("ORVA_MAX_BODY_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			active = append(active, "ORVA_MAX_BODY_BYTES")
			cfg.Server.MaxBodyBytes = n
		}
	}
	if v := os.Getenv("ORVA_CORS_ORIGINS"); v != "" {
		// Comma-separated allow-list. Default is ["*"]; set an explicit
		// list to lock the dashboard/API to known origins.
		var origins []string
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
		if len(origins) > 0 {
			active = append(active, "ORVA_CORS_ORIGINS")
			cfg.Security.CORSOrigins = origins
		}
	}
	if v := os.Getenv("ORVA_SECCOMP_POLICY"); v != "" {
		if validSeccompPolicies[v] {
			active = append(active, "ORVA_SECCOMP_POLICY")
			cfg.Sandbox.SeccompPolicy = v
		}
	}
	if v := os.Getenv("ORVA_WRITE_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			active = append(active, "ORVA_WRITE_TIMEOUT_SEC")
			cfg.Server.WriteTimeoutSec = n
		}
	}
	if v := os.Getenv("ORVA_DEFAULT_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			active = append(active, "ORVA_DEFAULT_TIMEOUT_MS")
			cfg.Functions.DefaultTimeoutMS = n
		}
	}
	if v := os.Getenv("ORVA_DEFAULT_MEMORY_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			active = append(active, "ORVA_DEFAULT_MEMORY_MB")
			cfg.Functions.DefaultMemoryMB = n
		}
	}
	if v := os.Getenv("ORVA_LOG_LEVEL"); v != "" {
		active = append(active, "ORVA_LOG_LEVEL")
		cfg.Logging.Level = v
	}
	if v := os.Getenv("ORVA_LOG_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			active = append(active, "ORVA_LOG_RETENTION_DAYS")
			cfg.Logging.RetentionDays = n
		}
	}
	if v := os.Getenv("ORVA_SECURE_COOKIES"); v == "true" || v == "1" {
		active = append(active, "ORVA_SECURE_COOKIES")
		cfg.Security.SecureCookies = true
	}
	if v := os.Getenv("ORVA_SESSION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			active = append(active, "ORVA_SESSION_DAYS")
			cfg.Security.SessionDays = n
		}
	}

	return active
}
