//go:build linux

package artifactfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func testOwnerHost(t *testing.T) *OwnerHostConfig {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "host")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "witness")
	if err := os.WriteFile(path, []byte(strings.Repeat("12", 32)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return &OwnerHostConfig{WitnessPath: path, BackupRoots: []string{t.TempDir()}}
}

func TestOwnerBootSelection(t *testing.T) {
	a := "aaca99ca-deec-4a58-bf5a-1cb50f2b7357"
	b := "baca99ca-deec-4a58-bf5a-1cb50f2b7357"
	c := "caca99ca-deec-4a58-bf5a-1cb50f2b7357"
	for _, tc := range []struct {
		name       string
		run        []string
		boot       string
		cold, deny bool
	}{
		{"same boot", []string{a, a, b, b}, b, false, false},
		{"prior boot", []string{a, a, b}, c, true, false},
		{"current boot in prefix", []string{a, b}, a, false, true},
		{"discontinuous", []string{a, b, a}, c, false, true},
		{"empty", nil, a, false, true},
		{"invalid kernel boot", []string{a}, "invalid", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var epochs []ownerEpochRecord
			for _, boot := range tc.run {
				epochs = append(epochs, ownerEpochRecord{Reference: workloadfence.EpochReference{Boot: boot}})
			}
			cold, err := ownerNeedsColdRecovery(epochs, tc.boot)
			if cold != tc.cold || (err != nil) != tc.deny {
				t.Fatalf("cold=%v error=%v", cold, err)
			}
		})
	}
}

func TestOwnerHostWitness(t *testing.T) {
	host := testOwnerHost(t)
	base := t.TempDir()
	cfg := ownerConfigAt(t, base, nil, "/unused")
	cfg.Host = host
	w, err := openOwnerWitness(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.file.Close() })
	if second, err := openOwnerWitness(cfg); !errors.Is(err, ErrBusy) || second != nil {
		t.Fatal("witness relocked", err)
	}

	record := ownerRecord{Version: 2, ID: "owner"}
	if err := w.sign(&record); err != nil {
		t.Fatal(err)
	}
	if err := w.verify(record); err != nil {
		t.Fatal(err)
	}
	changed := record
	changed.ID = "different owner"
	if !errors.Is(w.verify(changed), ErrIntegrity) {
		t.Fatal("foreign association accepted")
	}
	if err := os.WriteFile(host.WitnessPath, []byte(strings.Repeat("34", 32)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(w.sign(&changed), ErrIntegrity) {
		t.Fatal("rotated witness could re-sign a held association")
	}
	raw, err := json.Marshal(record)
	if err != nil || bytes.Contains(raw, []byte(strings.Repeat("12", 32))) {
		t.Fatal("witness secret serialized", err)
	}
	if err := os.Rename(host.WitnessPath, host.WitnessPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host.WitnessPath, []byte(strings.Repeat("12", 32)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(w.verify(record), ErrIntegrity) {
		t.Fatal("replaced witness accepted through held descriptor")
	}
	other, err := openOwnerWitness(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer other.file.Close()
	if !errors.Is(other.verify(record), ErrIntegrity) {
		t.Fatal("copied witness accepted")
	}
}

func TestOwnerHostRejectsInvalidWitness(t *testing.T) {
	for _, scenario := range []string{"missing", "permissions", "linked", "noncanonical", "oversize", "symlink-ancestor", "inside-asset", "inside-backup", "backup-alias", "unknown-backups", "zero"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := ownerConfigAt(t, t.TempDir(), nil, "/unused")
			cfg.Host = testOwnerHost(t)
			witness := cfg.Host.WitnessPath
			var err error
			switch scenario {
			case "missing":
				err = os.Remove(witness)
			case "permissions":
				err = os.Chmod(witness, 0644)
			case "linked":
				err = os.Link(witness, witness+".link")
			case "noncanonical":
				err = os.WriteFile(witness, []byte(strings.Repeat("AB", 32)+"\n"), 0600)
			case "oversize":
				err = os.WriteFile(witness, []byte(strings.Repeat("1", 4096)), 0600)
			case "symlink-ancestor":
				alias := filepath.Join(t.TempDir(), "alias")
				err = os.Symlink(filepath.Dir(witness), alias)
				cfg.Host.WitnessPath = filepath.Join(alias, filepath.Base(witness))
			case "inside-asset", "inside-backup", "backup-alias":
				dir := cfg.CodeRoot
				if scenario != "inside-asset" {
					dir = cfg.Host.BackupRoots[0]
				}
				if scenario == "backup-alias" {
					alias := filepath.Join(t.TempDir(), "alias")
					if err := os.Symlink(dir, alias); err != nil {
						t.Fatal(err)
					}
					cfg.Host.BackupRoots[0] = alias
				}
				cfg.Host.WitnessPath = filepath.Join(dir, "host", "witness")
				if err := os.Mkdir(filepath.Dir(cfg.Host.WitnessPath), 0700); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(cfg.Host.WitnessPath, []byte(strings.Repeat("12", 32)+"\n"), 0600)
			case "unknown-backups":
				cfg.Host.BackupRoots = nil
			case "zero":
				err = os.WriteFile(witness, []byte(strings.Repeat("00", 32)+"\n"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if w, err := openOwnerWitness(cfg); err == nil || w != nil {
				t.Fatal("invalid witness accepted", err)
			}
		})
	}
}

func TestOwnerHostBootRuns(t *testing.T) {
	const a = "12345678-1234-4234-8234-123456789abc"
	const b = "22345678-1234-4234-8234-123456789abc"
	const c = "32345678-1234-4234-8234-123456789abc"
	for _, tc := range []struct {
		boots []string
		start int
		valid bool
	}{
		{[]string{a, a, b, b, c, c}, 4, true},
		{[]string{a, a}, 0, true},
		{[]string{a, b, a}, 0, false},
		{[]string{a, b, c, b}, 0, false},
		{[]string{a, "00000000-0000-0000-0000-000000000000"}, 0, false},
		{[]string{a, ""}, 0, false},
		{nil, 0, false},
	} {
		var epochs []ownerEpochRecord
		for _, boot := range tc.boots {
			epochs = append(epochs, ownerEpochRecord{Reference: workloadfence.EpochReference{Boot: boot}})
		}
		start, err := lastOwnerBootRun(epochs)
		if (err == nil) != tc.valid || (tc.valid && start != tc.start) {
			t.Errorf("boot sequence %q: start=%d err=%v", tc.boots, start, err)
		}
	}
}

func TestOwnerKernelHostRecovery(t *testing.T) {
	budget, parent := ownerTestBudget(t, true)
	ctx := ownerTestContext(t)
	cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
	cfg.Host = testOwnerHost(t)
	cfg.MaxEpochs = 6
	initial, err := NewOwner(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	first, err := initial.Bindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code := sealed(t, first.Code, KindCode, ids.New())
	runtime := sealed(t, first.Runtime, KindRuntime, "node")
	if err := initial.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if o, err := OpenOwnerColdRecovery(ctx, cfg); err == nil || o != nil {
		t.Fatal("cold opener accepted a current boot", err)
	}
	assertNoOwnerIntent(t, cfg)

	current, err := OpenOwnerRecovery(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Close(ctx); err != nil {
		t.Fatal(err)
	}
	disk := current.s.disk
	// Synthesize historical metadata only. The budget and kernel boot ID are real.
	priorBoot := "22345678-1234-4234-8234-123456789abc"
	if disk.Epochs[0].Reference.Boot == priorBoot {
		t.Fatal("unexpected fixture boot ID collision")
	}
	for i := range disk.Epochs {
		e := &disk.Epochs[i]
		path := filepath.Join(current.s.epochRoot(e.ID), "epoch.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.Replace(raw, []byte(`"boot":"`+e.Reference.Boot+`"`), []byte(`"boot":"`+priorBoot+`"`), 1)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		e.Reference.Boot = priorBoot
		ownerRemoveGroups(t, filepath.Join(parent, "epoch-"+e.Reference.Epoch))
	}
	w, err := openOwnerWitness(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord := func() {
		t.Helper()
		raw, err := json.Marshal(disk)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg.MetadataRoot, "owner.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.sign(&disk); err != nil {
		t.Fatal(err)
	}
	if err := w.file.Close(); err != nil {
		t.Fatal(err)
	}
	writeRecord()

	legacy := cfg
	legacy.Host = nil
	if o, err := OpenOwnerColdRecovery(ctx, legacy); err == nil || o != nil {
		t.Fatal("cold opener accepted no witness", err)
	}
	if o, err := OpenOwnerRecovery(ctx, legacy); err == nil || o != nil {
		t.Fatal("same-boot opener downgraded a bound owner", err)
	}
	foreign := cfg
	foreign.Host = testOwnerHost(t)
	if o, err := OpenOwnerColdRecovery(ctx, foreign); err == nil || o != nil {
		t.Fatal("cold opener accepted foreign witness", err)
	}
	if o, err := OpenOwnerRecovery(ctx, cfg); err == nil || o != nil {
		t.Fatal("same-boot opener crossed a boot boundary", err)
	}
	intent := filepath.Join(cfg.MetadataRoot, "owner.intent")
	if err := os.WriteFile(intent, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if o, err := OpenOwnerColdRecovery(ctx, cfg); err == nil || o != nil {
		t.Fatal("cold opener accepted leftover intent", err)
	}
	if err := os.Remove(intent); err != nil {
		t.Fatal(err)
	}
	assertNoOwnerIntent(t, cfg)

	recovered, err := OpenOwnerColdRecovery(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	bind, err := recovered.Bindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for store, generation := range map[*Store]string{bind.Code: code, bind.Runtime: runtime} {
		lease, err := store.Acquire(generation)
		if err != nil {
			t.Fatal(err)
		}
		lease.Release()
	}
	if got := recovered.s.disk.Epochs; len(got) != 3 || got[2].Reference.Boot == priorBoot {
		t.Fatal("cold recovery did not append its real current run")
	}
	for _, old := range disk.Epochs {
		if _, err := os.Stat(filepath.Join(parent, "epoch-"+old.Reference.Epoch)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cold recovery recreated an old cgroup", err)
		}
	}
	if err := recovered.Close(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := OpenOwnerRecovery(ctx, cfg)
	if err != nil {
		t.Fatal("same-boot recovery after cold recovery:", err)
	}
	if err := next.Close(ctx); err != nil {
		t.Fatal(err)
	}
	missing := next.s.disk.Epochs[2] // Not the latest epoch, but in the current run.
	ownerRemoveGroups(t, filepath.Join(parent, "epoch-"+missing.Reference.Epoch))
	if o, err := OpenOwnerRecovery(ctx, cfg); err == nil || o != nil {
		t.Fatal("same-boot opener skipped a missing current-run cgroup", err)
	}
	if o, err := OpenOwnerColdRecovery(ctx, cfg); err == nil || o != nil {
		t.Fatal("cold opener skipped a missing current-run cgroup", err)
	}
	assertNoOwnerIntent(t, cfg)
}

func assertNoOwnerIntent(t *testing.T, cfg OwnerConfig) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(cfg.MetadataRoot, "owner.intent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unexpected transition intent", err)
	}
}
