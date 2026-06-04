package builder

import (
	"strings"
	"testing"
)

// TestActivateVersionRejectsBadHash ensures a traversal/garbage code hash never
// reaches the symlink target (path-injection defense).
func TestActivateVersionRejectsBadHash(t *testing.T) {
	dir := t.TempDir()
	bad := []string{"../../etc", "..", "abc", "ZZZ", strings.Repeat("g", 64), "/abs/path"}
	for _, h := range bad {
		if err := ActivateVersion(dir, "fn1", h); err == nil {
			t.Errorf("ActivateVersion accepted invalid hash %q", h)
		}
	}
	// A well-formed 64-hex hash passes validation (it then fails later only
	// because the version dir doesn't exist, which is fine — not our concern).
	good := strings.Repeat("a", 64)
	if err := ActivateVersion(dir, "fn1", good); err != nil && strings.Contains(err.Error(), "invalid code hash") {
		t.Errorf("ActivateVersion wrongly rejected a valid hash: %v", err)
	}
}

func TestIsHexHash(t *testing.T) {
	if !isHexHash(strings.Repeat("a", 64)) {
		t.Error("valid 64-hex rejected")
	}
	for _, s := range []string{"", "abc", strings.Repeat("A", 64), strings.Repeat("a", 63), "../" + strings.Repeat("a", 61)} {
		if isHexHash(s) {
			t.Errorf("isHexHash accepted invalid %q", s)
		}
	}
}
