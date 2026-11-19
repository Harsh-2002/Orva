package workloadfence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// RemovedSameBoot is kernel evidence of root and parent removal, not a
// Domain, fence Proof or cleanup authority. Its zero value proves nothing.
type RemovedSameBoot struct{ removed bool }

func (r RemovedSameBoot) Verified() bool { return r.removed }

// ValidateRemovedSameBootUnderBudget accepts only an authenticated root
// reference with a parent identity. An intact epoch returns no evidence and
// must be reopened and fenced normally.
func ValidateRemovedSameBootUnderBudget(ctx context.Context, budget *Budget, root, parent string, expected EpochReference) (RemovedSameBoot, error) {
	var none RemovedSameBoot
	if ctx == nil || budget == nil || budget.s == nil || expected.ParentInode == 0 {
		return none, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return none, err
	}
	s, err := openMetadata(root, parent)
	if err != nil {
		return none, err
	}
	defer s.dispose()
	b := budget.s
	rec, err := readRootEpoch(s.root, expected, b.boot)
	if err != nil {
		return none, err
	}
	current, err := openPrivateRoot(root)
	if err != nil {
		return none, err
	}
	defer current.Close()
	if reread, err := readRootEpoch(current, expected, b.boot); err != nil || reread != rec {
		return none, ErrIntegrity
	}
	return inspectSameBootRemoval(ctx, b, parent, expected)
}

func inspectSameBootRemoval(ctx context.Context, b *budgetState, path string, ref EpochReference) (RemovedSameBoot, error) {
	var none RemovedSameBoot
	if strconv.IntSize != 64 || ref.Version != 1 || ref.Boot != b.boot ||
		ref.ParentInode == 0 || ref.GroupInode == 0 || ref.ParentInode == ref.GroupInode ||
		ref.ParentDevice != ref.GroupDevice || !strictChild(b.path, path) ||
		b.servingParent != "" && b.servingParent != path {
		return none, ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.disposed {
		return none, ErrClosed
	}
	if err := b.check(""); err != nil {
		return none, err
	}
	if ref.ParentDevice != b.ids[0].Device {
		return none, ErrIntegrity
	}
	rel, err := filepath.Rel(b.path, path)
	if err != nil {
		return none, err
	}
	parent, err := b.openRemovalPath(b.objects[1], rel)
	if err != nil {
		return none, err
	}
	parentSame, groupSame := false, false
	if parent != nil {
		defer parent.Close()
		id, err := identity(parent)
		if err != nil {
			return none, err
		}
		parentSame = id == (objectID{ref.ParentDevice, ref.ParentInode})
		group, err := b.openRemovalPath(parent, "epoch-"+ref.Epoch)
		if err != nil {
			return none, err
		}
		if group != nil {
			defer group.Close()
			id, err := identity(group)
			if err != nil {
				return none, err
			}
			groupSame = id == (objectID{ref.GroupDevice, ref.GroupInode})
		}
	}
	if err := b.check(""); err != nil {
		return none, err
	}
	if err := ctx.Err(); err != nil {
		return none, err
	}
	return removedSameBootResult(parentSame, groupSame)
}

func removedSameBootResult(parentSame, groupSame bool) (RemovedSameBoot, error) {
	if parentSame != groupSame {
		return RemovedSameBoot{}, ErrIntegrity
	}
	return RemovedSameBoot{removed: !parentSame}, nil
}

// Resolve in the pinned cgroup mount; a covered path or a symlink must not
// look like a missing kernfs node.
func (b *budgetState) openRemovalPath(from *os.File, rel string) (*os.File, error) {
	fd, err := unix.Openat2(int(from.Fd()), rel, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
	if errors.Is(err, syscall.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), rel)
	if id, err := descriptorMountID(f); err != nil || id != b.mountID {
		f.Close()
		return nil, ErrIntegrity
	}
	return f, nil
}
