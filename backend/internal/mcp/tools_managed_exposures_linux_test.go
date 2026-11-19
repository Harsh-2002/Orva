//go:build linux

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadtrigger"
	"github.com/Harsh-2002/Orva/internal/ids"
)

type managedExposureFixture struct {
	*iamSurfaceFixture
	deps  Deps
	owner string
}

func newManagedExposureFixture(t *testing.T) *managedExposureFixture {
	t.Helper()
	f := iamSurfaceSetup(t)
	tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../database", "workload_exposure_schema.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(tree, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			if strings.Contains(s, "CREATE TABLE IF NOT EXISTS") {
				if _, err := f.sql.Exec(s); err != nil {
					t.Fatal(err)
				}
			}
		}
		return true
	})
	svc, err := workloadtrigger.New(f.db, f.registry, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	return &managedExposureFixture{iamSurfaceFixture: f, owner: f.owners[0],
		deps: Deps{DB: f.db, Registry: f.registry, ManagedServer: true, IAM: f.service, Triggers: svc}}
}

func (f *managedExposureFixture) key(t *testing.T, perms string) (string, context.Context) {
	t.Helper()
	id := ids.New()
	sum := sha256.Sum256([]byte("orva_" + id))
	if err := f.db.InsertAPIKey(&database.APIKey{ID: id, Name: "exposure-" + id[:8], KeyHash: hex.EncodeToString(sum[:]), Permissions: perms}); err != nil {
		t.Fatal(err)
	}
	return id, auth.WithOperator(context.Background(), &auth.Principal{Kind: auth.KindAPIKey, ID: id})
}

func (f *managedExposureFixture) call(ctx context.Context, perms permSet, name string, args any) (any, error) {
	body, _ := json.Marshal(args)
	return BuildAgentRegistry(f.deps, perms).Dispatch(ctx, name, body)
}

func (f *managedExposureFixture) expected() map[string]any {
	return map[string]any{f.owner: map[string]string{"policy_revision": "1", "authority_epoch": "1"}}
}

func TestManagedExposureCatalogGating(t *testing.T) {
	f := newManagedExposureFixture(t)
	names := func(p permSet) []string {
		var out []string
		for _, tool := range BuildAgentRegistry(f.deps, p).Tools() {
			out = append(out, tool.Name)
		}
		return out
	}
	read := names(permSet{permRead: true})
	for _, n := range []string{"list_routes", "list_inbound_webhooks", "inspect_exposure"} {
		if !slices.Contains(read, n) {
			t.Fatalf("read catalog lacks %s", n)
		}
	}
	for _, n := range []string{"set_route", "delete_route", "create_inbound_webhook", "update_inbound_webhook", "delete_inbound_webhook"} {
		if slices.Contains(read, n) {
			t.Fatalf("read catalog exposes mutation %s", n)
		}
	}
	write := names(permSet{permWrite: true})
	for _, n := range []string{"set_route", "delete_route", "create_inbound_webhook", "update_inbound_webhook", "delete_inbound_webhook"} {
		if !slices.Contains(write, n) {
			t.Fatalf("write catalog lacks %s", n)
		}
	}
	if slices.Contains(write, "list_routes") || slices.Contains(write, "inspect_exposure") {
		t.Fatal("write alone acquired READ")
	}
	// Legacy projections stay available only without the managed flag.
	d := f.deps
	d.ManagedServer = false
	if r := BuildAgentRegistry(d, permSet{permRead: true}); r.Get("list_inbound_webhooks") == nil {
		t.Fatal("legacy catalog lost list_inbound_webhooks")
	}
}

// Chat pauses for approval on any mutating tool under all_writes (the default)
// and on the delete tools under destructive_only: agent.Runner.approvalNeeded
// keys on exactly these registry facts.
func TestManagedExposureToolsCarryApprovalFacts(t *testing.T) {
	f := newManagedExposureFixture(t)
	reg := BuildAgentRegistry(f.deps, permSet{permRead: true, permWrite: true, permAdmin: true})
	for name, destructive := range map[string]bool{
		"set_route": false, "delete_route": true, "create_inbound_webhook": false,
		"update_inbound_webhook": false, "delete_inbound_webhook": true,
	} {
		tool := reg.Get(name)
		if tool == nil {
			t.Fatal("missing", name)
		}
		if tool.ReadOnly || tool.Perm == "read" || tool.Perm == "invoke" {
			t.Fatalf("%s would bypass approval: readonly=%v perm=%s", name, tool.ReadOnly, tool.Perm)
		}
		if tool.Destructive != destructive {
			t.Fatalf("%s destructive=%v want %v", name, tool.Destructive, destructive)
		}
	}
	for _, name := range []string{"list_routes", "list_inbound_webhooks", "inspect_exposure"} {
		if tool := reg.Get(name); tool == nil || !tool.ReadOnly || tool.Perm != "read" {
			t.Fatalf("%s must be a read-only read tool", name)
		}
	}
}

func TestManagedExposureAuthorityAndCAS(t *testing.T) {
	f := newManagedExposureFixture(t)
	all := permSet{permRead: true, permWrite: true, permAdmin: true}
	_, readCtx := f.key(t, `["read"]`)
	_, writeCtx := f.key(t, `["read","write"]`)
	revokedID, revokedCtx := f.key(t, `["read","write"]`)

	route := map[string]any{"path": "/exposure-tool", "function_id": f.owner, "version": "0", "expected": f.expected()}

	// Live authority: a registry advertising write cannot mutate on a read-only key.
	if _, err := f.call(readCtx, all, "set_route", route); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatalf("read-only key mutated or wrong error: %v", err)
	}
	// No operator in context at all.
	if _, err := f.call(context.Background(), all, "list_routes", map[string]any{}); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatalf("missing operator accepted: %v", err)
	}
	// Non-canonical identifiers are rejected as validation errors, never adopted.
	bad := map[string]any{"path": "/exposure-tool", "function_id": strings.ToUpper(f.owner), "version": "0", "expected": f.expected()}
	if _, err := f.call(writeCtx, all, "set_route", bad); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("uppercase UUID owner: %v", err)
	}
	badExpected := map[string]any{"path": "/exposure-tool", "function_id": f.owner, "version": "0",
		"expected": map[string]any{strings.ToUpper(f.owner): map[string]string{"policy_revision": "1", "authority_epoch": "1"}}}
	if _, err := f.call(writeCtx, all, "set_route", badExpected); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("uppercase expected key: %v", err)
	}
	numeric := map[string]any{"path": "/exposure-tool", "function_id": f.owner, "version": 0, "expected": f.expected()}
	if _, err := f.call(writeCtx, all, "set_route", numeric); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("numeric version accepted: %v", err)
	}
	if _, err := f.call(writeCtx, all, "list_inbound_webhooks", map[string]any{"function_id": "surface-node"}); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("function name resolved in managed list: %v", err)
	}
	// Stale CAS is a conflict.
	stale := map[string]any{"path": "/exposure-tool", "function_id": f.owner, "version": "7", "expected": f.expected()}
	if _, err := f.call(writeCtx, all, "set_route", stale); !errors.Is(err, database.ErrExposureConflict) {
		t.Fatalf("stale version: %v", err)
	}
	staleEpoch := map[string]any{"path": "/exposure-tool", "function_id": f.owner, "version": "0",
		"expected": map[string]any{f.owner: map[string]string{"policy_revision": "1", "authority_epoch": "9"}}}
	if _, err := f.call(writeCtx, all, "set_route", staleEpoch); !errors.Is(err, database.ErrExposureConflict) {
		t.Fatalf("stale epoch: %v", err)
	}

	// A truly empty owner accepts WRITE; reads need READ.
	if _, err := f.call(writeCtx, all, "set_route", route); err != nil {
		t.Fatalf("write on empty owner: %v", err)
	}
	if _, err := f.call(writeCtx, all, "set_route", route); !errors.Is(err, database.ErrExposureConflict) {
		t.Fatalf("replayed create did not conflict: %v", err)
	}
	out, err := f.call(readCtx, all, "list_routes", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	list := out.(ManagedListRoutesOutput)
	if len(list.Routes) != 1 || list.Routes[0].Path != "/exposure-tool" || list.Routes[0].Version != "1" {
		t.Fatalf("unexpected route list %+v", list)
	}
	got, err := f.call(readCtx, all, "inspect_exposure", map[string]any{"kind": "route", "key": "/exposure-tool"})
	if err != nil || got.(map[string]any)["version"] != "1" {
		t.Fatalf("inspect: %v %v", got, err)
	}

	// Delete requires confirm:true and the current version.
	del := map[string]any{"path": "/exposure-tool", "version": "1", "expected": f.expected()}
	if _, err := f.call(writeCtx, all, "delete_route", del); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("delete without confirm: %v", err)
	}
	del["confirm"] = false
	if _, err := f.call(writeCtx, all, "delete_route", del); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("delete confirm=false: %v", err)
	}
	del["confirm"], del["version"] = true, "0"
	if _, err := f.call(writeCtx, all, "delete_route", del); !errors.Is(err, database.ErrExposureConflict) {
		t.Fatalf("delete stale version: %v", err)
	}
	del["version"] = "1"
	if _, err := f.call(writeCtx, all, "delete_route", del); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// A revoked key loses authority immediately.
	if err := f.db.DeleteAPIKey(revokedID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(revokedCtx, all, "list_routes", map[string]any{}); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatalf("revoked key read: %v", err)
	}
	if _, err := f.call(revokedCtx, all, "set_route", map[string]any{"path": "/revoked", "function_id": f.owner, "version": "0", "expected": f.expected()}); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatalf("revoked key mutation: %v", err)
	}
}

func TestManagedInboundWebhookSecretOnlyOnCreate(t *testing.T) {
	f := newManagedExposureFixture(t)
	all := permSet{permRead: true, permWrite: true, permAdmin: true}
	_, ctx := f.key(t, `["read","write","admin"]`)

	created, err := f.call(ctx, all, "create_inbound_webhook", map[string]any{"function_id": f.owner, "name": "signed", "version": "0", "expected": f.expected()})
	if err != nil {
		t.Fatal(err)
	}
	c := created.(CreateInboundWebhookOutput)
	if len(c.Secret) != 64 {
		t.Fatalf("create did not return the secret once: %q", c.Secret)
	}
	id := c.InboundWebhook.ID

	outputs := map[string]any{}
	listed, err := f.call(ctx, all, "list_inbound_webhooks", map[string]any{"function_id": f.owner})
	if err != nil {
		t.Fatal(err)
	}
	outputs["list"] = listed
	if l := listed.(ManagedListInboundOutput); len(l.InboundWebhooks) != 1 || l.InboundWebhooks[0].Version != "1" ||
		l.Expected[f.owner]["policy_revision"] == "" {
		t.Fatalf("unexpected list %+v", l)
	}
	outputs["inspect"], err = f.call(ctx, all, "inspect_exposure", map[string]any{"kind": "inbound", "key": id, "function_id": f.owner})
	if err != nil {
		t.Fatal(err)
	}
	outputs["update"], err = f.call(ctx, all, "update_inbound_webhook", map[string]any{"function_id": f.owner, "id": id, "active": false, "version": "1", "expected": f.expected()})
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range outputs {
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), c.Secret) || strings.Contains(string(raw), `"secret"`) {
			t.Fatalf("%s output leaked the signing secret: %s", name, raw)
		}
	}

	del := map[string]any{"function_id": f.owner, "id": id, "version": "2", "expected": f.expected()}
	if _, err := f.call(ctx, all, "delete_inbound_webhook", del); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("delete without confirm: %v", err)
	}
	del["confirm"] = true
	if _, err := f.call(ctx, all, "delete_inbound_webhook", del); err != nil {
		t.Fatalf("confirmed delete: %v", err)
	}
	// A canonical-but-wrong trigger id is not adopted by name or prefix.
	del["id"], del["version"] = strings.ToUpper(id), "3"
	if _, err := f.call(ctx, all, "delete_inbound_webhook", del); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("uppercase trigger id: %v", err)
	}
}
