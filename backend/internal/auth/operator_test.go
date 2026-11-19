package auth

import (
	"context"
	"strings"
	"testing"
)

const operatorID = "0190a1b2-c3d4-7e5f-8a6b-1c2d3e4f5a6b"

func TestOperatorCaptureIsOwnedTokenFreeProvenance(t *testing.T) {
	for _, kind := range []string{KindAPIKey, KindOAuth, KindSession} {
		t.Run(kind, func(t *testing.T) {
			p := &Principal{Kind: kind, ID: operatorID, Label: "not identity", Perms: PermSet{"admin": true}}
			if kind == KindOAuth {
				p.ExpectedResource = "https://operator.invalid/mcp"
			}
			expected := OperatorReference{Kind: kind, ID: operatorID, ExpectedResource: p.ExpectedResource}
			ctx := WithOperator(context.Background(), p)
			p.ID, p.Kind, p.ExpectedResource = "modified", KindChannel, "modified"
			p.Perms["admin"] = false
			ref, ok := OperatorFromContext(ctx)
			if !ok || ref != expected {
				t.Fatal("principal mutation changed captured provenance")
			}
			ref.ID = "returned copy"
			if got, _ := OperatorFromContext(ctx); got != expected {
				t.Fatal("returned reference aliases context")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if got, ok := OperatorFromContext(cancelled); !ok || got != expected {
				t.Fatal("derived request context lost provenance")
			}
		})
	}
}

func TestInvalidCaptureClearsInheritedOperator(t *testing.T) {
	parent := WithOperator(context.Background(), &Principal{Kind: KindAPIKey, ID: operatorID})
	for _, p := range []*Principal{
		nil, {Kind: KindChannel, ID: operatorID},
		{Kind: "system", ID: operatorID},
		{Kind: KindAPIKey, ID: "cookie-prefix"},
		{Kind: KindSession, ID: strings.ToUpper(operatorID)},
		{Kind: KindOAuth, ID: operatorID},
		{Kind: KindOAuth, ID: operatorID, ExpectedResource: "bad\x00resource"},
		{Kind: KindOAuth, ID: operatorID, ExpectedResource: strings.Repeat("x", 4097)},
		{Kind: KindOAuth, ID: operatorID, ExpectedResource: "\xff"},
		{Kind: KindAPIKey, ID: operatorID, ExpectedResource: "unexpected"},
		{Kind: KindSession, ID: operatorID, ExpectedResource: "unexpected"},
	} {
		if _, ok := OperatorFromContext(WithOperator(parent, p)); ok {
			t.Fatal("invalid capture inherited previous operator authority")
		}
	}
	if _, ok := OperatorFromContext(nil); ok {
		t.Fatal("nil context produced provenance")
	}
	if _, ok := OperatorFromContext(context.WithValue(context.Background(), "operator", OperatorReference{Kind: KindAPIKey, ID: operatorID})); ok {
		t.Fatal("string context key forged provenance")
	}
}
