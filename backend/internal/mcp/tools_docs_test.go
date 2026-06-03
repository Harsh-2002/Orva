package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestGetOrvaDocsOriginFallback verifies the docs tool substitutes the
// per-request/per-turn Deps.BaseURL into {{ORIGIN}} when the caller omits an
// explicit origin — so the in-product agent (which forgets the origin arg far
// more often than it passes it) still gets real, pasteable URLs instead of the
// generic placeholder.
func TestGetOrvaDocsOriginFallback(t *testing.T) {
	const base = "https://orva.example.test"
	reg := BuildAgentRegistry(Deps{BaseURL: base}, permSet{"read": true})

	out, err := reg.Dispatch(context.Background(), "get_orva_docs", []byte(`{}`))
	if err != nil {
		t.Fatalf("dispatch get_orva_docs: %v", err)
	}
	res, ok := out.(GetOrvaDocsOutput)
	if !ok {
		t.Fatalf("unexpected output type %T", out)
	}
	if res.Origin != base {
		t.Errorf("origin = %q, want %q (Deps.BaseURL fallback)", res.Origin, base)
	}
	if strings.Contains(res.Markdown, "{{ORIGIN}}") {
		t.Error("markdown still contains unsubstituted {{ORIGIN}} placeholder")
	}
	if strings.Contains(res.Markdown, "your-orva-instance.example.com") {
		t.Error("markdown fell back to the generic placeholder despite a Deps.BaseURL")
	}
	// The whole reference is always returned — one document, no slicing.
	if res.ByteCount < 30000 {
		t.Errorf("expected the full reference, got only %d bytes", res.ByteCount)
	}
}

// TestGetOrvaDocsExplicitOriginWins confirms an explicit origin argument still
// takes precedence over the Deps.BaseURL fallback.
func TestGetOrvaDocsExplicitOriginWins(t *testing.T) {
	reg := BuildAgentRegistry(Deps{BaseURL: "https://from-deps.test"}, permSet{"read": true})
	out, err := reg.Dispatch(context.Background(), "get_orva_docs", []byte(`{"origin":"https://explicit.test/"}`))
	if err != nil {
		t.Fatalf("dispatch get_orva_docs: %v", err)
	}
	res := out.(GetOrvaDocsOutput)
	if res.Origin != "https://explicit.test" {
		t.Errorf("origin = %q, want the explicit (trailing-slash-trimmed) value", res.Origin)
	}
}

// TestGetOrvaDocsPlaceholderWhenNoBaseURL confirms the generic placeholder is
// still used when neither an explicit origin nor a Deps.BaseURL is available.
func TestGetOrvaDocsPlaceholderWhenNoBaseURL(t *testing.T) {
	reg := BuildAgentRegistry(Deps{}, permSet{"read": true})
	out, err := reg.Dispatch(context.Background(), "get_orva_docs", []byte(`{}`))
	if err != nil {
		t.Fatalf("dispatch get_orva_docs: %v", err)
	}
	res := out.(GetOrvaDocsOutput)
	if res.Origin != "https://your-orva-instance.example.com" {
		t.Errorf("origin = %q, want the generic placeholder", res.Origin)
	}
}
