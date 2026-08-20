package oauth

import (
	"strings"
	"testing"
)

// TestClientScopeIsACeiling — the registered scope was only ever used to
// fill in an omitted value, and the requested scope was then validated
// against the globally-supported set alone. A client registered with
// scope="read" could ask for "admin" and be issued it. IntersectScope
// existed for exactly this and had no callers, despite documenting that
// "the issued token never exceeds either side".
func TestClientScopeIsACeiling(t *testing.T) {
	cases := []struct {
		name       string
		requested  string
		registered string
		want       []string
		denied     []string
	}{
		{"escalation refused", "admin", "read", nil, []string{"admin"}},
		{"partial escalation trimmed", "read write admin", "read write",
			[]string{"read", "write"}, []string{"admin"}},
		{"within ceiling preserved", "read", "read write", []string{"read"}, []string{"write"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormaliseScope(IntersectScope(ParseScope(c.requested), ParseScope(c.registered)))
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("scope %q lost %q", got, w)
				}
			}
			for _, d := range c.denied {
				if strings.Contains(got, d) {
					t.Errorf("scope %q escalated to %q beyond the client's registration %q",
						got, d, c.registered)
				}
			}
		})
	}
}

// TestEmptyIntersectionIsRejected — a request with nothing in common with
// the registration must not fall through as an empty (and therefore
// unchecked) scope.
func TestEmptyIntersectionIsRejected(t *testing.T) {
	got := NormaliseScope(IntersectScope(ParseScope("admin"), ParseScope("read")))
	if strings.TrimSpace(got) != "" {
		t.Fatalf("expected an empty intersection, got %q", got)
	}
}
