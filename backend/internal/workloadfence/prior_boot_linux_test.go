//go:build linux

package workloadfence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixturePriorBoot = "baa7816b-6491-4790-9b10-47a429b7ea90"

func TestPriorBootEpochMetadata(t *testing.T) {
	current, err := bootID()
	if err != nil {
		t.Fatal(err)
	}
	if current == fixturePriorBoot {
		t.Fatal("fixture boot unexpectedly equals the kernel boot")
	}
	for _, tt := range []struct {
		name   string
		change func(*record, *EpochReference, *string)
		want   error
	}{
		{name: "prior open epoch"},
		{name: "prior closed epoch", change: func(r *record, _ *EpochReference, _ *string) { r.Closed = true }},
		{name: "same boot", change: func(r *record, ref *EpochReference, live *string) {
			r.Boot, ref.Boot = *live, *live
		}, want: ErrIntegrity},
		{name: "unknown live boot", change: func(_ *record, _ *EpochReference, live *string) { *live = "" }, want: ErrInvalid},
		{name: "unknown old boot", change: func(r *record, ref *EpochReference, _ *string) {
			r.Boot = "00000000-0000-0000-0000-000000000000"
			ref.Boot = r.Boot
		}, want: ErrInvalid},
		{name: "version", change: func(_ *record, ref *EpochReference, _ *string) { ref.Version++ }, want: ErrIntegrity},
		{name: "epoch", change: func(_ *record, ref *EpochReference, _ *string) { ref.Epoch += "0" }, want: ErrIntegrity},
		{name: "boot", change: func(_ *record, ref *EpochReference, live *string) { ref.Boot = *live }, want: ErrIntegrity},
		{name: "metadata device", change: func(_ *record, ref *EpochReference, _ *string) { ref.MetadataDevice++ }, want: ErrIntegrity},
		{name: "metadata inode", change: func(_ *record, ref *EpochReference, _ *string) { ref.MetadataInode++ }, want: ErrIntegrity},
		{name: "group device", change: func(_ *record, ref *EpochReference, _ *string) { ref.GroupDevice++ }, want: ErrIntegrity},
		{name: "group inode", change: func(_ *record, ref *EpochReference, _ *string) { ref.GroupInode++ }, want: ErrIntegrity},
		{name: "instance", change: func(_ *record, ref *EpochReference, _ *string) { ref.InstanceEpoch = "" }, want: ErrIntegrity},
		{name: "parent epoch", change: func(_ *record, ref *EpochReference, _ *string) { ref.ParentEpoch = ref.Epoch }, want: ErrIntegrity},
		{name: "depth", change: func(_ *record, ref *EpochReference, _ *string) { ref.Depth++ }, want: ErrIntegrity},
		{name: "copied metadata", change: func(r *record, ref *EpochReference, _ *string) {
			r.Root.Inode++
			ref.MetadataInode = r.Root.Inode
		}, want: ErrIntegrity},
		{name: "impossible old group", change: func(r *record, ref *EpochReference, _ *string) {
			r.Group = r.Parent
			ref.GroupDevice, ref.GroupInode = r.Group.Device, r.Group.Inode
		}, want: ErrIntegrity},
		{name: "valid child record", change: func(r *record, ref *EpochReference, _ *string) {
			instance := strings.Repeat("b", 32)
			r.Version, r.Lineage = 2, lineage{instance, r.Root, instance, r.Root, r.Parent, 1}
			ref.Version, ref.InstanceEpoch, ref.ParentEpoch, ref.Depth = 2, instance, instance, 1
		}, want: ErrUnsupported},
		{name: "root with lineage", change: func(r *record, _ *EpochReference, _ *string) {
			r.Lineage.Depth = 1
		}, want: ErrInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := privateRoot(t)
			dir, err := openDir(root)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			r, ref := priorBootFixture(t, dir)
			live := current
			if tt.change != nil {
				tt.change(&r, &ref, &live)
			}
			writePriorBootFixture(t, root, r)
			got, err := readPriorBootEpoch(dir, ref, live)
			if !errors.Is(err, tt.want) {
				t.Fatalf("validation = %v, want %v", err, tt.want)
			}
			if tt.want == nil && got != r {
				t.Fatalf("accepted record changed: got %+v, want %+v", got, r)
			}
		})
	}
}

func TestPriorBootEpochRejectsUncertainMetadata(t *testing.T) {
	current, err := bootID()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing", "extra file", "bad JSON", "unknown version", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := privateRoot(t)
			dir, err := openDir(root)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			r, ref := priorBootFixture(t, dir)
			path := filepath.Join(root, "epoch.json")
			switch kind {
			case "extra file":
				writePriorBootFixture(t, root, r)
				err = os.WriteFile(path+".tmp", nil, 0o600)
			case "bad JSON":
				err = os.WriteFile(path, []byte(`{"epoch":`), 0o600)
			case "unknown version":
				r.Version = 3
				writePriorBootFixture(t, root, r)
			case "symlink":
				other := privateRoot(t)
				writePriorBootFixture(t, other, r)
				err = os.Symlink(filepath.Join(other, "epoch.json"), path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readPriorBootEpoch(dir, ref, current); err == nil {
				t.Fatal("uncertain metadata accepted")
			}
		})
	}
}

func TestPriorBootRequiresLiveBudget(t *testing.T) {
	for _, budget := range []*Budget{nil, {}, {s: &budgetState{closed: true}}} {
		if err := ValidatePriorBootUnderBudget(t.Context(), budget, "/not-opened", "/not-authority", EpochReference{}); err == nil {
			t.Fatal("accepted absent or closed budget")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ValidatePriorBootUnderBudget(ctx, &Budget{s: &budgetState{}}, "/not-opened", "/not-authority", EpochReference{}); !errors.Is(err, context.Canceled) {
		t.Fatal("did not honor cancellation:", err)
	}
}

func TestKernelPriorBootValidationWithLiveBudget(t *testing.T) {
	b, parent := resourceBudget(t)
	root := privateRoot(t)
	s, err := openMetadata(root, parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.dispose)
	r, ref := priorBootFixture(t, s.root)
	writePriorBootFixture(t, root, r)
	path := filepath.Join(root, "epoch.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := timeout(t)
	if err := ValidatePriorBootUnderBudget(ctx, b, root, parent, ref); !errors.Is(err, ErrBusy) {
		t.Fatal("ignored held metadata flock:", err)
	}
	s.dispose()
	if err := ValidatePriorBootUnderBudget(ctx, b, root, b.s.path, ref); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted parent outside the strict-child delegation:", err)
	}
	currentBoot := b.s.boot
	b.s.boot = r.Boot
	err = ValidatePriorBootUnderBudget(ctx, b, root, parent, ref)
	b.s.boot = currentBoot
	if !errors.Is(err, ErrIntegrity) {
		t.Fatal("accepted stale budget boot:", err)
	}
	if err := ValidatePriorBootUnderBudget(ctx, b, root, parent, ref); err != nil {
		t.Fatalf("prior-boot metadata fixture with live budget: %v", err)
	}
	group := filepath.Join(parent, "epoch-"+r.Epoch)
	if _, err := os.Stat(group); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("validation created a cgroup or found unexpected state:", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) || b.s.refs != 0 {
		t.Fatal("validation mutated metadata or retained an owner:", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePriorBootUnderBudget(ctx, b, root, parent, ref); !errors.Is(err, ErrClosed) {
		t.Fatal("accepted closed budget:", err)
	}
}

func priorBootFixture(t *testing.T, dir *os.File) (record, EpochReference) {
	t.Helper()
	root, err := identity(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := record{Version: 1, Epoch: strings.Repeat("a", 32), Boot: fixturePriorBoot,
		Root: root, Parent: objectID{Device: 17, Inode: 2}, Group: objectID{Device: 17, Inode: 3}}
	return r, EpochReference{Version: 1, Epoch: r.Epoch, Boot: r.Boot, InstanceEpoch: r.Epoch,
		MetadataDevice: r.Root.Device, MetadataInode: r.Root.Inode, GroupDevice: r.Group.Device, GroupInode: r.Group.Inode}
}

func writePriorBootFixture(t *testing.T, root string, r record) {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "epoch.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}
