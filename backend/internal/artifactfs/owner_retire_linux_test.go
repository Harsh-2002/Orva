//go:build linux

package artifactfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestOwnerKernelRetirementBound(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	ctx := ownerTestContext(t)
	cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
	cfg.Host, cfg.MaxEpochs = testOwnerHost(t), 3
	o, err := NewOwner(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	code := sealed(t, o.s.code, KindCode, ids.New())
	runtime := sealed(t, o.s.runtime, KindRuntime, "node")
	retired := sealed(t, o.s.code, KindCode, ids.New())
	if err := o.s.code.Retire(retired); err != nil {
		t.Fatal(err)
	}
	stage, _ := begin(t, o.s.code, 100)
	stage.rec.quarantined = true // Simulate process loss without aborting the stage.
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var wantQuarantine []OwnerQuarantine
	for i := 1; i <= 12; i++ {
		before := o.s.disk
		o, err = OpenOwnerRecovery(ctx, cfg)
		if err != nil {
			t.Fatalf("restart %d: %v", i, err)
		}
		assertRetiredBindings(t, ctx, o, code, runtime)
		entries := o.Quarantined()
		if i == 1 {
			wantQuarantine = entries
			if len(entries) != 2 {
				t.Fatalf("expected retired generation and stage, got %v", entries)
			}
		}
		if !reflect.DeepEqual(entries, wantQuarantine) {
			t.Fatalf("restart %d changed quarantine inventory", i)
		}
		if len(before.Epochs) == cfg.MaxEpochs {
			assertEpochAbsent(t, cfg, before.Epochs[0])
			if !reflect.DeepEqual(before.Epochs[1:], o.s.disk.Epochs[:cfg.MaxEpochs-1]) {
				t.Fatal("retirement did not preserve the newer epochs")
			}
		}
		dirs, err := os.ReadDir(filepath.Join(cfg.MetadataRoot, "epochs"))
		if err != nil || len(dirs) != min(i+1, cfg.MaxEpochs) {
			t.Fatalf("restart %d: metadata count=%d, err=%v", i, len(dirs), err)
		}
		groups, err := os.ReadDir(parent)
		n := 0
		for _, entry := range groups {
			if entry.IsDir() {
				n++
			}
		}
		if err != nil || n != len(dirs) {
			t.Fatalf("restart %d: group count=%d, err=%v", i, n, err)
		}
		if err := o.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOwnerKernelRetirementFaults(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	for _, step := range []string{"marker-write", "marker-file-sync", "marker-dir-sync",
		"successor-write", "successor-sync", "rename", "commit-sync", "epoch-unlink",
		"epoch-sync", "epoch-rmdir", "epochs-sync", "marker-unlink", "final-sync"} {
		t.Run(step, func(t *testing.T) {
			ctx := ownerTestContext(t)
			cfg, first, code, runtime := fullRetireOwner(t, ctx, budget, parent)
			root := filepath.Join(cfg.MetadataRoot, "epochs", "epoch-"+first.ID)
			base := defaultOwnerOps()
			ops := base
			hit, promoted, unmarked := false, false, false
			fail := func(name string) bool {
				if name == step {
					hit = true
				}
				return name == step
			}
			ops.write = func(f *os.File, raw []byte) (int, error) {
				name := map[string]string{ownerRetireFile: "marker-write", "owner.json.tmp": "successor-write"}[filepath.Base(f.Name())]
				if fail(name) {
					return base.write(f, raw[:len(raw)/2])
				}
				return base.write(f, raw)
			}
			ops.sync = func(f *os.File) error {
				name := map[string]string{ownerRetireFile: "marker-file-sync", "owner.json.tmp": "successor-sync",
					"epochs": "epochs-sync", filepath.Base(root): "epoch-sync"}[filepath.Base(f.Name())]
				if f.Name() == cfg.MetadataRoot {
					switch {
					case unmarked:
						name = "final-sync"
					case promoted:
						name = "commit-sync"
					default:
						name = "marker-dir-sync"
					}
				}
				if fail(name) {
					return syscall.EIO
				}
				return base.sync(f)
			}
			ops.rename = func(from, to string) error {
				if fail("rename") {
					return syscall.EIO
				}
				promoted = true
				return base.rename(from, to)
			}
			ops.remove = func(path string) error {
				name := map[string]string{ownerRetireFile: "marker-unlink", "epoch.json": "epoch-unlink",
					filepath.Base(root): "epoch-rmdir"}[filepath.Base(path)]
				if fail(name) {
					return syscall.EIO
				}
				unmarked = name == "marker-unlink"
				return base.remove(path)
			}
			got, err := openOwner(ctx, cfg, true, false, ops)
			if !hit || got != nil || !errors.Is(err, ErrIO) {
				t.Fatalf("fault did not deny before publication: hit=%v, err=%v", hit, err)
			}
			got, err = OpenOwnerRecovery(ctx, cfg)
			if step == "marker-write" || step == "successor-write" {
				if got != nil || err == nil {
					t.Fatal("torn transition accepted", err)
				}
				if _, err := os.Stat(root); err != nil {
					t.Fatal("denial removed the epoch", err)
				}
				return
			}
			if err != nil {
				t.Fatal("exact replay failed:", err)
			}
			assertEpochAbsent(t, cfg, first)
			assertRetiredBindings(t, ctx, got, code, runtime)
			if err := got.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnerKernelRetirementDenials(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	for _, mode := range []string{"tampered", "foreign-witness", "unknown-layout", "populated", "replaced", "missing-bound"} {
		t.Run(mode, func(t *testing.T) {
			ctx := ownerTestContext(t)
			cfg, first, _, _ := fullRetireOwner(t, ctx, budget, parent)
			group := filepath.Join(parent, "epoch-"+first.Reference.Epoch)
			eventsPath := filepath.Join(group, "cgroup.events")
			root := filepath.Join(cfg.MetadataRoot, "epochs", "epoch-"+first.ID)
			if mode == "missing-bound" {
				ownerRemoveGroups(t, group)
			} else {
				ops := defaultOwnerOps()
				ops.sync = func(f *os.File) error {
					if f.Name() == cfg.MetadataRoot {
						d, err := readOwnerRecord(filepath.Join(cfg.MetadataRoot, "owner.json"))
						if err == nil && len(d.Epochs) == cfg.MaxEpochs-1 {
							return syscall.EIO
						}
					}
					return f.Sync()
				}
				if got, err := openOwner(ctx, cfg, true, false, ops); got != nil || !errors.Is(err, ErrIO) {
					t.Fatalf("could not interrupt before cleanup: %v", err)
				}
			}
			switch mode {
			case "tampered":
				path := filepath.Join(cfg.MetadataRoot, ownerRetireFile)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				i := bytes.Index(raw, []byte(`"mac":"`)) + len(`"mac":"`)
				if raw[i] == '0' {
					raw[i] = '1'
				} else {
					raw[i] = '0'
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign-witness":
				cfg.Host = testOwnerHost(t)
			case "unknown-layout":
				if err := os.Mkdir(filepath.Join(root, "unknown"), 0700); err != nil {
					t.Fatal(err)
				}
			case "populated":
				// Simulate an outside entrant after unbinding, using only this fixture's child.
				cmd := exec.CommandContext(ctx, "/bin/sleep", "60")
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
				pid := strconv.Itoa(cmd.Process.Pid)
				if err := os.WriteFile(filepath.Join(group, "supervisors", "cgroup.procs"), []byte(pid), 0600); err != nil {
					t.Fatal("move fixture child", err)
				}
				for {
					events, err := os.ReadFile(eventsPath)
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(events, []byte("populated 1\n")) {
						break
					}
					select {
					case <-ctx.Done():
						membership, _ := os.ReadFile(filepath.Join("/proc", pid, "cgroup"))
						t.Fatalf("fixture never became populated: %q (membership %q)", events, membership)
					case <-time.After(10 * time.Millisecond):
					}
				}
			case "replaced":
				oldGroup, err := os.Open(group)
				if err != nil {
					t.Fatal(err)
				}
				defer oldGroup.Close()
				ownerRemoveGroups(t, group)
				if err := os.Mkdir(group, 0700); err != nil {
					t.Fatal(err)
				}
				replacement, err := os.Stat(group)
				if err != nil || replacement.Sys().(*syscall.Stat_t).Ino == first.Reference.GroupInode {
					t.Fatal("fixture cgroup identity was not replaced", err)
				}
			}
			if got, err := OpenOwnerRecovery(ctx, cfg); got != nil || err == nil {
				t.Fatal("ambiguous or populated retirement accepted", err)
			}
			if mode == "populated" {
				events, err := os.ReadFile(eventsPath)
				if err != nil || !bytes.Contains(events, []byte("populated 1\n")) {
					t.Fatal("cleanup fenced or modified the populated retirement", err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "epoch.json")); err != nil {
				t.Fatal("denial removed retired evidence", err)
			}
			if mode != "missing-bound" {
				if _, err := os.Stat(group); err != nil {
					t.Fatal("denial removed a group", err)
				}
			}
		})
	}
}

func TestOwnerKernelRetirementPriorBoot(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	ctx := ownerTestContext(t)
	cfg, first, code, runtime := fullRetireOwner(t, ctx, budget, parent)
	path := filepath.Join(cfg.MetadataRoot, "owner.json")
	disk, err := readOwnerRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	// Historical metadata fixture; leave the same-named groups to check that cold cleanup never visits them.
	for i := range disk.Epochs {
		e := &disk.Epochs[i]
		p := filepath.Join(cfg.MetadataRoot, "epochs", "epoch-"+e.ID, "epoch.json")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		const priorBoot = "22345678-1234-4234-8234-123456789abc"
		if e.Reference.Boot == priorBoot {
			t.Fatal("fixture boot collision")
		}
		raw = bytes.Replace(raw, []byte(e.Reference.Boot), []byte(priorBoot), 1)
		if err := os.WriteFile(p, raw, 0600); err != nil {
			t.Fatal(err)
		}
		e.Reference.Boot = priorBoot
	}
	w, err := openOwnerWitness(cfg)
	if err != nil {
		t.Fatal(err)
	}
	signErr := w.sign(&disk)
	if err := errors.Join(signErr, w.file.Close()); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}

	firstRoot := filepath.Join(cfg.MetadataRoot, "epochs", "epoch-"+first.ID)
	base := defaultOwnerOps()
	ops := base
	ops.remove = func(p string) error {
		if p == filepath.Join(firstRoot, "epoch.json") {
			return syscall.EIO
		}
		return base.remove(p)
	}
	if got, err := openOwner(ctx, cfg, true, true, ops); got != nil || !errors.Is(err, ErrIO) {
		t.Fatalf("interrupted cold retirement published bindings: %v", err)
	}
	if got, err := OpenOwnerRecovery(ctx, cfg); got != nil || err == nil {
		t.Fatal("same-boot opener replayed a cold transition", err)
	}
	got, err := OpenOwnerColdRecovery(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	assertRetiredBindings(t, ctx, got, code, runtime)
	if _, err := os.Stat(firstRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("prior-boot metadata not retired", err)
	}
	for _, e := range disk.Epochs {
		if _, err := os.Stat(filepath.Join(parent, "epoch-"+e.Reference.Epoch)); err != nil {
			t.Fatal("prior-boot cleanup modified a same-named current group", err)
		}
	}
	if e := got.s.disk.Epochs[0]; e != disk.Epochs[1] {
		t.Fatal("newest historical epoch was not retained")
	}
	if err := got.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func fullRetireOwner(t *testing.T, ctx context.Context, budget *workloadfence.Budget, parent string) (OwnerConfig, ownerEpochRecord, string, string) {
	t.Helper()
	cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
	cfg.Host, cfg.MaxEpochs = testOwnerHost(t), 2
	o, err := NewOwner(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	first := o.s.disk.Epochs[0]
	code := sealed(t, o.s.code, KindCode, ids.New())
	runtime := sealed(t, o.s.runtime, KindRuntime, "node")
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
	o, err = OpenOwnerRecovery(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return cfg, first, code, runtime
}

func assertRetiredBindings(t *testing.T, ctx context.Context, o *Owner, code, runtime string) {
	t.Helper()
	bindings, err := o.Bindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for store, id := range map[*Store]string{bindings.Code: code, bindings.Runtime: runtime} {
		lease, err := store.Acquire(id)
		if err != nil {
			t.Fatal(err)
		}
		lease.Release()
	}
	if _, err := os.Lstat(filepath.Join(o.s.cfg.MetadataRoot, ownerRetireFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retirement marker remains", err)
	}
}

func assertEpochAbsent(t *testing.T, cfg OwnerConfig, e ownerEpochRecord) {
	t.Helper()
	for _, path := range []string{filepath.Join(cfg.MetadataRoot, "epochs", "epoch-"+e.ID),
		filepath.Join(cfg.Parent, "epoch-"+e.Reference.Epoch)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retired path still present: %s: %v", path, err)
		}
	}
}
