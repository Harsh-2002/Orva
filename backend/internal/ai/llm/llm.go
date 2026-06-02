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
	if berr != nil {
		cancel()
		return nil, errors.New(bifrostErr(berr))
	}

	out := make(chan Event, 32)
	go func() {
		defer close(out)
		defer cancel()

		// Tool calls arrive incrementally (per OpenAI streaming): each delta
		// carries a tool-call index plus a fragment of the name/arguments. We
		// accumulate by index and assemble the complete calls at finish.
		type acc struct {
			id, name string
			args     strings.Builder
		}
		byIndex := map[int]*acc{}
		var order []int
		var finish string
		var usage *Usage

		for chunk := range stream {
			if chunk == nil {
				continue
			}
			if chunk.BifrostError != nil {
				out <- Event{Type: EventError, Err: errors.New(bifrostErr(chunk.BifrostError))}
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
						a = &acc{}
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

		calls := make([]ToolCall, 0, len(order))
		for _, idx := range order {
			a := byIndex[idx]
			if a.name == "" {
				continue
			}
			calls = append(calls, ToolCall{ID: a.id, Name: a.name, Arguments: a.args.String()})
		}
		out <- Event{Type: EventDone, ToolCalls: calls, FinishReason: finish, Usage: usage}
	}()

	return out, nil
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
	// Cache the (large, stable) system prompt so providers that support prompt
	// caching charge a fraction for it after the first call. Bifrost applies
	// this only to provider families that support it (Anthropic); OpenAI-family
	// providers cache automatically and ignore the field, so this is safe for
	// custom OpenAI-compatible endpoints too.
	if strings.TrimSpace(req.System) != "" {
		params.CacheControl = &schemas.CacheControl{Type: schemas.CacheControlType("ephemeral")}
	}
	if r := reasoningFor(req.Thinking); r != nil {
		params.Reasoning = r
	}
	if len(req.Tools) > 0 {
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
				calls = append(calls, schemas.ChatAssistantMessageToolCall{
					Type: ptrStr("function"),
					ID:   &id,
					Function: schemas.ChatAssistantMessageToolCallFunction{
						Name:      &name,
						Arguments: tc.Arguments,
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
