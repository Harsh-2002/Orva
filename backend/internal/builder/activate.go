package builder

import (
	"fmt"
	"os"
	"path/filepath"
)

// ActivateVersion atomically points `<dataDir>/functions/<fnID>/current`
// at `versions/<codeHash>`. The two-step symlink + rename pattern avoids
// the empty-window that os.RemoveAll(current) + os.Symlink would create:
// rename(2) on a symlink is atomic on Linux ext4/xfs.
//
// The link target is a relative path ("versions/<hash>") so the same
// symlink resolves correctly if the data directory is moved or if the
// container's mountpoint changes. nsjail does not deref symlinks at the
// bind-mount source — `-R cfg.CodeDir:/code` binds the symlink path
// itself — but the spawn closure resolves the link fresh on each Spawn,
// so RefreshForDeploy draining workers is sufficient for the next spawn
// to pick up the new target.
func ActivateVersion(dataDir, fnID, codeHash string) error {
	if codeHash == "" {
		return fmt.Errorf("activate: empty code hash")
	}
	fnDir := filepath.Join(dataDir, "functions", fnID)
	target := filepath.Join("versions", codeHash) // relative; see comment above

	tmp := filepath.Join(fnDir, "current.tmp."+randSuffix())
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("activate: symlink: %w", err)
	}
	current := filepath.Join(fnDir, "current")
	if err := os.Rename(tmp, current); err != nil {
		// Best-effort cleanup of the tmp link so we don't leave debris.
		_ = os.Remove(tmp)
		return fmt.Errorf("activate: atomic rename: %w", err)
	}
	return nil
}

// ResolveActiveHash reads `<dataDir>/functions/<fnID>/current` and returns
// the hash it points at, or "" if the symlink is missing or malformed.
// Used by GC to know which version not to delete.
func ResolveActiveHash(dataDir, fnID string) string {
	link, err := os.Readlink(filepath.Join(dataDir, "functions", fnID, "current"))
	if err != nil {
		return ""
	}
	// Expecting "versions/<hash>"; strip the prefix.
	dir, hash := filepath.Split(link)
	if filepath.Clean(dir) != "versions" {
		return ""
	}
	return hash
}
