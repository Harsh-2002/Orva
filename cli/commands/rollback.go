package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"

	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

// rollbackCmd retargets a function to a prior succeeded deployment. It
// mirrors the dashboard's "Rollback" action and the rollback_function MCP
// tool: the server retargets the function's current symlink, restores the
// env + spawn-config snapshot that was active when that code last shipped,
// and drains the warm pool. Secrets are NOT versioned and keep their
// current values. This changes what's serving live traffic, so it
// confirms first (skip with --yes).
var rollbackCmd = &cobra.Command{
	Use:   "rollback <fn> [deployment-id]",
	Short: "Roll a function back to a prior deployment",
	Long: `Roll a function back to a prior succeeded deployment.

Pass an explicit deployment id (find one with ` + "`orva deployments list <fn>`" + `)
to restore that exact version — its code, env vars, and spawn config. With
no deployment id, the most recent earlier deployment whose code_hash
differs from the currently-active one is chosen, i.e. "undo the last code
change". Alternatively pin a content-addressed version with --code-hash.

Rollback changes what's serving live traffic, so it prompts for
confirmation; pass --yes to skip the prompt for non-interactive use.
Secrets are not versioned and keep their current values.

Examples:
  orva rollback greeter                       # undo the last code change
  orva rollback greeter dep_01J...            # roll back to a specific deployment
  orva rollback greeter --code-hash 9f8e7d... # roll back to a content hash
  orva rollback greeter dep_01J... --yes      # no confirmation prompt`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runRollback,
}

func init() {
	rollbackCmd.Flags().String("code-hash", "", "roll back to this content-addressed code hash instead of a deployment id")
}

func runRollback(cmd *cobra.Command, args []string) error {
	client, err := getClient(cmd)
	if err != nil {
		return err
	}
	fnName := args[0]
	fnID, err := resolveFnID(client, fnName)
	if err != nil {
		return err
	}

	codeHash, _ := cmd.Flags().GetString("code-hash")
	var deploymentID string
	if len(args) == 2 {
		deploymentID = args[1]
	}
	if deploymentID != "" && codeHash != "" {
		return fmt.Errorf("pass either a deployment id or --code-hash, not both")
	}

	// No explicit target: resolve the previous distinct-code_hash
	// deployment, mirroring `orva diff <fn>`'s default --from.
	target := deploymentID
	if deploymentID == "" && codeHash == "" {
		prev, err := resolvePreviousDeployment(client, fnID)
		if err != nil {
			return fmt.Errorf("resolve previous deployment: %w (specify a deployment id explicitly)", err)
		}
		deploymentID = prev
		target = prev
	}
	if codeHash != "" {
		target = "code_hash " + shortHash(codeHash)
	}

	if err := confirm(cmd, fmt.Sprintf("Roll %q back to %s? This changes live traffic.", fnName, target)); err != nil {
		return err
	}

	payload := map[string]string{}
	if deploymentID != "" {
		payload["deployment_id"] = deploymentID
	}
	if codeHash != "" {
		payload["code_hash"] = codeHash
	}

	resp, err := client.Post("/api/v1/functions/"+url.PathEscape(fnID)+"/rollback", payload)
	if err != nil {
		return fmt.Errorf("rollback request: %w", err)
	}
	if err := checkResponse(resp); err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if outputJSON(cmd) {
		return emitRaw(raw)
	}

	var d struct {
		ID       string `json:"id"`
		Version  int64  `json:"version"`
		CodeHash string `json:"code_hash"`
	}
	_ = json.Unmarshal(raw, &d)
	okf(cmd, "rolled %q back to %s (deployment %s, version %d)",
		fnName, shortHash(d.CodeHash), d.ID, d.Version)
	return nil
}

// resolvePreviousDeployment finds the most recent succeeded deployment
// whose code_hash differs from the function's currently-active code_hash.
// That makes a bare `orva rollback <fn>` mean "undo the last code change",
// matching the default-range logic in `orva diff`.
func resolvePreviousDeployment(client *cli.Client, fnID string) (string, error) {
	fnResp, err := client.Get("/api/v1/functions/" + url.PathEscape(fnID))
	if err != nil {
		return "", err
	}
	if err := checkResponse(fnResp); err != nil {
		return "", err
	}
	var fn struct {
		CodeHash string `json:"code_hash"`
	}
	if err := decodeJSON(fnResp, &fn); err != nil {
		return "", err
	}
	if fn.CodeHash == "" {
		return "", fmt.Errorf("function has no active code hash yet")
	}

	depResp, err := client.Get("/api/v1/functions/" + url.PathEscape(fnID) + "/deployments?limit=100")
	if err != nil {
		return "", err
	}
	if err := checkResponse(depResp); err != nil {
		return "", err
	}
	var deps struct {
		Deployments []struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			CodeHash string `json:"code_hash"`
		} `json:"deployments"`
	}
	if err := decodeJSON(depResp, &deps); err != nil {
		return "", err
	}

	// Deployments come back newest-first. Walk past the active code_hash to
	// the first succeeded row with a different hash.
	for _, d := range deps.Deployments {
		if d.Status != "succeeded" || d.CodeHash == "" {
			continue
		}
		if d.CodeHash != fn.CodeHash {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("only one distinct code_hash in deployment history — nothing earlier to roll back to")
}
