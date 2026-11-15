package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
)

func TestSDKPermissionMCPCreateUpdateView(t *testing.T) {
	db := newStatelessDatabase(t)
	deps := Deps{DB: db, Registry: registry.New(db)}
	for _, network := range []string{"none", "egress"} {
		fn, err := createFunction(deps, CreateFunctionInput{
			Name: "sdk-" + network, Description: "SDK permission test", Runtime: "node",
			Entrypoint: "handler.js", TimeoutMS: 1000, MemoryMB: 64, CPUs: 0.25,
			NetworkMode: network, AuthMode: "none",
		})
		if err != nil || fn.SDK {
			t.Fatalf("MCP create: %+v (%v)", fn, err)
		}
		on, err := createFunction(deps, CreateFunctionInput{
			Name: "sdk-on-" + network, Description: "Explicit SDK permission test", Runtime: "node",
			Entrypoint: "handler.js", TimeoutMS: 1000, MemoryMB: 64, CPUs: 0.25,
			NetworkMode: network, AuthMode: "none", SDK: true,
		})
		if err != nil || !on.SDK || on.AuthMode != database.AuthModeNone {
			t.Fatalf("MCP explicit SDK opt-in changed incoming auth or was lost: %+v (%v)", on, err)
		}
		for _, enabled := range []bool{true, false} {
			fn, err = updateFunction(deps, UpdateFunctionInput{FunctionID: fn.ID, SDK: &enabled})
			if err != nil || fn.SDK != enabled {
				t.Fatalf("MCP update: %+v (%v)", fn, err)
			}
			fn, err = updateFunction(deps, UpdateFunctionInput{FunctionID: fn.ID})
			if err != nil || fn.SDK != enabled {
				t.Fatalf("omitted SDK changed permission: %+v (%v)", fn, err)
			}
			view := toFunctionView(context.Background(), fn, deps)
			blob, err := json.Marshal(view)
			if err != nil || !strings.Contains(string(blob), `"sdk":`+strconv.FormatBool(enabled)) {
				t.Fatalf("MCP view: %s (%v)", blob, err)
			}
		}
	}
}

func TestSDKPermissionMCPSchemaRemainsOptional(t *testing.T) {
	reg := BuildAgentRegistry(Deps{}, allPerms())
	for _, name := range []string{"create_function", "update_function"} {
		tool := reg.Get(name)
		if tool == nil {
			t.Fatalf("missing tool %s", name)
		}
		var schema struct {
			Properties map[string]struct {
				Type json.RawMessage `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(tool.Schema, &schema); err != nil {
			t.Fatal(err)
		}
		var types []string
		rawType := schema.Properties["sdk"].Type
		if len(rawType) > 0 && rawType[0] == '"' {
			var typ string
			if err := json.Unmarshal(rawType, &typ); err != nil {
				t.Fatal(err)
			}
			types = []string{typ}
		} else if err := json.Unmarshal(rawType, &types); err != nil {
			t.Fatal(err)
		}
		boolean := false
		for _, typ := range types {
			if typ != "boolean" && typ != "null" {
				t.Fatalf("%s sdk accepts non-boolean values: %s", name, rawType)
			}
			boolean = boolean || typ == "boolean"
		}
		if !boolean {
			t.Fatalf("%s sdk is not boolean: %s", name, tool.Schema)
		}
		for _, required := range schema.Required {
			if required == "sdk" {
				t.Fatalf("%s now requires SDK; old clients must remain compatible", name)
			}
		}
	}
}

func TestSDKPermissionMCPRollbackRestoresSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, network string
		sdk, legacy   bool
	}{
		{"explicit-true-none", "none", true, false},
		{"explicit-false-egress", "egress", false, false},
		{"legacy-egress", "egress", true, true},
		{"legacy-none", "none", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newStatelessDatabase(t)
			fn := &database.Function{
				ID: "019df200-7b00-7e00-9c00-aab1cd2e3f41", Name: "rollback-sdk", Runtime: "node",
				Entrypoint: "handler.js", Status: "active", SDK: tc.sdk, NetworkMode: tc.network,
				CodeHash: strings.Repeat("a", 64), MemoryMB: 64, CPUs: 1, TimeoutMS: 1000,
			}
			if err := db.InsertFunction(fn); err != nil {
				t.Fatal(err)
			}
			dep := &database.Deployment{
				ID: "019df200-7b00-7e00-9c00-aab1cd2e3f42", FunctionID: fn.ID, Version: 1,
				Status: "succeeded", CodeHash: fn.CodeHash, Snapshot: database.SnapshotFromFunction(fn),
			}
			if err := db.InsertDeployment(dep); err != nil {
				t.Fatal(err)
			}
			if err := db.SetDeploymentSnapshot(dep.ID, dep.Snapshot); err != nil {
				t.Fatal(err)
			}
			if tc.legacy {
				for _, stmt := range []string{
					`ALTER TABLE functions DROP COLUMN sdk`,
					`UPDATE deployments SET snapshot = json_remove(snapshot, '$.sdk')`,
				} {
					if _, err := db.WriteDB().Exec(stmt); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Migrate(); err != nil {
					t.Fatal(err)
				}
			}
			fn.SDK = !tc.sdk
			reg := registry.New(db)
			if err := reg.Set(fn); err != nil {
				t.Fatal(err)
			}
			dataDir := t.TempDir()
			version := filepath.Join(dataDir, "functions", fn.ID, "versions", fn.CodeHash)
			if err := os.MkdirAll(version, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(version, ".orva-ready"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			_, out, err := rollbackFunction(Deps{DB: db, Registry: reg, DataDir: dataDir},
				RollbackFunctionInput{FunctionID: fn.ID, DeploymentID: dep.ID, Confirm: true})
			if err != nil || out.DeploymentID != dep.ID {
				t.Fatalf("rollback %+v (%v)", out, err)
			}
			got, err := db.GetFunction(fn.ID)
			if err != nil || got.SDK != tc.sdk || got.ActiveDeploymentID != dep.ID || got.NetworkMode != tc.network {
				t.Fatalf("rollback state %+v (%v)", got, err)
			}
			cached, err := reg.Get(fn.ID)
			if err != nil || cached.SDK != tc.sdk {
				t.Fatalf("rollback registry state %+v (%v)", cached, err)
			}
			stored, err := db.GetDeployment(dep.ID)
			if err != nil || stored.Snapshot == nil || stored.Snapshot.SDK != tc.sdk {
				t.Fatalf("normalized snapshot %+v (%v)", stored, err)
			}
		})
	}
}

func TestSDKPermissionErrorHintSeparateFromEgress(t *testing.T) {
	for _, network := range []string{"none", "egress"} {
		fn := &database.Function{NetworkMode: network}
		hint := networkErrorHint(fn, "OrvaUnavailableError", "")
		if !strings.Contains(hint, "sdk=true") {
			t.Fatalf("missing permission hint with %s: %s", network, hint)
		}
		fn.SDK = true
		if hint := networkErrorHint(fn, "SDK permission", ""); hint != "" {
			t.Fatalf("enabled SDK falsely diagnosed missing permission: %s", hint)
		}
	}
}
