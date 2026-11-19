//go:build linux

package builder

import (
	"strings"
	"testing"
)

func TestPublicationWireStrict(t *testing.T) {
	for _, raw := range []string{
		`{"code":"x","code":"y"}`, `{"Code":"x"}`, `{"code":"x","operator":{"admin":true}}`,
		`{"code":"x","artifact":"claim"}`, `{"code":"x","epoch":"1"}`, `{"code":null}`,
		`{"code":"x","extras":{}}`, `{"code":"x","dependencies":"{}"}`, `{"code":"\ud800"}`,
		"{\"code\":\"\xff\"}", `{"code":"x"} {}`, `[]`,
		`{"code":"x","function_id":"tool-field"}`, `{"code":"x","wait":false}`,
		strings.Repeat(" ", MaxPublicationRequestBytes) + `{"code":"x"}`,
	} {
		if _, err := DecodePublicationInline([]byte(raw), false); err == nil {
			t.Fatal("ambiguous input accepted")
		}
	}
	for _, raw := range []string{`{"code":"x","function_id":"id","wait":false,"wait":true}`, `{"code":"x","function_id":"id"}`,
		`{"code":"x","function_id":"id","wait":false,"provenance":{}}`} {
		if _, err := DecodePublicationInline([]byte(raw), true); err == nil {
			t.Fatal("ambiguous raw tool input accepted")
		}
	}
	for _, raw := range []string{`{"code":"hello"}`, `{"code":"\ud83d\ude00"}`, `{"code":"literal \\\\u text","dependencies":""}`} {
		if _, err := DecodePublicationInline([]byte(raw), false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublicationConstructorDeniesLegacyBuilder(t *testing.T) {
	if q, err := NewPublicationQueue(New(), nil, nil, nil, nil, nil, nil, nil); err == nil || q != nil {
		t.Fatal("partial mode enabled")
	}
}
