//go:build linux

package workloadfence

import (
	"errors"
	"os/exec"
	"testing"
)

func TestRecoveringBudgetRequiresConcreteCapture(t *testing.T) {
	for _, budget := range []*Budget{nil, {}} {
		if d, err := OpenRecoveringUnderBudget(budget, "/not-opened", "/not-authority"); !errors.Is(err, ErrInvalid) || d != nil {
			t.Fatal("unminted budget restored a domain", err)
		}
	}
}

func TestKernelRecoveringUnderBudgetPreservesStrictOrdinaryParent(t *testing.T) {
	b, parent := resourceBudget(t)
	root := privateRoot(t)
	d, err := CreateUnderBudget(b, root, parent)
	if err != nil {
		t.Fatal(err)
	}
	path := d.s.path
	t.Cleanup(func() {
		if d != nil && !d.s.disposed {
			if _, err := d.Fence(timeout(t)); err != nil {
				t.Error(err)
			}
			_ = d.Close()
		}
		removeGroups(t, path)
	})
	expected, err := d.Reference(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if ordinary, err := OpenRecovering(root, parent); ordinary != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("ordinary recovery gained sibling authority", err)
	}
	d, err = OpenRecoveringUnderBudget(b, root, parent)
	if err != nil {
		t.Fatal(err)
	}
	current, err := d.Reference(timeout(t))
	if err != nil || current != expected {
		t.Fatal("budget recovery rebound another epoch", err)
	}
	if err := d.Start(timeout(t), exec.Command("/bin/true")); !errors.Is(err, ErrClosed) {
		t.Fatal("recovery restored entry", err)
	}
	if child, err := d.NewChild(timeout(t), privateRoot(t)); child != nil || !errors.Is(err, ErrClosed) {
		t.Fatal("recovery restored child construction", err)
	}
	proof, err := d.Fence(timeout(t))
	if err != nil || d.Check(timeout(t), proof) != nil {
		t.Fatal("budget-root recovery could not prove emptiness", err)
	}
}
