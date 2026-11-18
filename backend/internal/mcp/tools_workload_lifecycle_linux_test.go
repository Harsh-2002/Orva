//go:build linux

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestWorkloadLifecycleMCPAndAgent(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	key := func(label, perms string) string {
		t.Helper()
		token := "orva_fixture_" + label
		hash := sha256.Sum256([]byte(token))
		if err := db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: label, Prefix: label,
			KeyHash: hex.EncodeToString(hash[:]), Permissions: perms}); err != nil {
			t.Fatal(err)
		}
		return token
	}
	admin := key("admin", `["admin"]`)
	writer := key("writer", `["write"]`)
	reader := key("reader", `["read"]`)
	deps := Deps{DB: db, WorkloadLifecycle: &builder.PublicationQueue{}}
	handler, defaultHandler := NewHandler(deps), NewHandler(Deps{DB: db})
	send := func(h http.Handler, token, method, params string) string {
		t.Helper()
		raw := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		body := w.Body.String()
		if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
			_, body, _ = strings.Cut(body, "\ndata: ")
			body, _, _ = strings.Cut(body, "\n")
		}
		if w.Code != 200 || !json.Valid([]byte(body)) {
			t.Fatalf("external MCP invalid response: status=%d content type=%q", w.Code, w.Header().Get("Content-Type"))
		}
		return body
	}
	var cases = []struct {
		perms         permSet
		key           string
		pause, resume bool
	}{
		{permSet{permAdmin: true}, admin, true, true},
		{permSet{permWrite: true}, writer, true, false},
		{permSet{permRead: true}, reader, false, false},
	}
	valid := `{"function_id":"` + ids.New() + `","generation":"` + ids.New() + `","policy_revision":"9007199254740993","authority_epoch":"2","confirm":true}`
	for _, tc := range cases {
		catalog := send(handler, tc.key, "tools/list", `{}`)
		agent := BuildAgentRegistry(deps, tc.perms)
		for name, visible := range map[string]bool{"pause_workload": tc.pause, "resume_workload": tc.resume} {
			tool := agent.Get(name)
			if (tool != nil) != visible || strings.Contains(catalog, `"name":"`+name+`"`) != visible {
				t.Fatalf("unexpected visibility for %s", name)
			}
			if !visible {
				continue
			}
			if !tool.Destructive || tool.ReadOnly || !strings.Contains(string(tool.Schema), `"const":true`) {
				t.Fatalf("missing explicit confirmation or destructive annotation: %+v", tool)
			}
			if name == "resume_workload" && (!strings.Contains(tool.Description, "inactive") ||
				!strings.Contains(tool.Description, "Does not resume execution or standing work")) {
				t.Fatalf("missed resume limits: %s", tool.Description)
			}
			for _, args := range []string{valid, strings.Replace(valid, `,"confirm":true`, "", 1),
				strings.Replace(valid, `true`, `false`, 1), strings.Replace(valid, `true`, `false,"confirm":true`, 1),
				strings.Replace(valid, `true`, `true,"actor":"admin"`, 1),
				strings.Replace(valid, `"9007199254740993"`, `9007199254740993`, 1)} {
				want, message := builder.ErrLifecycleInput, "Invalid IAM arguments"
				if args == valid {
					want, message = builder.ErrPublicationUnavailable, builder.ErrPublicationUnavailable.Error()
				}
				if _, err := agent.Dispatch(t.Context(), name, json.RawMessage(args)); !errors.Is(err, want) {
					t.Fatalf("shared AI %s: %v; want %v", name, err, want)
				}
				if body := send(handler, tc.key, "tools/call", `{"name":"`+name+`","arguments":`+args+`}`); !strings.Contains(body, `"isError":true`) || !strings.Contains(body, message) {
					t.Fatalf("external MCP expected %q: %s", message, body)
				}
			}
		}
	}
	catalog := send(defaultHandler, admin, "tools/list", `{}`)
	agent := BuildAgentRegistry(Deps{}, permSet{permAdmin: true, permWrite: true})
	for _, name := range []string{"pause_workload", "resume_workload"} {
		if agent.Get(name) != nil || strings.Contains(catalog, `"name":"`+name+`"`) {
			t.Fatalf("default catalog exposed %s", name)
		}
	}
}
