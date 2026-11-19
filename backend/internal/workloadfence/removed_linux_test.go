package workloadfence

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRemovedSameBootIdentityMatrix(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		parentSame, groupSame bool
		removed, deny         bool
	}{
		{"both gone", false, false, true, false},
		{"group gone only", true, false, false, true},
		{"parent gone only", false, true, false, true},
		{"both intact", true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := removedSameBootResult(tc.parentSame, tc.groupSame)
			if got.Verified() != tc.removed || errors.Is(err, ErrIntegrity) != tc.deny {
				t.Fatalf("verified=%v, err=%v", got.Verified(), err)
			}
		})
	}
	if (RemovedSameBoot{}).Verified() {
		t.Fatal("zero value is not evidence")
	}
}

func TestKernelRemovedSameBoot(t *testing.T) {
	b, parent := resourceBudget(t)
	root := privateRoot(t)
	d, err := CreateUnderBudget(b, root, parent)
	mustRemoval(t, err)
	ref, err := d.Reference(t.Context())
	mustRemoval(t, err)
	group := d.s.path
	replacement := filepath.Join(filepath.Dir(parent), "renamed")
	if err := os.Rename(parent, replacement); err == nil {
		mustRemoval(t, os.Rename(replacement, parent))
		t.Fatal("kernel allowed a cgroup-v2 rename; path absence isn't evidence")
	}
	proof, err := d.Fence(t.Context())
	mustRemoval(t, err)
	mustRemoval(t, d.Check(t.Context(), proof))
	mustRemoval(t, d.Close())

	check := func(want, deny bool) {
		t.Helper()
		proof, err := ValidateRemovedSameBootUnderBudget(t.Context(), b, root, parent, ref)
		if proof.Verified() != want || (err != nil) != deny {
			t.Fatalf("verification = %v, err=%v", proof.Verified(), err)
		}
	}
	check(false, false)
	removeGroups(t, group)
	check(false, true)
	mustRemoval(t, os.Mkdir(group, 0700))
	check(false, true)
	removeGroups(t, parent)
	check(true, false)
	mustRemoval(t, os.Mkdir(parent, 0700))
	mustRemoval(t, writeControl(parent, "cgroup.subtree_control", "+cpu +memory +pids"))
	check(true, false)
	mustRemoval(t, os.WriteFile(filepath.Join(root, "epoch.json"), []byte(`{}`), 0600))
	check(false, true)
}

func TestKernelCaptureServingBudgetChecksInitialOwnership(t *testing.T) {
	scratchParent(t)
	base, err := currentBudgetPath()
	mustRemoval(t, err)
	parent := filepath.Join(base, "orva.workers")
	mustRemoval(t, os.Mkdir(parent, 0700))
	t.Cleanup(func() { mustRemoval(t, os.Remove(parent)) })
	mustRemoval(t, os.Chown(parent, os.Geteuid()+1, -1))
	if b, err := CaptureServingBudget(parent); b != nil || !errors.Is(err, ErrIntegrity) {
		t.Fatalf("foreign worker parent accepted on initial capture: %v", err)
	}
	mustRemoval(t, os.Chown(parent, os.Geteuid(), -1))
	b, err := CaptureServingBudget(parent)
	mustRemoval(t, err)
	mustRemoval(t, b.Close())
}

func mustRemoval(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
