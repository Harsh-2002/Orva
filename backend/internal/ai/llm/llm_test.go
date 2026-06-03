package llm

import (
	"strings"
	"testing"
)

func acc(id, name, args string) *toolAcc {
	a := &toolAcc{id: id, name: name}
	a.args.WriteString(args)
	return a
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
