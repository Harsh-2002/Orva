package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedRollbackMCPAndAgentStrictAndNoFallback(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	key := "orva_fixture_only"
	hash := sha256.Sum256([]byte(key))
	if err := db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: "fixture", Prefix: "fixture", KeyHash: hex.EncodeToString(hash[:]), Permissions: `["write"]`}); err != nil {
		t.Fatal(err)
	}
	deps := Deps{DB: db, RequireDeployIAM: true}
	agent := BuildAgentRegistry(deps, permSet{permWrite: true})
	mcp := NewHandler(deps)
	owner, target := ids.New(), ids.New()
	idsJSON := `{"function_id":"` + owner + `","deployment_id":"` + target + `"`
	for _, tc := range []struct {
		args string
		want error
	}{
		{idsJSON + `,"confirm":true}`, builder.ErrPublicationUnavailable},
		{idsJSON + `}`, builder.ErrRollbackInput},
		{idsJSON + `,"confirm":false}`, builder.ErrRollbackInput},
		{idsJSON + `,"confirm":true,"Confirm":true}`, builder.ErrRollbackInput},
		{idsJSON + `,"confirm":true,"confirm":true}`, builder.ErrRollbackInput},
		{idsJSON + `,"confirm":true,"code_hash":"abc"}`, builder.ErrRollbackInput},
		{`{"function_id":"` + owner + `","deployment_id":"latest","confirm":true}`, builder.ErrRollbackInput},
	} {
		if _, err := agent.Dispatch(t.Context(), "rollback_function", json.RawMessage(tc.args)); !errors.Is(err, tc.want) {
			t.Fatalf("shared AI result: %v; want %v", err, tc.want)
		}
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rollback_function","arguments":` + tc.args + `}}`
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		w := httptest.NewRecorder()
		mcp.ServeHTTP(w, req)
		wantText := "Invalid IAM arguments"
		if tc.want == builder.ErrPublicationUnavailable {
			wantText = builder.ErrPublicationUnavailable.Error()
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), wantText) || !strings.Contains(w.Body.String(), `"isError":true`) {
			t.Fatalf("external MCP expected tool error %q: %d %s", wantText, w.Code, w.Body.String())
		}
	}
}

func TestPublicationAgentRawAndRequiredUnavailable(t *testing.T) {
	agent := BuildAgentRegistry(Deps{RequireDeployIAM: true}, permSet{permWrite: true})
	for _, raw := range []string{
		`{"function_id":"id","code":"x","wait":false,"epoch":"claim"}`,
		`{"function_id":"id","code":"x","wait":false,"wait":true}`,
		`{"function_id":"id","code":"\ud800","wait":false}`,
	} {
		if _, err := agent.Dispatch(t.Context(), "deploy_function_inline", json.RawMessage(raw)); err == nil || errors.Is(err, builder.ErrPublicationUnavailable) {
			t.Fatalf("raw metadata/ambiguity was not rejected before service: %v", err)
		}
	}
	_, err := agent.Dispatch(t.Context(), "deploy_function_inline", json.RawMessage(`{"function_id":"id","code":"x","wait":false}`))
	if !errors.Is(err, builder.ErrPublicationUnavailable) {
		t.Fatalf("required nil mode fell back: %v", err)
	}
}
