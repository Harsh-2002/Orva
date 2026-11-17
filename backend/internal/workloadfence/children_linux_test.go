//go:build linux

package workloadfence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLineageGatesCancellationAndClosedAncestors(t *testing.T) {
	root := &state{gate: make(chan struct{}, 1)}
	root.leave()
	child := &state{gate: make(chan struct{}, 1), parentState: root}
	child.leave()
	grandchild := &state{gate: make(chan struct{}, 1), parentState: child}
	grandchild.leave()
	<-child.gate
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if unlock, err := grandchild.enterLineage(ctx, true); !errors.Is(err, context.DeadlineExceeded) || unlock != nil {
		t.Fatalf("waiting child lock: %v", err)
	}
	select {
	case <-root.gate:
		root.leave()
	default:
		t.Fatal("cancelled descendant retained root gate")
	}
	child.leave()
	root.closed.Store(true)
	if _, err := grandchild.enterLineage(t.Context(), true); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed root: %v", err)
	}
	unlock, err := grandchild.enterLineage(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestLineageRecordCompatibility(t *testing.T) {
	base := record{Version: 1}
	b, err := json.Marshal(base)
	if err != nil || strings.Contains(string(b), "lineage") {
		t.Fatalf("root v1 bytes gained lineage: %s %v", b, err)
	}
	for _, l := range []lineage{{}, {Depth: 5}, {Depth: 1}} {
		if l.valid(strings.Repeat("a", 32)) {
			t.Fatal("invalid lineage accepted")
		}
	}
}

func newTestChild(t *testing.T, parent *Domain) *Domain {
	t.Helper()
	child, err := parent.NewChild(timeout(t), privateRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.s.reclaimed {
			return
		}
		proof, err := child.Fence(timeout(t))
		if err != nil {
			t.Error(err)
			return
		}
		if err := parent.ReclaimChild(timeout(t), child, proof); err != nil {
			t.Error(err)
		}
	})
	return child
}

func TestKernelNestedOwnershipAndFence(t *testing.T) {
	b, parent := resourceBudget(t)
	root := makeBudgetDomain(t, b, parent)
	left, right := newTestChild(t, root), newTestChild(t, root)
	grandchild := newTestChild(t, left)
	cap, err := grandchild.PrepareJails(timeout(t), "cpu", "memory", "pids")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := grandchild.Reference(timeout(t))
	if err != nil || ref.InstanceEpoch != root.s.rec.Epoch || ref.ParentEpoch != left.s.rec.Epoch || ref.Depth != 2 {
		t.Fatalf("association: %+v %v", ref, err)
	}
	raw, err := readRecord(filepath.Join(grandchild.s.root.Name(), "epoch.json"))
	if err != nil || raw.Version != 2 || raw.Lineage != left.s.childLineage() {
		t.Fatalf("persisted association: %+v %v", raw, err)
	}
	// Even the generic root recovery constructor must not adopt a child record.
	recoveryRoot := privateRoot(t)
	bytes, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(recoveryRoot, "epoch.json"), bytes, 0600); err != nil {
		t.Fatal(err)
	}
	recoveryParent := filepath.Join(filepath.Dir(parent), "daemon", "recovery")
	if err := os.Mkdir(recoveryParent, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(recoveryParent)
	if recovered, err := OpenRecovering(recoveryRoot, recoveryParent); !errors.Is(err, ErrUnsupported) || recovered != nil {
		t.Fatalf("generic child recovery: %v", err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("/bin/sh", "-c", `sleep 600 & echo "$$ $!" > "$1"; wait`, "helper", ready)
	if err := grandchild.Start(timeout(t), cmd, cap); err != nil {
		t.Fatal(err)
	}
	pids := strings.Fields(awaitFile(t, ready))
	for _, pid := range pids {
		data, err := os.ReadFile("/proc/" + pid + "/cgroup")
		if err != nil || !strings.Contains(string(data), strings.TrimPrefix(grandchild.s.path, "/sys/fs/cgroup")+"/supervisors") {
			t.Fatalf("nested membership: %s %v", data, err)
		}
	}
	rightCmd := exec.Command("/bin/sleep", "600")
	if err := right.Start(timeout(t), rightCmd); err != nil {
		t.Fatal(err)
	}
	proof, err := grandchild.Fence(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	for _, pid := range pids {
		assertDead(t, pid)
	}
	if empty, err := unpopulated(right.s.path); err != nil || empty {
		t.Fatal("child fence killed sibling", err)
	}
	trueCmd := exec.Command("/bin/true")
	if err := root.Start(timeout(t), trueCmd); err != nil {
		t.Fatal("child fence closed root", err)
	}
	_ = trueCmd.Wait()
	if err := left.ReclaimChild(timeout(t), grandchild, proof); err != nil {
		t.Fatal(err)
	}
	copyChild := *right
	rightCap, err := right.PrepareJails(timeout(t), "cpu", "memory", "pids")
	if err != nil {
		t.Fatal(err)
	}
	rootProof, err := root.Fence(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = rightCmd.Wait()
	for _, d := range []*Domain{left, right, &copyChild} {
		if err := d.Start(timeout(t), exec.Command("/bin/true")); !errors.Is(err, ErrClosed) {
			t.Fatalf("copied late start: %v", err)
		}
		if _, err := d.NewChild(timeout(t), privateRoot(t)); !errors.Is(err, ErrClosed) {
			t.Fatalf("late child: %v", err)
		}
	}
	if err := copyChild.Start(timeout(t), exec.Command("/bin/true"), rightCap); !errors.Is(err, ErrClosed) {
		t.Fatalf("prepared late start: %v", err)
	}
	if err := root.Check(timeout(t), rootProof); err != nil {
		t.Fatal("late entry invalidated fresh proof", err)
	}
	t.Logf("nested instance=%s child=%s boot=%s root populated=0 after copied/prepared late-entry attempts", rootProof.rec.Epoch, ref.Epoch, ref.Boot)
}

func TestKernelNestedBoundsCloseAndCancellation(t *testing.T) {
	b, parent := resourceBudget(t)
	root := makeBudgetDomain(t, b, parent)
	child := newTestChild(t, root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := root.s.descendants
	if d, err := root.NewChild(ctx, privateRoot(t)); !errors.Is(err, context.Canceled) || d != nil || root.s.descendants != before {
		t.Fatalf("cancelled child creation: %v", err)
	}
	for i := 1; i < maxChildDepth; i++ {
		child = newTestChild(t, child)
	}
	if d, err := child.NewChild(timeout(t), privateRoot(t)); !errors.Is(err, ErrCapacity) || d != nil {
		t.Fatalf("depth bound: %v", err)
	}
	// Exercise the exact capacity boundary without allocating 1024 kernel trees.
	root.s.descendants = maxDescendants
	if d, err := root.NewChild(timeout(t), privateRoot(t)); !errors.Is(err, ErrCapacity) || d != nil {
		t.Fatalf("capacity bound: %v", err)
	}
	root.s.descendants = maxChildDepth
	copyRoot := *root
	if err := copyRoot.Close(); !errors.Is(err, ErrBusy) || root.s.disposed {
		t.Fatalf("root ownership released while children attached: %v", err)
	}
	if err := child.Close(); !errors.Is(err, ErrBusy) || child.s.disposed {
		t.Fatalf("child Close dropped pins: %v", err)
	}
	if _, err := child.PrepareJails(timeout(t)); !errors.Is(err, ErrClosed) {
		t.Fatal("closed ancestor allowed entry", err)
	}
}

func TestKernelNestedConcurrentRetireAndStart(t *testing.T) {
	b, parent := resourceBudget(t)
	root := makeBudgetDomain(t, b, parent)
	child := newTestChild(t, root)
	grandchild := newTestChild(t, child)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			cmd := exec.Command("/bin/true")
			err := grandchild.Start(timeout(t), cmd)
			if err == nil {
				_ = cmd.Wait()
			} else if !errors.Is(err, ErrClosed) {
				t.Error(err)
			}
		})
	}
	proof, err := root.Fence(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := root.Check(timeout(t), proof); err != nil {
		t.Fatal(err)
	}
}

func TestKernelNestedReclaimAndCloseRace(t *testing.T) {
	b, parent := resourceBudget(t)
	root := makeBudgetDomain(t, b, parent)
	child := newTestChild(t, root)
	proof, err := child.Fence(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := root.ReclaimChild(ctx, child, proof); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reclaim", err)
	}
	if root.s.descendants != 1 || child.s.disposed {
		t.Fatal("cancelled cleanup dropped parent ownership")
	}
	copyChild, copyRoot := *child, *root
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			if err := copyRoot.ReclaimChild(timeout(t), &copyChild, proof); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := copyChild.Close(); err != nil && !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if root.s.descendants != 0 || len(root.s.children) != 0 || !child.s.disposed {
		t.Fatal("reclaim did not detach ownership exactly once")
	}
	cmd := exec.Command("/bin/true")
	if err := root.Start(timeout(t), cmd); err != nil {
		t.Fatal("reclaim closed parent", err)
	}
	_ = cmd.Wait()
}

func TestKernelNestedPartialConstructionRetainsPins(t *testing.T) {
	b, parent := resourceBudget(t)
	root := makeBudgetDomain(t, b, parent)
	cap, err := root.PrepareJails(timeout(t), "cpu", "memory", "pids")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the initializer's real post-mkdir durable-commit failure.
	child, err := openMetadata(privateRoot(t), cap.Path())
	if err != nil {
		t.Fatal(err)
	}
	child.parentState, child.depth = root.s, 1
	root.s.children = map[*state]struct{}{child: {}}
	root.s.descendants = 1
	child.persistFault = func(point string) error {
		if point == "file-sync" {
			return errors.New("injected child commit failure")
		}
		return nil
	}
	result, err := initialize(child)
	if err == nil || result == nil || !child.closed.Load() || child.disposed ||
		root.s.descendants != 1 || len(root.s.children) != 1 {
		t.Fatal("partial constructor dropped closed ownership", err)
	}
	if _, err := result.Fence(timeout(t)); err == nil {
		t.Fatal("missing durable association manufactured a proof")
	}
	if err := root.Close(); !errors.Is(err, ErrBusy) || root.s.disposed {
		t.Fatal("partial child released parent pins", err)
	}
	proof, err := root.Fence(timeout(t))
	if err != nil || root.Check(timeout(t), proof) != nil {
		t.Fatal("quarantined child prevented root fencing", err)
	}
	// Explicit fixture-only teardown of unrecoverable quarantine, after root proof.
	removeGroups(t, child.path)
	<-root.s.gate
	child.dispose()
	root.s.leave()
}
