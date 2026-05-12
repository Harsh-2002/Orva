package commands

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion script",
	Long: `Generate a shell completion script for orva.

To load completions:

Bash:
  $ source <(orva completion bash)

Zsh:
  $ orva completion zsh > "${fpath[1]}/_orva"

Fish:
  $ orva completion fish | source

PowerShell:
  PS> orva completion powershell | Out-String | Invoke-Expression
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	Run: func(cmd *cobra.Command, args []string) {
		root := cmd.Root()
		switch args[0] {
		case "bash":
			_ = root.GenBashCompletion(os.Stdout)
		case "zsh":
			_ = root.GenZshCompletion(os.Stdout)
		case "fish":
			_ = root.GenFishCompletion(os.Stdout, true)
		case "powershell":
			_ = root.GenPowerShellCompletionWithDesc(os.Stdout)
		}
	},
}

