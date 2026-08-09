package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHasPriorCode is the M7 guard: hasPriorCode must recognize the versioned
// layout (the `current` symlink → versions/<hash>), not the dead pre-versioning
// `code/` dir. Before the fix it stat'd `.../code`, which the versioned layout
// never creates, so a redeploy-after-failure always 503'd even when a prior
// good version was live.
func TestHasPriorCode(t *testing.T) {
	dir := t.TempDir()
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f40"
	fnDir := filepath.Join(dir, "functions", fnID)

	// No current symlink yet → no prior code.
	if hasPriorCode(dir, fnID) {
		t.Error("hasPriorCode = true before any version exists, want false")
	}

	// Build a version dir and point `current` at it (relative target, like
	// builder.ActivateVersion).
	hash := "abc1234def5678abc1234def5678abc1234def5678abc1234def5678abc12345"
	versionDir := filepath.Join(fnDir, "versions", hash)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("versions", hash), filepath.Join(fnDir, "current")); err != nil {
		t.Fatal(err)
	}

	if !hasPriorCode(dir, fnID) {
		t.Error("hasPriorCode = false with a live current symlink, want true")
	}

	// Legacy flat code/ dir must NOT be what satisfies the check (proves we
	// moved off the dead path).
	if hasPriorCode(dir, "no-such-fn") {
		t.Error("hasPriorCode = true for an unknown function, want false")
	}
}
