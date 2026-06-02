// Package llm wraps the embedded Bifrost AI gateway (github.com/maximhq/bifrost)
// behind a small, provider-neutral interface. The agent loop works only with
// the types here — Message, ToolDef, Request, Event — and never sees a Bifrost
// schema type, so the gateway stays swappable and the loop stays testable.
package llm

import "encoding/json"

// Role is a chat message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is one tool invocation the model requested (or that we replay back
// to the model on the next turn). Arguments is the raw JSON string the model
// produced — it may need repair, so it is kept as a string until dispatch.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message is one entry in the conversation sent to the model.
//   - user/system: Content only.
//   - assistant:   Content and/or ToolCalls.
//   - tool:        Content (the result) + ToolCallID it answers.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // assistant only
	ToolCallID string     // tool only
}

// ToolDef is a tool the model may call. Schema is a JSON Schema object for the
// arguments (produced by the agent tool registry).
type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Request is one streaming chat completion.
type Request struct {
	Provider    string // openai | anthropic | bedrock | gemini | groq | ollama | …
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDef
	Thinking    string   // off | standard | deep
	Temperature *float64 // nil = provider default
}

// Usage is token accounting for one model turn.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens"`
}

// EventType discriminates the streamed events from Stream.
type EventType string

const (
	EventText     EventType = "text"     // a text token delta
	EventThinking EventType = "thinking" // a reasoning token delta
	EventDone     EventType = "done"     // model turn finished (carries assembled tool calls + usage)
	EventError    EventType = "error"    // fatal error; stream ends after this
)

// Event is one item on the stream returned by Client.Stream.
type Event struct {
	Type EventType

	// Text is set for EventText and EventThinking (the incremental delta).
	Text string

	// ToolCalls is set on EventDone: the fully-assembled tool calls the model
	// requested this turn (empty if the model produced only text).
	ToolCalls []ToolCall

	// FinishReason and Usage are set on EventDone when the provider reports them.
	FinishReason string
	Usage        *Usage

	// Err is set on EventError.
	Err error
}
