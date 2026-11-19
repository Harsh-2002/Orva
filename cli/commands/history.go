package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

const historyRequestLimit = (1 << 20) + 8192

var historyCmd = &cobra.Command{
	Use: "history", Short: "Inspect deleted-work metadata; explicitly create fresh work",
	Long: "Optional history service only. Archives contain no old payload and cannot resume or execute. READ permits inspection; purge and reissue require live ADMIN. Ninety days is purge eligibility, not automatic expiry.",
}

func init() {
	for _, op := range []string{"list", "get", "capacity", "purge", "reissue"} {
		op := op
		cmd := &cobra.Command{
			Use: op, Short: "History " + op + " using the strict server contract", Args: cobra.NoArgs,
			Long:              "Send an original JSON request from --request FILE or --request - (stdin). Output is always JSON. Never supply old payload or inferred target names. Reissue requires source kind/UUID, an explicitly selected NEW target UUID, fresh payload/config, reviewed:true and confirm:true. Purge requires exact selected kind/UUIDs and confirm:true. No automatic retries.",
			RunE:              func(cmd *cobra.Command, _ []string) error { return runHistory(cmd, op) },
			ValidArgsFunction: cobra.NoFileCompletions,
		}
		cmd.Flags().String("request", "", "JSON request file, or - for stdin (required for get/purge/reissue)")
		_ = cmd.MarkFlagFilename("request", "json")
		if op != "list" && op != "capacity" {
			_ = cmd.MarkFlagRequired("request")
		}
		historyCmd.AddCommand(cmd)
	}
}

func runHistory(cmd *cobra.Command, op string) error {
	path, _ := cmd.Flags().GetString("request")
	raw := []byte(`{}`)
	if op == "list" {
		raw = []byte(`{"limit":100}`)
	}
	if path != "" {
		var input io.Reader = cmd.InOrStdin()
		if path != "-" {
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("open history request: %w", err)
			}
			defer file.Close()
			input = file
		}
		var err error
		raw, err = io.ReadAll(io.LimitReader(input, historyRequestLimit+1))
		if err != nil {
			return fmt.Errorf("read history request: %w", err)
		}
	}
	if len(raw) > historyRequestLimit || !utf8.Valid(raw) || !json.Valid(raw) {
		return fmt.Errorf("history request must be valid UTF-8 JSON within %d bytes", historyRequestLimit)
	}
	if op == "purge" || op == "reissue" {
		if err := confirm(cmd, "Confirm history "+op+" using the exact reviewed request? Archived work is never resumed."); err != nil {
			return err
		}
	}
	c, err := getClient(cmd)
	if err != nil {
		return err
	}
	resp, err := c.Send(client.Request{Method: "POST", Path: "/api/v1/work-history/" + op,
		Body: bytes.NewReader(raw), ContentType: "application/json", Ctx: cmd.Context()})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	out, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(out) > 2<<20 || !json.Valid(out) {
		return fmt.Errorf("invalid or oversized history response; outcome unknown, inspect before retrying")
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(out)); err != nil {
		return err
	}
	okf(cmd, "History %s completed", op)
	return nil
}
