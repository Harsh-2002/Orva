package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func acc(id, name, args string) *toolAcc {
	a := &toolAcc{id: id, name: name}
	a.args.WriteString(args)
	return a
}

type staticKeyResolver struct {
	apiKey  string
	baseURL string
	err     error
}

func (r staticKeyResolver) Providers() []string { return []string{"openai"} }

func (r staticKeyResolver) Resolve(string) (string, string, error) {
	return r.apiKey, r.baseURL, r.err
}

// Bifrost 1.7 replaced EnvVar with SecretVar for provider credentials. Orva's
// resolver already returns a decrypted value, so even an env.-prefixed value
// must remain literal rather than being interpreted as a new environment ref.
func TestAccountReturnsDecryptedProviderKeyAsLiteralSecret(t *testing.T) {
	const apiKey = "env.literal-api-key"
	keys, err := (&account{resolver: staticKeyResolver{apiKey: apiKey}}).
		GetKeysForProvider(context.Background(), schemas.OpenAI)
	if err != nil {
		t.Fatalf("GetKeysForProvider: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	if keys[0].Value.Val != apiKey {
		t.Fatalf("key value = %q, want literal %q", keys[0].Value.Val, apiKey)
	}
	if keys[0].Value.IsFromSecret() {
		t.Fatal("decrypted provider key was reinterpreted as an external secret reference")
	}
}

// TestAssembleToolCallsSynthesizesID covers the fix for endpoints that omit
// tool-call ids in their stream deltas: the assembled call must carry a stable,
// non-empty id so the replayed tool result matches it on the next turn.
func TestAssembleToolCallsSynthesizesID(t *testing.T) {
	byIndex := map[int]*toolAcc{
		0: acc("", "list_functions", `{"limit":5}`), // no id from provider
		1: acc("call_xyz", "get_function", `{"id":"a"}`),
		2: acc("", "", ""), // incomplete (no name) → dropped
	}
	calls := assembleToolCalls([]int{0, 1, 2}, byIndex)

	if len(calls) != 2 {
		t.Fatalf("expected 2 assembled calls (incomplete dropped), got %d", len(calls))
	}
	if calls[0].ID == "" {
		t.Error("missing tool-call id should be synthesized to a non-empty value")
	}
	if calls[0].ID != "call_0" {
		t.Errorf("synthesized id = %q, want call_0", calls[0].ID)
	}
	if calls[1].ID != "call_xyz" {
		t.Errorf("provider-supplied id should be preserved, got %q", calls[1].ID)
	}
	if calls[0].Name != "list_functions" || calls[0].Arguments != `{"limit":5}` {
		t.Errorf("name/args not assembled: %+v", calls[0])
	}
}

// ─── pump (stream termination) ───────────────────────────────────────────────

func textChunk(s string) *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{
		BifrostChatResponse: &schemas.BifrostChatResponse{
			Choices: []schemas.BifrostResponseChoice{{
				ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
					Delta: &schemas.ChatStreamResponseChoiceDelta{Content: &s},
				},
			}},
		},
	}
}

func finishChunk(reason string) *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{
		BifrostChatResponse: &schemas.BifrostChatResponse{
			Choices: []schemas.BifrostResponseChoice{{FinishReason: &reason}},
		},
	}
}

func toolFragmentChunk(idx int, id, name, argsFragment string) *schemas.BifrostStreamChunk {
	tc := schemas.ChatAssistantMessageToolCall{
		Function: schemas.ChatAssistantMessageToolCallFunction{Arguments: argsFragment},
	}
	tc.Index = uint16(idx)
	if id != "" {
		tc.ID = &id
	}
	if name != "" {
		tc.Function.Name = &name
	}
	return &schemas.BifrostStreamChunk{
		BifrostChatResponse: &schemas.BifrostChatResponse{
			Choices: []schemas.BifrostResponseChoice{{
				ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
					Delta: &schemas.ChatStreamResponseChoiceDelta{
						ToolCalls: []schemas.ChatAssistantMessageToolCall{tc},
					},
				},
			}},
		},
	}
}

// runPump feeds the given chunks through pump and returns every emitted event.
func runPump(ctx context.Context, chunks ...*schemas.BifrostStreamChunk) []Event {
	stream := make(chan *schemas.BifrostStreamChunk, len(chunks))
	for _, c := range chunks {
		stream <- c
	}
	close(stream)
	out := make(chan Event, 64)
	go func() {
		defer close(out)
		pump(ctx, stream, out)
	}()
	var events []Event
	for ev := range out {
		events = append(events, ev)
	}
	return events
}

// TestPumpCleanCompletion: a stream that reports a finish reason ends with
// EventDone carrying it.
func TestPumpCleanCompletion(t *testing.T) {
	events := runPump(context.Background(), textChunk("hello "), textChunk("world"), finishChunk("stop"))
	last := events[len(events)-1]
	if last.Type != EventDone {
		t.Fatalf("expected EventDone, got %s (%v)", last.Type, last.Err)
	}
	if last.FinishReason != "stop" {
		t.Errorf("finish reason = %q, want stop", last.FinishReason)
	}
}

// TestPumpRejectsStreamWithoutFinishReason covers the silent-truncation bug:
// a provider reset closes the stream with no finish reason, which must surface
// as EventError — not as a clean EventDone wrapping a half answer.
func TestPumpRejectsStreamWithoutFinishReason(t *testing.T) {
	events := runPump(context.Background(), textChunk("half an ans")) // reset mid-response
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("truncated stream must end with EventError, got %s", last.Type)
	}
	if last.Err == nil || !strings.Contains(last.Err.Error(), "stream ended unexpectedly") {
		t.Errorf("unexpected error: %v", last.Err)
	}
	for _, ev := range events {
		if ev.Type == EventDone {
			t.Error("truncated stream must not emit EventDone")
		}
	}
}

// TestPumpRejectsTruncatedToolCall: a reset mid tool call (fragmented JSON
// arguments, no finish reason) must error rather than hand the agent a
// half-assembled call — the trigger for the "stuck in tool calling" loop.
func TestPumpRejectsTruncatedToolCall(t *testing.T) {
	events := runPump(context.Background(),
		toolFragmentChunk(0, "call_1", "create_function", `{"name":"demo","runt`)) // cut mid-args
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("truncated tool-call stream must end with EventError, got %s", last.Type)
	}
}

// TestPumpAssemblesFragmentedToolCall: tool-call arguments arrive as indexed
// fragments across deltas (id/name only on the first); a properly finished
// stream must reassemble them into one complete call.
func TestPumpAssemblesFragmentedToolCall(t *testing.T) {
	events := runPump(context.Background(),
		toolFragmentChunk(0, "call_1", "create_function", `{"na`),
		toolFragmentChunk(0, "", "", `me":"demo"}`),
		finishChunk("tool_calls"))
	last := events[len(events)-1]
	if last.Type != EventDone {
		t.Fatalf("expected EventDone, got %s (%v)", last.Type, last.Err)
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("expected 1 assembled call, got %d", len(last.ToolCalls))
	}
	tc := last.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "create_function" || tc.Arguments != `{"name":"demo"}` {
		t.Errorf("fragments not reassembled: %+v", tc)
	}
}

// TestPumpCancelledContextClassifiedAsCancellation: when the request context
// is cancelled (client disconnect), the terminating error must be the context
// error so callers can keep filtering cancellations out of the error log.
func TestPumpCancelledContextClassifiedAsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := runPump(ctx, textChunk("partial"))
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("expected EventError, got %s", last.Type)
	}
	if !errors.Is(last.Err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", last.Err)
	}
}

// TestIsReasoningError gates the thinking graceful-fallback: only reasoning-
// related upstream errors should trigger the strip-and-retry; auth / model /
// network failures must surface immediately instead of being masked.
func TestIsReasoningError(t *testing.T) {
	reasoning := []string{
		"Unsupported parameter: 'reasoning_effort'",
		"this model does not support thinking",
		"budget_tokens must be >= 1024",
		"invalid effort level",
	}
	for _, m := range reasoning {
		if !isReasoningError(m) {
			t.Errorf("expected reasoning error for %q", m)
		}
		if !isReasoningError(strings.ToUpper(m)) {
			t.Errorf("expected case-insensitive match for %q", m)
		}
	}
	unrelated := []string{
		"401 Unauthorized: invalid api key",
		"model gpt-foo not found",
		"connection refused",
		"rate limit exceeded",
	}
	for _, m := range unrelated {
		if isReasoningError(m) {
			t.Errorf("unrelated error wrongly classified as reasoning: %q", m)
		}
	}
}
