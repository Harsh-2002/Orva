package onetime

import "testing"

func TestRedactIsIdempotentAndValuesFindsCredentials(t *testing.T) {
	in := `{"id":"1","secret":"abc-def-ghi-jkl-mno-pqr"}`
	once, changed := Redact(in, []string{"secret"})
	if !changed || once != `{"id":"1","secret":"`+Placeholder+`"}` {
		t.Fatalf("first pass: %q %v", once, changed)
	}
	if again, changed := Redact(once, []string{"secret"}); changed || again != once {
		t.Fatalf("second pass altered the result: %q %v", again, changed)
	}
	if v, ok := Values(in, []string{"secret"}); !ok || len(v) != 1 || v[0] != "abc-def-ghi-jkl-mno-pqr" {
		t.Fatalf("values: %v %v", v, ok)
	}
	if v, _ := Values(once, []string{"secret"}); len(v) != 0 {
		t.Fatalf("placeholder reported as a credential: %v", v)
	}
	if _, ok := Values(`{"secret":"x"} junk`, []string{"secret"}); ok {
		t.Fatal("malformed input reported as locatable")
	}
}

func TestRedactMatchingOnlyReplacesProvenValues(t *testing.T) {
	allowed := map[string]bool{"real-credential-value": true}
	if got, ok := RedactMatching(`{"key":"real-credential-value","n":1}`, []string{"key"}, allowed); !ok || got != `{"key":"`+Placeholder+`","n":1}` {
		t.Fatalf("matching value: %q %v", got, ok)
	}
	// Same field name, a different value: not provable, so untouched.
	for _, in := range []string{`{"key":"unrelated-setting"}`, `{"key":7}`, `not json`, `{"other":"real-credential-value"}`} {
		if got, ok := RedactMatching(in, []string{"key"}, allowed); ok || got != in {
			t.Fatalf("%q was changed to %q", in, got)
		}
	}
	if _, ok := RedactMatching(`{"key":"x"}`, []string{"key"}, nil); ok {
		t.Fatal("no allowed values must never redact")
	}
}
