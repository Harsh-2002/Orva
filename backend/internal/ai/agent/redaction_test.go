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

const oneTimeSecret = "s3cr3t-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"

type captureSink struct{ frames []map[string]any }

func (s *captureSink) Send(event string, data any) error {
	if m, ok := data.(map[string]any); ok && event == "tool_result" {
		s.frames = append(s.frames, m)
	}
	return nil
}

// idStore lets Resume find the rows processToolCalls inserted.
type idStore struct{ fakeStore }

func (s *idStore) InsertToolCall(t *database.AIToolCall) error {
	t.ID = "tc-" + t.CallID
	return s.fakeStore.InsertToolCall(t)
}
func (s *idStore) GetToolCall(id string) (*database.AIToolCall, error) {
	for _, t := range s.toolCalls {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, errors.New("not found")
}

func mintingTool(name string, paths ...string) Tool {
	return Tool{Def: llm.ToolDef{Name: name}, Group: "test", Perm: "write", Sensitive: paths}
}

func mintDispatch(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	switch name {
	case "create_api_key":
		return json.RawMessage(`{"id":"k1","key":"` + oneTimeSecret + `","name":"ci"}`), nil
	case "create_inbound_webhook":
		return json.RawMessage(`{"inbound_webhook":{"id":"w1","secret_preview":"s3cr3t-0…"},"secret":"` + oneTimeSecret + `"}`), nil
	}
	return json.RawMessage(`{"ok":true}`), nil
}

// everything lists every durable or model-visible copy the runner produced.
func everything(t *testing.T, r *Runner, st *idStore, history []llm.Message) string {
	t.Helper()
	var b strings.Builder
	for _, m := range st.messages {
		b.WriteString(m.Content + m.Parts)
	}
	for _, c := range st.toolCalls {
		b.WriteString(c.Args + c.Result)
	}
	for _, m := range history {
		b.WriteString(m.Content)
	}
	loaded, err := r.loadHistory("c")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range loaded {
		b.WriteString(m.Content)
	}
	return b.String()
}

func TestOneTimeCredentialsNeverStoredOrSentToModel(t *testing.T) {
	for name, paths := range map[string][]string{"create_api_key": {"key"}, "create_inbound_webhook": {"secret"}} {
		t.Run(name, func(t *testing.T) {
			st := &idStore{}
			r := New(nil, []Tool{mintingTool(name, paths...)}, mintDispatch, st, Config{ApprovalPolicy: "auto"})
			sink := &captureSink{}
			_, results, _ := r.processToolCalls(context.Background(), sink, "c", "m",
				[]llm.ToolCall{{ID: "1", Name: name, Arguments: `{}`}})
			if got := everything(t, r, st, results); strings.Contains(got, oneTimeSecret) {
				t.Fatalf("one-time value reached storage or model history: %s", got)
			}
			if got := everything(t, r, st, results); !strings.Contains(got, RedactedPlaceholder) {
				t.Fatal("placeholder missing from stored result")
			}
			if len(sink.frames) != 1 {
				t.Fatalf("expected one live tool_result frame, got %d", len(sink.frames))
			}
			live, _ := json.Marshal(sink.frames[0]["result"])
			if !strings.Contains(string(live), oneTimeSecret) || sink.frames[0]["one_time"] != true {
				t.Fatalf("live frame lacks the one-time value or marker: %v", sink.frames[0])
			}
		})
	}
}

// An approval-gated mint runs from Resume; it must follow the same rule.
func TestOneTimeCredentialsRedactedOnApprovalResume(t *testing.T) {
	st := &idStore{}
	r := New(nil, []Tool{mintingTool("create_api_key", "key"), mintingTool("set_secret")}, mintDispatch, st, Config{ApprovalPolicy: "all_writes"})
	sink := &captureSink{}
	paused, _, _ := r.processToolCalls(context.Background(), sink, "c", "m", []llm.ToolCall{
		{ID: "1", Name: "create_api_key", Arguments: `{}`}, {ID: "2", Name: "set_secret", Arguments: `{}`}})
	if !paused {
		t.Fatal("writes must pause under all_writes")
	}
	// The second pending call keeps the turn paused, so Resume does not advance.
	if err := r.Resume(context.Background(), sink, "c", "tc-1", true, "p"); err != nil {
		t.Fatal(err)
	}
	if got := everything(t, r, st, nil); strings.Contains(got, oneTimeSecret) || !strings.Contains(got, RedactedPlaceholder) {
		t.Fatalf("resume path stored the one-time value or no placeholder: %s", got)
	}
	live, _ := json.Marshal(sink.frames)
	if !strings.Contains(string(live), oneTimeSecret) {
		t.Fatal("resume path did not deliver the live one-time value")
	}
}

func TestOrdinaryResultsAndErrorsAreUntouched(t *testing.T) {
	st := &idStore{}
	r := New(nil, []Tool{mintingTool("create_api_key", "key"), mintingTool("set_secret")}, mintDispatch, st, Config{ApprovalPolicy: "auto"})
	sink := &captureSink{}
	r.processToolCalls(context.Background(), sink, "c", "m", []llm.ToolCall{{ID: "1", Name: "set_secret", Arguments: `{}`}})
	if st.toolCalls[0].Result != `{"ok":true}` || sink.frames[0]["one_time"] != nil {
		t.Fatalf("non-sensitive result altered: %q %v", st.toolCalls[0].Result, sink.frames[0])
	}
	failing := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("denied")
	}
	r = New(nil, []Tool{mintingTool("create_api_key", "key")}, failing, st, Config{ApprovalPolicy: "auto"})
	r.processToolCalls(context.Background(), sink, "c", "m", []llm.ToolCall{{ID: "2", Name: "create_api_key", Arguments: `{}`}})
	if !strings.Contains(st.toolCalls[1].Result, "denied") {
		t.Fatalf("error result was redacted: %q", st.toolCalls[1].Result)
	}
}

func TestRedactResult(t *testing.T) {
	for name, tc := range map[string]struct {
		in      string
		paths   []string
		want    string
		changed bool
	}{
		"top level":      {`{"a":1,"secret":"x"}`, []string{"secret"}, `{"a":1,"secret":"` + RedactedPlaceholder + `"}`, true},
		"nested":         {`{"o":{"token":"x","n":1}}`, []string{"o.token"}, `{"o":{"n":1,"token":"` + RedactedPlaceholder + `"}}`, true},
		"array elements": {`{"l":[{"k":"x"},{"k":"y"}]}`, []string{"l.k"}, `{"l":[{"k":"` + RedactedPlaceholder + `"},{"k":"` + RedactedPlaceholder + `"}]}`, true},
		"absent":         {`{"a":1}`, []string{"secret"}, `{"a":1}`, false},
		"no paths":       {`{"secret":"x"}`, nil, `{"secret":"x"}`, false},
		"large number":   {`{"n":12345678901234567890,"secret":"x"}`, []string{"secret"}, `{"n":12345678901234567890,"secret":"` + RedactedPlaceholder + `"}`, true},
		"unparseable":    {`not json secret`, []string{"secret"}, `"` + RedactedPlaceholder + `"`, true},
		"trailing data":  {`{"a":1} {"secret":"x"}`, []string{"secret"}, `"` + RedactedPlaceholder + `"`, true},
		"trailing junk":  {`{"a":1} garbage`, []string{"secret"}, `"` + RedactedPlaceholder + `"`, true},
		"empty":          {``, []string{"secret"}, `"` + RedactedPlaceholder + `"`, true},
	} {
		t.Run(name, func(t *testing.T) {
			got, changed := redactResult(tc.in, tc.paths)
			if got != tc.want || changed != tc.changed {
				t.Fatalf("got %q,%v want %q,%v", got, changed, tc.want, tc.changed)
			}
		})
	}
}
