//go:build linux

package workloadfence

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// RemoveRetiredRootUnderBudget is cleanup-only for the trusted host owner
// replaying an authenticated, committed retirement. It never fences or proves death.
func RemoveRetiredRootUnderBudget(ctx context.Context, budget *Budget, root, parent string, expected EpochReference) error {
	if ctx == nil || budget == nil || budget.s == nil || expected.Version != 1 ||
		expected.Depth != 0 || expected.InstanceEpoch != expected.Epoch || expected.ParentEpoch != "" ||
		len(expected.Epoch) != 32 || strings.ToLower(expected.Epoch) != expected.Epoch ||
		!validBootID(expected.Boot) || expected.Boot == zeroBootID {
		return ErrInvalid
	}
	if _, err := hex.DecodeString(expected.Epoch); err != nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b := budget.s
	if err := b.retain(parent); err != nil {
		return err
	}
	defer b.release()
	if expected.Boot != b.boot {
		return nil
	}

	path := filepath.Join(parent, "epoch-"+expected.Epoch)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil // Only meaningful after the wider owner has committed retirement.
	} else if err != nil {
		return err
	}
	dir, err := openPrivateRoot(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	rec, err := readRecordAt(dir)
	if err != nil {
		return err
	}
	metadataID, err := identity(dir)
	if err != nil || metadataID != rec.Root || !rec.Closed || rootEpochReference(rec) != expected {
		return ErrIntegrity
	}
	b.mu.Lock()
	err = b.checkParent(parent, &rec.Parent)
	b.mu.Unlock()
	if err != nil {
		return err
	}
	group, err := openCgroup(path)
	if err != nil {
		return err
	}
	defer group.Close()
	id, err := identity(group)
	if err != nil || id != rec.Group {
		return ErrIntegrity
	}
	mount, err := descriptorMountID(group)
	if err != nil || mount != b.mountID {
		return ErrIntegrity
	}
	empty, err := unpopulated(path)
	if err != nil {
		return err
	}
	if !empty {
		return ErrIntegrity
	}
	count := 0
	return removeEmptyGroups(ctx, path, 0, &count, mount)
}
