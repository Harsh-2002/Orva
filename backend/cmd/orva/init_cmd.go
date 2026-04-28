package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new Orva project",
	Long:  "Create an orva.yaml template in the current directory with example configuration.",
	Run:   runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

const orvaYAMLTemplate = `# Orva configuration
# Supported runtimes: node22 (Node.js 20), python313 (Python 3.12)

server:
  host: "0.0.0.0"
  port: 8443
  read_timeout_sec: 30
  write_timeout_sec: 60

database:
  path: "./orva.db"

sandbox:
  nsjail_bin: "/usr/local/bin/nsjail"
  rootfs_dir: "./rootfs"
  max_concurrent: 200

functions:
  default_timeout_ms: 30000
  default_memory_mb: 128
  default_cpus: 0.5
  max_code_size: 52428800

logging:
  level: "info"
  format: "json"
  retention_days: 7

security:
  cors_origins:
    - "*"

data:
  dir: "./data"
`

func runInit(cmd *cobra.Command, args []string) {
	const filename = "orva.yaml"

	if _, err := os.Stat(filename); err == nil {
		fmt.Fprintf(os.Stderr, "Error: %s already exists in current directory\n", filename)
		os.Exit(1)
	}

	if err := os.WriteFile(filename, []byte(orvaYAMLTemplate), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create %s: %v\n", filename, err)
		os.Exit(1)
	}

	fmt.Printf("Created %s\n", filename)
	fmt.Println("Edit this file to configure your Orva instance, then run: orva serve --config orva.yaml")
}
