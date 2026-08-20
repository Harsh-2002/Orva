package safepath

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsTraversal(t *testing.T) {
	bad := []string{
		"",
		"   ",
		".",
		"..",
		"../handler.js",
		"../../../.admin-key",
		"a/../../b",
		"/etc/passwd",
		`\etc\passwd`,
		"C:\\Windows\\system32",
	}
	for _, rel := range bad {
		t.Run(rel, func(t *testing.T) {
			if err := Validate(rel); err == nil {
				t.Errorf("Validate(%q) = nil, want an error", rel)
			}
		})
	}
}

func TestValidateAcceptsOrdinaryEntrypoints(t *testing.T) {
	good := []string{
		"handler.js",
		"handler.py",
		"src/index.ts",
		"dist/handler.js",
		"a/b/c/deep.js",
		"./handler.js",
	}
	for _, rel := range good {
		t.Run(rel, func(t *testing.T) {
			if err := Validate(rel); err != nil {
				t.Errorf("Validate(%q) = %v, want nil", rel, err)
			}
		})
	}
}

// TestJoinContainsTraversal is the read-side half. Rows written before
// validation existed still hold whatever they hold, so Join must contain
// them rather than trusting the stored value.
func TestJoinContainsTraversal(t *testing.T) {
	base := filepath.Join("/data", "functions", "abc", "current")

	// The concrete exploit: three levels up from <dataDir>/functions/<id>/current
	// lands on <dataDir>/.admin-key, the plaintext bootstrap admin key.
	got, err := Join(base, "../../../.admin-key")
	if err == nil {
		t.Fatalf("Join escaped to %q; want an error", got)
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestJoinResolvesNormalPaths(t *testing.T) {
	base := filepath.Join("/data", "functions", "abc", "current")
	got, err := Join(base, "src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "src", "index.ts")
	if got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}
}

// TestJoinRejectsSiblingPrefix — a naive HasPrefix check without the
// separator would let "<base>-evil" pass as inside "<base>".
func TestJoinRejectsSiblingPrefix(t *testing.T) {
	if _, err := Join("/data/functions", "../functions-evil/x"); err == nil {
		t.Error("a sibling directory sharing the base's prefix must not pass")
	}
}
