package mcp

import (
	"encoding/json"
	"testing"
)

// TestBuildAgentRegistrySecrets verifies that the migrated secret tools land
// in the in-process registry with the expected metadata + an inferred JSON
// schema, and that permission gating filters the catalog.
func TestBuildAgentRegistrySecrets(t *testing.T) {
	perms := permSet{"read": true, "write": true}
	reg := BuildAgentRegistry(Deps{}, perms)

	want := map[string]struct {
		perm        string
		destructive bool
	}{
		"list_secrets": {perm: "read", destructive: false},
		"set_secret":   {perm: "write", destructive: false},
		"delete_secret": {perm: "write", destructive: true},
	}

	for name, exp := range want {
		tool := reg.Get(name)
		if tool == nil {
			t.Fatalf("expected tool %q in registry", name)
		}
		if tool.Perm != exp.perm {
			t.Errorf("%s: perm = %q, want %q", name, tool.Perm, exp.perm)
		}
		if tool.Destructive != exp.destructive {
			t.Errorf("%s: destructive = %v, want %v", name, tool.Destructive, exp.destructive)
		}
		if tool.Group != "secrets" {
			t.Errorf("%s: group = %q, want secrets", name, tool.Group)
		}
		if len(tool.Schema) == 0 {
			t.Errorf("%s: empty schema", name)
		}
		// The schema must be a valid JSON object describing function_id etc.
		var obj map[string]any
		if err := json.Unmarshal(tool.Schema, &obj); err != nil {
			t.Errorf("%s: schema not valid JSON: %v", name, err)
		}
		if obj["type"] != "object" {
			t.Errorf("%s: schema type = %v, want object", name, obj["type"])
		}
	}
}

// TestBuildAgentRegistryPermGating confirms a read-only principal sees only
// read tools — never write/destructive ones.
func TestBuildAgentRegistryPermGating(t *testing.T) {
	reg := BuildAgentRegistry(Deps{}, permSet{"read": true})
	if reg.Get("list_secrets") == nil {
		t.Error("read principal should see list_secrets")
	}
	if reg.Get("set_secret") != nil {
		t.Error("read principal must NOT see set_secret")
	}
	if reg.Get("delete_secret") != nil {
		t.Error("read principal must NOT see delete_secret")
	}
}
