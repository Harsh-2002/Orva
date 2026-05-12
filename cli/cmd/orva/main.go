// Slim Orva CLI binary. Imports only the cli/commands library — no server
// packages, no embedded UI/adapters/docs/MCP. Targets ~6–9 MB stripped.
//
// The server binary at backend/cmd/orva also imports cli/commands so the
// same subcommands work from both binaries. This binary is purely a
// remote-API client.
package main

import (
	"os"

	"github.com/Harsh-2002/Orva/cli/commands"
)

// Version is overridden at build time via:
//
//	go build -ldflags='-X main.Version=vYYYY.MM.DD' ./cli/cmd/orva
//
// We forward into commands.Version so `orva --version` (registered on
// the root command) and `orva upgrade` (which reads commands.Version)
// both see the same string.
var Version = "dev"

func main() {
	commands.Version = Version
	root := commands.NewRoot()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
