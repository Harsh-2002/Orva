//go:build linux

package workloadfence

import (
	"context"
	"io"
	"os"
)

const zeroBootID = "00000000-0000-0000-0000-000000000000"

// ValidatePriorBootUnderBudget checks an exact durable root reference supplied
// by a trusted owner of the same persistent state and kernel lineage. The live
// budget and parent are current-boot authority, not old cgroup identities.
// Success is a validation outcome only; it grants no Domain or fence Proof.
func ValidatePriorBootUnderBudget(ctx context.Context, budget *Budget, root, parent string, expected EpochReference) error {
	if ctx == nil || budget == nil || budget.s == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b := budget.s
	s, err := openOwnerWithBudget(root, parent, b)
	if err != nil {
		return err
	}
	defer s.dispose()

	pin, err := openCgroup(parent)
	if err != nil {
		return err
	}
	defer pin.Close()
	parentID, err := identity(pin)
	if err != nil {
		return err
	}
	mount, err := descriptorMountID(pin)
	if err != nil {
		return err
	}
	if mount != b.mountID {
		return ErrIntegrity
	}
	prior, err := readPriorBootEpoch(s.root, expected, b.boot)
	if err != nil {
		return err
	}

	// Keep the original flock while checking the path still names that record.
	currentRoot, err := openPrivateRoot(root)
	if err != nil {
		return err
	}
	defer currentRoot.Close()
	current, err := readPriorBootEpoch(currentRoot, expected, b.boot)
	if err != nil {
		return err
	}
	if current != prior {
		return ErrIntegrity
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.disposed {
		return ErrClosed
	}
	if err := b.checkParent(parent, &parentID); err != nil {
		return err
	}
	return ctx.Err()
}

func readPriorBootEpoch(dir *os.File, expected EpochReference, liveBoot string) (record, error) {
	r, err := readRootEpoch(dir, expected, liveBoot)
	if err == nil && r.Boot == liveBoot {
		return record{}, ErrIntegrity
	}
	return r, err
}

func readRootEpoch(dir *os.File, expected EpochReference, liveBoot string) (record, error) {
	names, err := dir.Readdirnames(2)
	if err != nil && err != io.EOF {
		return record{}, err
	}
	if len(names) != 1 || names[0] != "epoch.json" {
		return record{}, ErrInvalid
	}
	r, err := readRecordAt(dir)
	if err != nil {
		return record{}, err
	}
	if r.Version != 1 {
		return record{}, ErrUnsupported
	}
	if !validBootID(liveBoot) || liveBoot == zeroBootID || r.Boot == zeroBootID {
		return record{}, ErrInvalid
	}
	if r.Group == r.Parent || r.Group.Device != r.Parent.Device {
		return record{}, ErrIntegrity
	}
	root, err := identity(dir)
	if err != nil {
		return record{}, err
	}
	if root != r.Root || !rootEpochReference(r).Matches(expected) {
		return record{}, ErrIntegrity
	}
	return r, nil
}

func rootEpochReference(r record) EpochReference {
	return EpochReference{Version: r.Version, Epoch: r.Epoch, Boot: r.Boot, InstanceEpoch: r.Epoch,
		MetadataDevice: r.Root.Device, MetadataInode: r.Root.Inode, GroupDevice: r.Group.Device, GroupInode: r.Group.Inode,
		ParentDevice: r.Parent.Device, ParentInode: r.Parent.Inode}
}
