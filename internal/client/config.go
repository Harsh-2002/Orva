package client

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// CLIConfig holds the CLI configuration loaded from ~/.orva/config.yaml.
type CLIConfig struct {
	Endpoint string `yaml:"endpoint"`
	APIKey   string `yaml:"api_key"`
}

// ConfigPath returns the path to the CLI config file. The ORVA_CONFIG
// environment variable overrides the default ~/.orva/config.yaml so an
// operator can keep separate profiles (staging, prod) and select one per
// invocation: ORVA_CONFIG=~/.orva/staging.yaml orva functions list.
func ConfigPath() string {
	if p := os.Getenv("ORVA_CONFIG"); p != "" {
		return expandHome(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".orva", "config.yaml")
}

// expandHome expands a leading ~ to the user's home directory so values
// like ORVA_CONFIG=~/.orva/staging.yaml work as written in a shell that
// didn't perform tilde expansion itself.
func expandHome(p string) string {
	if p == "~" || (len(p) >= 2 && p[:2] == "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// LoadCLIConfig resolves CLI configuration with a clear precedence:
//
//	flags  >  environment  >  config file  >  built-in default
//
// Flags are applied by the caller (getClient) on top of this. Here we fold
// the environment over the config file so that ORVA_ENDPOINT / ORVA_API_KEY
// always win against the on-disk file, matching the documented behaviour.
func LoadCLIConfig() (*CLIConfig, error) {
	cfg := &CLIConfig{
		Endpoint: "http://localhost:8443",
	}

	path := ConfigPath()
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parse config: %w", err)
			}
		case os.IsNotExist(err):
			// No file yet — defaults + env still apply.
		default:
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	// Environment overrides the file.
	if v := os.Getenv("ORVA_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}
	if v := os.Getenv("ORVA_API_KEY"); v != "" {
		cfg.APIKey = v
	}

	return cfg, nil
}

// SaveCLIConfig writes the CLI config to the resolved config path.
func SaveCLIConfig(cfg *CLIConfig) error {
	path := ConfigPath()
	if path == "" {
		return fmt.Errorf("cannot determine home directory")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
