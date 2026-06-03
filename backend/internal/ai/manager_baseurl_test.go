package ai

import (
	"context"
	"strings"
	"testing"
)

func TestWithBaseURLRoundTrip(t *testing.T) {
	ctx := WithBaseURL(context.Background(), "https://orva.example.test/")
	if got := baseURLFromContext(ctx); got != "https://orva.example.test" {
		t.Errorf("baseURLFromContext = %q, want trailing-slash-trimmed value", got)
	}
	// Empty base URL must not pollute the context (so callers fall through to the
	// no-base-URL behaviour rather than an empty string masquerading as a value).
	if got := baseURLFromContext(WithBaseURL(context.Background(), "  ")); got != "" {
		t.Errorf("blank base URL leaked into context: %q", got)
	}
	if got := baseURLFromContext(context.Background()); got != "" {
		t.Errorf("bare context returned %q, want empty", got)
	}
}

func TestAppendInstanceContext(t *testing.T) {
	const base = "https://orva.example.test"
	out := appendInstanceContext("BASE PROMPT", base, "v2026.06.03")

	for _, want := range []string{
		"BASE PROMPT",       // original prompt preserved as the prefix
		"# This instance",   // addendum section header
		base + "/fn/",       // real invoke URL form
		"v2026.06.03",       // running version surfaced
		"never substitute a placeholder",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("instance context missing %q\n---\n%s", want, out)
		}
	}
	if !strings.HasPrefix(out, "BASE PROMPT") {
		t.Error("addendum must be appended after the base prompt, not prepended")
	}

	// Version is optional — omit the line entirely when unknown.
	noVer := appendInstanceContext("P", base, "")
	if strings.Contains(noVer, "Running version") {
		t.Error("blank version should not emit a 'Running version' line")
	}
}
