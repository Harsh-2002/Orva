package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/internal/client"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const workloadLifecycleLimit = 1024

func init() {
	for _, op := range []string{"pause", "resume"} {
		short := "Record durable managed-workload pause"
		if op == "resume" {
			short = "Replace managed-workload pause with an inactive review-required generation"
		}
		cmd := &cobra.Command{
			Use: op + " <function-uuid>", Short: short, Args: cobra.ExactArgs(1),
			Long: short + ` (opt-in server only).

Inspect the exact generation, policy_revision and authority_epoch through the
staged configuration API before each mutation; use them in a JSON request with
confirm:true. --request FILE or --request - passes those bytes unchanged.
--yes skips the CLI prompt, not the server's required confirm:true. Output is
JSON. No automatic inspection, fallback, or retry is performed.

Resume does not resume execution or standing work. Fresh Factory installation
and explicit source-aware review are separate.`,
			RunE:              func(cmd *cobra.Command, args []string) error { return runWorkloadLifecycle(cmd, op, args[0]) },
			ValidArgsFunction: cobra.NoFileCompletions,
		}
		cmd.Flags().String("request", "", "Required reviewed JSON request file, or - for stdin")
		_ = cmd.MarkFlagFilename("request", "json")
		_ = cmd.MarkFlagRequired("request")
		functionsCmd.AddCommand(cmd)
	}
}

func runWorkloadLifecycle(cmd *cobra.Command, op, owner string) error {
	u, err := uuid.Parse(owner)
	if err != nil || u.String() != owner || u.Variant() != uuid.RFC4122 || u.Version() < 1 || u.Version() > 8 {
		return fmt.Errorf("exact canonical function UUID required")
	}
	path, _ := cmd.Flags().GetString("request")
	var input io.Reader = cmd.InOrStdin()
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open lifecycle request: %w", err)
		}
		defer f.Close()
		input = f
	}
	raw, err := io.ReadAll(io.LimitReader(input, workloadLifecycleLimit+1))
	if err != nil {
		return fmt.Errorf("read lifecycle request: %w", err)
	}
	if len(raw) > workloadLifecycleLimit || !utf8.Valid(raw) || !json.Valid(raw) {
		return fmt.Errorf("lifecycle request must be valid UTF-8 JSON within %d bytes", workloadLifecycleLimit)
	}
	if err := confirm(cmd, fmt.Sprintf("Confirm managed %s for %s using the reviewed request? This will not start execution or standing work.", op, owner)); err != nil {
		return err
	}
	c, err := getClient(cmd)
	if err != nil {
		return err
	}
	c.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Send(client.Request{Method: http.MethodPost, Path: "/api/v1/functions/" + owner + "/" + op,
		Body: bytes.NewReader(raw), ContentType: "application/json", Ctx: cmd.Context()})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	out, err := io.ReadAll(io.LimitReader(resp.Body, workloadLifecycleLimit+1))
	if err != nil || len(out) > workloadLifecycleLimit || !json.Valid(out) {
		return fmt.Errorf("invalid or oversized lifecycle acknowledgement; outcome unknown, inspect before retrying")
	}
	return writeRawJSON(cmd.OutOrStdout(), out)
}
