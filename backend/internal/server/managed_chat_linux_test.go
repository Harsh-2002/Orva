//go:build linux

package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/ai"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedChatApprovesNamedPolicy(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "chat.db"))
	managedMust(t, err)
	defer func() { managedMust(t, db.Close()) }()
	managedMust(t, db.Migrate())
	_, err = db.ApplyRecoveryForFixture(t.Context(), "")
	managedMust(t, err)
	reg := registry.New(db)
	authority, err := workloadiam.NewAuthority(8, nil)
	managedMust(t, err)
	policy, err := workloadpolicy.NewManaged(db, reg, authority, nil)
	managedMust(t, err)
	const fixtureKey = "orva_scratch_chat_operator"
	key := sha256.Sum256([]byte(fixtureKey))
	managedMust(t, db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: "operator", Prefix: "fixture",
		KeyHash: fmt.Sprintf("%x", key), Permissions: `["read","admin"]`}))
	provider, guard := managedChatProvider(t)
	manager := ai.New(db, secrets.New(db, t.TempDir()), mcp.Deps{ManagedServer: true, DB: db, Registry: reg, IAM: policy})
	defer manager.Close()
	manager.SetEgressGuard(guard)
	configureManagedChat(t, manager, provider)
	mux := http.NewServeMux()
	registerManagedChat(mux, &AIHandler{Manager: manager, DB: db})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	call := func(path, body string) string {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("X-Orva-API-Key", fixtureKey)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
			t.Fatalf("Chat response: %d %s", w.Code, w.Body)
		}
		return w.Body.String()
	}
	sse := call("/api/v1/ai/chat", `{"content":"Create an empty named policy"}`)
	var id string
	for _, frame := range strings.Split(sse, "\n\n") {
		if !strings.HasPrefix(frame, "event: tool_call\n") {
			continue
		}
		for _, line := range strings.Split(frame, "\n") {
			if payload, ok := strings.CutPrefix(line, "data: "); ok {
				var row struct {
					ID      string
					Pending bool `json:"requires_approval"`
				}
				managedMust(t, json.Unmarshal([]byte(payload), &row))
				if row.Pending {
					id = row.ID
				}
			}
		}
	}
	before, err := db.ListIAMManagedPolicies(ctx, 0)
	managedMust(t, err)
	if id == "" || len(before) != 0 {
		t.Fatalf("missing approval or policy was already committed: %s", sse)
	}
	result := call("/api/v1/ai/tool-calls/"+id+"/approve", `{}`)
	after, err := db.ListIAMManagedPolicies(ctx, 0)
	managedMust(t, err)
	if len(after) != 1 || after[0].Name != "from-chat" {
		t.Fatalf("explicit Chat approval did not commit named policy: %s", result)
	}
}

func managedChatProvider(t *testing.T) (string, firewall.Guard) {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"managed-fixture","object":"model","owned_by":"fixture"}]}`)
			return
		}
		var in struct {
			Messages []struct{ Role string }
			Tools    []struct{ Function struct{ Name string } }
		}
		if r.URL.Path != "/v1/chat/completions" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil || len(in.Messages) == 0 {
			http.Error(w, "unexpected provider request", 400)
			return
		}
		var managed bool
		for _, tool := range in.Tools {
			managed = managed || tool.Function.Name == "create_iam_policy"
			if tool.Function.Name == "update_function" {
				t.Error("managed Chat exposed legacy update")
			}
		}
		if !managed {
			t.Error("managed Chat did not advertise named policy creation")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta any, finish any) {
			b, err := json.Marshal(map[string]any{
				"id": "managed-chat-fixture", "object": "chat.completion.chunk", "created": 1, "model": "managed-fixture",
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			})
			if err != nil {
				t.Error(err)
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		if in.Messages[len(in.Messages)-1].Role == "tool" {
			chunk(map[string]any{"role": "assistant", "content": "Named policy created."}, nil)
			chunk(map[string]any{}, "stop")
		} else {
			const args = `{"request":{"name":"from-chat","policy":{"schema_version":1,"allow":[],"inbound":[]}}}`
			chunk(map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"index": 0, "id": "managed_policy_call", "type": "function", "function": map[string]any{
					"name": "create_iam_policy", "arguments": args,
				}},
			}}, nil)
			chunk(map[string]any{}, "tool_calls")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(provider.Close)
	addr, err := netip.ParseAddrPort(strings.TrimPrefix(provider.URL, "http://"))
	managedMust(t, err)
	return provider.URL + "/v1", firewall.GuardFunc(func(a netip.Addr, port uint16) bool {
		return a != addr.Addr() || port != addr.Port()
	})
}

func configureManagedChat(t *testing.T, manager *ai.Manager, baseURL string) {
	t.Helper()
	_, err := manager.SaveProvider(ai.ProviderInput{Provider: "openai", APIKey: "synthetic-loopback-only", BaseURL: baseURL})
	managedMust(t, err)
	_, err = manager.SaveSettings(database.AISettings{Provider: "openai", Model: "managed-fixture", ThinkingLevel: "off", ApprovalPolicy: "all_writes"})
	managedMust(t, err)
}
