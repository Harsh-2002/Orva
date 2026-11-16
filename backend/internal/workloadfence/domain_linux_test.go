//go:build linux

package workloadfence

import (
	"context"
	"encoding/json"
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

// Kernel tests are deliberately opt-in: never discover or edit host cgroups.
func scratchParent(t *testing.T) string {
	t.Helper()
	p := os.Getenv("ORVA_FENCE_TEST_PARENT")
	if p == "" {
		t.Skip("requires an explicitly owned disposable-VM cgroup subtree")
	}
	if os.Getenv("ORVA_FENCE_TEST_SCRATCH") != "disposable-vm" {
		t.Fatal("refusing cgroup tests without disposable-VM acknowledgement")
	}
	return p
}

func makeDomain(t *testing.T) *Domain {
	t.Helper()
	d, err := Create(privateRoot(t), scratchParent(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := d.Fence(ctx); err != nil && !errors.Is(err, ErrClosed) {
			t.Error(err)
		}
		d.Close()
		removeGroups(t, d.s.path)
	})
	return d
}

func privateRoot(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func removeGroups(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Error(err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			removeGroups(t, filepath.Join(path, entry.Name()))
		}
	}
	if err := os.Remove(path); err != nil {
		t.Error(err)
	}
}

func timeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func awaitFile(t *testing.T, path string) string {
	t.Helper()
	ctx := timeout(t)
	for {
		if b, err := os.ReadFile(path); err == nil && len(b) != 0 {
			return strings.TrimSpace(string(b))
		}
		select {
		case <-ctx.Done():
			t.Fatal("helper did not become ready")
			return ""
		case <-time.After(time.Millisecond):
		}
	}
}

func assertDead(t *testing.T, pid string) {
	t.Helper()
	b, err := os.ReadFile("/proc/" + pid + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 || !strings.HasPrefix(string(b[end+1:]), " Z ") {
		t.Fatalf("process %s still alive: %s", pid, b)
	}
}

func TestKernelDescendantsAndProof(t *testing.T) {
	d := makeDomain(t)
	childGroup := filepath.Join(d.s.path, "jails", "dummy")
	if err := os.Mkdir(childGroup, 0o700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "pids")
	cmd := exec.Command("/bin/sh", "-c",
		`cat /proc/self/cgroup > "$2.initial"; echo $$ > "$1/cgroup.procs"; sleep 600 & echo "$$ $!" > "$2"; wait`,
		"helper", childGroup, ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := d.Start(timeout(t), cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr.UseCgroupFD || cmd.SysProcAttr.CgroupFD != 0 || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Start leaked cgroup descriptor or lost existing attributes")
	}
	pids := strings.Fields(awaitFile(t, ready))
	initial, err := os.ReadFile(ready + ".initial")
	if err != nil || !strings.Contains(string(initial), "/epoch-"+d.s.rec.Epoch+"/supervisors") {
		t.Fatalf("host command was not enrolled before its first instruction: %s %v", initial, err)
	}
	if len(pids) != 2 {
		t.Fatal(pids)
	}
	for _, pid := range pids {
		b, err := os.ReadFile("/proc/" + pid + "/cgroup")
		if err != nil || !strings.Contains(string(b), "/epoch-"+d.s.rec.Epoch+"/jails/dummy") {
			t.Fatalf("descendant not inside exact epoch: %s %v", b, err)
		}
	}
	if empty, err := unpopulated(d.s.path); err != nil || empty {
		t.Fatalf("populated witness missing: %v %v", empty, err)
	}
	proof, err := d.Fence(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	for _, pid := range pids {
		assertDead(t, pid)
	}
	if err := d.Check(timeout(t), proof); err != nil {
		t.Fatal(err)
	}
	if err := d.Check(timeout(t), &Proof{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged proof: %v", err)
	}
	if err := d.Start(timeout(t), exec.Command("/bin/true")); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-fence Start: %v", err)
	}
	t.Logf("kernel populated=1 -> cgroup.kill -> populated=0; epoch=%s boot=%s identity=%+v", d.s.rec.Epoch, d.s.rec.Boot, d.s.rec.Group)
}

func TestKernelPreStartGateAndCancelledFence(t *testing.T) {
	d := makeDomain(t)
	<-d.s.gate
	result := make(chan error, 1)
	go func() { result <- d.Start(timeout(t), exec.Command("/bin/sleep", "600")) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	proof, err := d.Fence(ctx)
	if proof != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fence minted proof: %v %v", proof, err)
	}
	d.s.leave()
	if err := <-result; !errors.Is(err, ErrClosed) {
		t.Fatalf("old prepared Start entered after closure: %v", err)
	}
	proof, err = d.Fence(timeout(t))
	if err != nil || d.Check(timeout(t), proof) != nil {
		t.Fatalf("retry failed: %v", err)
	}
}

func TestKernelStartFenceRace(t *testing.T) {
	d := makeDomain(t)
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command("/bin/sleep", "600")
			err := d.Start(timeout(t), cmd)
			if err == nil {
				_ = cmd.Wait()
			} else if !errors.Is(err, ErrClosed) {
				t.Error(err)
			}
		}()
	}
	proof, err := d.Fence(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := d.Check(timeout(t), proof); err != nil {
		t.Fatal(err)
	}
}

func TestKernelOwnerCrashAndReopen(t *testing.T) {
	parent := scratchParent(t)
	root, report := privateRoot(t), filepath.Join(t.TempDir(), "child")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashOwnerHelper$")
	cmd.Env = append(os.Environ(), "ORVA_FENCE_HELPER_ROOT="+root, "ORVA_FENCE_HELPER_REPORT="+report)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash owner helper: %v %s", err, out)
	}
	pid := awaitFile(t, report)
	if _, err := strconv.Atoi(pid); err != nil {
		t.Fatal(pid)
	}
	d, err := OpenRecovering(root, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { d.Close(); removeGroups(t, d.s.path) }()
	if empty, err := unpopulated(d.s.path); err != nil || empty {
		t.Fatalf("owner exit incorrectly treated as dead descendants: %v %v", empty, err)
	}
	if _, err := OpenRecovering(root, parent); !errors.Is(err, ErrBusy) {
		t.Fatalf("second owner: %v", err)
	}
	if err := d.Start(timeout(t), exec.Command("/bin/true")); !errors.Is(err, ErrClosed) {
		t.Fatalf("recovered entry reopened: %v", err)
	}
	proof, err := d.Fence(timeout(t))
	if err != nil {
		t.Fatal(err)
	}
	assertDead(t, pid)
	if err := d.Check(timeout(t), proof); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Check(timeout(t), proof); !errors.Is(err, ErrClosed) {
		t.Fatalf("proof used after ownership released: %v", err)
	}
	reopened, err := OpenRecovering(root, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if proof, err := reopened.Fence(timeout(t)); err != nil || reopened.Check(timeout(t), proof) != nil {
		t.Fatalf("closed-record recovery failed: %v", err)
	}
}

func TestCrashOwnerHelper(t *testing.T) {
	root := os.Getenv("ORVA_FENCE_HELPER_ROOT")
	if root == "" {
		t.Skip("subprocess only")
	}
	d, err := Create(root, scratchParent(t))
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command("/bin/sleep", "600")
	if err := d.Start(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("ORVA_FENCE_HELPER_REPORT"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Exit(0) // No Close/Wait: a real process lifetime boundary, not a mock flock.
}

func TestKernelRecoveryIdentityAndCorruptionDeny(t *testing.T) {
	parent := scratchParent(t)
	for _, change := range []string{"boot", "group", "parent", "root", "extra", "temporary", "replace-group", "missing-group"} {
		t.Run(change, func(t *testing.T) {
			root := privateRoot(t)
			d, err := Create(root, parent)
			if err != nil {
				t.Fatal(err)
			}
			path := d.s.path
			d.Close()
			t.Cleanup(func() {
				if _, err := os.Stat(path); err == nil {
					removeGroups(t, path)
				}
			})
			r := d.s.rec
			switch change {
			case "boot":
				r.Boot = "00000000-0000-0000-0000-000000000000"
			case "group":
				r.Group.Inode++
			case "parent":
				r.Parent.Inode++
			case "root":
				r.Root.Inode++
			case "extra", "temporary":
				name := "unknown"
				if change == "temporary" {
					name = "epoch.json.tmp"
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "replace-group", "missing-group":
				removeGroups(t, path)
				if change == "replace-group" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			data, _ := json.Marshal(r)
			if err := os.WriteFile(filepath.Join(root, "epoch.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(root, "epoch.json"))
			if err != nil {
				t.Fatal(err)
			}
			if recovered, err := OpenRecovering(root, parent); err == nil {
				recovered.Close()
				t.Fatal("ambiguous recovery accepted")
			}
			after, _ := os.ReadFile(filepath.Join(root, "epoch.json"))
			if string(before) != string(after) {
				t.Fatal("denial changed persisted data")
			}
		})
	}
}

func TestKernelDurableCloseFailures(t *testing.T) {
	for _, stage := range []string{"file-sync", "rename", "directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			d := makeDomain(t)
			marker := filepath.Join(t.TempDir(), "ready")
			cmd := exec.Command("/bin/sh", "-c", `echo ready > "$1"; exec sleep 600`, "helper", marker)
			if err := d.Start(timeout(t), cmd); err != nil {
				t.Fatal(err)
			}
			awaitFile(t, marker)
			injected := errors.New("injected persistence failure")
			d.s.persistFault = func(name string) error {
				if name == stage {
					return injected
				}
				return nil
			}
			t.Cleanup(func() {
				d.s.persistFault = nil
				// Test-only repair, after preservation assertions. Production has
				// no cleanup/repair API for interrupted temporary metadata.
				_ = os.Remove(filepath.Join(d.s.root.Name(), "epoch.json.tmp"))
			})
			proof, err := d.Fence(timeout(t))
			if proof != nil || !errors.Is(err, injected) {
				t.Fatalf("failed durability minted proof: %v %v", proof, err)
			}
			if empty, err := unpopulated(d.s.path); err != nil || empty {
				t.Fatalf("kill preceded durable closure: %v %v", empty, err)
			}
			if err := d.Start(timeout(t), exec.Command("/bin/true")); !errors.Is(err, ErrClosed) {
				t.Fatalf("failed fence reopened entry: %v", err)
			}
			d.s.persistFault = nil
			if stage == "directory-sync" {
				proof, err = d.Fence(timeout(t))
				if err != nil || d.Check(timeout(t), proof) != nil {
					t.Fatalf("visible closed rename was not durably retried: %v", err)
				}
				_ = cmd.Wait()
			} else {
				path := filepath.Join(d.s.root.Name(), "epoch.json.tmp")
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if proof, err = d.Fence(timeout(t)); proof != nil || err == nil {
					t.Fatal("pending temporary state silently repaired")
				}
				after, err := os.ReadFile(path)
				if err != nil || string(before) != string(after) {
					t.Fatal("failed retry discarded pending state")
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if _, err := d.Fence(timeout(t)); err != nil {
					t.Fatal(err)
				}
				_ = cmd.Wait()
			}
		})
	}
}

func TestKernelFenceRefusesChangedMetadata(t *testing.T) {
	for _, change := range []string{"record", "extra", "root-mode", "root-replaced"} {
		t.Run(change, func(t *testing.T) {
			d := makeDomain(t)
			root := d.s.root.Name()
			data, _ := json.Marshal(d.s.rec)
			t.Cleanup(func() {
				if change == "root-replaced" {
					_ = os.Remove(root)
					_ = os.Rename(root+"-moved", root)
				}
				_ = os.Chmod(root, 0o700)
				_ = os.Remove(filepath.Join(root, "unknown"))
				_ = os.WriteFile(filepath.Join(root, "epoch.json"), data, 0o600)
			})
			switch change {
			case "record":
				if err := os.WriteFile(filepath.Join(root, "epoch.json"), []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.WriteFile(filepath.Join(root, "unknown"), []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "root-mode":
				if err := os.Chmod(root, 0o777); err != nil {
					t.Fatal(err)
				}
			case "root-replaced":
				if err := os.Rename(root, root+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			proof, err := d.Fence(timeout(t))
			if proof != nil || err == nil {
				t.Fatalf("changed metadata accepted: %v %v", proof, err)
			}
			if err := d.Start(timeout(t), exec.Command("/bin/true")); !errors.Is(err, ErrClosed) {
				t.Fatalf("failed fence reopened entry: %v", err)
			}
			if change == "record" {
				after, _ := os.ReadFile(filepath.Join(root, "epoch.json"))
				if string(after) != "corrupt" {
					t.Fatal("Fence overwrote corrupt metadata")
				}
			}
		})
	}
}

func TestKernelFenceDeadlineClosesEntryWithoutProof(t *testing.T) {
	d := makeDomain(t)
	cmd := exec.Command("/bin/sleep", "600")
	if err := d.Start(timeout(t), cmd); err != nil {
		t.Fatal(err)
	}
	<-d.s.gate
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	proof, err := d.Fence(ctx)
	d.s.leave()
	if proof != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out drain minted proof: %v %v", proof, err)
	}
	if empty, err := unpopulated(d.s.path); err != nil || empty {
		t.Fatalf("deadline without gate killed survivor: %v %v", empty, err)
	}
	if err := d.Start(timeout(t), exec.Command("/bin/true")); !errors.Is(err, ErrClosed) {
		t.Fatalf("deadline reopened entry: %v", err)
	}
	if _, err := d.Fence(timeout(t)); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
}

func TestKernelEnrollmentFailureHasNoFallback(t *testing.T) {
	d := makeDomain(t)
	if err := os.Remove(filepath.Join(d.s.path, "supervisors")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "must-not-run")
	cmd := exec.Command("/bin/sh", "-c", `echo escaped > "$1"`, "helper", marker)
	if err := d.Start(timeout(t), cmd); err == nil {
		_ = cmd.Wait()
		t.Fatal("kernel rejected enrollment but command started anyway")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target executed despite rejected enrollment: %v", err)
	}
	if cmd.SysProcAttr != nil {
		t.Fatal("failed Start retained a raw cgroup descriptor")
	}
	if empty, err := unpopulated(d.s.path); err != nil || !empty {
		t.Fatalf("failed enrollment left a process: %v %v", empty, err)
	}
}

func TestRecordRejectsNoncanonicalAndLinks(t *testing.T) {
	r := record{
		Version: 1, Epoch: strings.Repeat("a", 32), Boot: "00000000-0000-0000-0000-000000000001",
		Root: objectID{Inode: 1}, Parent: objectID{Inode: 2}, Group: objectID{Inode: 3},
	}
	canonical, _ := json.Marshal(r)
	for i, data := range [][]byte{
		[]byte("{}"), append(canonical, '\n'), []byte(`{"version":1,"version":1}`),
		[]byte(strings.Replace(string(canonical), `"closed":false`, `"closed":null`, 1)),
		[]byte(strings.Replace(string(canonical), `"closed":false`, `"closed":"false"`, 1)),
		[]byte(strings.Replace(string(canonical), `"closed":false`, `"closed":false,"unknown":1`, 1)),
		[]byte(strings.Replace(string(canonical), `"closed":false`, `"closed":false,"closed":false`, 1)),
		[]byte(strings.Replace(string(canonical), `"boot":"00000000-0000-0000-0000-000000000001"`, `"boot":"invalid"`, 1)),
		[]byte(strings.Replace(string(canonical), `"inode":1`, `"inode":0`, 1)),
		[]byte(strings.Repeat("x", 4097)),
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRecord(path); err == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecord(path); err != nil {
		t.Fatalf("valid control record rejected: %v", err)
	}
	link := path + "-link"
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecord(path); err == nil {
		t.Fatal("hardlinked record accepted")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecord(link); err == nil {
		t.Fatal("symlink record accepted")
	}
	fifo := path + "-fifo"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecord(fifo); err == nil {
		t.Fatal("FIFO record accepted")
	}
}

func TestInvalidHandlesAndNonCgroup(t *testing.T) {
	if _, err := (&Domain{}).Fence(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	root, ordinary := privateRoot(t), t.TempDir()
	if _, err := Create(root, ordinary); err == nil {
		t.Fatal("ordinary filesystem accepted as cgroup")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed constructor changed metadata root")
	}
}

func TestParentMustRemainInsideOwnBudget(t *testing.T) {
	for _, parent := range []string{
		"/sys/fs/cgroup", "/sys/fs/cgroup/service", "/sys/fs/cgroup/sibling/child",
		"/sys/fs/cgroup/service/../escape", "/elsewhere", "relative",
	} {
		if err := validateParent(parent, "0::/service\n"); err == nil {
			t.Fatalf("out-of-budget parent accepted: %s", parent)
		}
	}
	for _, membership := range []string{"0::/\n", "0::/../service\n", "1:cpu:/service\n", "0::/service\n0::/other\n"} {
		if err := validateParent("/sys/fs/cgroup/service/owned", membership); err == nil {
			t.Fatalf("ambiguous membership accepted: %q", membership)
		}
	}
	if err := validateParent("/sys/fs/cgroup/service/owned", "0::/service\n"); err != nil {
		t.Fatal(err)
	}
}
