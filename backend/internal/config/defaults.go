package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// maxConcurrentDefault scales the sandbox concurrency ceiling by CPU count.
// Was hard-coded at 200; with the warm pool landing, the real gate is pool
// size, not this ceiling, so we want headroom on bigger hosts.
func maxConcurrentDefault() int {
	n := runtime.NumCPU() * 64
	if n < 200 {
		n = 200
	}
	return n
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/var/lib/orva"
	}
	return filepath.Join(home, ".orva")
}

func Defaults() *Config {
	dataDir := defaultDataDir()
	return &Config{
		Server: ServerConfig{
			Host:            "0.0.0.0",
			Port:            8443,
			ReadTimeoutSec:  30,
			WriteTimeoutSec: 60,
			MaxBodyBytes:    6 * 1024 * 1024, // 6MB
		},
		Database: DatabaseConfig{
			Path: filepath.Join(dataDir, "orva.db"),
		},
		Sandbox: SandboxConfig{
			NsjailBin:     "/usr/local/bin/nsjail",
			RootfsDir:     filepath.Join(dataDir, "rootfs"),
			MaxConcurrent: maxConcurrentDefault(),
			SeccompPolicy: "default",
		},
		Functions: FunctionsConfig{
			DefaultTimeoutMS: 30000,
			DefaultMemoryMB:  64,
			DefaultCPUs:      0.5,
			MaxCodeSize:      50 * 1024 * 1024, // 50MB
		},
		Logging: LoggingConfig{
			Level:         "info",
			Format:        "json",
			RetentionDays: 7,
		},
		Security: SecurityConfig{
			CORSOrigins: []string{"*"},
			SessionDays: 7,
		},
		Data: DataConfig{
			Dir: dataDir,
		},
	}
}
