package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const iamReaderJSON = `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"prefix":"config/"}},"context":"execution"}],"inbound":[]}`
const iamEmptyJSON = `{"schema_version":1,"allow":[],"inbound":[]}`

type iamSurfaceFixture struct {
	db                 *database.Database
	sql                *sql.DB
	service            *workloadpolicy.Service
	gate               *workloadiam.Authority
	registry           *registry.Registry
	owners             []string
	states             map[string]workloadpolicy.ArtifactState
	key, reader, keyID string
	ctx                context.Context
	server             *httptest.Server
}

func iamSurfaceSetup(t *testing.T) *iamSurfaceFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scratch.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	stage := func(name, marker string) {
		tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../database", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var schema string
		ast.Inspect(tree, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				value, _ := strconv.Unquote(lit.Value)
				if strings.Contains(value, marker) {
					schema = value
				}
			}
			return true
		})
		if schema == "" {
			t.Fatal("canonical schema absent", name)
		}
		if _, err := conn.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InitIAMSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	f := &iamSurfaceFixture{db: db, sql: conn, states: map[string]workloadpolicy.ArtifactState{}, keyID: ids.New(), key: "orva_" + strings.ReplaceAll(ids.New(), "-", ""), reader: "orva_" + strings.ReplaceAll(ids.New(), "-", "")}
	compiled := []*workloadiam.Evaluator{}
	for i, lang := range []string{"node", "python"} {
		id := ids.New()
		f.owners = append(f.owners, id)
		if err := db.InsertFunction(&database.Function{ID: id, Name: "surface-" + lang, Runtime: lang, Status: "active", Entrypoint: "handler.js"}); err != nil {
			t.Fatal(err)
		}
		record, err := db.NewIAMPolicy(t.Context(), id, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
		if err != nil {
			t.Fatal(err)
		}
		e, err := record.AdmissionEvaluator()
		if err != nil {
			t.Fatal(err)
		}
		compiled = append(compiled, e)
		f.states[id] = workloadpolicy.ArtifactState{OwnerID: id, Artifact: strings.Repeat(string(rune('a'+i)), 64), Known: true}
	}
	stage("workload_managed_schema.go", "CREATE TABLE IF NOT EXISTS workload_iam_inline")
	for _, a := range []struct{ id, key, perms string }{{f.keyID, f.key, `["read","admin"]`}, {ids.New(), f.reader, `["read"]`}} {
		hash := sha256.Sum256([]byte(a.key))
		if err := db.InsertAPIKey(&database.APIKey{ID: a.id, Name: "surface-test", KeyHash: hex.EncodeToString(hash[:]), Prefix: "dummy", Permissions: a.perms}); err != nil {
			t.Fatal(err)
		}
	}
	f.gate, err = workloadiam.NewAuthority(8, compiled)
	if err != nil {
		t.Fatal(err)
	}
	f.registry = registry.New(db)
	f.service, err = workloadpolicy.NewManaged(db, f.registry, f.gate, func(id string) workloadpolicy.ArtifactState { return f.states[id] })
	if err != nil {
		t.Fatal(err)
	}
	f.ctx = auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: f.keyID})
	mux := http.NewServeMux()
	adapter := &workloadpolicy.ManagedHTTPAdapter{Service: f.service, Authenticate: func(r *http.Request) (*auth.Principal, error) {
		hash := sha256.Sum256([]byte(r.Header.Get("X-Orva-API-Key")))
		row, err := db.GetAPIKeyByHash(hex.EncodeToString(hash[:]))
		if err != nil {
			return nil, err
		}
		perms := auth.PermSet{}
		for _, p := range row.PermissionsList() {
			perms[p] = true
		}
		return &auth.Principal{Kind: auth.KindAPIKey, ID: row.ID, Perms: perms}, nil
	}}
	adapter.Register(mux)
	mux.Handle("/mcp", NewHandler(Deps{DB: db, Registry: f.registry, IAM: f.service, Version: "scratch"}))
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *iamSurfaceFixture) expected(t *testing.T, owners ...string) string {
	t.Helper()
	parts := []any{}
	for _, id := range owners {
		row, err := f.db.GetIAMPolicy(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		var approved any
		if row.ApprovedArtifact != database.IAMUnboundArtifact {
			approved = row.ApprovedArtifact
		}
		parts = append(parts, map[string]any{"function_id": id, "expected": map[string]any{"revision": strconv.FormatUint(row.Revision, 10), "current_artifact": f.states[id].Artifact, "approved_artifact": approved}})
	}
	body, _ := json.Marshal(parts)
	return string(body)
}

func (f *iamSurfaceFixture) http(t *testing.T, method, path, key, body string, want int) map[string]json.RawMessage {
	t.Helper()
	req, _ := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	req.Header.Set("X-Orva-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		t.Fatalf("%s %s: status %d: %s", method, path, res.StatusCode, raw)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("management response can be cached")
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *iamSurfaceFixture) mcp(t *testing.T, name, args string, wantError bool) map[string]json.RawMessage {
	t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, args)
	req, _ := http.NewRequest("POST", f.server.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if bytes.HasPrefix(raw, []byte("event:")) || bytes.HasPrefix(raw, []byte("data:")) {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "data: ") {
				raw = []byte(strings.TrimPrefix(line, "data: "))
				break
			}
		}
	}
	var out struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError    bool            `json:"isError"`
			Structured json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("MCP response parse: %v: %s", err, raw)
	}
	failed := len(out.Error) > 0 || out.Result.IsError
	if failed != wantError {
		t.Fatalf("MCP failed=%v want=%v: %s", failed, wantError, raw)
	}
	var result map[string]json.RawMessage
	if !failed {
		if err := json.Unmarshal(out.Result.Structured, &result); err != nil {
			t.Fatal("missing structured result", err, string(raw))
		}
	}
	return result
}

func policyFromResponse(t *testing.T, raw map[string]json.RawMessage) database.IAMManagedPolicy {
	t.Helper()
	var p database.IAMManagedPolicy
	if err := json.Unmarshal(raw["policy"], &p); err != nil || p.ID == "" {
		t.Fatal("policy response invalid", err)
	}
	return p
}

func TestIAMSurfacesRealMCPAIAndSlimCLI(t *testing.T) {
	f := iamSurfaceSetup(t)
	created := f.http(t, "POST", "/api/v1/iam/policies", f.key, `{"name":"own-config","policy":`+iamReaderJSON+`}`, 200)
	p := policyFromResponse(t, created)
	bin := filepath.Join(t.TempDir(), "orva")
	build := exec.Command("go", "build", "-o", bin, "./cli/cmd/orva")
	build.Dir = "../../.."
	build.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off")
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(raw))
	}
	cli := func(args []string, request string) map[string]json.RawMessage {
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"HOME=" + t.TempDir(), "ORVA_ENDPOINT=" + f.server.URL, "ORVA_API_KEY=" + f.key, "NO_COLOR=1"}
		cmd.Stdin = strings.NewReader(request)
		raw, err := cmd.Output()
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				t.Fatal(err, string(e.Stderr))
			}
			t.Fatal(err)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal("CLI stdout not JSON", err, string(raw))
		}
		return out
	}
	cli([]string{"iam", "policies", "attach", p.ID, "--request", "@-"}, `{"membership_revision":"1","version":"1","owners":`+f.expected(t, f.owners[0])+`}`)
	f.mcp(t, "attach_iam_policy", fmt.Sprintf(`{"id":%q,"request":{"membership_revision":"2","version":"1","owners":%s}}`, p.ID, f.expected(t, f.owners[1])), false)
	for _, id := range f.owners {
		snapshot := f.http(t, "GET", "/api/v1/functions/"+id+"/permissions", f.key, "", 200)
		if string(snapshot["inline_policy"]) != iamEmptyJSON || string(snapshot["effective_policy"]) != iamReaderJSON {
			t.Fatal("effective union was not separate from inline")
		}
		key := "config/value"
		c := workloadiam.Context{Principal: workloadiam.Principal{FunctionID: id, WorkerID: "fixture", ArtifactRevision: f.states[id].Artifact}, Phase: workloadiam.Execution, Ready: true, Lease: workloadiam.Lease{ID: "fixture-lease", WorkerID: "fixture", ExecutionID: ids.New(), Active: true}}
		if err := f.gate.Do(c, workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: id, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}, func() error { return nil }); err != nil {
			t.Fatal("shared service did not publish live own-KV grant", err)
		}
	}
	reg := BuildAgentRegistry(Deps{DB: f.db, Registry: f.registry, IAM: f.service}, permSet{"read": true, "admin": true})
	args := fmt.Sprintf(`{"id":%q,"request":{"definition_revision":"1","policy":%s}}`, p.ID, iamEmptyJSON)
	if _, err := reg.Dispatch(f.ctx, "update_iam_policy", json.RawMessage(args)); err != nil {
		t.Fatal("AI registry publication failed", err)
	}
	inspect := cli([]string{"iam", "policies", "get", p.ID, "--version", "1", "--history"}, "")
	if got := policyFromResponse(t, inspect); got.LatestVersion != 2 || got.MembershipRevision != 3 {
		t.Fatal("version creation changed membership")
	}
	for _, id := range f.owners {
		s, err := f.db.GetIAMSources(t.Context(), id)
		if err != nil || s.Attachments[0].Version != 1 || s.Record.PolicyJSON != iamReaderJSON {
			t.Fatal("v2 auto-followed", err)
		}
	}
	owners := f.expected(t, f.owners...)
	impact := fmt.Sprintf(`{"id":%q,"request":{"operation":"attach","membership_revision":"3","version":"2","owners":%s}}`, p.ID, owners)
	f.mcp(t, "get_iam_policy_impact", impact, false)
	f.mcp(t, "attach_iam_policy", fmt.Sprintf(`{"id":%q,"request":{"membership_revision":"3","version":"2","owners":%s}}`, p.ID, owners), false)
	for _, id := range f.owners {
		s, err := f.db.GetIAMSources(t.Context(), id)
		if err != nil || s.Record.PolicyJSON != iamEmptyJSON {
			t.Fatal("explicit rollout not published", err)
		}
	}
	for _, bad := range []string{
		`{"request":{"name":"bad","name":"bad2","policy":` + iamReaderJSON + `}}`,
		`{"request":{"name":"bad","policy":` + iamReaderJSON + `,"actor":{"kind":"system"}}}`,
		`{"request":{"name":"bad","policy":` + strings.Replace(iamReaderJSON, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1) + `}}`,
	} {
		f.mcp(t, "create_iam_policy", bad, true)
		if _, err := reg.Dispatch(f.ctx, "create_iam_policy", json.RawMessage(bad)); err == nil {
			t.Fatal("AI lost duplicate/unknown claim")
		}
	}
	f.http(t, "POST", "/api/v1/iam/policies", f.reader, `{"name":"forbidden","policy":`+iamReaderJSON+`}`, 403)
	readerReg := BuildAgentRegistry(Deps{DB: f.db, IAM: f.service}, permSet{"read": true})
	if readerReg.Get("create_iam_policy") != nil || readerReg.Get("get_iam_policy") == nil {
		t.Fatal("read-only catalog mismatch")
	}
	if BuildAgentRegistry(Deps{}, permSet{"read": true, "admin": true}).Get("create_iam_policy") != nil {
		t.Fatal("normal catalog half-activated")
	}
}

func TestIAMSurfaceStrictRequestsAndReadRevocation(t *testing.T) {
	f := iamSurfaceSetup(t)
	for _, body := range []string{
		`{"name":"x","policy":` + iamReaderJSON + `,"capabilities":{}}`,
		`{"name":"x","name":"y","policy":` + iamReaderJSON + `}`,
		`{"name":"x","policy":` + iamReaderJSON + `} {}`,
		`{"name":"x","policy":` + strings.Replace(iamReaderJSON, "config/", string([]byte{255}), 1) + `}`,
	} {
		f.http(t, "POST", "/api/v1/iam/policies", f.key, body, 400)
	}
	if _, err := f.sql.Exec(`DELETE FROM api_keys WHERE id=?`, f.keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ExecuteManaged(f.ctx, "list", "", []byte(`{}`)); err != database.ErrOperatorDenied {
		t.Fatal("revoked key inspected policy", err)
	}
}

func TestIAMSurfaceReadPermissionRecheckedAtCommit(t *testing.T) {
	f := iamSurfaceSetup(t)
	_, err := f.sql.Exec(fmt.Sprintf(`CREATE TRIGGER drop_management_read AFTER INSERT ON workload_iam_managed_versions
 BEGIN UPDATE api_keys SET permissions='["admin"]' WHERE id='%s'; END;`, f.keyID))
	if err != nil {
		t.Fatal(err)
	}
	f.http(t, "POST", "/api/v1/iam/policies", f.key, `{"name":"must-rollback","policy":`+iamReaderJSON+`}`, 403)
	result := f.http(t, "GET", "/api/v1/iam/policies", f.key, "", 200)
	var items []any
	if err := json.Unmarshal(result["items"], &items); err != nil || len(items) != 0 {
		t.Fatal("read revocation partially committed definition", err)
	}
}
