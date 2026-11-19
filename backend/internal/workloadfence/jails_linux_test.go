//go:build linux

package workloadfence

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKernelJailParentLifetimeAndFence(t *testing.T) {
	d := makeDomain(t)
	binding, err := d.PrepareJails(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	if binding.Path() != filepath.Join(d.s.path, "jails") || (&JailParent{}).Path() != "" {
		t.Fatal("binding exposed an unowned path")
	}
	child := filepath.Join(binding.Path(), "NSJAIL.123")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "pids")
	cmd := exec.Command("/bin/sh", "-c",
		`echo $$ > "$1/cgroup.procs"; sleep 600 & echo "$$ $!" > "$2"; wait`, "helper", child, ready)
	if err := d.Start(timeout(t), cmd, binding); err != nil {
		t.Fatal(err)
	}
	pids := strings.Fields(awaitFile(t, ready))
	for _, pid := range pids {
		b, err := os.ReadFile("/proc/" + pid + "/cgroup")
		if err != nil || !strings.Contains(string(b), "/epoch-"+d.s.rec.Epoch+"/jails/NSJAIL.123") {
			t.Fatalf("owned jail descendant not contained: %s %v", b, err)
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
	if err := d.Start(timeout(t), exec.Command("/bin/true"), binding); !errors.Is(err, ErrClosed) {
		t.Fatalf("prepared-before-fence entered: %v", err)
	}
	if _, err := d.PrepareJails(timeout(t)); !errors.Is(err, ErrClosed) {
		t.Fatalf("prepare after fence: %v", err)
	}
	t.Logf("bound jail lifetime populated -> empty: epoch=%s binding=%+v boot=%s", d.s.rec.Epoch, binding.id, d.s.rec.Boot)
}

func TestKernelJailParentWrongOwnerAndCancelledPreparation(t *testing.T) {
	first, second := makeDomain(t), makeDomain(t)
	binding, err := first.PrepareJails(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*JailParent{nil, {}, binding} {
		if err := second.Start(timeout(t), exec.Command("/bin/true"), p); !errors.Is(err, ErrInvalid) {
			t.Fatalf("foreign/zero binding accepted: %v", err)
		}
	}
	if err := first.Start(timeout(t), exec.Command("/bin/true"), binding, binding); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := first.PrepareJails(ctx); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preparation: %v %v", p, err)
	}
}

func TestKernelPreparedJailEntryCannotRacePastFence(t *testing.T) {
	d := makeDomain(t)
	binding, err := d.PrepareJails(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	<-d.s.gate
	started := make(chan error, 1)
	go func() {
		started <- d.Start(timeout(t), exec.Command("/bin/sleep", "600"), binding)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if proof, err := d.Fence(ctx); proof != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fence returned proof: %v %v", proof, err)
	}
	d.s.leave()
	if err := <-started; !errors.Is(err, ErrClosed) {
		t.Fatalf("old bound entry crossed fence: %v", err)
	}
	if _, err := d.Fence(timeout(t)); err != nil {
		t.Fatal(err)
	}
}

func TestKernelJailParentReplacementAndUnexpectedEntries(t *testing.T) {
	for _, change := range []string{"replacement", "unknown-epoch", "unknown-jail", "threaded"} {
		t.Run(change, func(t *testing.T) {
			d := makeDomain(t)
			binding, err := d.PrepareJails(timeout(t))
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "replacement":
				if err := os.Remove(binding.Path()); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(binding.Path(), 0o700); err != nil {
					t.Fatal(err)
				}
			case "unknown-epoch":
				if err := os.Mkdir(filepath.Join(d.s.path, "foreign"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "unknown-jail":
				if err := os.Mkdir(filepath.Join(binding.Path(), "foreign"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "threaded":
				if err := writeControl(binding.Path(), "cgroup.type", "threaded"); err != nil {
					t.Fatal(err)
				}
			}
			if p, err := d.PrepareJails(timeout(t)); p != nil || err == nil {
				t.Fatalf("changed jail path/state prepared: %v %v", p, err)
			}
			if err := d.Start(timeout(t), exec.Command("/bin/true"), binding); err == nil {
				t.Fatal("stale binding entered changed hierarchy")
			}
			if change == "unknown-epoch" || change == "unknown-jail" {
				base := d.s.path
				if change == "unknown-jail" {
					base = binding.Path()
				}
				if err := os.Remove(filepath.Join(base, "foreign")); err != nil {
					t.Fatal(err)
				}
				if err := d.Start(timeout(t), exec.Command("/bin/true"), binding); !errors.Is(err, ErrClosed) {
					t.Fatalf("repair reopened a corrupted domain: %v", err)
				}
			}
		})
	}
}

func TestKernelJailParentNoSilentResourceDowngrade(t *testing.T) {
	d := makeDomain(t)
	available, err := readControllers(d.s.path, "cgroup.controllers")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cpu", "memory", "pids"} {
		if available&controllerBit(name) != 0 {
			t.Fatalf("test requires known undelegated controller %s; use a lifetime-only scratch parent", name)
		}
		p, err := d.PrepareJails(timeout(t), name)
		if p != nil || !errors.Is(err, ErrUnsupported) {
			t.Fatalf("missing controller %s silently downgraded: %v %v", name, p, err)
		}
	}
	if _, err := d.PrepareJails(timeout(t)); err != nil {
		t.Fatalf("unsupported request poisoned lifetime-only use: %v", err)
	}
	for _, path := range []string{d.s.parent, d.s.path, filepath.Join(d.s.path, "jails")} {
		b, err := os.ReadFile(filepath.Join(path, "cgroup.subtree_control"))
		if err != nil || len(strings.TrimSpace(string(b))) != 0 {
			t.Fatalf("unavailable request modified hierarchy: %s %s %v", path, b, err)
		}
	}
}

func TestKernelJailParentRecoveryAndCloseDeny(t *testing.T) {
	root, parent := privateRoot(t), scratchParent(t)
	d, err := Create(root, parent)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := d.PrepareJails(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	t.Cleanup(func() { removeGroups(t, d.s.path) })
	if _, err := d.PrepareJails(timeout(t)); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := d.Start(timeout(t), exec.Command("/bin/true"), binding); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	recovered, err := OpenRecovering(root, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if _, err := recovered.PrepareJails(timeout(t)); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := recovered.Start(timeout(t), exec.Command("/bin/true"), binding); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := recovered.Fence(timeout(t)); err != nil {
		t.Fatal(err)
	}
}

func TestControllerRequestsAreBoundedAndExplicit(t *testing.T) {
	for _, names := range [][]string{{"io"}, {"CPU"}, {"cpu", "cpu"}, {"cpu", "memory", "pids", "cpu"}} {
		if _, err := controllerMask(names); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid controller request accepted: %v %v", names, err)
		}
	}
	if mask, err := controllerMask([]string{"cpu", "memory", "pids"}); err != nil || mask != 7 {
		t.Fatalf("valid explicit controller set: %v %v", mask, err)
	}
}
