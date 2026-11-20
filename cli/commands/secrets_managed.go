package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/Harsh-2002/Orva/internal/client"
)

// managedExpectedFields are the seven revision strings a managed configuration
// write must echo back exactly. The CLI never fabricates or edits them: it
// forwards what the server just reported, so a concurrent change is a 409.
var managedExpectedFields = []string{
	"function_version", "artifact_revision", "artifact_epoch", "generation",
	"policy_revision", "authority_epoch", "approved_artifact",
}

type managedConfiguration struct {
	Expected        json.RawMessage `json:"expected"`
	CurrentArtifact string          `json:"current_artifact"`
}

// managedSecretWrite sets (value != nil) or deletes (value == nil) one secret
// through the CAS-checked managed configuration route. It sends the change once.
// A conflict is reported and never retried, because the secret may have been
// overwritten by someone else since it was read.
func managedSecretWrite(cmd *cobra.Command, c *client.Client, owner, fnLabel, key string, value *string) error {
	install, _ := cmd.Flags().GetBool("install")
	path := "/api/v1/functions/" + owner + "/configuration"

	cfg, err := readManagedConfiguration(c, path)
	if err != nil {
		return err
	}

	secret := map[string]string{"key": key}
	method := http.MethodDelete
	if value != nil {
		secret["value"] = *value
		method = http.MethodPut
	}
	body := map[string]any{"expected": cfg.Expected, "current_artifact": cfg.CurrentArtifact, "secret": secret}
	resp, err := c.Do(method, path+"/secrets", body)
	if err != nil {
		return fmt.Errorf("secret write: %w", err)
	}
	successor, err := decodeManagedSuccessor(resp)
	if err != nil {
		return err
	}

	installed := false
	if install {
		resp, err := c.Do(http.MethodPost, path+"/install", map[string]any{"expected": successor})
		if err != nil {
			return fmt.Errorf("the secret was saved but installing failed: %w", err)
		}
		if err := checkManagedConfigResponse(resp); err != nil {
			return fmt.Errorf("the secret was saved but was not installed: %w", err)
		}
		resp.Body.Close()
		installed = true
	}

	if outputJSON(cmd) {
		return emitJSON(map[string]any{"saved": value != nil, "deleted": value == nil, "installed": installed, "review_required": true})
	}
	verb := "saved on"
	if value == nil {
		verb = "deleted from"
	}
	okf(cmd, "Secret %q %s %s", key, verb, fnLabel)
	if installed {
		infof(cmd, "Configuration installed. Permissions still need review; installing does not approve them.")
	} else {
		infof(cmd, "The function stays inactive until this configuration is installed and its permissions are reviewed. Re-run with --install, or use the dashboard.")
	}
	return nil
}

func readManagedConfiguration(c *client.Client, path string) (*managedConfiguration, error) {
	resp, err := c.Get(path)
	if err != nil {
		return nil, fmt.Errorf("read configuration: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read configuration: %w", err)
	}
	var cfg managedConfiguration
	if json.Unmarshal(raw, &cfg) != nil || len(cfg.Expected) == 0 {
		return nil, fmt.Errorf("unexpected configuration response; nothing was changed")
	}
	if len(cfg.CurrentArtifact) != 64 {
		return nil, fmt.Errorf("this function has no installed build, so its secrets cannot be changed yet; install a deployment first")
	}
	return &cfg, nil
}

// decodeManagedSuccessor validates a configuration-write reply and returns its
// seven fresh revision fields, which are the only valid input to install.
func decodeManagedSuccessor(resp *http.Response) (map[string]json.RawMessage, error) {
	if err := checkManagedConfigResponse(resp); err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read reply: %w", err)
	}
	var reply map[string]json.RawMessage
	if json.Unmarshal(raw, &reply) != nil || !bytes.Equal(reply["review_required"], []byte("true")) {
		return nil, fmt.Errorf("unverified reply; inspect the function's configuration before continuing")
	}
	successor := make(map[string]json.RawMessage, len(managedExpectedFields))
	for _, field := range managedExpectedFields {
		v, ok := reply[field]
		if !ok {
			return nil, fmt.Errorf("unverified reply; inspect the function's configuration before continuing")
		}
		successor[field] = v
	}
	return successor, nil
}

// checkManagedConfigResponse explains the two outcomes a managed configuration
// write can have beyond a generic API error.
func checkManagedConfigResponse(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusConflict:
		resp.Body.Close()
		return fmt.Errorf("the configuration changed since it was read (HTTP 409)\nnothing was sent again: inspect the function and run the command again")
	case http.StatusForbidden:
		resp.Body.Close()
		return fmt.Errorf("not authorized (HTTP 403): changing secrets or installing a configuration needs an administrator key for this function")
	}
	return checkResponse(resp)
}
