package ids

import (
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewIsUUIDv7(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := New()
		u, err := uuid.Parse(s)
		if err != nil {
			t.Fatalf("not a parseable UUID: %q (err=%v)", s, err)
		}
		if u.Version() != 7 {
			t.Fatalf("wrong version %d in %q (want 7)", u.Version(), s)
		}
		// RFC 4122 variant = 0b10xx — UUIDv7 inherits this.
		if u.Variant() != uuid.RFC4122 {
			t.Fatalf("wrong variant %v in %q", u.Variant(), s)
		}
	}
}

func TestNewMonotonic(t *testing.T) {
	// UUIDv7 prefixes 48 bits of unix-millis. In a tight loop the
	// timestamp may collide; the google/uuid library's NewV7
	// implementation handles intra-millisecond ordering via the
	// rand_a field. Sorted lex order should match generation order.
	const N = 10_000
	gen := make([]string, N)
	for i := 0; i < N; i++ {
		gen[i] = New()
	}
	sorted := make([]string, N)
	copy(sorted, gen)
	sort.Strings(sorted)
	mismatches := 0
	for i := range gen {
		if gen[i] != sorted[i] {
			mismatches++
		}
	}
	// Allow a tiny slack — the library's monotonic guarantee can
	// reset on rare clock-stall edge cases. >1% drift would mean
	// monotonicity is genuinely broken.
	if mismatches > N/100 {
		t.Fatalf("monotonicity broken: %d/%d out-of-order", mismatches, N)
	}
}

func TestNewUnique(t *testing.T) {
	const N = 10_000
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		s := New()
		if _, dup := seen[s]; dup {
			t.Fatalf("collision after %d generations: %q", i, s)
		}
		seen[s] = struct{}{}
	}
}

func TestNewCanonicalForm(t *testing.T) {
	s := New()
	if len(s) != 36 {
		t.Fatalf("expected 36-char canonical form, got %d: %q", len(s), s)
	}
	// Standard 8-4-4-4-12 dash positions.
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		t.Fatalf("dash positions wrong in %q", s)
	}
	if strings.ToLower(s) != s {
		t.Fatalf("expected lowercase, got %q", s)
	}
}

func TestIsUUID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{New(), true},
		{"01939a6e-3a4f-7b8c-9d2e-f1a2b3c4d5e6", true}, // v7 example
		{"00000000-0000-0000-0000-000000000000", true}, // nil UUID parses
		{"my-function-name", false},
		{"send-receipt", false},
		{"fn_ttp836b9x3m1", false}, // legacy prefix-typed ID
		{"", false},
		{"not-a-uuid", false},
	}
	for _, c := range cases {
		got := IsUUID(c.in)
		if got != c.want {
			t.Errorf("IsUUID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
