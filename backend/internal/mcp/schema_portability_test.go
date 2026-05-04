package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSchemaPortability walks every operator-mode tool the MCP server
// would register and asserts the cross-client portability rules from
// CHECKLIST.md. Goal: fail-closed enforcement so a new tool that
// silently regresses (no Title, missing additionalProperties, short
// description, forbidden keyword) breaks the test, not a customer.
//
// Implementation: spin up the server with every register* function
// called, connect a client over an in-memory transport, call ListTools,
// then walk each tool's emitted JSON Schema.
func TestSchemaPortability(t *testing.T) {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	deps := Deps{} // empty deps — handlers won't run; we only inspect schemas
	perms := allPerms()

	// Register every operator-mode tool surface.
	registerSystemTools(srv, deps, perms)
	registerFunctionTools(srv, deps, perms)
	registerInvokeTools(srv, deps, perms)
	registerDeployTools(srv, deps, perms)
	registerSecretTools(srv, deps, perms)
	registerRouteTools(srv, deps, perms)
	registerFixtureTools(srv, deps, perms)
	registerFirewallTools(srv, deps, perms)
	registerWebhookTools(srv, deps, perms)
	registerInboundWebhookTools(srv, deps, perms)
	registerKeyTools(srv, deps, perms)
	registerKVTools(srv, deps, perms)
	registerJobTools(srv, deps, perms)
	registerCronTools(srv, deps, perms)
	registerPoolTools(srv, deps, perms)
	registerTraceTools(srv, deps, perms)
	registerDocsTools(srv, deps, perms)

	// Connect via an in-memory transport so we can hit the public
	// ListTools API without needing a real network listener.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cTransport, sTransport := mcpsdk.NewInMemoryTransports()
	go func() { _, _ = srv.Connect(ctx, sTransport, nil) }()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) < 50 {
		t.Fatalf("expected ≥50 registered tools, got %d", len(res.Tools))
	}

	for _, tt := range res.Tools {
		t.Run(tt.Name, func(t *testing.T) {
			assertPortableTool(t, tt)
		})
	}
}

// ── helpers ───────────────────────────────────────────────────────

var portableToolNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func assertPortableTool(t *testing.T, tt *mcpsdk.Tool) {
	t.Helper()

	if !portableToolNameRE.MatchString(tt.Name) {
		t.Errorf("name %q violates portable regex ^[a-z][a-z0-9_]{0,62}$", tt.Name)
	}
	if strings.TrimSpace(tt.Title) == "" {
		t.Errorf("title is empty (every tool should set Title for human display)")
	}
	if len(tt.Description) < 80 {
		t.Errorf("description is %d chars (< 80, see CHECKLIST.md rule 9): %q",
			len(tt.Description), tt.Description)
	}
	if tt.Annotations == nil {
		t.Errorf("annotations missing — set ReadOnlyHint / DestructiveHint / IdempotentHint / OpenWorldHint honestly")
	}

	if tt.InputSchema == nil {
		t.Errorf("inputSchema missing")
		return
	}
	inSchema, err := json.Marshal(tt.InputSchema)
	if err != nil {
		t.Fatalf("inputSchema marshal: %v", err)
	}
	assertSchemaPortable(t, "inputSchema", inSchema)

	if tt.OutputSchema != nil {
		outSchema, err := json.Marshal(tt.OutputSchema)
		if err != nil {
			t.Fatalf("outputSchema marshal: %v", err)
		}
		assertSchemaPortable(t, "outputSchema", outSchema)
	}
}

func assertSchemaPortable(t *testing.T, label string, raw []byte) {
	t.Helper()
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("%s: unmarshal: %v", label, err)
	}
	walkSchema(t, label, root)
}

func walkSchema(t *testing.T, path string, node any) {
	t.Helper()
	m, ok := node.(map[string]any)
	if !ok {
		return
	}

	// Forbidden keywords anywhere in the tree.
	for _, k := range []string{"oneOf", "allOf", "pattern"} {
		if _, has := m[k]; has {
			t.Errorf("%s: %q present (forbidden — strict modes reject; see CHECKLIST.md)", path, k)
		}
	}
	// External $ref (anything not starting with #).
	if ref, has := m["$ref"].(string); has && !strings.HasPrefix(ref, "#") {
		t.Errorf("%s: external $ref %q forbidden", path, ref)
	}

	// Object nodes must declare additionalProperties: false (rule 2).
	// Exception: a known allow-list of paths where free-form user JSON
	// is the explicit contract (cron/job payload, kv value object, the
	// invoke_function body envelope's json branch). These are documented
	// in CHECKLIST.md; expanding the allow-list requires explicit
	// review and the matching field-level description.
	if typ, _ := m["type"].(string); typ == "object" {
		ap, present := m["additionalProperties"]
		if !present {
			if !isOpenObjectAllowed(path) {
				t.Errorf("%s: object missing additionalProperties:false", path)
			}
		} else {
			ok := false
			switch v := ap.(type) {
			case bool:
				if !v {
					ok = true
				} else if isOpenObjectAllowed(path) {
					ok = true
				}
			case map[string]any:
				// SDK encodes "false" as {"not":{}} — match that form.
				if not, has := v["not"]; has {
					if nm, isMap := not.(map[string]any); isMap && len(nm) == 0 {
						ok = true
					}
				}
				// Or a typed value schema for keyed maps (e.g. headers map[string]string)
				if _, has := v["type"]; has {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s: additionalProperties is not 'false' (got %v)", path, ap)
			}
		}
	}

	// Recurse into properties, items, anyOf, $defs, additionalProperties (when it's a schema).
	if props, ok := m["properties"].(map[string]any); ok {
		for k, v := range props {
			walkSchema(t, path+".properties."+k, v)
		}
	}
	if items, ok := m["items"]; ok {
		walkSchema(t, path+".items", items)
	}
	if anyOf, ok := m["anyOf"].([]any); ok {
		for i, v := range anyOf {
			walkSchema(t, path+".anyOf["+itoa(i)+"]", v)
		}
	}
	if defs, ok := m["$defs"].(map[string]any); ok {
		for k, v := range defs {
			walkSchema(t, path+".$defs."+k, v)
		}
	}
	if ap, ok := m["additionalProperties"].(map[string]any); ok {
		walkSchema(t, path+".additionalProperties", ap)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// isOpenObjectAllowed marks the schema paths where free-form user JSON
// is the documented contract. Adding to this list is deliberate, not a
// dodge — the field's per-property description must explain to the
// agent what shape of object to construct.
func isOpenObjectAllowed(path string) bool {
	allowed := []string{
		// invoke_function: body envelope's json branch is intentionally
		// free-form (the function decides its own shape).
		".body.properties.json",
		// cron + jobs: payload is operator-supplied JSON, passed
		// through to the function unchanged.
		".payload",
		".items.properties.payload",
		// kv_get / kv_put / kv_list: value object is whatever the
		// caller chose to store.
		".value.properties.object",
		".items.properties.value.properties.object",
		".entry.properties.value.properties.object",
	}
	for _, suffix := range allowed {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// allPerms returns the full permission set so every gated tool registers
// during the portability test (otherwise read-only tools would be the
// only ones inspected).
func allPerms() permSet {
	return permSet{
		permRead:   true,
		permWrite:  true,
		permInvoke: true,
		permAdmin:  true,
	}
}
