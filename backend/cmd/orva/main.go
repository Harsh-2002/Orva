// Orva server binary. Includes the daemon (orva serve), the host-setup
// command (orva setup), AND every
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
	// `orva init` was removed: it wrote an orva.yaml that nothing read, then
	// told the operator to run `orva serve --config orva.yaml`, a flag that
	// does not exist. docs/CONFIG.md has always documented configuration as
	// environment variables only.
	root.AddCommand(newServeCmd(), newSetupCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
