package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/ai/llm"
	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// ─── fakes ───────────────────────────────────────────────────────────────────

type fakeStore struct {
	messages  []*database.AIMessage
	toolCalls []*database.AIToolCall
}

func (s *fakeStore) InsertMessage(m *database.AIMessage) error {
	s.messages = append(s.messages, m)
	return nil
}
func (s *fakeStore) UpdateMessage(id, content, parts, tokenUsage string) error { return nil }
func (s *fakeStore) ListMessages(conversationID string, sinceSeq int) ([]*database.AIMessage, error) {
	return s.messages, nil
}
func (s *fakeStore) InsertToolCall(t *database.AIToolCall) error {
	s.toolCalls = append(s.toolCalls, t)
	return nil
}
func (s *fakeStore) GetToolCall(id string) (*database.AIToolCall, error) {
	return nil, errors.New("not found")
}
func (s *fakeStore) UpdateToolCall(t *database.AIToolCall) error { return nil }
func (s *fakeStore) ListToolCalls(conversationID string) ([]*database.AIToolCall, error) {
	return s.toolCalls, nil
}
func (s *fakeStore) TouchConversation(id string) error { return nil }

type fakeSink struct{ events []string }

func (s *fakeSink) Send(event string, data any) error {
	s.events = append(s.events, event)
	return nil
}

func testRunner(dispatch Dispatcher, tools ...Tool) *Runner {
	return New(nil, tools, dispatch, &fakeStore{}, Config{ApprovalPolicy: "auto"})
}

func readOnlyTool(name string) Tool {
	return Tool{Def: llm.ToolDef{Name: name}, Group: "test", Perm: "read", ReadOnly: true}
}

// ─── invalid-argument guard (truncated tool calls) ──────────────────────────

// TestRunToolCallRejectsInvalidArgsWithoutDispatch: arguments that aren't
// valid JSON (the signature of a provider stream truncated mid tool call)
// must fail fast with a self-correctable error — and never reach the
// dispatcher, whose tool-specific unmarshal error would obscure the cause.
func TestRunToolCallRejectsInvalidArgsWithoutDispatch(t *testing.T) {
	dispatched := false
	r := testRunner(func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		dispatched = true
		return json.RawMessage(`{}`), nil
	}, readOnlyTool("list_functions"))

	row := &database.AIToolCall{
		ConversationID: "conv1", CallID: "call_0",
		ToolName: "list_functions",
		Args:     `{"name":"demo","runt`, // truncated mid-stream
	}
	r.runToolCall(context.Background(), &fakeSink{}, "conv1", row)

	if dispatched {
		t.Error("dispatcher must not be called with invalid JSON arguments")
	}
	if row.Status != "failed" {
		t.Errorf("status = %q, want failed", row.Status)
	}
	if !strings.Contains(row.Result, "invalid tool arguments") {
		t.Errorf("result should explain the invalid arguments, got %q", row.Result)
	}
}

// TestRunToolCallValidArgsStillDispatch: the guard must not get in the way of
// well-formed calls (including empty args, which normalize to {}).
func TestRunToolCallValidArgsStillDispatch(t *testing.T) {
	for _, args := range []string{`{"limit":5}`, "", "  "} {
		dispatched := false
		r := testRunner(func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
			dispatched = true
			return json.RawMessage(`{"ok":true}`), nil
		}, readOnlyTool("list_functions"))

		row := &database.AIToolCall{ConversationID: "c", CallID: "call_0", ToolName: "list_functions", Args: args}
		r.runToolCall(context.Background(), &fakeSink{}, "c", row)

		if !dispatched {
			t.Errorf("args %q: dispatcher should have been called", args)
		}
		if row.Status != "succeeded" {
			t.Errorf("args %q: status = %q, want succeeded", args, row.Status)
		}
	}
}

// ─── loop-breaker signal ─────────────────────────────────────────────────────

// TestProcessToolCallsFailedSignatures: the third return value feeds the
// agent's loop-breaker; it must carry the signature (name + args) of exactly
// the calls that failed, so the breaker can track per-call streaks — a
// succeeding companion call must not mask a repeatedly failing one, and
// distinct failing probes must not be conflated into one signature.
func TestProcessToolCallsFailedSignatures(t *testing.T) {
	succeedFor := func(okName string) Dispatcher {
		return func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
			if name == okName {
				return json.RawMessage(`{}`), nil
			}
			return nil, errors.New("boom")
		}
	}
	calls := []llm.ToolCall{
		{ID: "c1", Name: "alpha", Arguments: `{"x":1}`},
		{ID: "c2", Name: "beta", Arguments: `{}`},
	}
	tools := []Tool{readOnlyTool("alpha"), readOnlyTool("beta")}

	r := testRunner(succeedFor("beta"), tools...)
	_, _, sigs := r.processToolCalls(context.Background(), &fakeSink{}, "c", "m", calls)
	if len(sigs) != 1 {
		t.Fatalf("exactly the failing call must be reported, got %d signatures", len(sigs))
	}
	if sigs[0] != "alpha\x00"+`{"x":1}` {
		t.Errorf("signature must be name+args, got %q", sigs[0])
	}

	r = testRunner(succeedFor("alpha"), tools...)
	_, _, sigs = r.processToolCalls(context.Background(), &fakeSink{}, "c", "m", calls)
	if len(sigs) != 1 || sigs[0] != "beta\x00{}" {
		t.Errorf("expected only beta's signature, got %v", sigs)
	}

	allOK := func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}
	r = testRunner(allOK, tools...)
	_, _, sigs = r.processToolCalls(context.Background(), &fakeSink{}, "c", "m", calls)
	if len(sigs) != 0 {
		t.Errorf("no failures → no signatures, got %v", sigs)
	}
}
