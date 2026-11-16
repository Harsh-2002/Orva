//go:build linux

package workloadfence

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func moveTestProcess(path string) error {
	return writeControl(path, "cgroup.procs", strconv.Itoa(os.Getpid()))
}

// Run the compiled test binary directly: a go-test driver or shell remaining in
// the captured budget would violate the explicit empty-budget prerequisite.
func resourceBudget(t *testing.T) (*Budget, string) {
	t.Helper()
	scratchParent(t)
	b, err := CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(b.s.path, fmt.Sprintf("owned-budget-%d", time.Now().UnixNano()))
	daemon, parent := filepath.Join(base, "daemon"), filepath.Join(base, "domains")
	for _, path := range []string{base, daemon, parent} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := writeControl(parent, "cgroup.subtree_control", "-cpu -memory -pids"); err != nil {
			t.Error(err)
		}
		if err := os.Remove(parent); err != nil {
			t.Error(err)
		}
		for _, path := range []string{base, b.s.path} {
			if err := writeControl(path, "cgroup.subtree_control", "-cpu -memory -pids"); err != nil {
				t.Error(err)
			}
		}
		if err := moveTestProcess(b.s.path); err != nil {
			t.Error(err)
		}
		for _, path := range []string{daemon, base} {
			if err := os.Remove(path); err != nil {
				t.Error(err)
			}
		}
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := moveTestProcess(daemon); err != nil {
		t.Fatal(err)
	}
	procs, err := os.ReadFile(filepath.Join(b.s.path, "cgroup.procs"))
	if err != nil || strings.TrimSpace(string(procs)) != "" {
		t.Fatalf("fixture refuses to move other processes; budget is not empty: %s %v", procs, err)
	}
	// This is explicit guest-root fixture provisioning, not package behavior.
	for _, path := range []string{b.s.path, base, parent} {
		if err := writeControl(path, "cgroup.subtree_control", "+cpu +memory +pids"); err != nil {
			t.Fatalf("fixture delegation failed at %s: %v", path, err)
		}
	}
	return b, parent
}

func makeBudgetDomain(t *testing.T, b *Budget, parent string) *Domain {
	t.Helper()
	d, err := CreateUnderBudget(b, privateRoot(t), parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := d.Fence(timeout(t)); err != nil {
			t.Error(err)
		}
		d.Close()
		removeGroups(t, d.s.path)
	})
	return d
}

func TestKernelCapturedBudgetResourceDelegation(t *testing.T) {
	b, parent := resourceBudget(t)
	// The old constructor must not gain implicit writable-ancestor authority.
	if d, err := Create(privateRoot(t), parent); err == nil {
		d.Close()
		t.Fatal("legacy Create accepted a sibling of the daemon leaf")
	}
	for _, outside := range []string{b.s.path, "/sys/fs/cgroup"} {
		if d, err := CreateUnderBudget(b, privateRoot(t), outside); err == nil {
			d.Close()
			t.Fatalf("captured budget accepted its own root or ancestor: %s", outside)
		}
	}
	d := makeBudgetDomain(t, b, parent)
	cap, err := d.PrepareJails(timeout(t), "memory", "cpu", "pids")
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(cap.Path(), "NSJAIL.456")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	limits := map[string]string{"memory.max": "33554432", "cpu.max": "50000 100000", "pids.max": "8"}
	for name, value := range limits {
		if err := writeControl(child, name, value); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filepath.Join(child, name))
		if err != nil || strings.TrimSpace(string(actual)) != value {
			t.Fatalf("resource setting not installed: %s %s %v", name, actual, err)
		}
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("/bin/sh", "-c",
		`cat /proc/self/cgroup > "$2.initial"; echo $$ > "$1/cgroup.procs"; sleep 600 & echo "$$ $!" > "$2"; wait`,
		"owned-helper", child, ready)
	if err := d.Start(timeout(t), cmd, cap); err != nil {
		t.Fatal(err)
	}
	pids := strings.Fields(awaitFile(t, ready))
	initial, err := os.ReadFile(ready + ".initial")
	if err != nil || !strings.Contains(string(initial), "/epoch-"+d.s.rec.Epoch+"/supervisors") {
		t.Fatalf("host helper not enrolled before execution: %s %v", initial, err)
	}
	for _, pid := range pids {
		membership, err := os.ReadFile("/proc/" + pid + "/cgroup")
		if err != nil || !strings.Contains(string(membership), "/epoch-"+d.s.rec.Epoch+"/jails/NSJAIL.456") {
			t.Fatalf("limited descendant outside epoch: %s %v", membership, err)
		}
	}
	proof, err := d.Fence(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if err := d.Check(timeout(t), proof); err != nil {
		t.Fatal(err)
	}
	for _, pid := range pids {
		assertDead(t, pid)
	}
	t.Logf("resource-positive: budget=%+v mount=%d boot=%s epoch=%s controls=%v populated=0",
		b.s.ids[1], b.s.mountID, b.s.boot, d.s.rec.Epoch, limits)
}

func TestKernelCapturedBudgetCloseRetainsDomainPins(t *testing.T) {
	b, parent := resourceBudget(t)
	d := makeBudgetDomain(t, b, parent)
	copy := *b
	if err := copy.Close(); !errors.Is(err, ErrBusy) {
		t.Fatalf("budget released live pins: %v", err)
	}
	if _, err := CreateUnderBudget(b, privateRoot(t), parent); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed copied capture minted new domain: %v", err)
	}
	cap, err := d.PrepareJails(timeout(t), "memory", "cpu", "pids")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/true")
	if err := d.Start(timeout(t), cmd, cap); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestKernelBudgetBoundIdentityDenials(t *testing.T) {
	parent := scratchParent(t)
	for _, field := range []string{"boot", "inode", "mount", "cgroup-namespace", "mount-namespace", "process"} {
		t.Run(field, func(t *testing.T) {
			b, err := CaptureBudget()
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			switch field {
			case "boot":
				b.s.boot = "00000000-0000-0000-0000-000000000000"
			case "inode":
				b.s.ids[1].Inode++
			case "mount":
				b.s.mountID++
			case "cgroup-namespace":
				b.s.ids[2].Inode++
			case "mount-namespace":
				b.s.ids[3].Inode++
			case "process":
				b.s.pid++
			}
			if d, err := CreateUnderBudget(b, privateRoot(t), parent); err == nil {
				d.Close()
				t.Fatal("replayed bound identity accepted")
			}
		})
	}
	if _, err := CreateUnderBudget(&Budget{}, privateRoot(t), parent); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestKernelBudgetOutsideAndReplacementDeny(t *testing.T) {
	scratchParent(t)
	original, err := currentBudgetPath()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(original, fmt.Sprintf("owned-membership-%d", time.Now().UnixNano()))
	home, outside := filepath.Join(base, "home"), filepath.Join(base, "outside")
	for _, path := range []string{base, home, outside} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(home, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := moveTestProcess(original); err != nil {
			t.Error(err)
		}
		removeGroups(t, base)
	})
	if err := moveTestProcess(home); err != nil {
		t.Fatal(err)
	}
	b, err := CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := moveTestProcess(outside); err != nil {
		t.Fatal(err)
	}
	if d, err := CreateUnderBudget(b, privateRoot(t), filepath.Join(home, "parent")); err == nil {
		d.Close()
		t.Fatal("current membership outside captured budget accepted")
	}
	// cgroup-v2 does not support directory rename; replace the empty group
	// while its original inode remains pinned by the captured capability.
	removeGroups(t, home)
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := moveTestProcess(home); err != nil {
		t.Fatal(err)
	}
	if d, err := CreateUnderBudget(b, privateRoot(t), filepath.Join(home, "parent")); err == nil {
		d.Close()
		t.Fatal("replaced budget path accepted")
	}
}

func TestKernelBudgetRootCaptureDenies(t *testing.T) {
	scratchParent(t)
	original, err := currentBudgetPath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := moveTestProcess(original); err != nil {
			t.Error(err)
		}
	})
	// Only this test process moves, within this explicitly owned disposable VM.
	if err := moveTestProcess("/sys/fs/cgroup"); err != nil {
		t.Fatal(err)
	}
	if b, err := CaptureBudget(); !errors.Is(err, ErrInvalid) {
		if b != nil {
			b.Close()
		}
		t.Fatalf("kernel root membership captured: %v", err)
	}
}

func TestKernelBudgetCloseCreateRaceAndFailedReservation(t *testing.T) {
	parent := scratchParent(t)
	b, err := CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	bad := privateRoot(t)
	if err := os.WriteFile(filepath.Join(bad, "unknown"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, err := CreateUnderBudget(b, bad, parent); err == nil {
		d.Close()
		t.Fatal("nonempty constructor accepted")
	}
	b.s.mu.Lock()
	refs := b.s.refs
	b.s.mu.Unlock()
	if refs != 0 {
		t.Fatalf("failed constructor leaked a budget reservation: %d", refs)
	}
	roots := make([]string, 12)
	for i := range roots {
		roots[i] = privateRoot(t)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, root := range roots {
		wg.Add(1)
		go func(root string) {
			defer wg.Done()
			<-start
			d, err := CreateUnderBudget(b, root, parent)
			if errors.Is(err, ErrClosed) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := d.Fence(timeout(t)); err != nil {
				t.Error(err)
			}
			d.Close()
			removeGroups(t, d.s.path)
		}(root)
	}
	close(start)
	if err := b.Close(); err != nil && !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	wg.Wait()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestKernelBudgetRejectsNestedForeignMount(t *testing.T) {
	scratchParent(t)
	original, err := currentBudgetPath()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(original, fmt.Sprintf("owned-mount-%d", time.Now().UnixNano()))
	home, foreign := filepath.Join(base, "home"), filepath.Join(base, "foreign")
	target := filepath.Join(home, "parent")
	for _, path := range []string{base, home, foreign, target} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	mounted := false
	t.Cleanup(func() {
		if mounted {
			if err := syscall.Unmount(target, 0); err != nil {
				t.Error(err)
			}
		}
		if err := moveTestProcess(original); err != nil {
			t.Error(err)
		}
		removeGroups(t, base)
	})
	if err := moveTestProcess(home); err != nil {
		t.Fatal(err)
	}
	b, err := CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// Guest-only mount on our own fixture: a lexical descendant now exposes
	// a different cgroup and kernel mount, without changing the capture.
	if err := syscall.Mount(foreign, target, "", syscall.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	mounted = true
	root := privateRoot(t)
	if d, err := CreateUnderBudget(b, root, target); !errors.Is(err, ErrIntegrity) {
		if d != nil {
			d.Close()
		}
		t.Fatalf("lexical child on foreign mount accepted: %v", err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("mount denial changed metadata: %v %v", entries, err)
	}
}
