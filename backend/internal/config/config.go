package config

import (
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Sandbox   SandboxConfig   `yaml:"sandbox"`
	Functions FunctionsConfig `yaml:"functions"`
	Logging   LoggingConfig   `yaml:"logging"`
	Security  SecurityConfig  `yaml:"security"`
	Data      DataConfig      `yaml:"data"`
}

type ServerConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	ReadTimeoutSec  int    `yaml:"read_timeout_sec"`
	WriteTimeoutSec int    `yaml:"write_timeout_sec"`
	MaxBodyBytes    int64  `yaml:"max_body_bytes"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type SandboxConfig struct {
	NsjailBin     string `yaml:"nsjail_bin"`
	RootfsDir     string `yaml:"rootfs_dir"`
	MaxConcurrent int    `yaml:"max_concurrent"`
	SeccompPolicy string `yaml:"seccomp_policy"` // "default", "strict", "permissive", "disabled"
}

type FunctionsConfig struct {
	DefaultTimeoutMS int     `yaml:"default_timeout_ms"`
	DefaultMemoryMB  int     `yaml:"default_memory_mb"`
	DefaultCPUs      float64 `yaml:"default_cpus"`
	MaxCodeSize      int64   `yaml:"max_code_size"`
}

type LoggingConfig struct {
	Level         string `yaml:"level"`
	Format        string `yaml:"format"`
	RetentionDays int    `yaml:"retention_days"`
}

type SecurityConfig struct {
	CORSOrigins []string `yaml:"cors_origins"`
}

type DataConfig struct {
	Dir string `yaml:"dir"`
}

func Load() (*Config, error) {
	cfg := Defaults()

	path := os.Getenv("ORVA_CONFIG")
	if path == "" {
		path = "/etc/orva/config.yaml"
	}

	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	applyEnvOverrides(cfg)
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("ORVA_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("ORVA_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("ORVA_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("ORVA_DATA_DIR"); v != "" {
		cfg.Data.Dir = v
		if cfg.Database.Path == Defaults().Database.Path {
			cfg.Database.Path = v + "/orva.db"
		}
	}
	if v := os.Getenv("ORVA_NSJAIL_BIN"); v != "" {
		cfg.Sandbox.NsjailBin = v
	}
	if v := os.Getenv("ORVA_ROOTFS_DIR"); v != "" {
		cfg.Sandbox.RootfsDir = v
	}
	if v := os.Getenv("ORVA_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Sandbox.MaxConcurrent = n
		}
	}
	if v := os.Getenv("ORVA_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := os.Getenv("ORVA_LOG_FORMAT"); v != "" {
		cfg.Logging.Format = v
	}
	if v := os.Getenv("ORVA_CORS_ORIGINS"); v != "" {
		cfg.Security.CORSOrigins = strings.Split(v, ",")
	}
}
