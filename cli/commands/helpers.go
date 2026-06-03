package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/Harsh-2002/Orva/internal/ids"
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

// resolveFnID maps a name-or-id to a function ID, returning a clear error when
// no function matches instead of silently passing the name through to a later
// 404. A value that parses as a UUID is used directly.
func resolveFnID(client *cli.Client, nameOrID string) (string, error) {
	if ids.IsUUID(nameOrID) {
		return nameOrID, nil
	}

	// Request a high limit: the list endpoint defaults to 20, which would make
	// name resolution silently fail for the 21st+ function on larger instances.
	resp, err := client.Get("/api/v1/functions?limit=10000")
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return "", err
	}

	var result struct {
		Functions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"functions"`
	}
	if err := decodeJSON(resp, &result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	for _, fn := range result.Functions {
		if fn.Name == nameOrID {
			return fn.ID, nil
		}
	}
	return "", fmt.Errorf("no function named %q (run `orva functions list` to see available functions)", nameOrID)
}
