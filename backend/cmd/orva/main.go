package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var Version = "0.1.0"

var rootCmd = &cobra.Command{
	Use:   "orva",
	Short: "Orva serverless function platform",
	Long:  "Orva is a serverless function platform for building, deploying, and running functions.",
}

func init() {
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate(fmt.Sprintf("orva %s\n", Version))

	// Global flags for API endpoint and key.
	rootCmd.PersistentFlags().String("endpoint", "", "Orva API endpoint (overrides config)")
	rootCmd.PersistentFlags().String("api-key", "", "Orva API key (overrides config)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
