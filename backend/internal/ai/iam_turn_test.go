package ai

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const turnReaderPolicy = `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"prefix":"config/"}},"context":"execution"}],"inbound":[]}`

type turnScript struct{ name, args string }

// Only the provider is scripted: the gateway, turn store, approval and tools are real.
type turnProvider struct {
	mu       sync.Mutex
	scripts  map[string]turnScript
	requests int
	results  int
	catalogs [][]string
}

func (p *turnProvider) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
		t.Errorf("unexpected loopback provider route: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected route", 400)
		return
	}
	var req struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
		t.Errorf("decode provider request: %v", err)
		http.Error(w, "bad request", 400)
		return
	}
	if !req.Stream || len(req.Messages) == 0 {
		t.Error("expected a real streaming chat request")
		http.Error(w, "bad request", 400)
		return
	}
	var label string
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			label = msg.Content
		}
	}
	p.mu.Lock()
	script, ok := p.scripts[label]
	p.requests++
	var names []string
	for _, tool := range req.Tools {
		names = append(names, tool.Function.Name)
	}
	p.catalogs = append(p.catalogs, names)
	last := req.Messages[len(req.Messages)-1]
	if last.Role == "tool" {
		p.results++
	}
	p.mu.Unlock()
	if !ok {
		t.Error("unexpected deterministic turn label")
		http.Error(w, "unknown script", 400)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta any, finish any) {
		b, err := json.Marshal(map[string]any{
			"id": "loopback-completion", "object": "chat.completion.chunk",
			"created": 1, "model": "deterministic-iam",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
	if last.Role == "tool" {
		if last.ToolCallID != "call_iam" || !json.Valid([]byte(last.Content)) {
			t.Error("agent did not replay a correlated JSON tool result")
		}
		chunk(map[string]any{"role": "assistant", "content": "Deterministic turn complete."}, nil)
		chunk(map[string]any{}, "stop")
	} else {
		// Fragment the original string so duplicate keys cannot be normalized away.
		cut := len(script.args) / 2
		chunk(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
			"index": 0, "id": "call_iam", "type": "function",
			"function": map[string]any{"name": script.name, "arguments": script.args[:cut]},
		}}}, nil)
		chunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "function": map[string]any{"arguments": script.args[cut:]},
		}}}, nil)
		chunk(map[string]any{}, "tool_calls")
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

type turnSink struct{ events []string }

func (s *turnSink) Send(event string, data any) error {
	if _, err := json.Marshal(data); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

func (s *turnSink) has(event string) bool {
	for _, got := range s.events {
		if got == event {
			return true
		}
	}
	return false
}

type iamTurnFixture struct {
	db       *database.Database
	sql      *sql.DB
	manager  *Manager
	provider *turnProvider
	gate     *workloadiam.Authority
	ctx      context.Context
	actor    Principal
	owner    string
	artifact string
}

func newIAMTurnFixture(t *testing.T) *iamTurnFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "turn.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	stage := func(name, marker string) {
		tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../database", name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var schema string
		ast.Inspect(tree, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				value, _ := strconv.Unquote(lit.Value)
				if strings.Contains(value, marker) {
					schema = value
				}
			}
			return true
		})
		if schema == "" {
			t.Fatal("canonical staged schema absent", name)
		}
		if _, err := conn.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InitIAMSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	f := &iamTurnFixture{
		db: db, sql: conn, owner: ids.New(), artifact: strings.Repeat("a", 64),
		actor:    Principal{ID: ids.New(), Label: "synthetic-turn-operator", Perms: auth.PermSet{"read": true, "admin": true}},
		provider: &turnProvider{scripts: map[string]turnScript{}},
	}
	if err := db.InsertAPIKey(&database.APIKey{ID: f.actor.ID, Name: "synthetic-turn-operator", KeyHash: "not-a-bearer", Prefix: "dummy", Permissions: `["read","admin"]`}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertFunction(&database.Function{ID: f.owner, Name: "turn-owner", Runtime: "node", Status: "active", Entrypoint: "handler.js"}); err != nil {
		t.Fatal(err)
	}
	row, err := db.NewIAMPolicy(t.Context(), f.owner, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := row.AdmissionEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	stage("workload_managed_schema.go", "CREATE TABLE IF NOT EXISTS workload_iam_inline")
	f.gate, err = workloadiam.NewAuthority(8, []*workloadiam.Evaluator{evaluator})
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New(db)
	service, err := workloadpolicy.NewManaged(db, reg, f.gate, func(id string) workloadpolicy.ArtifactState {
		return workloadpolicy.ArtifactState{OwnerID: f.owner, Artifact: f.artifact, Known: id == f.owner}
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.provider.serve(t, w, r) }))
	t.Cleanup(server.Close)
	f.manager = New(db, secrets.New(db, dir), mcp.Deps{DB: db, Registry: reg, IAM: service})
	t.Cleanup(f.manager.Close)
	// account.GetConfigForProvider enables private networking only for this explicit loopback base URL.
	if _, err := f.manager.SaveProvider(ProviderInput{Provider: "openai", APIKey: "synthetic-loopback-only", BaseURL: server.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.SaveSettings(database.AISettings{
		Provider: "openai", Model: "deterministic-iam", ThinkingLevel: "off", ApprovalPolicy: "all_writes",
	}); err != nil {
		t.Fatal(err)
	}
	f.ctx = auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: f.actor.ID})
	return f
}

func (f *iamTurnFixture) start(t *testing.T, label, name, args string) (string, *database.AIToolCall, *turnSink) {
	t.Helper()
	f.provider.mu.Lock()
	f.provider.scripts[label] = turnScript{name, args}
	f.provider.mu.Unlock()
	sink := &turnSink{}
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	conv, err := f.manager.Chat(ctx, sink, f.actor, ChatParams{Content: label})
	if err != nil {
		t.Fatal("actual Manager.Chat", err)
	}
	calls, err := f.db.ListToolCalls(conv)
	if err != nil || len(calls) != 1 {
		t.Fatalf("tool rows: count=%d err=%v", len(calls), err)
	}
	if calls[0].ToolName != name || calls[0].Args != args || calls[0].CallID != "call_iam" {
		t.Fatal("streamed original arguments/name/call ID were not preserved")
	}
	return conv, calls[0], sink
}

func (f *iamTurnFixture) resume(t *testing.T, conv string, call *database.AIToolCall, approved bool, want string) {
	t.Helper()
	sink := &turnSink{}
	ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
	defer cancel()
	if err := f.manager.Resume(ctx, sink, f.actor, conv, call.ID, approved); err != nil {
		t.Fatal("actual Manager.Resume", err)
	}
	stored, err := f.db.GetToolCall(call.ID)
	if err != nil || stored.Status != want || stored.ApprovedBy != f.actor.ID {
		t.Fatalf("terminal tool status=%v err=%v; want %s and real approver", stored, err, want)
	}
	if !sink.has("tool_result") || !sink.has("done") {
		t.Fatalf("resume did not finish the actual agent loop: %v", sink.events)
	}
	messages, err := f.db.ListMessages(conv, 0)
	if err != nil || len(messages) != 4 {
		t.Fatalf("expected user/assistant/tool/assistant history: count=%d err=%v", len(messages), err)
	}
	for i, role := range []string{"user", "assistant", "tool", "assistant"} {
		if messages[i].Role != role || messages[i].Seq != i+1 {
			t.Fatal("message roles or atomic sequence changed")
		}
	}
	if err := f.manager.Resume(ctx, &turnSink{}, f.actor, conv, call.ID, approved); err == nil {
		t.Fatal("second resume must refuse an already resolved call")
	}
	after, err := f.db.ListMessages(conv, 0)
	if err != nil || !reflect.DeepEqual(messages, after) {
		t.Fatal("refused replay changed conversation history")
	}
	t.Logf("turn=%s tool=%s terminal=%s approver_matches=true replay_refused=true events=%v", conv, call.ToolName, want, sink.events)
}

func (f *iamTurnFixture) policies(t *testing.T) []database.IAMManagedPolicy {
	t.Helper()
	items, err := f.db.ListIAMManagedPolicies(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestIAMAgentTurnApprovalResume(t *testing.T) {
	f := newIAMTurnFixture(t)
	conv, call, sink := f.start(t, "create-reader", "create_iam_policy", `{"request":{"name":"turn-reader","policy":`+turnReaderPolicy+`}}`)
	if call.Status != "pending_approval" || !call.RequiresApproval || !sink.has("awaiting_approval") || sink.has("tool_result") || len(f.policies(t)) != 0 {
		t.Fatalf("create ran before approval: status=%s events=%v", call.Status, sink.events)
	}
	t.Log("create pending: named_policy_count=0 tool_result_count=0")
	f.resume(t, conv, call, true, "succeeded")
	items := f.policies(t)
	if len(items) != 1 || items[0].Name != "turn-reader" || items[0].DefinitionRevision != 1 || items[0].MembershipRevision != 1 || items[0].LatestVersion != 1 {
		t.Fatalf("expected exactly one version/definition after approval: %+v", items)
	}
	audit, err := f.db.ListIAMManagedAudit(t.Context(), items[0].ID)
	if err != nil || len(audit) != 1 || audit[0].Operation != "create" || audit[0].Sequence != 1 || audit[0].Actor.Kind != auth.KindAPIKey || audit[0].Actor.ID != f.actor.ID || len(audit[0].Digest) != 64 {
		t.Fatalf("create audit/actor mismatch: %+v err=%v", audit, err)
	}
	version, err := f.db.GetIAMManagedVersion(t.Context(), items[0].ID, 1)
	if err != nil || version.Version != 1 || string(version.Policy) != turnReaderPolicy ||
		version.Actor.Kind != auth.KindAPIKey || version.Actor.ID != f.actor.ID {
		t.Fatal("immutable version does not match the approved policy/actor", err)
	}
	f.provider.mu.Lock()
	requests, results := f.provider.requests, f.provider.results
	f.provider.mu.Unlock()
	if requests != 2 || results != 1 {
		t.Fatalf("expected tool generation plus correlated result completion: requests=%d results=%d", requests, results)
	}
	t.Log("create committed once: definition_revision=1 membership_revision=1 latest_version=1 audit_sequence=1 actor_matches=true provider_requests=2 correlated_tool_results=1")

	policy := items[0]
	before, err := f.db.GetIAMSources(t.Context(), f.owner)
	if err != nil {
		t.Fatal(err)
	}
	beforeState := f.iamState(t)
	conv, call, sink = f.start(t, "attach-reader", "attach_iam_policy", f.attachArgs(t, policy))
	if call.Status != "pending_approval" || !call.RequiresApproval || !sink.has("awaiting_approval") || sink.has("tool_result") {
		t.Fatalf("attach did not pause for approval: status=%s events=%v", call.Status, sink.events)
	}
	f.requireState(t, beforeState)
	if f.allows(t) {
		t.Fatal("pending attachment published authority")
	}
	t.Log("attach pending: attachment_count=0 source/audit/revision_unchanged=true authority=DENY")
	f.resume(t, conv, call, true, "succeeded")
	after, err := f.db.GetIAMSources(t.Context(), f.owner)
	if err != nil || len(after.Attachments) != 1 || after.Attachments[0].PolicyID != policy.ID || after.Attachments[0].Version != 1 || !after.Attachments[0].Enabled {
		t.Fatal("approved turn did not create exactly one pinned attachment", err)
	}
	if after.InlineJSON != before.InlineJSON || after.Record.PolicyJSON != turnReaderPolicy ||
		after.Record.Revision != before.Record.Revision+1 || after.Record.AuthorityEpoch != before.Record.AuthorityEpoch+1 {
		t.Fatal("attachment did not advance exactly one source revision/authority epoch while preserving inline")
	}
	p, err := f.db.GetIAMManagedPolicy(t.Context(), policy.ID)
	if err != nil || p.DefinitionRevision != 1 || p.MembershipRevision != 2 || p.LatestVersion != 1 {
		t.Fatal("attachment advanced incorrect definition/membership counters", err)
	}
	audit, err = f.db.ListIAMManagedAudit(t.Context(), policy.ID)
	if err != nil || len(audit) != 2 || audit[0].Sequence != 2 || audit[0].Operation != "attach" ||
		audit[0].Actor.Kind != auth.KindAPIKey || audit[0].Actor.ID != f.actor.ID {
		t.Fatal("attachment managed audit is not exactly-once or actor-correlated", err)
	}
	ownerAudit, err := f.db.ListIAMPolicyAudit(t.Context(), f.owner)
	approvedState, marshalErr := json.Marshal(after.Record)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	digest := sha256.Sum256(approvedState)
	if err != nil || len(ownerAudit) != 2 || ownerAudit[0].Revision != after.Record.Revision ||
		ownerAudit[0].AuthorityEpoch != after.Record.AuthorityEpoch || ownerAudit[0].Actor.Kind != auth.KindAPIKey ||
		ownerAudit[0].Actor.ID != f.actor.ID || ownerAudit[0].Digest != hex.EncodeToString(digest[:]) || !f.allows(t) {
		t.Fatal("attachment durable audit/approved-state digest/live authority did not correlate", err)
	}
	t.Logf("attach committed once: source_revision=%d authority_epoch=%d membership_revision=2 pin=1 managed_audit_sequence=2 owner_audit_matches=true actor_matches=true approved_state_digest_matches=true authority=ALLOW",
		after.Record.Revision, after.Record.AuthorityEpoch)
}

func (f *iamTurnFixture) attachArgs(t *testing.T, policy database.IAMManagedPolicy) string {
	t.Helper()
	row, err := f.db.GetIAMPolicy(t.Context(), f.owner)
	if err != nil {
		t.Fatal(err)
	}
	var approved any
	if row.ApprovedArtifact != database.IAMUnboundArtifact {
		approved = row.ApprovedArtifact
	}
	args, err := json.Marshal(map[string]any{"id": policy.ID, "request": map[string]any{
		"membership_revision": strconv.FormatUint(policy.MembershipRevision, 10), "version": "1",
		"owners": []any{map[string]any{"function_id": f.owner, "expected": map[string]any{
			"revision": strconv.FormatUint(row.Revision, 10), "current_artifact": f.artifact, "approved_artifact": approved,
		}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(args)
}

func (f *iamTurnFixture) allows(t *testing.T) bool {
	t.Helper()
	key := "config/value"
	return f.gate.Do(workloadiam.Context{
		Principal: workloadiam.Principal{FunctionID: f.owner, WorkerID: "synthetic-worker", ArtifactRevision: f.artifact},
		Phase:     workloadiam.Execution, Ready: true,
		Lease: workloadiam.Lease{ID: "synthetic-lease", WorkerID: "synthetic-worker", ExecutionID: ids.New(), Active: true},
	}, workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: f.owner, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}},
		func() error { return nil }) == nil
}

// Capture only staged IAM records in memory, never credentials or a database dump.
func (f *iamTurnFixture) iamState(t *testing.T) []byte {
	t.Helper()
	src, err := f.db.GetIAMSources(t.Context(), f.owner)
	if err != nil {
		t.Fatal(err)
	}
	ownerAudit, err := f.db.ListIAMPolicyAudit(t.Context(), f.owner)
	if err != nil {
		t.Fatal(err)
	}
	managed := []any{}
	for _, p := range f.policies(t) {
		audit, err := f.db.ListIAMManagedAudit(t.Context(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		versions := []any{}
		for v := uint64(1); v <= p.LatestVersion; v++ {
			version, err := f.db.GetIAMManagedVersion(t.Context(), p.ID, v)
			if err != nil {
				t.Fatal(err)
			}
			versions = append(versions, version)
		}
		managed = append(managed, []any{p, audit, versions})
	}
	raw, err := json.Marshal([]any{src, ownerAudit, managed})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *iamTurnFixture) requireState(t *testing.T, before []byte) {
	t.Helper()
	if string(f.iamState(t)) != string(before) {
		t.Fatal("unexpected durable IAM source/version/audit/counter mutation")
	}
}

func (f *iamTurnFixture) createApproved(t *testing.T) database.IAMManagedPolicy {
	t.Helper()
	conv, call, sink := f.start(t, "seed-reader", "create_iam_policy", `{"request":{"name":"seed-reader","policy":`+turnReaderPolicy+`}}`)
	if call.Status != "pending_approval" || !sink.has("awaiting_approval") || len(f.policies(t)) != 0 {
		t.Fatal("seed creation was not approval gated")
	}
	f.resume(t, conv, call, true, "succeeded")
	items := f.policies(t)
	if len(items) != 1 {
		t.Fatal("expected one seed policy")
	}
	return items[0]
}

func TestIAMAgentTurnRejection(t *testing.T) {
	for _, name := range []string{"create_iam_policy", "attach_iam_policy"} {
		t.Run(name, func(t *testing.T) {
			f := newIAMTurnFixture(t)
			args := `{"request":{"name":"rejected","policy":` + turnReaderPolicy + `}}`
			if name == "attach_iam_policy" {
				args = f.attachArgs(t, f.createApproved(t))
			}
			before := f.iamState(t)
			conv, call, sink := f.start(t, "reject", name, args)
			if call.Status != "pending_approval" || !sink.has("awaiting_approval") {
				t.Fatal("expected actual approval pause")
			}
			f.requireState(t, before)
			f.resume(t, conv, call, false, "rejected")
			f.requireState(t, before)
			if f.allows(t) {
				t.Fatal("rejection published authority")
			}
			t.Log("rejection: source/version/audit/counters_unchanged=true authority=DENY")
		})
	}
}

func TestIAMAgentTurnDestructiveConfirm(t *testing.T) {
	f := newIAMTurnFixture(t)
	p := f.createApproved(t)
	for _, confirm := range []bool{false, true} {
		before := f.iamState(t)
		args := fmt.Sprintf(`{"id":%q,"request":{"definition_revision":"1","membership_revision":"1","confirm":%t}}`, p.ID, confirm)
		conv, call, sink := f.start(t, fmt.Sprintf("delete-confirm-%t", confirm), "delete_iam_policy", args)
		if call.Status != "pending_approval" || !call.RequiresApproval || !call.Destructive || !sink.has("awaiting_approval") {
			t.Fatal("destructive tool bypassed human approval")
		}
		f.requireState(t, before)
		want := "failed"
		if confirm {
			want = "succeeded"
		}
		f.resume(t, conv, call, true, want)
		if !confirm {
			f.requireState(t, before)
			f.requireToolError(t, call, database.ErrIAMInvalidPolicy.Error())
			t.Log("human approval granted; confirm=false independently denied; IAM state unchanged")
		} else if len(f.policies(t)) != 0 {
			t.Fatal("approved confirm=true did not delete the unattached policy")
		} else {
			t.Log("human approval plus confirm=true: unattached policy deleted exactly once")
		}
	}
}

func (f *iamTurnFixture) requireToolError(t *testing.T, call *database.AIToolCall, contains string) {
	t.Helper()
	stored, err := f.db.GetToolCall(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(stored.Result), &result) != nil || !strings.Contains(result.Error, contains) {
		t.Fatalf("expected tool error containing %q", contains)
	}
}

func TestIAMAgentTurnRevokedPendingCredential(t *testing.T) {
	for _, name := range []string{"create_iam_policy", "attach_iam_policy"} {
		t.Run(name, func(t *testing.T) {
			f := newIAMTurnFixture(t)
			args := `{"request":{"name":"must-not-commit","policy":` + turnReaderPolicy + `}}`
			if name == "attach_iam_policy" {
				args = f.attachArgs(t, f.createApproved(t))
			}
			before := f.iamState(t)
			conv, call, sink := f.start(t, "revoke-pending-credential", name, args)
			if call.Status != "pending_approval" || !sink.has("awaiting_approval") {
				t.Fatal("expected actual approval pause")
			}
			f.requireState(t, before)
			if _, err := f.sql.Exec(`DELETE FROM api_keys WHERE id=?`, f.actor.ID); err != nil {
				t.Fatal(err)
			}
			// Retain the old request's claimed perms; the service must consult live storage.
			f.resume(t, conv, call, true, "failed")
			f.requireToolError(t, call, database.ErrOperatorDenied.Error())
			f.requireState(t, before)
			if f.allows(t) {
				t.Fatal("revoked credential published authority")
			}
			t.Log("pending credential revoked; approval cannot restore authority; source/version/audit/counters_unchanged=true")
		})
	}
}

func TestIAMAgentTurnStrictGeneratedArguments(t *testing.T) {
	for _, tc := range []struct{ name, args, errorText string }{
		{"malformed", `{"request":`, "invalid tool arguments"},
		{"duplicate-request", `{"request":{"name":"first","policy":` + turnReaderPolicy + `},"request":{"name":"second","policy":` + turnReaderPolicy + `}}`, database.ErrIAMInvalidPolicy.Error()},
		{"duplicate-name", `{"request":{"name":"first","name":"second","policy":` + turnReaderPolicy + `}}`, database.ErrIAMInvalidPolicy.Error()},
		{"duplicate-policy-field", `{"request":{"name":"bad","policy":` + strings.Replace(turnReaderPolicy, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1) + `}}`, database.ErrIAMInvalidPolicy.Error()},
		{"host-actor", `{"request":{"name":"bad","policy":` + turnReaderPolicy + `,"actor":{"kind":"system"}}}`, database.ErrIAMInvalidPolicy.Error()},
		{"host-capabilities", `{"request":{"name":"bad","policy":` + turnReaderPolicy + `},"capabilities":{"can_manage":true}}`, database.ErrIAMInvalidPolicy.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIAMTurnFixture(t)
			before := f.iamState(t)
			conv, call, sink := f.start(t, tc.name, "create_iam_policy", tc.args)
			if call.Status != "pending_approval" || !sink.has("awaiting_approval") {
				t.Fatal("expected generated tool call approval pause")
			}
			f.requireState(t, before)
			f.resume(t, conv, call, true, "failed")
			f.requireToolError(t, call, tc.errorText)
			f.requireState(t, before)
			t.Log("original fragmented arguments preserved; approved invalid call denied without IAM mutation")
		})
	}
}

func TestIAMAgentTurnReadOnlyForcedMutation(t *testing.T) {
	f := newIAMTurnFixture(t)
	f.actor.Perms = auth.PermSet{"read": true}
	if _, err := f.sql.Exec(`UPDATE api_keys SET permissions='["read"]' WHERE id=?`, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	before := f.iamState(t)
	_, call, sink := f.start(t, "forced-reader-write", "create_iam_policy", `{"request":{"name":"forbidden","policy":`+turnReaderPolicy+`}}`)
	if call.Status != "failed" || call.RequiresApproval || sink.has("awaiting_approval") || !sink.has("tool_result") || !sink.has("done") {
		t.Fatal("forced tool call did not fail inside the completed actual turn")
	}
	f.requireToolError(t, call, "unknown tool")
	f.requireState(t, before)
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	if f.provider.requests != 2 || f.provider.results != 1 {
		t.Fatal("reader refusal did not return through the provider loop")
	}
	for _, catalog := range f.provider.catalogs {
		readFound := false
		for _, name := range catalog {
			if name == "create_iam_policy" || name == "attach_iam_policy" || name == "delete_iam_policy" {
				t.Fatal("read-only provider catalog exposed an IAM write")
			}
			readFound = readFound || name == "get_iam_policy"
		}
		if !readFound {
			t.Fatal("read-only catalog omitted IAM reads")
		}
	}
	t.Log("read catalog excludes writes; provider-forced unknown write failed; source/version/audit/counters_unchanged=true")
}
