//go:build linux

package artifactfs

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestOwnerKernelPreparedTransition(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	for _, mode := range []string{"pre-rename", "post-rename", "marker-only",
		"final-barrier", "tampered", "foreign-witness", "unknown-layout", "missing-group", "capacity", "unprepared"} {
		t.Run(mode, func(t *testing.T) {
			ctx := ownerTestContext(t)
			cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
			cfg.Host = testOwnerHost(t)
			cfg.MaxEpochs = 4
			if mode == "capacity" {
				cfg.MaxEpochs = 2
			}
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
			base := defaultOwnerOps()
			ops := base
			switch mode {
			case "post-rename":
				ops.remove = func(path string) error { return os.ErrPermission }
			case "marker-only":
				ops.remove = func(path string) error {
					if filepath.Base(path) == ownerPreparedFile {
						return os.ErrPermission
					}
					return base.remove(path)
				}
			case "final-barrier":
				removed := false
				ops.remove = func(path string) error {
					err := base.remove(path)
					removed = err == nil && filepath.Base(path) == ownerPreparedFile
					return err
				}
				ops.sync = func(f *os.File) error {
					if removed && f.Name() == cfg.MetadataRoot {
						return os.ErrPermission
					}
					return base.sync(f)
				}
			case "unprepared":
				ops.write = func(f *os.File, raw []byte) (int, error) {
					if filepath.Base(f.Name()) == "owner.json.tmp" {
						return 0, os.ErrPermission
					}
					return base.write(f, raw)
				}
			default:
				ops.rename = func(_, _ string) error { return os.ErrPermission }
			}
			if got, err := openOwner(ctx, cfg, true, false, ops); got != nil || !errors.Is(err, ErrIO) {
				t.Fatalf("interruption returned an owner or wrong error: %v", err)
			}
			temp := "owner.json.tmp"
			if mode == "post-rename" || mode == "marker-only" || mode == "final-barrier" {
				temp = "owner.json"
			}
			succ, err := readOwnerRecord(filepath.Join(cfg.MetadataRoot, temp))
			if mode != "unprepared" && (err != nil || len(succ.Epochs) != 2) {
				t.Fatalf("candidate not staged: %v", err)
			}
			var candidate ownerEpochRecord
			if mode != "unprepared" {
				candidate = succ.Epochs[1]
			}
			preparePath := filepath.Join(cfg.MetadataRoot, ownerPreparedFile)
			switch mode {
			case "tampered":
				raw, err := os.ReadFile(preparePath)
				if err != nil {
					t.Fatal(err)
				}
				if raw[len(raw)-3] == '0' {
					raw[len(raw)-3] = '1'
				} else {
					raw[len(raw)-3] = '0'
				}
				if err := os.WriteFile(preparePath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign-witness":
				cfg.Host = testOwnerHost(t)
			case "unknown-layout":
				if err := os.Mkdir(filepath.Join(cfg.MetadataRoot, "unknown"), 0700); err != nil {
					t.Fatal(err)
				}
			case "missing-group":
				ownerRemoveGroups(t, filepath.Join(parent, "epoch-"+candidate.Reference.Epoch))
			}
			got, err := OpenOwnerRecovery(ctx, cfg)
			switch mode {
			case "tampered", "foreign-witness", "unknown-layout", "missing-group", "unprepared":
				if got != nil || err == nil {
					t.Fatal("ambiguous transition accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = got.Close(ctx) })
			bindings, err := got.Bindings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := bindings.Domain.Reference(ctx)
			n := len(got.s.disk.Epochs)
			want := 3
			if mode == "capacity" {
				want = 2
				for _, path := range []string{got.s.epochRoot(first.ID), filepath.Join(parent, "epoch-"+first.Reference.Epoch)} {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("oldest prepared predecessor not retired", err)
					}
				}
			}
			if err != nil || n != want || got.s.disk.Epochs[n-2] != candidate ||
				ref == candidate.Reference || ref != got.s.disk.Epochs[n-1].Reference {
				t.Fatalf("did not fence and rebind to a fresh exact epoch: %v", err)
			}
			candidateFile := filepath.Join(got.s.epochRoot(candidate.ID), "epoch.json")
			raw, err := os.ReadFile(candidateFile)
			if err != nil || !bytes.Contains(raw, []byte(`"closed":true`)) {
				t.Fatalf("candidate entry was not durably closed: %v", err)
			}
			for _, pair := range []struct {
				store *Store
				id    string
			}{{bindings.Code, code}, {bindings.Runtime, runtime}} {
				lease, err := pair.store.Acquire(pair.id)
				if err != nil {
					t.Fatal(err)
				}
				lease.Release()
			}
			assertNoOwnerIntent(t, cfg)
			if _, err := os.Lstat(preparePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("prepared marker remains", err)
			}
			if err := got.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnerKernelPreparedProcessCrash(t *testing.T) {
	testOwnerProcessCrash(t, false)
}

func TestOwnerKernelRetirementProcessCrash(t *testing.T) {
	testOwnerProcessCrash(t, true)
}

func testOwnerProcessCrash(t *testing.T, retirement bool) {
	t.Helper()
	phases := []string{"pre-rename", "post-rename"}
	if retirement {
		phases = []string{"retire-pre-rename", "retire-post-rename"}
	}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			budget, parent := ownerTestBudget(t, false)
			base, dir := filepath.Dir(parent), t.TempDir()
			ctx := ownerTestContext(t)
			cfg := ownerConfigAt(t, dir, budget, parent)
			cfg.Host = testOwnerHost(t)
			if retirement {
				cfg.MaxEpochs = 2
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, self, "-test.run=^TestOwnerPreparedCrashHelper$")
			cmd.Env = []string{"PATH=/usr/bin:/bin", "OWNER_PREPARED_CRASH=" + phase,
				"OWNER_CRASH_DIR=" + dir, "OWNER_CRASH_BUDGET=" + base,
				"OWNER_CRASH_WITNESS=" + cfg.Host.WitnessPath}
			fd, err := syscall.Open(base, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: fd}
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			err = cmd.Start()
			_ = syscall.Close(fd)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			awaitOwnerFile(t, filepath.Join(dir, "captured"))
			ownerDelegate(t, base)
			o, err := NewOwner(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			code := sealed(t, o.s.code, KindCode, ids.New())
			runtime := sealed(t, o.s.runtime, KindRuntime, "node")
			first := o.s.disk.Epochs[0]
			if err := o.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if retirement {
				o, err = OpenOwnerRecovery(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := o.Close(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "delegate"), []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			var exit *exec.ExitError
			if err := cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 42 {
				t.Fatalf("did not exit at the selected barrier: %v (%s)", err, output.String())
			}
			got, err := OpenOwnerRecovery(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = got.Close(ctx) }()
			if retirement {
				assertEpochAbsent(t, cfg, first)
				assertRetiredBindings(t, ctx, got, code, runtime)
			} else if len(got.s.disk.Epochs) != 3 {
				t.Fatal("crashed candidate was not retained and replaced")
			}
			lease, err := got.s.code.Acquire(code)
			if err != nil {
				t.Fatal(err)
			}
			lease.Release()
			if err := got.Close(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOwnerPreparedCrashHelper(t *testing.T) {
	phase := os.Getenv("OWNER_PREPARED_CRASH")
	switch phase {
	case "pre-rename", "post-rename", "retire-pre-rename", "retire-post-rename":
	default:
		t.Skip("subprocess only")
	}
	base, dir := os.Getenv("OWNER_CRASH_BUDGET"), os.Getenv("OWNER_CRASH_DIR")
	budget, err := workloadfence.CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(base, "crashed-daemon")
	if err := os.Mkdir(leaf, 0700); err != nil {
		t.Fatal(err)
	}
	ownerMove(t, leaf)
	if err := os.WriteFile(filepath.Join(dir, "captured"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitOwnerFile(t, filepath.Join(dir, "delegate"))
	cfg := ownerConfigAt(t, dir, budget, filepath.Join(base, "domains"))
	cfg.Host = &OwnerHostConfig{WitnessPath: os.Getenv("OWNER_CRASH_WITNESS"), BackupRoots: []string{}}
	if phase == "retire-pre-rename" || phase == "retire-post-rename" {
		cfg.MaxEpochs = 2
	}
	ops := defaultOwnerOps()
	if phase == "pre-rename" || phase == "retire-pre-rename" {
		ops.rename = func(_, _ string) error {
			os.Exit(42)
			return nil
		}
	} else {
		ops.remove = func(_ string) error {
			os.Exit(42)
			return nil
		}
	}
	_, err = openOwner(ownerTestContext(t), cfg, true, false, ops)
	t.Fatal("crash hook not reached", err)
}
