package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	client "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

const maxIAMRequest = 36 * 1024

var iamCmd = &cobra.Command{Use: "iam", Short: "Function IAM management (requires an explicitly enabled compatible endpoint)"}

func init() {
	policies := &cobra.Command{Use: "policies", Short: "Named policies, immutable versions and explicit attachments"}
	permissions := &cobra.Command{Use: "permissions", Short: "Separate inline and effective function permissions"}
	iamCmd.AddCommand(policies, permissions)
	for _, op := range []string{"list", "get", "create", "update", "impact", "attach", "detach", "revoke", "delete"} {
		cmd := newIAMCommand(op, false)
		policies.AddCommand(cmd)
	}
	for _, op := range []string{"get", "put", "review"} {
		permissions.AddCommand(newIAMCommand(op, true))
	}
}

func newIAMCommand(op string, function bool) *cobra.Command {
	use := op + " <policy-id>"
	if function {
		use = op + " <function>"
	}
	if op == "list" || op == "create" {
		use = op
	}
	cmd := &cobra.Command{Use: use, Short: map[string]string{
		"list": "List named policies", "get": "Inspect sources, pinned version or audit history", "create": "Create immutable version 1 without attaching",
		"update": "Create a NEW version; existing pins do not follow", "impact": "Preview exact-owner source changes without publishing",
		"attach": "Attach an explicit version or roll out to selected owners", "detach": "Remove only this source; overlapping grants remain",
		"revoke": "Terminally revoke this policy ID across ALL attached owners", "delete": "Delete an unattached policy; never cascade as revoke",
		"put": "Replace ONLY inline policy, preserving attached sources", "review": "Review exact aggregate/current artifact without replacing policy",
	}[op], Args: cobra.ExactArgs(1)}
	if op == "list" || op == "create" {
		cmd.Args = cobra.NoArgs
	}
	read := op == "get" || op == "list"
	if !read {
		cmd.Flags().String("request", "", "Exact JSON request from @file or @- (stdin); inline argv bodies are refused")
		_ = cmd.MarkFlagRequired("request")
	}
	if op == "get" && !function {
		cmd.Flags().String("version", "", "Explicit decimal-string version")
		cmd.Flags().Bool("history", false, "Include bounded audit history")
	}
	if op == "list" {
		cmd.Flags().String("offset", "0", "Decimal-string list offset")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		c, err := getClient(cmd)
		if err != nil {
			return err
		}
		path := "/api/v1/iam/policies"
		if len(args) > 0 {
			id := args[0]
			if function {
				id, err = resolveFnID(c, id)
				if err != nil {
					return err
				}
				path = "/api/v1/functions/" + url.PathEscape(id) + "/permissions"
			} else {
				path += "/" + url.PathEscape(id)
			}
		}
		method := "GET"
		var body []byte
		query := url.Values{}
		if read {
			if op == "list" {
				offset, _ := cmd.Flags().GetString("offset")
				query.Set("offset", offset)
			}
			if op == "get" && !function {
				v, _ := cmd.Flags().GetString("version")
				if v != "" {
					query.Set("version", v)
				}
				history, _ := cmd.Flags().GetBool("history")
				if history {
					query.Set("history", "true")
				}
			}
		} else {
			arg, _ := cmd.Flags().GetString("request")
			if !strings.HasPrefix(arg, "@") {
				return fmt.Errorf("--request requires @file or @-; do not place policy bodies in argv")
			}
			var input io.Reader = cmd.InOrStdin()
			var file *os.File
			if arg != "@-" {
				file, err = os.Open(strings.TrimPrefix(arg, "@"))
				if err != nil {
					return err
				}
				defer file.Close()
				input = file
			}
			body, err = io.ReadAll(io.LimitReader(input, maxIAMRequest+1))
			if err != nil {
				return err
			}
			if len(body) == 0 || len(body) > maxIAMRequest || !json.Valid(body) {
				return fmt.Errorf("invalid or oversized IAM request")
			}
			if op == "detach" || op == "revoke" || op == "delete" {
				if err := confirm(cmd, "Confirm IAM "+op+"? Other sources may retain access."); err != nil {
					return err
				}
				// The request itself must explicitly confirm; never rewrite raw JSON
				// and lose duplicate-key evidence before server validation.
				var fields map[string]json.RawMessage
				if json.Unmarshal(body, &fields) != nil || string(fields["confirm"]) != "true" {
					return fmt.Errorf("request requires confirm:true")
				}
			}
			method = "POST"
			if function && op == "put" {
				method = "PUT"
			} else if op != "create" {
				path += "/" + op
			}
		}
		response, err := c.Send(client.Request{Method: method, Path: path, Query: query, Body: bytes.NewReader(body), ContentType: "application/json", Ctx: cmd.Context()})
		if err != nil {
			return err
		}
		if err := checkResponse(response); err != nil {
			return err
		}
		var value any
		if err := decodeJSON(response, &value); err != nil {
			return err
		}
		return emitJSON(value)
	}
	return cmd
}
