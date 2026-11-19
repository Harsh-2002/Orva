//go:build linux

package artifactfs

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

	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func ownerTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func ownerTestBudget(t *testing.T, delegate bool) (*workloadfence.Budget, string) {
	t.Helper()
	if os.Getenv("ORVA_OWNER_KERNEL_TEST") != "disposable-vm" {
		t.Skip("owned disposable VM only")
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	base := "/sys/fs/cgroup" + strings.TrimSpace(strings.TrimPrefix(string(raw), "0::"))
	if err != nil || base == "/sys/fs/cgroup" || base != os.Getenv("ORVA_OWNER_TEST_BUDGET") {
		t.Fatal("not inside explicitly owned test budget", err)
	}
	budget, err := workloadfence.CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"daemon", "domains"} {
		if err := os.Mkdir(filepath.Join(base, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ownerMove(t, filepath.Join(base, "daemon"))
	t.Cleanup(func() {
		ownerRemoveGroups(t, filepath.Join(base, "domains"))
		for _, name := range []string{"crashed-daemon", "daemon"} {
			path := filepath.Join(base, name)
			if name == "daemon" {
				if err := os.WriteFile(filepath.Join(base, "cgroup.subtree_control"), []byte("-cpu -memory -pids"), 0600); err != nil {
					t.Error(err)
				}
				ownerMove(t, base)
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
		}
		if err := budget.Close(); err != nil {
			t.Error(err)
		}
	})
	if delegate {
		ownerDelegate(t, base)
	}
	return budget, filepath.Join(base, "domains")
}

func ownerDelegate(t *testing.T, base string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(base, "cgroup.procs"))
	if err != nil || strings.TrimSpace(string(raw)) != "" {
		t.Fatal("refusing to move any other process", err)
	}
	for _, path := range []string{base, filepath.Join(base, "domains")} {
		if err := os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func ownerMove(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
}

func ownerRemoveGroups(t *testing.T, path string) {
	t.Helper()
	events, err := os.ReadFile(filepath.Join(path, "cgroup.events"))
	if err != nil || !strings.Contains(string(events), "populated 0") {
		t.Fatal("fixture refuses to remove populated ownership", path, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			ownerRemoveGroups(t, filepath.Join(path, entry.Name()))
		}
	}
	if err := os.Remove(path); err != nil {
		t.Error(err)
	}
}

func ownerConfigAt(t *testing.T, base string, budget *workloadfence.Budget, parent string) OwnerConfig {
	t.Helper()
	for _, name := range []string{"owner", "code", "runtime"} {
		path := filepath.Join(base, name)
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	return OwnerConfig{MetadataRoot: filepath.Join(base, "owner"), CodeRoot: filepath.Join(base, "code"),
		RuntimeRoot: filepath.Join(base, "runtime"), Parent: parent, Budget: budget,
		CodeLimits: testLimits(), RuntimeLimits: testLimits(), MaxEpochs: 4}
}

func TestOwnerInvalidConfigAndRecord(t *testing.T) {
	if o, err := NewOwner(t.Context(), OwnerConfig{}); err == nil || o != nil {
		t.Fatal("missing ownership accepted")
	}
	root := t.TempDir()
	for _, data := range []string{`null`, `{"version":1,"version":1}`, `{"version":1,"unknown":true}`} {
		path := filepath.Join(root, "record")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readOwnerRecord(path); err == nil {
			t.Fatal("invalid record accepted", data)
		}
	}
}

func TestOwnerKernelCommittedRecoveryAndQuarantine(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
	o, err := NewOwner(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := o.Bindings(ownerTestContext(t))
	if err != nil {
		t.Fatal(err)
	}
	codeID := sealed(t, refs.Code, KindCode, ids.New())
	runtimeID := sealed(t, refs.Runtime, KindRuntime, "node")
	oldCode, _ := refs.Code.Acquire(codeID)
	wantCode, wantRuntime := oldCode.Identity(), refs.Runtime.records[runtimeID].identity
	copyOwner := *o
	if err := copyOwner.Close(ownerTestContext(t)); !errors.Is(err, ErrBusy) {
		t.Fatal("owner dropped outstanding lease", err)
	}
	if _, err := OpenOwnerRecovery(ownerTestContext(t), cfg); !errors.Is(err, ErrBusy) {
		t.Fatal("busy owner released its locks", err)
	}
	oldCode.Release()
	if err := o.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := refs.Code.Acquire(codeID); !errors.Is(err, ErrClosed) {
		t.Fatal("stale store acquired after owner close", err)
	}
	if err := refs.Domain.Start(t.Context(), exec.Command("/bin/true")); !errors.Is(err, workloadfence.ErrClosed) {
		t.Fatal("old entry capability revived after owner close", err)
	}
	inspection, err := OpenRecovering(t.Context(), cfg.CodeRoot, cfg.CodeLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspection.Acquire(codeID); !errors.Is(err, ErrRetired) {
		t.Fatal("unbound inspection activated")
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := OpenOwnerRecovery(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	next, err := recovered.Bindings(ownerTestContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.s.disk.Epochs[1].Reference.Epoch == o.s.disk.Epochs[0].Reference.Epoch {
		t.Fatal("recovery reused an epoch")
	}
	for _, pair := range []struct {
		store    *Store
		identity Identity
	}{{next.Code, wantCode}, {next.Runtime, wantRuntime}} {
		lease, err := pair.store.Acquire(pair.identity.Generation)
		if err != nil || lease.Identity() != pair.identity {
			t.Fatal("committed identity changed", err)
		}
		lease.Release()
	}
	stage, _ := begin(t, next.Code, 100)
	if err := recovered.Close(ownerTestContext(t)); !errors.Is(err, ErrBusy) {
		t.Fatal("live producer stage dropped", err)
	}
	// Simulate process loss of a known uncommitted stage, not public activation.
	recovered.s.code.mu.Lock()
	stage.rec.quarantined = true
	recovered.s.code.mu.Unlock()
	if err := recovered.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
	third, err := OpenOwnerRecovery(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	q := third.Quarantined()
	if len(q) != 1 || q[0].Store != KindCode || q[0].Identity.Generation != stage.rec.identity.Generation ||
		third.s.code.stages != 1 || third.s.code.bytes != 200 {
		t.Fatal("staging quarantine lost accounting", q)
	}
	if err := third.s.code.Reclaim(q[0].Identity.Generation); !errors.Is(err, ErrRetired) {
		t.Fatal("unknown stage reclaimed", err)
	}
	if err := third.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerKernelChildPinsAndConcurrentClose(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
	o, err := NewOwner(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	refs, _ := o.Bindings(t.Context())
	childRoot := t.TempDir()
	if err := os.Chmod(childRoot, 0700); err != nil {
		t.Fatal(err)
	}
	child, err := refs.Domain.NewChild(t.Context(), childRoot)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sleep", "600")
	if err := child.Start(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(ownerTestContext(t)); !errors.Is(err, workloadfence.ErrBusy) {
		t.Fatal("owner released attached child", err)
	}
	_ = cmd.Wait()
	proof, err := child.Fence(ownerTestContext(t))
	if err != nil || refs.Domain.ReclaimChild(ownerTestContext(t), child, proof) != nil {
		t.Fatal("child cleanup after owner Close", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if err := o.Close(ownerTestContext(t)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if _, err := refs.Code.Begin(t.Context(), 1); !errors.Is(err, ErrClosed) {
		t.Fatal("stale reference began after close", err)
	}
}

func TestOwnerKernelBindingsRejectReplacedRootPaths(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	for _, role := range []string{"owner", "code", "runtime"} {
		t.Run(role, func(t *testing.T) {
			cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
			o, err := NewOwner(ownerTestContext(t), cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := cfg.MetadataRoot
			if role == "code" {
				path = cfg.CodeRoot
			} else if role == "runtime" {
				path = cfg.RuntimeRoot
			}
			saved := path + ".saved"
			if err := os.Rename(path, saved); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if refs, err := o.Bindings(ownerTestContext(t)); err == nil || refs.Code != nil || refs.Domain != nil {
				t.Fatal("replaced root path yielded bindings")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(saved, path); err != nil {
				t.Fatal(err)
			}
			if err := o.Close(ownerTestContext(t)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnerKernelCancelledCloseRetainsOwnership(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
	o, err := NewOwner(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := o.Bindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := o.Bindings(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("binding cancellation lost", err)
	}
	if err := o.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled close lost ownership", err)
	}
	if got, err := OpenOwnerRecovery(ownerTestContext(t), cfg); got != nil || !errors.Is(err, ErrBusy) {
		t.Fatal("cancelled close released owner locks", err)
	}
	if _, err := refs.Code.Begin(t.Context(), 1); !errors.Is(err, ErrClosed) {
		t.Fatal("cancelled close reopened store entry", err)
	}
	if err := refs.Domain.Start(t.Context(), exec.Command("/bin/true")); !errors.Is(err, workloadfence.ErrClosed) {
		t.Fatal("cancelled close reopened process entry", err)
	}
	if err := o.Close(ownerTestContext(t)); err != nil {
		t.Fatal("close retry", err)
	}
	fresh := ownerConfigAt(t, t.TempDir(), budget, parent)
	if got, err := NewOwner(ctx, fresh); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled construction", err)
	}
	names, err := os.ReadDir(fresh.MetadataRoot)
	if err != nil || len(names) != 0 {
		t.Fatal("pre-cancelled constructor changed metadata", err)
	}
}

func TestOwnerKernelDescriptorCloseRetry(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	for _, role := range []string{"code", "runtime", "owner"} {
		for _, afterClose := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%t", role, afterClose), func(t *testing.T) {
				cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
				o, err := NewOwner(ownerTestContext(t), cfg)
				if err != nil {
					t.Fatal(err)
				}
				refs, _ := o.Bindings(t.Context())
				id := sealed(t, refs.Code, KindCode, ids.New())
				target := o.s.code.lock
				if role == "runtime" {
					target = o.s.runtime.lock
				} else if role == "owner" {
					target = o.s.meta.lock
				}
				fail := true
				o.s.closeFile = func(f *os.File) error {
					if f == target && fail {
						fail = false
						if afterClose {
							_ = f.Close()
						}
						return os.ErrPermission
					}
					return f.Close()
				}
				if err := o.Close(ownerTestContext(t)); !errors.Is(err, ErrIO) || !o.s.domainReleased || o.s.closed {
					t.Fatal("descriptor error did not retain terminal shutdown phase", err)
				}
				if _, err := refs.Code.Acquire(id); !errors.Is(err, ErrClosed) {
					t.Fatal("descriptor failure reopened asset acquisition", err)
				}
				if err := refs.Domain.Start(t.Context(), exec.Command("/bin/true")); !errors.Is(err, workloadfence.ErrClosed) {
					t.Fatal("descriptor failure reopened command entry", err)
				}
				var next *Owner
				if role == "owner" && afterClose {
					next, err = OpenOwnerRecovery(ownerTestContext(t), cfg)
					if err != nil {
						t.Fatal("fully released failed-close state could not recover", err)
					}
				} else if got, err := OpenOwnerRecovery(ownerTestContext(t), cfg); got != nil || !errors.Is(err, ErrBusy) {
					t.Fatal("remaining owner lock lost", err)
				}
				copy := *o
				if err := copy.Close(ownerTestContext(t)); err != nil {
					t.Fatal("descriptor-close retry could not finish", err)
				}
				if next != nil {
					current, err := next.Bindings(t.Context())
					if err != nil {
						t.Fatal("old close retry released replacement ownership", err)
					}
					lease, err := current.Code.Acquire(id)
					if err != nil {
						t.Fatal(err)
					}
					lease.Release()
					if err := next.Close(ownerTestContext(t)); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestOwnerKernelAssociationDenials(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	for _, mode := range []string{"swapped", "owner-root", "store-root", "foreign-record", "unknown", "intent", "temp", "epoch-extra", "record", "asset", "role", "missing-group", "wrong-boot"} {
		t.Run(mode, func(t *testing.T) {
			cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
			o, err := NewOwner(ownerTestContext(t), cfg)
			if err != nil {
				t.Fatal(err)
			}
			id := sealed(t, o.s.code, KindCode, ids.New())
			var runtimeID string
			if mode == "role" {
				runtimeID = sealed(t, o.s.runtime, KindRuntime, "node")
			}
			if err := o.Close(ownerTestContext(t)); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(cfg.MetadataRoot, "owner.json")
			raw, _ := os.ReadFile(path)
			switch mode {
			case "swapped":
				cfg.CodeRoot, cfg.RuntimeRoot = cfg.RuntimeRoot, cfg.CodeRoot
			case "owner-root", "store-root":
				disk := o.s.disk
				if mode == "owner-root" {
					disk.Root.Inode++
				} else {
					disk.Code.Identity.Inode++
				}
				data, _ := json.Marshal(disk)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign-record":
				foreign, err := NewOwner(ownerTestContext(t), ownerConfigAt(t, t.TempDir(), budget, parent))
				if err != nil {
					t.Fatal(err)
				}
				if err := foreign.Close(ownerTestContext(t)); err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(filepath.Join(foreign.s.cfg.MetadataRoot, "owner.json"))
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown", "intent", "temp":
				name := "unexpected"
				if mode == "intent" {
					name = "owner.intent"
				} else if mode == "temp" {
					name = "owner.json.tmp"
				}
				if err := os.WriteFile(filepath.Join(cfg.MetadataRoot, name), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "epoch-extra":
				if err := os.Mkdir(filepath.Join(cfg.MetadataRoot, "epochs", "unknown"), 0700); err != nil {
					t.Fatal(err)
				}
			case "record":
				if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			case "asset":
				file := filepath.Join(cfg.CodeRoot, "generation-"+id, "handler")
				if err := os.WriteFile(file, []byte("corrupt"), 0640); err != nil {
					t.Fatal(err)
				}
			case "role":
				recordPath := filepath.Join(cfg.RuntimeRoot, controlName, runtimeID+".json")
				data, err := os.ReadFile(recordPath)
				if err != nil {
					t.Fatal(err)
				}
				var record persistentRecord
				if err := json.Unmarshal(data, &record); err != nil {
					t.Fatal(err)
				}
				record.Kind, record.Owner = KindCode, ids.New()
				data, _ = json.Marshal(record)
				if err := os.WriteFile(recordPath, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-group":
				e := o.s.disk.Epochs[0]
				ownerRemoveGroups(t, filepath.Join(parent, "epoch-"+e.Reference.Epoch))
			case "wrong-boot":
				disk := o.s.disk
				disk.Epochs[0].Reference.Boot = "00000000-0000-0000-0000-000000000000"
				data, _ := json.Marshal(disk)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			if got, err := OpenOwnerRecovery(ownerTestContext(t), cfg); err == nil || got != nil {
				t.Fatal("corrupt/mixed ownership activated")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("denial rewrote association")
			}
		})
	}
}

func TestOwnerKernelDurabilityFaults(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	warm, err := NewOwner(ownerTestContext(t), ownerConfigAt(t, t.TempDir(), budget, parent))
	if err != nil {
		t.Fatal(err)
	}
	if err := warm.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"intent-write", "intent-sync", "record-write", "record-sync", "rename", "commit-sync", "remove", "final-sync"} {
		t.Run(mode, func(t *testing.T) {
			cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
			ops := defaultOwnerOps()
			base := ops
			renamed, removed := false, false
			ops.write = func(f *os.File, raw []byte) (int, error) {
				if (mode == "intent-write" && filepath.Base(f.Name()) == "owner.intent") ||
					(mode == "record-write" && filepath.Base(f.Name()) == "owner.json.tmp") {
					return 0, os.ErrPermission
				}
				return base.write(f, raw)
			}
			ops.sync = func(f *os.File) error {
				name := filepath.Base(f.Name())
				if (mode == "intent-sync" && name == "owner.intent") || (mode == "record-sync" && name == "owner.json.tmp") ||
					(mode == "commit-sync" && renamed && f.Name() == cfg.MetadataRoot) ||
					(mode == "final-sync" && removed && f.Name() == cfg.MetadataRoot) {
					return os.ErrPermission
				}
				return base.sync(f)
			}
			ops.rename = func(a, b string) error {
				if mode == "rename" {
					return os.ErrPermission
				}
				err := base.rename(a, b)
				renamed = err == nil
				return err
			}
			ops.remove = func(path string) error {
				if mode == "remove" {
					return os.ErrPermission
				}
				err := base.remove(path)
				removed = err == nil
				return err
			}
			if o, err := openLifecycleOwner(ownerTestContext(t), cfg, false, ops); err == nil || o != nil {
				t.Fatal("fsync/publication failure exposed capabilities")
			}
			if _, err := os.Stat(filepath.Join(cfg.MetadataRoot, "owner.intent")); err != nil {
				t.Fatal("fault lost denial evidence", err)
			}
			if o, err := OpenOwnerRecovery(ownerTestContext(t), cfg); err == nil || o != nil {
				t.Fatal("partial transition resumed")
			}
		})
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil || len(after) != len(before) {
		t.Fatalf("constructor failure leaked descriptors: before=%d after=%d err=%v", len(before), len(after), err)
	}
}

func TestOwnerKernelRecoveryTransitionFailures(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	for _, mode := range []string{"new-epoch-dir", "new-domain-record", "rename", "commit-sync", "post-commit-proof", "publish"} {
		t.Run(mode, func(t *testing.T) {
			cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
			o, err := NewOwner(ownerTestContext(t), cfg)
			if err != nil {
				t.Fatal(err)
			}
			code := sealed(t, o.s.code, KindCode, ids.New())
			runtime := sealed(t, o.s.runtime, KindRuntime, "python")
			oldDisk := o.s.disk
			if err := o.Close(ownerTestContext(t)); err != nil {
				t.Fatal(err)
			}
			ops := defaultOwnerOps()
			base := ops
			renamed := false
			ops.write = func(f *os.File, raw []byte) (int, error) {
				if mode == "new-domain-record" && filepath.Base(f.Name()) == "owner.json.tmp" {
					return 0, os.ErrPermission
				}
				return base.write(f, raw)
			}
			ops.rename = func(a, b string) error {
				if mode == "rename" {
					return os.ErrPermission
				}
				err := base.rename(a, b)
				renamed = err == nil
				return err
			}
			ops.sync = func(f *os.File) error {
				if (mode == "new-epoch-dir" && f.Name() == filepath.Join(cfg.MetadataRoot, "epochs")) ||
					(mode == "commit-sync" && renamed && f.Name() == cfg.MetadataRoot) {
					return os.ErrPermission
				}
				if mode == "post-commit-proof" && renamed && f.Name() == cfg.MetadataRoot {
					path := o.s.epochRoot(oldDisk.Epochs[0].ID)
					if err := os.Rename(filepath.Join(path, "epoch.json"), filepath.Join(path, "epoch.saved")); err != nil {
						return err
					}
				}
				return base.sync(f)
			}
			ops.remove = func(path string) error {
				if mode == "publish" {
					return os.ErrPermission
				}
				return base.remove(path)
			}
			if got, err := openLifecycleOwner(ownerTestContext(t), cfg, true, ops); got != nil || err == nil {
				t.Fatal("failed transition published bindings")
			}
			if _, err := os.Stat(filepath.Join(cfg.MetadataRoot, "owner.intent")); err != nil {
				t.Fatal("failed recovery lost its intent", err)
			}
			current, err := readOwnerRecord(filepath.Join(cfg.MetadataRoot, "owner.json"))
			if err != nil || current.Code != oldDisk.Code || current.Runtime != oldDisk.Runtime {
				t.Fatal("association mixed store bindings", err)
			}
			names, _ := os.ReadDir(filepath.Join(cfg.MetadataRoot, "epochs"))
			if len(names) != 2 {
				t.Fatal("transition did not preserve bounded epoch evidence", len(names))
			}
			if got, err := OpenOwnerRecovery(ownerTestContext(t), cfg); got != nil || err == nil {
				t.Fatal("incomplete recovery resumed without reconciliation")
			}
			after, _ := os.ReadDir(filepath.Join(cfg.MetadataRoot, "epochs"))
			if len(after) != len(names) {
				t.Fatal("denied retries grew epoch directories")
			}
			for _, pair := range []struct {
				root string
				id   string
			}{{cfg.CodeRoot, code}, {cfg.RuntimeRoot, runtime}} {
				inspection, err := OpenRecovering(t.Context(), pair.root, testLimits())
				if err != nil {
					t.Fatal("failed recovery damaged committed assets", err)
				}
				if _, err := inspection.Acquire(pair.id); !errors.Is(err, ErrRetired) {
					t.Fatal("failed recovery leaked activation", err)
				}
				_ = inspection.Close()
			}
		})
	}
}

func TestOwnerKernelCrashAndSameBootRecovery(t *testing.T) {
	budget, parent := ownerTestBudget(t, false)
	base := filepath.Dir(parent)
	dir := t.TempDir()
	cfg := ownerConfigAt(t, dir, budget, parent)
	self, _ := os.Executable()
	cmd := exec.Command(self, "-test.run=^TestOwnerCrashHelper$")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "ORVA_OWNER_CRASH_HELPER=1", "OWNER_CRASH_DIR=" + dir, "OWNER_CRASH_BUDGET=" + base}
	fd, err := syscall.Open(base, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: fd}
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Close(fd)
	awaitOwnerFile(t, filepath.Join(dir, "captured"))
	ownerDelegate(t, base)
	if err := os.WriteFile(filepath.Join(dir, "delegate"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitOwnerFile(t, filepath.Join(dir, "started"))
	if err := cmd.Wait(); err != nil {
		t.Fatal("crash helper", err, stderr.String())
	}
	o, err := OpenOwnerRecovery(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	refs, err := o.Bindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var data struct{ Code, Runtime, PID string }
	raw, _ := os.ReadFile(filepath.Join(dir, "started"))
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		store *Store
		id    string
	}{{refs.Code, data.Code}, {refs.Runtime, data.Runtime}} {
		lease, err := pair.store.Acquire(pair.id)
		if err != nil {
			t.Fatal(err)
		}
		lease.Release()
	}
	if raw, err := os.ReadFile("/proc/" + data.PID + "/stat"); err == nil && !strings.Contains(string(raw), ") Z ") {
		t.Fatal("recovery left old descendant alive")
	}
	if err := o.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
	t.Log("separate owner process exited with live descendant; exact same-boot root fenced before both committed stores rebound")
}

func awaitOwnerFile(t *testing.T, path string) {
	t.Helper()
	ctx := ownerTestContext(t)
	for {
		if data, err := os.ReadFile(path); err == nil && len(data) != 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("helper readiness", path)
			return
		case <-time.After(time.Millisecond):
		}
	}
}

func TestOwnerCrashHelper(t *testing.T) {
	if os.Getenv("ORVA_OWNER_CRASH_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	base, dir := os.Getenv("OWNER_CRASH_BUDGET"), os.Getenv("OWNER_CRASH_DIR")
	budget, err := workloadfence.CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "crashed-daemon"), 0700); err != nil {
		t.Fatal(err)
	}
	ownerMove(t, filepath.Join(base, "crashed-daemon"))
	if err := os.WriteFile(filepath.Join(dir, "captured"), []byte("captured"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitOwnerFile(t, filepath.Join(dir, "delegate"))
	cfg := ownerConfigAt(t, dir, budget, filepath.Join(base, "domains"))
	o, err := NewOwner(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := o.Bindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	code, runtime := sealed(t, refs.Code, KindCode, ids.New()), sealed(t, refs.Runtime, KindRuntime, "node")
	cmd := exec.Command("/bin/sleep", "600")
	if err := refs.Domain.Start(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(struct{ Code, Runtime, PID string }{code, runtime, strconv.Itoa(cmd.Process.Pid)})
	if err := os.WriteFile(filepath.Join(dir, "started"), data, 0600); err != nil {
		t.Fatal(err)
	}
	// Exit without Close/Wait to model ownership loss, not graceful shutdown.
	os.Exit(0)
}

func TestOwnerKernelRoleAndEpochCapacity(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
	cfg.MaxEpochs = 2
	o, err := NewOwner(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	st, path := begin(t, o.s.runtime, 100)
	write(t, filepath.Join(path, "handler"), "hello")
	if _, err := sealManifest(t, st, KindCode, ids.New(), fixtureManifest(t, path)); !errors.Is(err, ErrInvalid) {
		t.Fatal("store role changed", err)
	}
	if err := st.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
	next, err := OpenOwnerRecovery(ownerTestContext(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(cfg.MetadataRoot, "owner.json"))
	if got, err := OpenOwnerRecovery(ownerTestContext(t), cfg); !errors.Is(err, ErrCapacity) || got != nil {
		t.Fatal("epoch history unbounded", err)
	}
	after, _ := os.ReadFile(filepath.Join(cfg.MetadataRoot, "owner.json"))
	if string(before) != string(after) {
		t.Fatal("capacity denial rewrote owner")
	}
	t.Log(fmt.Sprintf("retained epoch history bound=%d", cfg.MaxEpochs))
}

func TestOwnerKernelPreparedPathsRejectMixedBindings(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	one, err := NewOwner(ownerTestContext(t), ownerConfigAt(t, t.TempDir(), budget, parent))
	if err != nil {
		t.Fatal(err)
	}
	two, err := NewOwner(ownerTestContext(t), ownerConfigAt(t, t.TempDir(), budget, parent))
	if err != nil {
		t.Fatal(err)
	}
	code := sealed(t, one.s.code, KindCode, ids.New())
	runtime := sealed(t, one.s.runtime, KindRuntime, "node")
	foreign := sealed(t, two.s.runtime, KindRuntime, "node")
	legacy := newStore(t, testLimits())
	unbound := sealed(t, legacy, KindRuntime, "node")
	for _, pair := range []struct {
		store *Store
		id    string
	}{{two.s.runtime, foreign}, {legacy, unbound}} {
		if paths, err := PreparePaths(one.s.code, code, pair.store, pair.id); paths != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("cross-owner/unbound pair accepted", err)
		}
	}
	paths, err := PreparePaths(one.s.code, code, one.s.runtime, runtime)
	if err != nil {
		t.Fatal("same-owner pair denied", err)
	}
	paths.Release()
	if err := one.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
	if err := two.Close(ownerTestContext(t)); err != nil {
		t.Fatal(err)
	}
}
