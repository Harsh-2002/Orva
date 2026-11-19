//go:build linux

package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedServerToolsShareRestrictedCatalog(t *testing.T) {
	f := iamSurfaceSetup(t)
	deps := Deps{ManagedServer: true, DB: f.db, Registry: f.registry, IAM: f.service}
	allowed := permSet{"read": true, "write": true, "invoke": true, "admin": true}
	reg := BuildAgentRegistry(deps, allowed)
	for _, prohibited := range []string{"create_function", "invoke_function", "update_function", "set_secret", "enqueue_job"} {
		if reg.Get(prohibited) != nil {
			t.Fatalf("legacy tool %q registered", prohibited)
		}
	}
	if reg.Get("create_iam_policy") == nil {
		t.Fatal("managed policy authoring omitted")
	}
	srv := httptest.NewServer(NewHandler(deps))
	t.Cleanup(srv.Close)
	f.server = srv
	out := f.mcp(t, "create_iam_policy", `{"request":{"name":"via-mcp","policy":`+iamReaderJSON+`}}`, false)
	if len(out["policy"]) == 0 {
		t.Fatal("external MCP did not commit policy")
	}
	_, err := reg.Dispatch(f.ctx, "create_iam_policy", json.RawMessage(`{"request":{"name":"via-chat","policy":`+iamReaderJSON+`}}`))
	if err != nil {
		t.Fatal(err)
	}
	items, err := f.db.ListIAMManagedPolicies(t.Context(), 0)
	if err != nil || len(items) != 2 {
		t.Fatalf("shared registry and external MCP diverged from durable state: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST", srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("failed to list restricted tools: %d %v", res.StatusCode, err)
	}
	if strings.Contains(string(body), `"name":"update_function"`) || !strings.Contains(string(body), `"name":"create_iam_policy"`) {
		t.Fatal("external MCP catalog did not use the shared managed registration")
	}
}
