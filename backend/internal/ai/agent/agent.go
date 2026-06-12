// Package agent runs Orva's in-product AI assistant: the agentic loop that
// streams a model turn, detects tool calls, gates writes behind human
// approval, dispatches approved calls in-process, and feeds results back to
// the model until it produces a final answer.
//
// The loop is deliberately decoupled from the tool layer: it receives a set of
// Tools (name + JSON schema + approval metadata) and a Dispatcher, so it never
// imports the mcp package. ai.Manager wires the mcp registry to it.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/ai/llm"
	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// maxFailedToolIterations is the loop-breaker budget: after this many
// consecutive iterations in which every dispatched tool call failed, the turn
// stops with an error instead of running out the full MaxIterations budget.
// Three rounds gives the model two honest chances to self-correct a bad call.
const maxFailedToolIterations = 3

// Tool is one callable the agent may offer the model, plus the metadata that
// drives approval gating and UI grouping.
type Tool struct {
	Def              llm.ToolDef
	Group            string
	Perm             string // read | write | invoke | admin
	ReadOnly         bool
	Destructive      bool
}

// Dispatcher executes a tool by name with raw JSON arguments and returns the
// tool's output (already JSON-encoded). Implemented over the mcp registry.
type Dispatcher func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error)

// Store is the persistence surface the loop needs. *database.Database
// satisfies it.
type Store interface {
	InsertMessage(m *database.AIMessage) error
	UpdateMessage(id, content, parts, tokenUsage string) error
	ListMessages(conversationID string, sinceSeq int) ([]*database.AIMessage, error)
	InsertToolCall(t *database.AIToolCall) error
	GetToolCall(id string) (*database.AIToolCall, error)
	UpdateToolCall(t *database.AIToolCall) error
	ListToolCalls(conversationID string) ([]*database.AIToolCall, error)
	TouchConversation(id string) error
}

// Sink writes SSE frames to the client. The handler implements it over the
// http.ResponseWriter (with flushing).
type Sink interface {
	Send(event string, data any) error
}

// Config is the per-run agent configuration (resolved from ai_settings).
type Config struct {
	Provider       string
	Model          string
	Thinking       string  // off | standard | deep
	System         string  // system prompt
	Temperature    *float64
	ApprovalPolicy string // all_writes | destructive_only | auto
	MaxIterations  int
}

// Runner executes agent turns for one configured provider/model.
type Runner struct {
	llm      *llm.Client
	tools    []Tool
	byName   map[string]Tool
	dispatch Dispatcher
	store    Store
	cfg      Config
}

// New builds a Runner. tools is the principal-scoped catalog; dispatch runs
// them in-process.
func New(client *llm.Client, tools []Tool, dispatch Dispatcher, store Store, cfg Config) *Runner {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 25
	}
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		byName[t.Def.Name] = t
	}
	return &Runner{llm: client, tools: tools, byName: byName, dispatch: dispatch, store: store, cfg: cfg}
}

// ─── message parts (stored in ai_messages.parts; also the frontend's render model) ──

type part struct {
	Type string `json:"type"` // text | thinking | tool_call | tool_result

	Text string `json:"text,omitempty"`

	// tool_call
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Group     string `json:"group,omitempty"`

	// tool_result
	CallID string `json:"call_id,omitempty"`
	Status string `json:"status,omitempty"`
}

// ─── public entry points ────────────────────────────────────────────────────

// Run handles a fresh user message: persists it, then advances the agentic
// loop. principalID is recorded as the approver/actor.
func (r *Runner) Run(ctx context.Context, sink Sink, convID, userContent, principalID string) error {
	if err := r.store.InsertMessage(&database.AIMessage{
		ConversationID: convID,
		Role:           "user",
		Content:        userContent,
		Parts:          mustJSON([]part{{Type: "text", Text: userContent}}),
	}); err != nil {
		return err
	}
	_ = r.store.TouchConversation(convID)
	return r.advance(ctx, sink, convID, principalID)
}

// Resume continues a paused turn after the user approves or rejects a tool
// call. If approved, the tool runs now; once every pending tool call for the
// triggering assistant message is resolved, the loop advances.
func (r *Runner) Resume(ctx context.Context, sink Sink, convID, toolCallID string, approved bool, principalID string) error {
	tc, err := r.store.GetToolCall(toolCallID)
	if err != nil {
		return err
	}
	if tc.Status != "pending_approval" {
		return fmt.Errorf("tool call %s is not awaiting approval (status=%s)", toolCallID, tc.Status)
	}

	if !approved {
		tc.Status = "rejected"
		tc.ApprovedBy = principalID
		_ = r.store.UpdateToolCall(tc)
		// Record a tool message so the model learns the call was declined.
		r.persistToolResult(convID, tc, `{"error":"rejected by user"}`)
		_ = sink.Send("tool_result", map[string]any{"id": tc.ID, "status": "rejected"})
	} else {
		tc.Status = "approved"
		tc.ApprovedBy = principalID
		_ = r.store.UpdateToolCall(tc)
		r.runToolCall(ctx, sink, convID, tc)
	}

	// Are any tool calls for the same assistant message still pending?
	if r.hasPendingForMessage(convID, tc.MessageID) {
		_ = sink.Send("awaiting_approval", map[string]any{"conversation_id": convID})
		return nil // still waiting on the user for another call
	}
	return r.advance(ctx, sink, convID, principalID)
}

// ─── core loop ──────────────────────────────────────────────────────────────

func (r *Runner) advance(ctx context.Context, sink Sink, convID, principalID string) error {
	// Load the conversation once and grow it in memory as the loop appends new
	// assistant + tool messages, instead of re-reading and re-parsing the whole
	// history from SQLite on every iteration (was O(n^2) on the streaming path).
	history, err := r.loadHistory(convID)
	if err != nil {
		return err
	}
	// Convert the (fixed) tool catalog to provider format once per turn, reused
	// across iterations rather than re-serialized on every Stream call.
	prepared, err := r.llm.PrepareTools(r.toolDefs())
	if err != nil {
		_ = sink.Send("error", map[string]any{"message": err.Error()})
		return err
	}

	// Loop-breaker: consecutive iterations in which every dispatched tool call
	// failed. A model that keeps re-issuing a broken call (classically: after a
	// provider reset truncated the original call's arguments) would otherwise
	// burn the whole iteration budget — minutes of churn and billed calls with
	// no progress.
	noProgress := 0

	for iter := 0; iter < r.cfg.MaxIterations; iter++ {
		// Bail if the client disconnected (or the request was cancelled).
		// Otherwise the loop would keep calling the billed provider and
		// dispatching auto-approved tools against a dead connection.
		if err := ctx.Err(); err != nil {
			return err
		}

		req := llm.Request{
			Provider:    r.cfg.Provider,
			Model:       r.cfg.Model,
			System:      r.cfg.System,
			Messages:    history,
			Prepared:    prepared,
			Thinking:    r.cfg.Thinking,
			Temperature: r.cfg.Temperature,
		}
		stream, err := r.llm.Stream(ctx, req)
		if err != nil {
			_ = sink.Send("error", map[string]any{"message": err.Error()})
			return err
		}

		// Persist a placeholder assistant message up-front so we have a stable
		// id to attach tool calls to and to update when streaming finishes.
		assistant := &database.AIMessage{ConversationID: convID, Role: "assistant"}
		if err := r.store.InsertMessage(assistant); err != nil {
			return err
		}
		_ = sink.Send("message_start", map[string]any{"message_id": assistant.ID, "role": "assistant"})

		var textB, thinkB strings.Builder
		var toolCalls []llm.ToolCall
		var usage *llm.Usage
		for ev := range stream {
			switch ev.Type {
			case llm.EventText:
				textB.WriteString(ev.Text)
				_ = sink.Send("delta", map[string]any{"text": ev.Text})
			case llm.EventThinking:
				thinkB.WriteString(ev.Text)
				_ = sink.Send("thinking", map[string]any{"text": ev.Text})
			case llm.EventDone:
				toolCalls = ev.ToolCalls
				usage = ev.Usage
			case llm.EventError:
				r.finalizeAssistant(assistant, textB.String(), thinkB.String(), nil, nil)
				_ = sink.Send("error", map[string]any{"message": ev.Err.Error()})
				return ev.Err
			}
		}

		r.finalizeAssistant(assistant, textB.String(), thinkB.String(), toolCalls, usage)
		_ = sink.Send("message_end", map[string]any{"message_id": assistant.ID})
		_ = r.store.TouchConversation(convID)

		// No tool calls → the model produced a final answer.
		if len(toolCalls) == 0 {
			_ = sink.Send("done", map[string]any{"conversation_id": convID})
			return nil
		}

		// Mirror the just-persisted assistant turn into the in-memory history so
		// the next iteration sees it without a DB round-trip — matches exactly what
		// loadHistory reconstructs from the stored parts.
		asst := llm.Message{Role: llm.RoleAssistant, Content: textB.String()}
		for _, c := range toolCalls {
			asst.ToolCalls = append(asst.ToolCalls, llm.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
		}
		history = append(history, asst)

		// Process the requested tool calls. Read-only / auto ones run now;
		// anything gated pauses the turn for approval.
		paused, results, allFailed := r.processToolCalls(ctx, sink, convID, assistant.ID, toolCalls)
		if paused {
			_ = sink.Send("awaiting_approval", map[string]any{"conversation_id": convID})
			return nil
		}
		if allFailed {
			noProgress++
		} else {
			noProgress = 0
		}
		if noProgress >= maxFailedToolIterations {
			err := fmt.Errorf("stopped after %d consecutive tool iterations in which every call failed — the model is not making progress; retry the message or rephrase the request", noProgress)
			_ = sink.Send("error", map[string]any{"message": err.Error()})
			return err
		}
		// Append the persisted tool results, then loop and let the model continue.
		history = append(history, results...)
	}

	_ = sink.Send("done", map[string]any{"conversation_id": convID, "note": "max tool iterations reached"})
	return nil
}

// processToolCalls persists each requested call, emits its event, runs the
// non-gated ones immediately, and returns true if any call is awaiting approval
// (the turn must pause). It also returns the tool-result messages it persisted,
// in order, so the caller can append them to the in-memory history without a
// re-read, and whether every dispatched call failed (the caller's loop-breaker
// signal). When paused, results is irrelevant (the turn returns).
func (r *Runner) processToolCalls(ctx context.Context, sink Sink, convID, msgID string, calls []llm.ToolCall) (paused bool, results []llm.Message, allFailed bool) {
	ran, failed := 0, 0
	for _, c := range calls {
		meta, known := r.byName[c.Name]
		requiresApproval := known && r.approvalNeeded(meta)
		group := ""
		destructive := false
		if known {
			group = meta.Group
			destructive = meta.Destructive
		}
		status := "running"
		if requiresApproval {
			status = "pending_approval"
		}
		row := &database.AIToolCall{
			ConversationID:   convID,
			MessageID:        msgID,
			CallID:           c.ID,
			ToolName:         c.Name,
			ToolGroup:        group,
			Args:             emptyToObj(c.Arguments),
			Status:           status,
			RequiresApproval: requiresApproval,
			Destructive:      destructive,
		}
		_ = r.store.InsertToolCall(row)
		// Embed the args verbatim only when they're valid JSON. Invalid args
		// (truncated stream) would fail the frame's own marshal and the sink
		// degrades the whole payload to {} — losing the id and name the UI
		// needs to render the (failing) call. Fall back to a JSON string.
		argsPayload := json.RawMessage(row.Args)
		if !json.Valid(argsPayload) {
			argsPayload = json.RawMessage(mustJSON(row.Args))
		}
		_ = sink.Send("tool_call", map[string]any{
			"id": row.ID, "call_id": c.ID, "name": c.Name, "group": group,
			"args": argsPayload, "requires_approval": requiresApproval,
		})

		if requiresApproval {
			paused = true
			continue // wait for the user; do NOT run it
		}
		r.runToolCall(ctx, sink, convID, row)
		ran++
		if row.Status == "failed" {
			failed++
		}
		// Record the result for the in-memory history (mirrors persistToolResult:
		// a role=tool message keyed by the call id).
		results = append(results, llm.Message{Role: llm.RoleTool, Content: row.Result, ToolCallID: row.CallID})
	}
	return paused, results, ran > 0 && failed == ran
}

// runToolCall dispatches one (approved or auto) tool call, persists the
// result, records a tool message for the model, and emits tool_result.
func (r *Runner) runToolCall(ctx context.Context, sink Sink, convID string, row *database.AIToolCall) {
	// Don't dispatch if the client already disconnected — covers the case where
	// a single turn auto-runs several tools and the connection drops mid-batch.
	if ctx.Err() != nil {
		return
	}
	started := time.Now().UTC()
	row.StartedAt = &started
	row.Status = "running"
	_ = r.store.UpdateToolCall(row)

	// Refuse to dispatch arguments that aren't valid JSON. They can't be a
	// real call — the most common cause is a provider stream truncated mid
	// tool call — and the dispatcher would only fail later with a confusing
	// tool-specific unmarshal error. Failing here gives the model (and the
	// audit row) a precise, self-correctable signal.
	var out json.RawMessage
	var err error
	if args := emptyToObj(row.Args); !json.Valid([]byte(args)) {
		err = errors.New("invalid tool arguments: not valid JSON (the model response may have been truncated) — re-issue the call with complete arguments")
	} else {
		out, err = r.dispatch(ctx, row.ToolName, json.RawMessage(args))
	}
	var resultJSON string
	if err != nil {
		resultJSON = mustJSON(map[string]string{"error": err.Error()})
		row.Status = "failed"
	} else {
		resultJSON = string(out)
		if resultJSON == "" {
			resultJSON = "null"
		}
		row.Status = "succeeded"
	}
	finished := time.Now().UTC()
	durMS := finished.Sub(started).Milliseconds()
	row.FinishedAt = &finished
	row.DurationMS = &durMS
	row.Result = resultJSON
	_ = r.store.UpdateToolCall(row)
	r.persistToolResult(convID, row, resultJSON)
	_ = sink.Send("tool_result", map[string]any{
		"id": row.ID, "call_id": row.CallID, "status": row.Status,
		"result": json.RawMessage(resultJSON),
	})
}

// ─── persistence + history helpers ───────────────────────────────────────────

func (r *Runner) finalizeAssistant(m *database.AIMessage, text, thinking string, calls []llm.ToolCall, usage *llm.Usage) {
	parts := make([]part, 0, 2+len(calls))
	if thinking != "" {
		parts = append(parts, part{Type: "thinking", Text: thinking})
	}
	if text != "" {
		parts = append(parts, part{Type: "text", Text: text})
	}
	for _, c := range calls {
		meta := r.byName[c.Name]
		parts = append(parts, part{Type: "tool_call", ID: c.ID, Name: c.Name, Arguments: c.Arguments, Group: meta.Group})
	}
	usageJSON := ""
	if usage != nil {
		usageJSON = mustJSON(usage)
	}
	if err := r.store.UpdateMessage(m.ID, text, mustJSON(parts), usageJSON); err != nil {
		// If we can't persist the assistant turn, the next loadHistory will be
		// missing it and the model loses context — surface it rather than hide it.
		slog.Warn("ai: persist assistant message failed", "conversation", m.ConversationID, "message", m.ID, "error", err)
	}
}

// persistToolResult writes a role=tool message so the model sees the result on
// the next turn (the ai_tool_calls row is the UI/audit view).
func (r *Runner) persistToolResult(convID string, row *database.AIToolCall, resultJSON string) {
	_ = r.store.InsertMessage(&database.AIMessage{
		ConversationID: convID,
		Role:           "tool",
		Content:        resultJSON,
		Parts:          mustJSON([]part{{Type: "tool_result", CallID: row.CallID, Status: row.Status}}),
	})
}

// loadHistory rebuilds the model-facing message list from stored messages.
func (r *Runner) loadHistory(convID string) ([]llm.Message, error) {
	rows, err := r.store.ListMessages(convID, 0)
	if err != nil {
		return nil, err
	}
	out := make([]llm.Message, 0, len(rows))
	for _, m := range rows {
		switch m.Role {
		case "user":
			out = append(out, llm.Message{Role: llm.RoleUser, Content: m.Content})
		case "assistant":
			msg := llm.Message{Role: llm.RoleAssistant, Content: textFromParts(m.Parts)}
			for _, p := range parseParts(m.Parts) {
				if p.Type == "tool_call" {
					msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: p.ID, Name: p.Name, Arguments: p.Arguments})
				}
			}
			// Skip empty assistant messages (no text, no tool calls) to avoid
			// confusing the provider with blank turns.
			if msg.Content == "" && len(msg.ToolCalls) == 0 {
				continue
			}
			out = append(out, msg)
		case "tool":
			callID := ""
			for _, p := range parseParts(m.Parts) {
				if p.Type == "tool_result" {
					callID = p.CallID
				}
			}
			out = append(out, llm.Message{Role: llm.RoleTool, Content: m.Content, ToolCallID: callID})
		}
	}
	return out, nil
}

// hasPendingForMessage reports whether any tool call belonging to the given
// assistant message is still awaiting approval. Used to decide, after one
// approve/reject, whether the turn can advance or must keep waiting (an
// assistant turn may request several gated tools at once).
func (r *Runner) hasPendingForMessage(convID, msgID string) bool {
	rows, err := r.store.ListToolCalls(convID)
	if err != nil {
		// On a read failure, assume still-pending and keep waiting rather than
		// advancing the turn (which could run past an unresolved approval gate).
		slog.Warn("ai: list tool calls failed; treating turn as still pending", "conversation", convID, "error", err)
		return true
	}
	for _, tc := range rows {
		if tc.MessageID == msgID && tc.Status == "pending_approval" {
			return true
		}
	}
	return false
}

// ─── policy + small helpers ──────────────────────────────────────────────────

// approvalNeeded applies the configured policy. Reads and invokes never pause
// for approval; writes/admin depend on policy.
//
// invoke_function is intentionally approval-free even under all_writes: calling
// a function you already deployed is a read-like operation in a FaaS (it's how
// you USE the platform), and the system prompt states the same contract ("reads
// and invokes never pause"). Side effects live in the handler's own code, gated
// at deploy time and by network_mode — not by the chat approval policy. A
// destructive tool still needs its separate code-enforced confirm=true.
func (r *Runner) approvalNeeded(t Tool) bool {
	if t.ReadOnly || t.Perm == "read" || t.Perm == "invoke" {
		return false
	}
	switch r.cfg.ApprovalPolicy {
	case "auto":
		return false
	case "destructive_only":
		return t.Destructive
	default: // all_writes
		return true
	}
}

func (r *Runner) toolDefs() []llm.ToolDef {
	out := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Def)
	}
	return out
}

func parseParts(s string) []part {
	if s == "" {
		return nil
	}
	var ps []part
	_ = json.Unmarshal([]byte(s), &ps)
	return ps
}

func textFromParts(s string) string {
	var b strings.Builder
	for _, p := range parseParts(s) {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func emptyToObj(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}
