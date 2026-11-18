package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadtrigger"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestExposureLegacyProjectionMCPAndAI(t *testing.T) {
	for _, transport := range []string{"mcp", "ai"} {
		for _, role := range []string{"admin", "write"} {
			t.Run(transport+"-"+role, func(t *testing.T) {
				f := iamSurfaceSetup(t)
				tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../database", "workload_exposure_schema.go"), nil, 0)
				if err != nil {
					t.Fatal(err)
				}
				ast.Inspect(tree, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						s, _ := strconv.Unquote(lit.Value)
						if strings.Contains(s, "CREATE TABLE IF NOT EXISTS") {
							if _, err := f.sql.Exec(s); err != nil {
								t.Fatal(err)
							}
						}
					}
					return true
				})
				svc, err := workloadtrigger.New(f.db, f.registry, f.gate)
				if err != nil {
					t.Fatal(err)
				}
				deps := Deps{DB: f.db, Registry: f.registry, Triggers: svc}
				id, key := ids.New(), "orva_"+ids.New()
				hash := sha256.Sum256([]byte(key))
				if err = f.db.InsertAPIKey(&database.APIKey{ID: id, Name: "projection", KeyHash: hex.EncodeToString(hash[:]), Permissions: `["` + role + `"]`}); err != nil {
					t.Fatal(err)
				}
				ctx := auth.WithOperator(context.Background(), &auth.Principal{Kind: auth.KindAPIKey, ID: id})
				agent := BuildAgentRegistry(deps, permSet{role: true})
				server := httptest.NewServer(NewHandler(deps))
				defer server.Close()
				call := func(name string, args any) map[string]json.RawMessage {
					t.Helper()
					body, _ := json.Marshal(args)
					var result any
					if transport == "ai" {
						out, err := agent.Dispatch(ctx, name, body)
						if err != nil {
							t.Fatal("AI operation failed", name, err)
						}
						result = out
					} else {
						raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": json.RawMessage(body)}})
						req, _ := http.NewRequest("POST", server.URL, bytes.NewReader(raw))
						req.Header.Set("Authorization", "Bearer "+key)
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("Accept", "application/json, text/event-stream")
						res, err := server.Client().Do(req)
						if err != nil {
							t.Fatal(err)
						}
						raw, err = io.ReadAll(io.LimitReader(res.Body, 1<<20))
						res.Body.Close()
						if err != nil {
							t.Fatal(err)
						}
						for _, line := range strings.Split(string(raw), "\n") {
							if strings.HasPrefix(line, "data: ") {
								raw = []byte(strings.TrimPrefix(line, "data: "))
								break
							}
						}
						var out struct {
							Error  json.RawMessage `json:"error"`
							Result struct {
								IsError    bool            `json:"isError"`
								Structured json.RawMessage `json:"structuredContent"`
							} `json:"result"`
						}
						if json.Unmarshal(raw, &out) != nil || len(out.Error) > 0 || out.Result.IsError {
							t.Fatal("MCP operation failed", name)
						}
						result = out.Result.Structured
					}
					raw, _ := json.Marshal(result)
					var out map[string]json.RawMessage
					if json.Unmarshal(raw, &out) != nil {
						t.Fatal("missing structured result")
					}
					return out
				}
				owner := f.owners[0]
				expected := map[string]any{owner: map[string]string{"policy_revision": "1", "authority_epoch": "1"}}
				for _, label := range []string{"first-" + transport + "-" + role, "second-" + transport + "-" + role} {
					if _, err = f.sql.Exec(`UPDATE functions SET name=? WHERE id=?`, label, owner); err != nil {
						t.Fatal(err)
					}
					created := call("create_inbound_webhook", map[string]any{"function_id": owner, "name": "signed", "version": "0", "expected": expected})
					if len(created) != 2 || created["inbound_webhook"] == nil || created["secret"] == nil {
						t.Fatal("create differs from legacy field locations")
					}
					var view InboundWebhookView
					var secret string
					if json.Unmarshal(created["inbound_webhook"], &view) != nil || json.Unmarshal(created["secret"], &secret) != nil || len(secret) != 64 {
						t.Fatal("invalid create projection")
					}
					row, err := f.db.GetInboundWebhook(view.ID)
					if err != nil {
						t.Fatal(err)
					}
					row.FunctionName = label
					if !reflect.DeepEqual(view, toInboundWebhookView(row)) {
						t.Fatal("legacy view/function label/relative trigger URL mismatch")
					}
					var fields map[string]json.RawMessage
					json.Unmarshal(created["inbound_webhook"], &fields)
					canonical, _ := json.Marshal(toInboundWebhookView(row))
					var canonicalFields map[string]json.RawMessage
					json.Unmarshal(canonical, &canonicalFields)
					if !reflect.DeepEqual(fields, canonicalFields) || fields["secret"] != nil || view.TriggerURL != "/webhook/"+view.ID {
						t.Fatal("secret leak or incorrect relative URL")
					}
					updated := call("update_inbound_webhook", map[string]any{"function_id": owner, "id": view.ID, "active": false, "version": "1", "expected": expected})
					if len(updated) != 1 || updated["inbound_webhook"] == nil || updated["secret"] != nil {
						t.Fatal("update exported extra fields")
					}
					var after InboundWebhookView
					json.Unmarshal(updated["inbound_webhook"], &after)
					if after.FunctionName != label || after.TriggerURL != view.TriggerURL || after.Active {
						t.Fatal("update did not use canonical masked view")
					}
					json.Unmarshal(updated["inbound_webhook"], &fields)
					if fields["secret"] != nil || len(fields) != len(canonicalFields) {
						t.Fatal("update view exposed noncanonical fields")
					}
					current, err := f.db.GetInboundWebhook(view.ID)
					if err != nil || current.Secret != secret {
						t.Fatal("update rotated signing secret")
					}
					deleted := call("delete_inbound_webhook", map[string]any{"function_id": owner, "id": view.ID, "confirm": true, "version": "2", "expected": expected})
					if len(deleted) != 2 || string(deleted["id"]) != strconv.Quote(view.ID) || string(deleted["status"]) != `"deleted"` {
						t.Fatal("delete projection differs from legacy")
					}
				}
				if agent.Get("inspect_exposure") != nil {
					t.Fatal("mutation role acquired READ")
				}
				if BuildAgentRegistry(Deps{}, permSet{permRead: true}).Get("inspect_exposure") != nil {
					t.Fatal("nil catalog activated exposure")
				}
			})
		}
	}
}
