package mcp

import (
	"encoding/json"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
)

func TestIAMOperationSchemasAreClosedAndSpecific(t *testing.T) {
	for op, field := range map[string]string{"create": "policy", "update": "definition_revision", "attach": "version", "detach": "confirm", "revoke": "owners", "delete": "confirm", "inline": "expected", "review": "expected", "get": "history", "list": "offset"} {
		s := workloadpolicy.ManagedRequestSchema(op)
		if s["additionalProperties"] != false || s["properties"].(map[string]any)[field] == nil {
			t.Fatalf("%s has no closed operation schema", op)
		}
		raw, _ := json.Marshal(s)
		if !json.Valid(raw) {
			t.Fatal("invalid tool schema")
		}
	}
	impact := workloadpolicy.ManagedRequestSchema("impact")["oneOf"].([]any)
	if len(impact) != 3 {
		t.Fatal("impact must enumerate attach/detach/revoke")
	}
}
