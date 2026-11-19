//go:build linux

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedFixtureTools(t *testing.T) {
	db := newStatelessDatabase(t)
	owner, reader, writer := ids.New(), ids.New(), ids.New()
	if err := db.InsertFunction(&database.Function{
		ID: owner, Name: "managed-fixtures", Runtime: "node", Status: "inactive", Entrypoint: "handler.js",
	}); err != nil {
		t.Fatal(err)
	}
	const (
		readKey  = "orva_managed_fixtures_read_test"
		writeKey = "orva_managed_fixtures_write_test"
	)
	seedStatelessAPIKey(t, db, readKey, reader, "fixture-reader", `["read"]`)
	seedStatelessAPIKey(t, db, writeKey, writer, "fixture-writer", `["write"]`)
	deps := Deps{ManagedServer: true, DB: db, Registry: registry.New(db)}
	handler := NewHandler(deps)
	call := func(key, tool, args string) *httptest.ResponseRecorder {
		t.Helper()
		return serveModernRPC(t, handler, key, "", "tools/call", map[string]any{
			"_meta": newProtocolMeta(true), "name": tool, "arguments": json.RawMessage(args),
		})
	}
	list := `{"function_id":"` + owner + `"}`
	remove := `{"function_id":"` + owner + `","name":"preset"}`
	save := `{"function_id":"managed-fixtures","name":" preset ","method":" post ","path":"hello","headers":{"X-Test":"yes"},"body":"{\"hello\":1}"}`
	saved := decodeStructuredToolResult[SaveFixtureOutput](t, call(writeKey, "save_fixture", save)).Fixture
	if saved.ID == "" || saved.FunctionID != owner || saved.Name != "preset" || saved.Method != "POST" ||
		saved.Path != "/hello" || saved.Headers["X-Test"] != "yes" || saved.Body != `{"hello":1}` {
		t.Fatalf("unexpected saved fixture: %+v", saved)
	}

	perms := permSet{permRead: true, permWrite: true}
	chat := BuildAgentRegistry(deps, perms)
	writerCtx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: writer})
	readerCtx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: reader})
	updated := `{"function_id":"managed-fixtures","name":"preset","body":"updated"}`
	out, err := chat.Dispatch(writerCtx, "save_fixture", json.RawMessage(updated))
	if got, ok := out.(SaveFixtureOutput); err != nil || !ok || got.Fixture.ID != saved.ID || got.Fixture.Body != "updated" {
		t.Fatalf("Chat upsert: %+v, %v", out, err)
	}
	for _, tc := range []struct {
		tool, args string
		wrongTier  context.Context
	}{
		{"list_fixtures", list, writerCtx},
		{"save_fixture", save, readerCtx},
		{"delete_fixture", remove, readerCtx},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			for _, c := range []struct {
				name string
				ctx  context.Context
			}{{"missing reference", t.Context()}, {"wrong tier", tc.wrongTier}} {
				if _, err := chat.Dispatch(c.ctx, tc.tool, json.RawMessage(tc.args)); !errors.Is(err, database.ErrOperatorDenied) {
					t.Errorf("%s: got %v, want operator denial", c.name, err)
				}
			}
		})
	}
	if err := db.DeleteAPIKey(writer); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Dispatch(writerCtx, "delete_fixture", json.RawMessage(remove)); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatalf("Chat delete with revoked credential: got %v, want operator denial", err)
	}
	rows := decodeStructuredToolResult[ListFixturesOutput](t, call(readKey, "list_fixtures", list)).Fixtures
	if len(rows) != 1 || rows[0].ID != saved.ID || rows[0].Body != "updated" {
		t.Fatalf("MCP list after denied writes: %+v", rows)
	}

	legacyDeps := deps
	legacyDeps.ManagedServer = false
	legacy := BuildAgentRegistry(legacyDeps, perms)
	if _, err := legacy.Dispatch(t.Context(), "list_fixtures", json.RawMessage(list)); err != nil {
		t.Fatalf("legacy list unexpectedly required a live operator reference: %v", err)
	}

	const deleteKey = "orva_managed_fixtures_delete_test"
	seedStatelessAPIKey(t, db, deleteKey, ids.New(), "fixture-deleter", `["write"]`)
	for range 2 {
		deleted := decodeStructuredToolResult[DeleteFixtureOutput](t, call(deleteKey, "delete_fixture", remove))
		if deleted.Status != "deleted" || deleted.Name != "preset" {
			t.Fatalf("unexpected delete result: %+v", deleted)
		}
	}
	if rows := decodeStructuredToolResult[ListFixturesOutput](t, call(readKey, "list_fixtures", list)).Fixtures; rows == nil || len(rows) != 0 {
		t.Fatalf("MCP list after deletion: %+v, want empty array", rows)
	}
}
