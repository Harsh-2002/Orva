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
	root.PersistentFlags().String("endpoint", "", "Orva API endpoint (overrides config / $ORVA_ENDPOINT)")
	root.PersistentFlags().String("api-key", "", "Orva API key (overrides config / $ORVA_API_KEY)")
	registerGlobalFlags(root)
	return root
}

// Command help groups. Assigning each subcommand a GroupID clusters the flat
// command list in `orva --help` into readable sections instead of one long
// alphabetical wall.
const (
	groupFunctions = "functions"
	groupData      = "data"
	groupEventing  = "eventing"
	groupNetwork   = "network"
	groupAI        = "ai"
	groupSystem    = "system"
)

// commandGroups maps each subcommand to its help group. Commands not listed
// here fall under cobra's default "Additional Commands" section.
func commandGroups() map[*cobra.Command]string {
	return map[*cobra.Command]string{
		functionsCmd:   groupFunctions,
		deployCmd:      groupFunctions,
		deploymentsCmd: groupFunctions,
		rollbackCmd:    groupFunctions,
		diffCmd:        groupFunctions,
		invokeCmd:      groupFunctions,
		logsCmd:        groupFunctions,
		executionsCmd:  groupFunctions,
		fixturesCmd:    groupFunctions,
		tracesCmd:      groupFunctions,
		poolCmd:        groupFunctions,

		kvCmd:      groupData,
		secretsCmd: groupData,

		cronCmd:     groupEventing,
		jobsCmd:     groupEventing,
		webhooksCmd: groupEventing,
		channelsCmd: groupEventing,

		routesCmd:   groupNetwork,
		dnsCmd:      groupNetwork,
		firewallCmd: groupNetwork,

		chatCmd: groupAI,
		docsCmd: groupAI,

		systemCmd:     groupSystem,
		activityCmd:   groupSystem,
		backupCmd:     groupSystem,
		keysCmd:       groupSystem,
		loginCmd:      groupSystem,
		completionCmd: groupSystem,
		upgradeCmd:    groupSystem,
	}
}

// RegisterClient adds every client-side subcommand (the ones that talk
// to a remote orvad over HTTP) to the supplied root. Both the slim CLI
// binary and the server binary call this — single source of truth.
func RegisterClient(root *cobra.Command) {
	root.AddGroup(
		&cobra.Group{ID: groupFunctions, Title: "Functions & deployments:"},
		&cobra.Group{ID: groupData, Title: "State (kv, secrets):"},
		&cobra.Group{ID: groupEventing, Title: "Eventing (cron, jobs, webhooks, channels):"},
		&cobra.Group{ID: groupNetwork, Title: "Network (routes, dns, firewall):"},
		&cobra.Group{ID: groupAI, Title: "AI:"},
		&cobra.Group{ID: groupSystem, Title: "System & maintenance:"},
	)
	root.AddCommand(
		activityCmd,
		backupCmd,
		channelsCmd,
		chatCmd,
		completionCmd,
		cronCmd,
		deployCmd,
		deploymentsCmd,
		diffCmd,
		dnsCmd,
		docsCmd,
		executionsCmd,
		firewallCmd,
		fixturesCmd,
		functionsCmd,
		invokeCmd,
		jobsCmd,
		keysCmd,
		kvCmd,
		loginCmd,
		logsCmd,
		poolCmd,
		rollbackCmd,
		routesCmd,
		secretsCmd,
		systemCmd,
		tracesCmd,
		upgradeCmd,
		webhooksCmd,
	)
	for cmd, group := range commandGroups() {
		cmd.GroupID = group
	}
	addConvenienceAliases(root)
	wireCompletions(root)
}

// addConvenienceAliases adds the reflexive aliases developers reach for:
// short/singular names for the noun groups (fn, secret, route, key) and
// `ls`/`rm`/`del` on every list/delete leaf. Purely additive — primary names
// are unchanged, so scripts and the command-tree golden test are unaffected.
func addConvenienceAliases(root *cobra.Command) {
	top := map[string][]string{
		"functions": {"fn", "fns"},
		"secrets":   {"secret"},
		"routes":    {"route"},
		"keys":      {"key"},
	}
	for _, c := range root.Commands() {
		if a, ok := top[c.Name()]; ok {
			c.Aliases = appendMissing(c.Aliases, a...)
		}
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			switch sub.Name() {
			case "list":
				sub.Aliases = appendMissing(sub.Aliases, "ls")
			case "delete":
				sub.Aliases = appendMissing(sub.Aliases, "rm", "del")
			}
			walk(sub)
		}
	}
	walk(root)
}

// appendMissing appends each value not already present (alias de-dup so we
// never hand cobra a duplicate, which it rejects).
func appendMissing(have []string, add ...string) []string {
	for _, a := range add {
		found := false
		for _, h := range have {
			if h == a {
				found = true
				break
			}
		}
		if !found {
			have = append(have, a)
		}
	}
	return have
}
