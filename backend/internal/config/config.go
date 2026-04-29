package config

import (
	"os"
	"strconv"
)

// Supported env vars — printed at startup so operators can confirm their
// environment is being picked up. Alphabetical order.
var SupportedEnvVars = []string{
	"ORVA_DATA_DIR",
	"ORVA_DEFAULT_MEMORY_MB",
	"ORVA_DEFAULT_TIMEOUT_MS",
	"ORVA_LOG_LEVEL",
	"ORVA_LOG_RETENTION_DAYS",
	"ORVA_PORT",
	"ORVA_SECURE_COOKIES",
	"ORVA_SESSION_DAYS",
	"ORVA_WRITE_TIMEOUT_SEC",
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
	if v := os.Getenv("ORVA_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			active = append(active, "ORVA_PORT")
			cfg.Server.Port = port
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
