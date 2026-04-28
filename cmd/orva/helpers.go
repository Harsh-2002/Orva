package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/Harsh-2002/Orva/internal/cli"
	"github.com/spf13/cobra"
)

// getClient creates an API client using flags or config.
func getClient(cmd *cobra.Command) (*cli.Client, error) {
	cfg, err := cli.LoadCLIConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	endpoint, _ := cmd.Root().PersistentFlags().GetString("endpoint")
	apiKey, _ := cmd.Root().PersistentFlags().GetString("api-key")

	if endpoint == "" {
		endpoint = cfg.Endpoint
	}
	if apiKey == "" {
		apiKey = cfg.APIKey
	}

	if endpoint == "" {
		endpoint = "http://localhost:8443"
	}

	return cli.NewClient(endpoint, apiKey), nil
}

// checkResponse checks for HTTP errors and prints the error body if present.
func checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
		return fmt.Errorf("API error (%d): %s - %s", resp.StatusCode, errResp.Error.Code, errResp.Error.Message)
	}
	return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
}

// decodeJSON reads and decodes the response body into v.
func decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

// exitError prints an error and exits.
func exitError(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", a...)
	os.Exit(1)
}
