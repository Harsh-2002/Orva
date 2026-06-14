package commands

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/cli/commands/theme"
	cli "github.com/Harsh-2002/Orva/internal/client"
	"github.com/spf13/cobra"
)

// sseFrame writes one SSE event frame and flushes.
func sseFrame(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// newTestSession builds a chatSession wired to client with captured output and
// color disabled, suitable for driving canned SSE streams in tests.
func newTestSession(client *cli.Client) (*chatSession, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	s := &chatSession{
		cmd:       &cobra.Command{},
		client:    client,
		styles:    theme.New(false),
		stdin:     bufio.NewReader(strings.NewReader("")),
		out:       out,
		errOut:    errOut,
		toolNames: map[string]string{},
	}
	return s, out, errOut
}

// TestDriveSSE drives a complete chat stream and asserts the assistant text is
// accumulated to stdout and the conversation id is captured.
func TestDriveSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "conversation", `{"id":"c1"}`)
		sseFrame(w, "message_start", `{"message_id":"m1","role":"assistant"}`)
		sseFrame(w, "delta", `{"text":"Hello "}`)
		fmt.Fprint(w, ": ping\n\n") // heartbeat — must be ignored
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		sseFrame(w, "delta", `{"text":"world"}`)
		sseFrame(w, "message_end", `{"message_id":"m1"}`)
		sseFrame(w, "done", `{"conversation_id":"c1"}`)
	}))
	defer srv.Close()

	s, out, _ := newTestSession(cli.NewClient(srv.URL, "k"))
	resp, err := s.postChat(context.Background(), "hi")
	if err != nil {
		t.Fatalf("postChat: %v", err)
	}
	res, err := s.drive(resp)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if !res.done {
		t.Errorf("expected done=true, got %+v", res)
	}
	if s.convID != "c1" {
		t.Errorf("convID = %q, want c1", s.convID)
	}
	if got := out.String(); !strings.Contains(got, "Hello world") {
		t.Errorf("stdout = %q, want it to contain %q", got, "Hello world")
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("stdout contains ANSI escapes with color disabled: %q", out.String())
	}
}

// TestDrivePrematureEOF asserts the PR-C guard: a stream that ends (clean EOF)
// without a terminal frame (done / awaiting_approval / error) is surfaced as an
// error, not silently accepted as a successful (truncated) turn.
func TestDrivePrematureEOF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "message_start", `{"message_id":"m1","role":"assistant"}`)
		sseFrame(w, "delta", `{"text":"partial answer"}`)
		// Handler returns here — connection closes mid-turn, NO done/error frame.
	}))
	defer srv.Close()

	s, _, _ := newTestSession(cli.NewClient(srv.URL, "k"))
	resp, err := s.postChat(context.Background(), "hi")
	if err != nil {
		t.Fatalf("postChat: %v", err)
	}
	_, err = s.drive(resp)
	if err == nil {
		t.Fatal("expected an error for a stream that ended without a terminal frame, got nil")
	}
	if !strings.Contains(err.Error(), "ended unexpectedly") {
		t.Errorf("error = %v, want it to mention the stream ending unexpectedly", err)
	}
}

// TestClientIdleTimeout asserts the PR-C client idle deadline: a stream that
// goes silent after the headers is cancelled within the idle window instead of
// hanging forever. Reading blocks until the idle timer cancels the request ctx.
func TestClientIdleTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": ping\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // stall: never send more, never close, until the test ends
	}))
	defer srv.Close()
	defer close(release)

	c := cli.NewClient(srv.URL, "k")
	resp, err := c.Send(cli.Request{Path: "/", NoTimeout: true, IdleTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close()

	start := time.Now()
	buf := make([]byte, 4096)
	for {
		if _, rerr := resp.Body.Read(buf); rerr != nil {
			break // idle timer cancelled the ctx → Read unblocks with an error
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("read did not unblock within the idle window; took %v (idle was 200ms)", elapsed)
	}
}

// TestApprovalFailClosedNonTTY ensures a tool requiring approval, in a
// non-interactive context without --auto-approve, fails closed and never issues
// an approve/reject POST.
func TestApprovalFailClosedNonTTY(t *testing.T) {
	var approveHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tool-calls/") {
			approveHits++
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "message_start", `{"message_id":"m1","role":"assistant"}`)
		sseFrame(w, "message_end", `{"message_id":"m1"}`)
		sseFrame(w, "tool_call", `{"id":"t1","call_id":"call_1","name":"delete_function","args":{"name":"x"},"requires_approval":true}`)
		sseFrame(w, "awaiting_approval", `{"conversation_id":"c1"}`)
	}))
	defer srv.Close()

	s, _, _ := newTestSession(cli.NewClient(srv.URL, "k"))
	resp, err := s.postChat(context.Background(), "delete x")
	if err != nil {
		t.Fatalf("postChat: %v", err)
	}
	res, err := s.drive(resp)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if !res.awaiting || len(res.pending) != 1 {
		t.Fatalf("expected one pending approval, got awaiting=%v pending=%d", res.awaiting, len(res.pending))
	}
	_, err = s.handleApprovals(context.Background(), res.pending)
	if err == nil || !strings.Contains(err.Error(), "requires approval") {
		t.Errorf("expected fail-closed approval error, got %v", err)
	}
	if approveHits != 0 {
		t.Errorf("expected no approve/reject POST, got %d", approveHits)
	}
}

// TestApprovalAutoApprove confirms --auto-approve issues the approve POST and
// consumes the continuation stream to completion.
func TestApprovalAutoApprove(t *testing.T) {
	var approvePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/approve") {
			approvePath = r.URL.Path
			w.Header().Set("Content-Type", "text/event-stream")
			sseFrame(w, "tool_result", `{"id":"t1","call_id":"call_1","status":"succeeded","result":{"ok":true}}`)
			sseFrame(w, "done", `{"conversation_id":"c1"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sseFrame(w, "message_start", `{"message_id":"m1","role":"assistant"}`)
		sseFrame(w, "message_end", `{"message_id":"m1"}`)
		sseFrame(w, "tool_call", `{"id":"t1","call_id":"call_1","name":"create_function","args":{},"requires_approval":true}`)
		sseFrame(w, "awaiting_approval", `{"conversation_id":"c1"}`)
	}))
	defer srv.Close()

	s, _, _ := newTestSession(cli.NewClient(srv.URL, "k"))
	s.autoApprove = true
	resp, err := s.postChat(context.Background(), "make a function")
	if err != nil {
		t.Fatalf("postChat: %v", err)
	}
	res, err := s.drive(resp)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	res, err = s.handleApprovals(context.Background(), res.pending)
	if err != nil {
		t.Fatalf("handleApprovals: %v", err)
	}
	if !res.done {
		t.Errorf("expected continuation done, got %+v", res)
	}
	if approvePath != "/api/v1/ai/tool-calls/t1/approve" {
		t.Errorf("approve path = %q, want /api/v1/ai/tool-calls/t1/approve", approvePath)
	}
}

// TestChatThinkingValidation rejects an invalid --thinking value before any
// network call.
func TestChatThinkingValidation(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{"chat", "--thinking", "bogus", "-p", "hi"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid --thinking") {
		t.Errorf("expected thinking validation error, got %v", err)
	}
}

// TestScreenRows checks the wrapping-aware row count used by the glamour
// re-render eraser.
func TestScreenRows(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  int
	}{
		{"hello", 80, 1},
		{"a\nb", 80, 2},
		{"a\nb\n", 80, 3},                 // trailing newline = one extra (empty) row
		{strings.Repeat("x", 100), 40, 3}, // 100/40 -> 3 wrapped rows
		{"", 80, 1},
	}
	for _, c := range cases {
		if got := screenRows(c.text, c.width); got != c.want {
			t.Errorf("screenRows(%q,%d) = %d, want %d", c.text, c.width, got, c.want)
		}
	}
}
