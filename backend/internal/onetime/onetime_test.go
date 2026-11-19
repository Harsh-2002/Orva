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
