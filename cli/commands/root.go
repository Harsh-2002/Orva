// Package commands holds the Cobra subcommand library shared by both the
// slim CLI binary (cli/cmd/orva) and the server binary (backend/cmd/orva).
//
// Each subcommand file defines its commands as package-level vars and uses
// init() to wire up flags + child commands. The top-level command registry
// lives here so a consumer can call NewRoot() (gets root + persistent
// flags + every client subcommand registered) or NewRootEmpty() + add only
// the subcommands they want.
package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set via -ldflags '-X .../commands.Version=vYYYY.MM.DD' at
// build time. Each binary's main() can override it too.
var Version = "dev"

// NewRoot returns a Cobra root command with the global persistent flags
// (--endpoint, --api-key) and every client subcommand registered. This is
// what the slim CLI binary uses verbatim. The server binary adds its own
// serve/setup/init subcommands on top.
func NewRoot() *cobra.Command {
	root := newRootEmpty()
	RegisterClient(root)
	return root
}

// newRootEmpty returns just the root + persistent flags, with no
// subcommands registered. Useful if a consumer wants to add subcommands
// selectively (or in a specific order).
func newRootEmpty() *cobra.Command {
	root := &cobra.Command{
		Use:           "orva",
		Short:         "Orva serverless function platform",
		Long:          "Orva is a serverless function platform for building, deploying, and running functions.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.Version = Version
	root.SetVersionTemplate(fmt.Sprintf("orva %s\n", Version))
	root.PersistentFlags().String("endpoint", "", "Orva API endpoint (overrides config)")
	root.PersistentFlags().String("api-key", "", "Orva API key (overrides config)")
	return root
}

// RegisterClient adds every client-side subcommand (the ones that talk
// to a remote orvad over HTTP) to the supplied root. Both the slim CLI
// binary and the server binary call this — single source of truth.
func RegisterClient(root *cobra.Command) {
	root.AddCommand(
		activityCmd,
		backupCmd,
		channelsCmd,
		completionCmd,
		cronCmd,
		deployCmd,
		functionsCmd,
		invokeCmd,
		jobsCmd,
		keysCmd,
		kvCmd,
		loginCmd,
		logsCmd,
		routesCmd,
		secretsCmd,
		systemCmd,
		upgradeCmd,
		webhooksCmd,
	)
}
