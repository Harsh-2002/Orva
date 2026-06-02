package llm

import "github.com/maximhq/bifrost/core/schemas"

// reasoningFor maps Orva's three UI thinking levels onto Bifrost's neutral
// reasoning config. Bifrost normalises this per provider: OpenAI/Bedrock use
// the effort string, Anthropic/Gemini/Cohere use the token budget. We set both
// so whichever strategy the chosen provider uses is satisfied. Anthropic
// requires a budget ≥1024, so the smallest budget we ever send is 4096.
//
// Returns nil for "off" (and any unknown value), which disables reasoning.
func reasoningFor(level string) *schemas.ChatReasoning {
	switch level {
	case "standard":
		return &schemas.ChatReasoning{
			Enabled:   ptrBool(true),
			Effort:    ptrStr("medium"),
			MaxTokens: ptrInt(4096),
		}
	case "deep":
		return &schemas.ChatReasoning{
			Enabled:   ptrBool(true),
			Effort:    ptrStr("high"),
			MaxTokens: ptrInt(16384),
		}
	default: // "off" or unrecognised
		return nil
	}
}

func ptrStr(s string) *string    { return &s }
func ptrBool(b bool) *bool       { return &b }
func ptrInt(i int) *int          { return &i }
