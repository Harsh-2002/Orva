package commands

import (
	"fmt"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with an Orva server",
	Long: `Save an API endpoint and key to ~/.orva/config.yaml for subsequent CLI
commands. Pass --test to verify the credentials against the server before
they are written to disk.

  orva login --endpoint https://orva.example.com --api-key orva_...
  orva login --endpoint https://orva.example.com --api-key orva_... --test`,
	Args: cobra.NoArgs,
	RunE: runLogin,
}

func init() {
	loginCmd.Flags().String("endpoint", "", "Orva API endpoint URL (required)")
	loginCmd.Flags().String("api-key", "", "API key for authentication (required)")
	loginCmd.Flags().Bool("test", false, "verify credentials against the server before saving")
	loginCmd.MarkFlagRequired("endpoint")
	loginCmd.MarkFlagRequired("api-key")
}

func runLogin(cmd *cobra.Command, args []string) error {
	endpoint, _ := cmd.Flags().GetString("endpoint")
	apiKey, _ := cmd.Flags().GetString("api-key")
	test, _ := cmd.Flags().GetBool("test")

	if test {
		infof(cmd, "Testing credentials against %s ...", endpoint)
		probe := cli.NewClient(endpoint, apiKey)
		resp, err := probe.Get("/api/v1/system/health")
		if err != nil {
			return fmt.Errorf("could not reach %s: %w", endpoint, err)
		}
		if err := checkResponse(resp); err != nil {
			resp.Body.Close()
			return fmt.Errorf("credentials rejected by server: %w", err)
		}
		resp.Body.Close()
	}

	cfg := &cli.CLIConfig{
		Endpoint: endpoint,
		APIKey:   apiKey,
	}
	if err := cli.SaveCLIConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	okf(cmd, "Logged in to %s — config saved to %s", endpoint, cli.ConfigPath())
	return nil
}
