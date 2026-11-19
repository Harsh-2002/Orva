//go:build linux

package mcp

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
	"github.com/Harsh-2002/Orva/backend/internal/workloadhistory"
	"github.com/Harsh-2002/Orva/backend/internal/workloadtrigger"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOperatorCatalogMatchesChat(t *testing.T) {
	f := iamSurfaceSetup(t)
	normal := Deps{DB: f.db, Registry: f.registry}
	async := new(workloadasync.Service)
	publication := new(builder.PublicationQueue)
	managed := Deps{
		DB: f.db, Registry: f.registry, ManagedServer: true, IAM: f.service,
		Configuration: new(workloadconfig.Service), History: new(workloadhistory.Service),
		DeployIAM: publication, RequireDeployIAM: true, WorkloadLifecycle: publication,
		AsyncEnabled: true, AsyncIAM: async, CronEnabled: true, CronIAM: async,
		Triggers: new(workloadtrigger.Service),
		// The serving composition always wires a replay path; without one the
		// catalog silently omits replay_execution and the count is short by one.
		ReplayExecution: func(http.ResponseWriter, *http.Request) {},
	}
	managed = withManagedFixtureProxy(t, f, managed)
	withFirewall := managed
	withFirewall.Firewall = firewall.NewManager(f.db, t.TempDir())
	all := permSet{permRead: true, permWrite: true, permInvoke: true, permAdmin: true}
	read := permSet{permRead: true}
	seedStatelessAPIKey(t, f.db, "orva_catalog_all_test", "catalog-all", "catalog-all", `["read","write","invoke","admin"]`)
	seedStatelessAPIKey(t, f.db, "orva_catalog_read_test", "catalog-read", "catalog-read", `["read"]`)
	for _, tc := range []struct {
		name, key string
		deps      Deps
		perms     permSet
		want      string
		count     int
	}{
		{"normal-admin", "orva_catalog_all_test", normal, all, "invoke_function", 73},
		{"normal-read", "orva_catalog_read_test", normal, read, "list_functions", 28},
		{"managed-admin-no-firewall", "orva_catalog_all_test", managed, all, "create_iam_policy", 76},
		{"managed-admin", "orva_catalog_all_test", withFirewall, all, "set_dns_config", 82},
		{"managed-read", "orva_catalog_read_test", withFirewall, read, "list_iam_policies", 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewHandler(tc.deps)
			var external []string
			var cursor string
			for page := 0; ; page++ {
				if page == 20 {
					t.Fatal("catalog did not finish paging")
				}
				params := map[string]any{"_meta": newProtocolMeta(true)}
				if cursor != "" {
					params["cursor"] = cursor
				}
				req := httptest.NewRequest(http.MethodPost, "/mcp",
					strings.NewReader(rpcBody(t, page+1, "tools/list", params)))
				req.Header.Set("Authorization", "Bearer "+tc.key)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
				req.Header.Set(hdrMethod, "tools/list")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("tools/list HTTP status: %d", w.Code)
				}
				list := decodeResult[mcpsdk.ListToolsResult](t, decodeRPC(t, w))
				for _, tool := range list.Tools {
					external = append(external, tool.Name)
				}
				cursor = list.NextCursor
				if cursor == "" {
					break
				}
			}
			slices.Sort(external)
			var chat []string
			for _, tool := range BuildAgentRegistry(tc.deps, tc.perms).Tools() {
				chat = append(chat, tool.Name)
			}
			if !slices.Equal(external, chat) || !slices.Contains(external, tc.want) {
				t.Fatalf("MCP and Chat catalogs differ or omit %s: %v vs %v", tc.want, external, chat)
			}
			if tc.deps.ManagedServer && slices.Contains(external, "invoke_function") {
				t.Fatal("managed catalog registered legacy invocation")
			}
			if tc.deps.ManagedServer {
				if has := slices.Contains(external, "test_function_with_fixture"); has != tc.perms.Has(permInvoke) {
					t.Fatalf("test_function_with_fixture present=%v for perms %v", has, tc.perms)
				}
				// Replay is a WRITE tool: in both the external MCP and the Chat catalog
				// (equal above) for a write-capable key, and absent for a read-only one.
				if has := slices.Contains(external, "replay_execution"); has != tc.perms.Has(permWrite) {
					t.Fatalf("replay_execution present=%v for perms %v", has, tc.perms)
				}
			}
			if len(external) != tc.count {
				t.Fatalf("catalog has %d tools, documented count %d", len(external), tc.count)
			}
			t.Logf("%s: %d tools; external MCP and Chat names match", tc.name, len(external))
		})
	}
}
