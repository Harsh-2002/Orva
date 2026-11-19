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

// The managed catalog carries the shared operator surfaces at the legacy
// tiers, and none of the tools that dispatch function code except replay.
func TestManagedCatalogOperatorSurfaces(t *testing.T) {
	f := iamSurfaceSetup(t)
	base := Deps{ManagedServer: true, DB: f.db, Registry: f.registry, IAM: f.service}
	withReplay := base
	withReplay.ReplayExecution = func(http.ResponseWriter, *http.Request) {}
	admin := permSet{permRead: true, permWrite: true, permInvoke: true, permAdmin: true}
	read := permSet{permRead: true}
	write := permSet{permWrite: true}

	names := func(deps Deps, perms permSet) map[string]bool {
		out := map[string]bool{}
		for _, tool := range BuildAgentRegistry(deps, perms).Tools() {
			out[tool.Name] = true
		}
		return out
	}
	tiers := map[string][]string{
		"read": {"list_executions", "get_execution", "get_execution_logs", "list_traces", "get_trace",
			"get_function_baseline", "system_health", "system_metrics", "list_activity"},
		"write": {"delete_execution", "bulk_delete_executions", "replay_execution"},
		"admin": {"system_storage", "system_vacuum", "list_api_keys", "create_api_key", "delete_api_key",
			"list_channels", "get_channel"},
	}
	all, readOnly, writeOnly := names(withReplay, admin), names(withReplay, read), names(withReplay, write)
	for tier, tools := range tiers {
		for _, tool := range tools {
			if !all[tool] {
				t.Fatalf("managed catalog is missing %s", tool)
			}
			if got := readOnly[tool]; got != (tier == "read") {
				t.Fatalf("%s tier %s visible to a read-only key: %v", tool, tier, got)
			}
			if got := writeOnly[tool]; got != (tier == "write") {
				t.Fatalf("%s tier %s visible to a write-only key: %v", tool, tier, got)
			}
		}
	}
	if names(base, admin)["replay_execution"] {
		t.Fatal("replay is registered without a managed replay path")
	}
	for _, prohibited := range []string{"invoke_function", "test_function_with_fixture", "list_runtimes", "update_function"} {
		if all[prohibited] {
			t.Fatalf("legacy tool %q leaked into the managed catalog", prohibited)
		}
	}
}

// The replayed function's own status is a result, not a tool failure; only
// refusals before or during dispatch (no replay headers) are errors.
func TestManagedReplayToolReportsTheFunctionStatus(t *testing.T) {
	f := iamSurfaceSetup(t)
	run := func(status int, replayed bool, body string) (any, error) {
		deps := Deps{ManagedServer: true, DB: f.db, Registry: f.registry, IAM: f.service,
			ReplayExecution: func(w http.ResponseWriter, _ *http.Request) {
				if replayed {
					w.Header().Set("X-Orva-Execution-ID", "new-exec")
					w.Header().Set("X-Orva-Replay-Of", "old-exec")
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}}
		reg := BuildAgentRegistry(deps, permSet{permRead: true, permWrite: true, permAdmin: true})
		return reg.Dispatch(f.ctx, "replay_execution", json.RawMessage(`{"execution_id":"old-exec"}`))
	}
	for _, status := range []int{200, 201, 204, 404, 500} {
		out, err := run(status, true, "fn-body")
		if err != nil {
			t.Fatalf("function status %d was reported as a tool failure: %v", status, err)
		}
		view, ok := out.(ReplayExecutionOutput)
		if !ok || view.StatusCode != status || view.ExecutionID != "new-exec" || view.ReplayOf != "old-exec" {
			t.Fatalf("function status %d lost in the result: %#v", status, out)
		}
	}
	for _, status := range []int{404, 409, 410, 502, 503, 504} {
		if _, err := run(status, false, `{"error":{"code":"X","message":"denied by admission"}}`); err == nil ||
			!strings.Contains(err.Error(), "denied by admission") {
			t.Fatalf("refusal %d was not a tool error: %v", status, err)
		}
	}
}
