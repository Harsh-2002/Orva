// Orva server binary. Includes the daemon (orva serve), the host-setup
// command (orva setup), the project init command (orva init), AND every
// client-side subcommand from cli/commands — so an operator on the server
// box can `orva functions list / deploy / invoke / ...` without installing
// the standalone CLI.
package main

import (
	"os"

	"github.com/Harsh-2002/Orva/backend/internal/version"
	"github.com/Harsh-2002/Orva/cli/commands"
)

func main() {
	// internal/version.* is the single source of truth, stamped at link
	// time. Forward it into the Cobra root so `orva --version` reports
	// the same identity as /api/v1/system/health.
	commands.Version = version.Version
	root := commands.NewRoot()
	root.AddCommand(newServeCmd(), newSetupCmd(), newInitCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
