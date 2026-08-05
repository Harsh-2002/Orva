package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// Client is an embedded Bifrost gateway scoped to one KeyResolver. Construct
// it once (it owns provider connection pools); rebuild it when the set of
// configured providers changes (see ai.Manager).
type Client struct {
	bf *bifrost.Bifrost
}

// New initialises the in-process gateway. The resolver supplies credentials
// lazily, so New succeeds even with zero providers configured; requests for an
// unconfigured provider fail at call time with a clear error.
func New(resolver KeyResolver) (*Client, error) {
	bf, err := bifrost.Init(context.Background(), schemas.BifrostConfig{
		Account:         &account{resolver: resolver},
		Logger:          bifrost.NewNoOpLogger(),
		InitialPoolSize: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("init bifrost: %w", err)
	}
	return &Client{bf: bf}, nil
}

// Close releases the gateway's pools.
func (c *Client) Close() {
	if c.bf != nil {
		c.bf.Shutdown()
	}
}

// Stream runs one streaming chat completion and returns a channel of neutral
// events. The channel closes after EventDone or EventError. The request's
// context cancels the underlying provider call (e.g. on client disconnect).
func (c *Client) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	breq, err := buildRequest(req)
	if err != nil {
		return nil, err
	}

	bctx, cancel := schemas.NewBifrostContextWithCancel(ctx)
	stream, berr := c.bf.ChatCompletionStreamRequest(bctx, breq)
	// Robust thinking: some models/endpoints reject the reasoning params. When the
	// up-front request fails AND the error is specifically about reasoning, drop
	// reasoning and retry once so a "thinking on" turn degrades to a normal answer.
	// We gate on the error text so unrelated failures (bad key, unknown model,
	// network) surface immediately instead of being masked by a second doomed
	// attempt that doubles latency and hides the real cause. (Endpoints that merely
	// IGNORE reasoning don't error, so they fall through here unchanged.)
	if berr != nil && breq.Params != nil && breq.Params.Reasoning != nil && isReasoningError(bifrostErr(berr)) {
		cancel()
		breq.Params.Reasoning = nil
		bctx, cancel = schemas.NewBifrostContextWithCancel(ctx)
		stream, berr = c.bf.ChatCompletionStreamRequest(bctx, breq)
	}
	if berr != nil {
		cancel()
		return nil, errors.New(bifrostErr(berr))
	}

	out := make(chan Event, 32)
	go func() {
		defer close(out)
		defer cancel()
		pump(ctx, stream, out)
	}()

	return out, nil
}

// pump consumes one provider stream and translates it into neutral events.
// It always terminates the event stream with exactly one EventDone or
// EventError. Split out of Stream so the termination logic is unit-testable
// with a synthetic chunk channel.
func pump(ctx context.Context, stream chan *schemas.BifrostStreamChunk, out chan<- Event) {
	// Tool calls arrive incrementally (per OpenAI streaming): each delta
	// carries a tool-call index plus a fragment of the name/arguments. We
	// accumulate by index and assemble the complete calls at finish.
	byIndex := map[int]*toolAcc{}
	var order []int
	var finish string
	var usage *Usage

	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			// When the request context is already cancelled, the provider error
			// is just the wreckage of our own abort (Bifrost may surface the
			// cancellation as an error chunk instead of a bare channel close).
			// Surface the context error so callers can keep filtering routine
			// disconnects out of the error log with errors.Is.
			err := error(errors.New(bifrostErr(chunk.BifrostError)))
			if ctxErr := ctx.Err(); ctxErr != nil {
				err = ctxErr
			}
			out <- Event{Type: EventError, Err: err}
			return
		}
		resp := chunk.BifrostChatResponse
		if resp == nil {
			continue
		}
		if resp.Usage != nil {
			usage = &Usage{
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				TotalTokens:      resp.Usage.TotalTokens,
			}
		}
		for i := range resp.Choices {
			choice := resp.Choices[i]
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = *choice.FinishReason
			}
			sc := choice.ChatStreamResponseChoice
			if sc == nil || sc.Delta == nil {
				continue
			}
			d := sc.Delta
			if d.Content != nil && *d.Content != "" {
				out <- Event{Type: EventText, Text: *d.Content}
			}
			if d.Reasoning != nil && *d.Reasoning != "" {
				out <- Event{Type: EventThinking, Text: *d.Reasoning}
			}
			for _, tc := range d.ToolCalls {
				idx := int(tc.Index)
				a := byIndex[idx]
				if a == nil {
					a = &toolAcc{}
					byIndex[idx] = a
					order = append(order, idx)
				}
				if tc.ID != nil && *tc.ID != "" {
					a.id = *tc.ID
				}
				if tc.Function.Name != nil && *tc.Function.Name != "" {
					a.name = *tc.Function.Name
				}
				a.args.WriteString(tc.Function.Arguments)
			}
		}
	}

	// A healthy stream always reports a finish reason ("stop", "tool_calls", …)
	// before the provider closes it. The channel closing without one means the
	// upstream connection was reset or the response was truncated mid-stream —
	// reporting Done here would hand the caller a half answer (or half a tool
	// call) marked as success.
	if finish == "" {
		err := ctx.Err()
		if err == nil {
			err = errors.New("provider stream ended unexpectedly (connection reset or truncated response)")
		}
		// Usage may have arrived before the cut — pass it along so the turn's
		// token accounting survives the error path.
		out <- Event{Type: EventError, Err: err, Usage: usage}
		return
	}

	out <- Event{Type: EventDone, ToolCalls: assembleToolCalls(order, byIndex), FinishReason: finish, Usage: usage}
}

// toolAcc accumulates the streamed fragments of one tool call (its id, name, and
// argument chunks arrive across multiple deltas, keyed by index).
type toolAcc struct {
	id, name string
	args     strings.Builder
}

// assembleToolCalls finalizes the accumulated tool calls in arrival order. Calls
// with no name are dropped (incomplete). When an endpoint omits the tool-call id
// in its stream deltas, a stable non-empty id is synthesized from the index so
// the persisted assistant tool_call and its replayed tool result share a
// matching id — strict providers reject an empty tool_call_id on the next turn,
// which would otherwise break every multi-step turn.
func assembleToolCalls(order []int, byIndex map[int]*toolAcc) []ToolCall {
	calls := make([]ToolCall, 0, len(order))
	for _, idx := range order {
		a := byIndex[idx]
		if a == nil || a.name == "" {
			continue
		}
		id := a.id
		if id == "" {
			id = fmt.Sprintf("call_%d", idx)
		}
		calls = append(calls, ToolCall{ID: id, Name: a.name, Arguments: a.args.String()})
	}
	return calls
}

// PreparedTools is an opaque, provider-format tool catalog converted once and
// reused across the iterations of a single turn (the catalog is fixed per run).
// Keeps the Bifrost schema types from leaking past this package.
type PreparedTools struct {
	tools []schemas.ChatTool
}

// PrepareTools converts the neutral tool defs to the provider format once, so
// the agentic loop doesn't re-unmarshal every tool's JSON schema on each of its
// (up to ~25) Stream calls.
func (c *Client) PrepareTools(defs []ToolDef) (*PreparedTools, error) {
	if len(defs) == 0 {
		return &PreparedTools{}, nil
	}
	tools, err := toBifrostTools(defs)
	if err != nil {
		return nil, err
	}
	return &PreparedTools{tools: tools}, nil
}

// buildRequest translates a neutral Request into a Bifrost chat request.
func buildRequest(req Request) (*schemas.BifrostChatRequest, error) {
	if strings.TrimSpace(req.Provider) == "" || strings.TrimSpace(req.Model) == "" {
		return nil, errors.New("provider and model are required")
	}

	input := make([]schemas.ChatMessage, 0, len(req.Messages)+1)
	if s := strings.TrimSpace(req.System); s != "" {
		input = append(input, textMessage(schemas.ChatMessageRoleSystem, req.System))
	}
	for _, m := range req.Messages {
		input = append(input, toBifrostMessage(m))
	}

	params := &schemas.ChatParameters{}
	if req.Temperature != nil {
		params.Temperature = req.Temperature
	}
	// Cache the (large, stable) system prompt so Anthropic charges a fraction
	// for it after the first call.
	//
	// This MUST stay gated on the provider. cache_control is an Anthropic-family
	// field that Bifrost keeps on the shared ChatParameters struct, and its
	// OpenAI-format request marshaller only strips cache_control from message
	// and tool content blocks — the top-level params field is embedded verbatim
	// and reaches the wire. Strict OpenAI-compatible providers reject unknown
	// properties, so sending it unconditionally fails every request with
	// "property 'cache_control' is unsupported" (observed on Groq).
	if strings.TrimSpace(req.System) != "" && supportsCacheControl(req.Provider) {
		params.CacheControl = &schemas.CacheControl{Type: schemas.CacheControlTypeEphemeral}
	}
	if r := reasoningFor(req.Thinking); r != nil {
		params.Reasoning = r
	}
	// Prefer pre-converted tools (built once per turn) to avoid re-unmarshaling
	// every tool's JSON schema on every Stream call within the agentic loop.
	if req.Prepared != nil {
		params.Tools = req.Prepared.tools
	} else if len(req.Tools) > 0 {
		tools, err := toBifrostTools(req.Tools)
		if err != nil {
			return nil, err
		}
		params.Tools = tools
	}

	return &schemas.BifrostChatRequest{
		Provider: schemas.ModelProvider(strings.ToLower(req.Provider)),
		Model:    req.Model,
		Input:    input,
		Params:   params,
	}, nil
}

// supportsCacheControl reports whether the provider's native API accepts a
// top-level cache_control field. Only Anthropic does: Bifrost builds a native
// Anthropic request for it, whereas every other provider goes out through an
// OpenAI-format body where the field is either ignored or rejected outright.
func supportsCacheControl(provider string) bool {
	return schemas.ModelProvider(strings.ToLower(strings.TrimSpace(provider))) == schemas.Anthropic
}

func textMessage(role schemas.ChatMessageRole, text string) schemas.ChatMessage {
	t := text
	return schemas.ChatMessage{Role: role, Content: &schemas.ChatMessageContent{ContentStr: &t}}
}

func toBifrostMessage(m Message) schemas.ChatMessage {
	switch m.Role {
	case RoleAssistant:
		msg := schemas.ChatMessage{Role: schemas.ChatMessageRoleAssistant}
		if m.Content != "" {
			c := m.Content
			msg.Content = &schemas.ChatMessageContent{ContentStr: &c}
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]schemas.ChatAssistantMessageToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				id := tc.ID
				name := tc.Name
				// Replay only valid-JSON arguments. A persisted call whose args
				// were cut mid-stream (or emitted malformed by the model) would
				// otherwise be replayed verbatim on every subsequent iteration
				// AND every future turn of the conversation — strict providers
				// reject the whole request, permanently bricking the chat. The
				// model already saw the failure in the call's tool result.
				args := tc.Arguments
				if strings.TrimSpace(args) == "" || !json.Valid([]byte(args)) {
					args = "{}"
				}
				calls = append(calls, schemas.ChatAssistantMessageToolCall{
					Type: ptrStr("function"),
					ID:   &id,
					Function: schemas.ChatAssistantMessageToolCallFunction{
						Name:      &name,
						Arguments: args,
					},
				})
			}
			msg.ChatAssistantMessage = &schemas.ChatAssistantMessage{ToolCalls: calls}
		}
		return msg
	case RoleTool:
		c := m.Content
		id := m.ToolCallID
		return schemas.ChatMessage{
			Role:            schemas.ChatMessageRoleTool,
			Content:         &schemas.ChatMessageContent{ContentStr: &c},
			ChatToolMessage: &schemas.ChatToolMessage{ToolCallID: &id},
		}
	case RoleSystem:
		return textMessage(schemas.ChatMessageRoleSystem, m.Content)
	default:
		return textMessage(schemas.ChatMessageRoleUser, m.Content)
	}
}

func toBifrostTools(defs []ToolDef) ([]schemas.ChatTool, error) {
	out := make([]schemas.ChatTool, 0, len(defs))
	for _, d := range defs {
		fn := &schemas.ChatToolFunction{Name: d.Name}
		if d.Description != "" {
			desc := d.Description
			fn.Description = &desc
		}
		if len(d.Schema) > 0 {
			var params schemas.ToolFunctionParameters
			if err := json.Unmarshal(d.Schema, &params); err != nil {
				return nil, fmt.Errorf("tool %s: bad schema: %w", d.Name, err)
			}
			fn.Parameters = &params
		}
		out = append(out, schemas.ChatTool{Type: schemas.ChatToolTypeFunction, Function: fn})
	}
	return out, nil
}

// isReasoningError reports whether an upstream error is about the reasoning /
// thinking parameters (so the caller can safely retry without them) rather than
// an unrelated failure like auth, an unknown model, or the network.
func isReasoningError(msg string) bool {
	m := strings.ToLower(msg)
	for _, kw := range []string{"reasoning", "thinking", "reasoning_effort", "budget", "effort"} {
		if strings.Contains(m, kw) {
			return true
		}
	}
	return false
}

// bifrostErr renders a Bifrost error to a readable string.
func bifrostErr(e *schemas.BifrostError) string {
	if e == nil {
		return "unknown bifrost error"
	}
	if s := e.GetErrorString(); s != "" {
		return s
	}
	return e.String()
}
