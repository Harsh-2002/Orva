package main

import (
	"fmt"
	"os"

	"github.com/Harsh-2002/Orva/internal/cli"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with an Orva server",
	Long:  "Save API endpoint and key to ~/.orva/config.yaml for subsequent CLI commands.",
	Run:   runLogin,
}

func init() {
	loginCmd.Flags().String("endpoint", "", "Orva API endpoint URL (required)")
	loginCmd.Flags().String("api-key", "", "API key for authentication (required)")
	loginCmd.MarkFlagRequired("endpoint")
	loginCmd.MarkFlagRequired("api-key")
	rootCmd.AddCommand(loginCmd)
}

func runLogin(cmd *cobra.Command, args []string) {
	endpoint, _ := cmd.Flags().GetString("endpoint")
	apiKey, _ := cmd.Flags().GetString("api-key")

	cfg := &cli.CLIConfig{
		Endpoint: endpoint,
		APIKey:   apiKey,
	}

	if err := cli.SaveCLIConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to save config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Logged in to %s\n", endpoint)
	fmt.Printf("Config saved to %s\n", cli.ConfigPath())
}
