package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
)

func TestConfigurationCatalogOptInAndStrictDispatch(t *testing.T) {
	for _, tt := range []struct {
		name    string
		perms   permSet
		enabled bool
		count   int
	}{
		{"default", permSet{permRead: true, permWrite: true, permAdmin: true}, false, 0},
		{"read", permSet{permRead: true}, true, 1},
		{"write-no-read", permSet{permWrite: true}, true, 4},
		{"admin-no-read", permSet{permAdmin: true}, true, 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			deps := Deps{}
			if tt.enabled {
				deps.Configuration = &workloadconfig.Service{}
			}
			reg := newRegistry()
			registerConfigurationTools(&regCtx{deps: deps, perms: tt.perms, reg: reg})
			if len(reg.Tools()) != tt.count {
				t.Fatal("unexpected optional catalog")
			}
			for _, tool := range reg.Tools() {
				if tool.ReadOnly && tool.Name != "inspect_workload_configuration" {
					t.Fatal("configuration mutation marked read-only")
				}
				if (tool.Name == "delete_workload_secret") != tool.Destructive {
					t.Fatal("AI destructive gate mismatch")
				}
				for _, bad := range []string{
					`{"id":"a","id":"b","request":{}}`,
					`{"id":"a","request":{"expected":{},"expected":{}}}`,
					`{"id":"a","request":{},"actor":"author"}`,
					`{"id":"a","request":{},"confirm":null}`,
					`{"id":"a","request":{},"confirm":true}`,
				} {
					if _, err := reg.Dispatch(context.Background(), tool.Name, json.RawMessage(bad)); err == nil {
						t.Fatal("untrusted arguments/context accepted")
					}
				}
			}
		})
	}
}

func TestConfigurationInspectionRejectsUnadvertisedConfirmation(t *testing.T) {
	reg := newRegistry()
	registerConfigurationTools(&regCtx{deps: Deps{Configuration: &workloadconfig.Service{}}, perms: permSet{permRead: true}, reg: reg})
	ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: "019a9872-0000-7000-8000-000000000001"})
	for _, value := range []string{"true", "false"} {
		args := json.RawMessage(`{"id":"019a9872-0000-7000-8000-000000000001","request":{},"confirm":` + value + `}`)
		if _, err := reg.Dispatch(ctx, "inspect_workload_configuration", args); !errors.Is(err, database.ErrWorkloadArtifactInvalid) {
			t.Fatalf("inspection did not reject a field absent from its schema: %v", err)
		}
	}
}
