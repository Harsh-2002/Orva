package commands

import (
	"fmt"
	"net/http"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
	"io"
	"os"
	"strings"
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
	loginCmd.Flags().String("api-key", "", "API key for authentication (required; use - or @- to read from stdin)")
	loginCmd.Flags().Bool("test", false, "verify credentials against the server before saving")
	loginCmd.MarkFlagRequired("endpoint")
	loginCmd.MarkFlagRequired("api-key")
}

func runLogin(cmd *cobra.Command, args []string) error {
	endpoint, _ := cmd.Flags().GetString("endpoint")
	apiKey, _ := cmd.Flags().GetString("api-key")
	test, _ := cmd.Flags().GetBool("test")

	// Accept the key on stdin. The only documented way to persist a key put
	// it in argv, where any local user can read it out of `ps` and where it
	// lands in shell history. `orva secrets set` already supports --value @-
	// for exactly this reason.
	if apiKey == "-" || apiKey == "@-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read api key from stdin: %w", err)
		}
		apiKey = strings.TrimSpace(string(b))
	}
	if apiKey == "" {
		return fmt.Errorf("api key is empty")
	}

	if test {
		infof(cmd, "Testing credentials against %s ...", endpoint)
		probe := cli.NewClient(endpoint, apiKey)
		// Probe must sit behind authMiddleware AND be satisfiable by an API
		// key. Two prefixes bypass the middleware entirely — /api/v1/system/health
		// and all of /api/v1/auth/ — so neither can verify a key; /api/v1/auth/me
		// additionally demands a session cookie the CLI never has. /api/v1/runtimes
		// is a cheap read-gated GET behind the middleware.
		resp, err := probe.Get("/api/v1/runtimes")
		if err != nil {
			return fmt.Errorf("could not reach %s: %w", endpoint, err)
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return fmt.Errorf("credentials rejected by %s: invalid or expired API key", endpoint)
		case resp.StatusCode == http.StatusForbidden:
			// Authenticated, but this key has no "read" permission. The
			// credential itself is genuine, so saving it is correct.
			infof(cmd, "Key accepted (limited permissions: no read access)")
		case resp.StatusCode == http.StatusNotFound:
			// Server predates /api/v1/runtimes. Don't refuse a good config
			// just because the CLI is newer than the server.
			infof(cmd, "Key saved (server too old to verify: /api/v1/runtimes not found)")
		case resp.StatusCode >= 400:
			return fmt.Errorf("credential check failed: %s", resp.Status)
		}
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
