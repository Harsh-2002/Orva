//go:build linux

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedOperatorKV(t *testing.T) {
	db := newStatelessDatabase(t)
	owner, writer, reader := ids.New(), ids.New(), ids.New()
	if err := db.InsertFunction(&database.Function{ID: owner, Name: "stored-kv", Runtime: "node", Status: "inactive", Entrypoint: "handler.js"}); err != nil {
		t.Fatal(err)
	}
	const writerKey = "orva_managed_kv_writer_test"
	seedStatelessAPIKey(t, db, writerKey, writer, "kv-writer", `["write"]`)
	seedStatelessAPIKey(t, db, "orva_managed_kv_reader_test", reader, "kv-reader", `["read"]`)
	deps := Deps{ManagedServer: true, DB: db, Registry: registry.New(db)}
	ctx := t.Context()
	writerCtx := auth.WithOperator(ctx, &auth.Principal{Kind: auth.KindAPIKey, ID: writer})
	readerCtx := auth.WithOperator(ctx, &auth.Principal{Kind: auth.KindAPIKey, ID: reader})
	reg := BuildAgentRegistry(deps, permSet{permRead: true, permWrite: true})
	put := `{"function_id":"stored-kv","key":"counter","value":{"type":"number","number":41}}`
	key := `{"function_id":"` + owner + `","key":"counter"}`

	// Real external MCP request, then a Chat registry read against the same DB.
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		rpcBody(t, 1, "tools/call", map[string]any{
			"_meta": newProtocolMeta(true), "name": "kv_put", "arguments": json.RawMessage(put),
		})))
	req.Header.Set("Authorization", "Bearer "+writerKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(hdrProtocolVersion, protocolVersion20260728)
	req.Header.Set(hdrMethod, "tools/call")
	w := httptest.NewRecorder()
	NewHandler(deps).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("kv_put HTTP status: %d", w.Code)
	}
	result := decodeResult[struct {
		IsError bool        `json:"isError"`
		Output  KVPutOutput `json:"structuredContent"`
	}](t, decodeRPC(t, w))
	if result.IsError || result.Output.Key != "counter" {
		t.Fatalf("kv_put did not succeed: %+v", result)
	}
	out, err := reg.Dispatch(readerCtx, "kv_get", json.RawMessage(key))
	if got, ok := out.(KVGetOutput); err != nil || !ok || !got.Found || got.Entry == nil ||
		got.Entry.FunctionID != owner || got.Entry.Value.Type != "number" || got.Entry.Value.Number != 41 {
		t.Fatalf("kv_get: %+v, %v", out, err)
	}

	for _, tc := range []struct{ tool, args string }{
		{"kv_get", key}, {"kv_list", key},
		{"kv_put", strings.Replace(put, "41", "42", 1)}, {"kv_delete", key}, {"kv_incr", key},
		{"kv_cas", `{"function_id":"stored-kv","key":"counter","expected":{"type":"number","number":41},"new":{"type":"number","number":42}}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			wrongTier := readerCtx
			if reg.Get(tc.tool).Perm == permRead {
				wrongTier = writerCtx
			}
			for _, noAuthority := range []struct {
				name string
				ctx  context.Context
			}{{"missing reference", ctx}, {"wrong tier", wrongTier}} {
				if _, err := reg.Dispatch(noAuthority.ctx, tc.tool, json.RawMessage(tc.args)); !errors.Is(err, database.ErrOperatorDenied) {
					t.Errorf("%s: expected authority denial, got %v", noAuthority.name, err)
				}
			}
		})
	}
	for _, tc := range []struct {
		id, tool string
		ctx      context.Context
	}{{writer, "kv_delete", writerCtx}, {reader, "kv_get", readerCtx}} {
		if err := db.DeleteAPIKey(tc.id); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.Dispatch(tc.ctx, tc.tool, json.RawMessage(key)); !errors.Is(err, database.ErrOperatorDenied) {
			t.Fatalf("%s with revoked credential: %v", tc.tool, err)
		}
	}

	deps.ManagedServer = false
	legacy := BuildAgentRegistry(deps, permSet{permRead: true})
	out, err = legacy.Dispatch(ctx, "kv_get", json.RawMessage(key))
	if got, ok := out.(KVGetOutput); err != nil || !ok || got.Entry == nil || got.Entry.Value.Number != 41 {
		t.Fatalf("legacy read changed or denied managed write touched stored data: %+v, %v", out, err)
	}
}
