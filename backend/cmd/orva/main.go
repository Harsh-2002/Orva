// Orva server binary. Includes the daemon (orva serve), the host-setup
// command (orva setup), the project init command (orva init), AND every
// client-side subcommand from cli/commands — so an operator on the server
// box can `orva functions list / deploy / invoke / ...` without installing
// the standalone CLI.
package main

import (
	"os"

	"github.com/Harsh-2002/Orva/cli/commands"
)

// Version is overridden at build time via:
//
//	go build -ldflags='-X main.Version=vYYYY.MM.DD' ./backend/cmd/orva
//
// It is forwarded into commands.Version so the CLI subcommands (including
// `orva --version` and any future `orva upgrade`) see the same string.
var Version = "0.1.0"

func main() {
	commands.Version = Version
	root := commands.NewRoot()
	root.AddCommand(newServeCmd(), newSetupCmd(), newInitCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
