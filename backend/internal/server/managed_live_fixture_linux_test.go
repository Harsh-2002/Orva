package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
)

func managedFixtureInvoke(t *testing.T, db *database.Database, call func(*testing.T, string, string, any, int) []byte) {
	t.Helper()
	var owner struct{ ID string }
	managedMust(t, json.Unmarshal(call(t, http.MethodPost, "/api/v1/functions",
		map[string]string{"name": "managed-fixture", "runtime": "node"}, 201), &owner))
	base := "/api/v1/functions/" + owner.ID
	const name = "saved-request"
	call(t, http.MethodPost, base+"/fixtures", map[string]any{
		"name": name, "method": "POST", "path": "/saved", "body": "original",
		"headers": map[string]string{"x-fixture-marker": "saved"},
	}, 201)
	run := func(override map[string]any) (bool, mcp.TestFunctionWithFixtureOutput) {
		t.Helper()
		args := map[string]any{"function_id": owner.ID, "fixture_name": name}
		if override != nil {
			args["override"] = override
		}
		raw := call(t, http.MethodPost, "/mcp", map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "test_function_with_fixture", "arguments": args},
		}, 200)
		for _, line := range strings.Split(string(raw), "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				raw = []byte(data)
				break
			}
		}
		var reply struct {
			Result *struct {
				IsError bool                              `json:"isError"`
				Output  mcp.TestFunctionWithFixtureOutput `json:"structuredContent"`
			} `json:"result"`
		}
		managedMust(t, json.Unmarshal(raw, &reply))
		if reply.Result == nil {
			t.Fatal("fixture call did not return an MCP tool result")
		}
		return reply.Result.IsError, reply.Result.Output
	}

	var deployment struct {
		ID string `json:"deployment_id"`
	}
	managedMust(t, json.Unmarshal(call(t, http.MethodPost, base+"/deploy-inline", map[string]string{
		"filename": "handler.js",
		"code":     `exports.handler = e => ({statusCode:200,body:[e.method,e.path,e.headers["x-fixture-marker"],e.body].join("|")})`,
	}, 202), &deployment))
	deadline := time.Now().Add(30 * time.Second)
	for {
		d, err := db.GetDeployment(deployment.ID)
		managedMust(t, err)
		if d.Status == "succeeded" {
			break
		}
		if d.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("fixture deployment: %s %s", d.Status, d.ErrorMessage)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if failed, out := run(nil); !failed || out.ExecutionID != "" {
		t.Fatal("fixture dispatched an uninstalled deployment")
	}
	call(t, http.MethodPost, "/api/v1/deployments/"+deployment.ID+"/install", nil, 200)
	check := func(override map[string]any, expected string, fields int) {
		t.Helper()
		failed, out := run(override)
		if failed || out.StatusCode != 200 || out.Body != expected || out.ExecutionID == "" ||
			out.Fixture.Name != name || len(out.Fixture.AppliedOverrides) != fields {
			t.Fatalf("fixture result did not match its request: failed=%v status=%d body=%q", failed, out.StatusCode, out.Body)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			row, err := db.GetExecution(out.ExecutionID)
			if err == nil {
				if row.FunctionID != owner.ID {
					t.Fatal("fixture execution persisted under a different function")
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("fixture execution record was not persisted")
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	check(nil, "POST|/saved|saved|original", 0)
	check(map[string]any{"method": "PUT", "path": "/override", "body": "changed",
		"headers": map[string]string{"X-Fixture-Marker": "override"}}, "PUT|/override|override|changed", 4)
	check(nil, "POST|/saved|saved|original", 0)
}
