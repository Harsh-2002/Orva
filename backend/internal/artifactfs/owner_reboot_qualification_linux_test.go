//go:build linux && owner_reboot_qualification

package artifactfs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

const (
	rebootState  = "/scratch/qual/state"
	rebootBudget = "/sys/fs/cgroup/orva-owner-reboot"
)

type rebootFacts struct {
	Boot            string
	Epoch           ownerEpochRecord
	Code, Runtime   Identity
	OwnerPID, Child int
	Objects         map[string]ownerObject
	Inventory       [32]byte
	EpochDigest     [32]byte
}

func TestOwnerActualReboot(t *testing.T) {
	if os.Getenv("ORVA_OWNER_KERNEL_TEST") != "disposable-vm" || os.Geteuid() != 0 {
		t.Fatal("explicit disposable guest root required")
	}
	phase := os.Getenv("ORVA_OWNER_REBOOT_PHASE")
	boot := strings.TrimSpace(string(rebootRead(t, "/proc/sys/kernel/random/boot_id")))
	if !ownerBoot(boot) || boot == "00000000-0000-0000-0000-000000000000" {
		t.Fatal("invalid current kernel boot")
	}
	if phase == "check" {
		awaitOwnerFile(t, filepath.Join(rebootState, "ready.json"))
		facts := readRebootFacts(t)
		if facts.Boot != boot {
			t.Fatal("wrong boot during pre-stop verification")
		}
		verifyRebootChild(t, facts)
		t.Logf("pre-hard-stop boot=%s owner_pid=%d child_pid=%d enrolled=yes populated=1", boot, facts.OwnerPID, facts.Child)
		return
	}
	if (phase != "first" && phase != "second") || os.Getenv("ORVA_OWNER_TEST_BUDGET") != rebootBudget {
		t.Fatal("unknown phase or budget")
	}
	budget, parent := ownerTestBudget(t, true)
	ctx := ownerTestContext(t)
	host := OwnerHostConfig{WitnessPath: filepath.Join(rebootState, "host", "witness"),
		BackupRoots: []string{filepath.Join(rebootState, "backup")}}
	if phase == "first" {
		rebootMkdir(t, rebootState)
		rebootMkdir(t, host.BackupRoots[0])
		provisionRebootWitness(t, host.WitnessPath)
		cfg := ownerConfigAt(t, rebootState, budget, parent)
		cfg.Host = &host
		o, err := NewOwner(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		b, err := o.Bindings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		facts := rebootFacts{Boot: boot, OwnerPID: os.Getpid(), Epoch: o.s.disk.Epochs[0]}
		if facts.Epoch.Reference.Boot != boot {
			t.Fatal("owner and kernel disagree on boot")
		}
		facts.Code = rebootAsset(t, b.Code, sealed(t, b.Code, KindCode, ids.New()), Identity{})
		facts.Runtime = rebootAsset(t, b.Runtime, sealed(t, b.Runtime, KindRuntime, "node"), Identity{})
		facts.Objects = rebootObjects(t, cfg, facts.Epoch)
		facts.Inventory = rebootInventory(t, cfg)
		facts.EpochDigest = sha256.Sum256(rebootRead(t, rebootEpochRecord(cfg, facts.Epoch)))
		cmd := exec.Command("/bin/sleep", "300")
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		if err := b.Domain.Start(ctx, cmd); err != nil {
			t.Fatal("enroll child:", err)
		}
		facts.Child = cmd.Process.Pid
		verifyRebootChild(t, facts)
		writeRebootFacts(t, facts)
		t.Fatal("child exited before guest hard stop:", cmd.Wait())
	}

	facts := readRebootFacts(t)
	if boot == facts.Boot {
		t.Fatal("kernel did not reboot")
	}
	oldGroup := filepath.Join(parent, "epoch-"+facts.Epoch.Reference.Epoch)
	rebootAbsent(t, oldGroup)
	t.Logf("real boot changed: old=%s new=%s; old cgroup absent", facts.Boot, boot)
	cfg := OwnerConfig{
		MetadataRoot: filepath.Join(rebootState, "owner"), CodeRoot: filepath.Join(rebootState, "code"),
		RuntimeRoot: filepath.Join(rebootState, "runtime"), Parent: parent,
		CodeLimits: testLimits(), RuntimeLimits: testLimits(), Budget: budget, MaxEpochs: 4, Host: &host,
	}
	for name, got := range rebootObjects(t, cfg, facts.Epoch) {
		want := facts.Objects[name]
		t.Logf("identity %s before=%d:%d after=%d:%d", name, want.Device, want.Inode, got.Device, got.Inode)
		if got != want {
			t.Errorf("identity changed for %s; hold qualification", name)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	if rebootInventory(t, cfg) != facts.Inventory {
		t.Fatal("artifact roots changed across hard stop")
	}
	wrong := cfg
	wrongHost := host
	wrongHost.WitnessPath = filepath.Join(rebootState, "foreign", "witness")
	provisionRebootWitness(t, wrongHost.WitnessPath)
	wrong.Host = &wrongHost
	if o, err := OpenOwnerColdRecovery(ctx, wrong); o != nil || !errors.Is(err, ErrIntegrity) {
		t.Fatal("unexpected wrong-witness result:", err)
	}
	if rebootInventory(t, cfg) != facts.Inventory {
		t.Fatal("wrong-witness denial mutated artifact roots")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("wrong-witness denial created cgroup %s", entry.Name())
		}
	}
	t.Log("wrong fresh witness denied; artifact inventory and empty parent unchanged")

	cold, err := OpenOwnerColdRecovery(ctx, cfg)
	if err != nil {
		t.Fatal("cold recover:", err)
	}
	verifyRebootOwner(t, ctx, cold, facts, boot, 2)
	t.Log("cold open recovered both exact sealed generations")
	same, err := OpenOwnerRecovery(ctx, cfg)
	if err != nil {
		t.Fatal("same-boot recover:", err)
	}
	verifyRebootOwner(t, ctx, same, facts, boot, 3)
	rebootAbsent(t, filepath.Join(cfg.MetadataRoot, "owner.intent"))
	rebootAbsent(t, oldGroup)
	if sha256.Sum256(rebootRead(t, rebootEpochRecord(cfg, facts.Epoch))) != facts.EpochDigest {
		t.Fatal("old epoch metadata was rewritten")
	}
	t.Log("same-boot reopen recovered both generations; old record and absent cgroup preserved")
}

func verifyRebootOwner(t *testing.T, ctx context.Context, o *Owner, facts rebootFacts, boot string, count int) {
	t.Helper()
	b, err := o.Bindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rebootAsset(t, b.Code, facts.Code.Generation, facts.Code)
	rebootAsset(t, b.Runtime, facts.Runtime.Generation, facts.Runtime)
	epochs := o.s.disk.Epochs
	if len(epochs) != count || epochs[0] != facts.Epoch {
		t.Fatal("incorrect epoch history")
	}
	for _, e := range epochs[1:] {
		if e.Reference.Boot != boot {
			t.Fatal("successor was not in current boot run")
		}
	}
	if err := o.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func rebootAsset(t *testing.T, s *Store, generation string, want Identity) Identity {
	t.Helper()
	lease, err := s.Acquire(generation)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if got := rebootRead(t, filepath.Join(lease.Path(), "handler")); string(got) != "hello" {
		t.Fatal("sealed contents changed")
	}
	got := lease.Identity()
	if want != (Identity{}) && got != want {
		t.Fatal("sealed identity changed")
	}
	return got
}

func rebootEpochRecord(cfg OwnerConfig, epoch ownerEpochRecord) string {
	return filepath.Join(cfg.MetadataRoot, "epochs", "epoch-"+epoch.ID, "epoch.json")
}

func rebootObjects(t *testing.T, cfg OwnerConfig, epoch ownerEpochRecord) map[string]ownerObject {
	t.Helper()
	out := make(map[string]ownerObject)
	for name, path := range map[string]string{"owner": cfg.MetadataRoot, "code": cfg.CodeRoot, "runtime": cfg.RuntimeRoot,
		"witness": cfg.Host.WitnessPath, "epoch": filepath.Dir(rebootEpochRecord(cfg, epoch))} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("identity %s: %v", name, err)
		}
		st := info.Sys().(*syscall.Stat_t)
		out[name] = ownerObject{Device: uint64(st.Dev), Inode: st.Ino}
	}
	return out
}

func verifyRebootChild(t *testing.T, facts rebootFacts) {
	t.Helper()
	for _, pid := range []int{facts.OwnerPID, facts.Child} {
		if pid < 2 || strings.Contains(string(rebootRead(t, fmt.Sprintf("/proc/%d/stat", pid))), ") Z ") {
			t.Fatal("owner or child not alive")
		}
	}
	group := filepath.Join(strings.TrimPrefix(rebootBudget, "/sys/fs/cgroup"), "domains", "epoch-"+facts.Epoch.Reference.Epoch)
	membership := strings.TrimSpace(string(rebootRead(t, fmt.Sprintf("/proc/%d/cgroup", facts.Child))))
	if membership != "0::"+filepath.Join(group, "supervisors") {
		t.Fatal("child not enrolled in exact old root:", membership)
	}
	events := rebootRead(t, "/sys/fs/cgroup"+group+"/cgroup.events")
	if !strings.HasPrefix(string(events), "populated 1\n") {
		t.Fatal("old root not populated")
	}
}

func rebootInventory(t *testing.T, cfg OwnerConfig) [32]byte {
	t.Helper()
	h := sha256.New()
	for _, root := range []string{cfg.MetadataRoot, cfg.CodeRoot, cfg.RuntimeRoot} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			st := info.Sys().(*syscall.Stat_t)
			fmt.Fprintf(h, "%q %d %d %d %d %d\n", path, st.Dev, st.Ino, st.Mode, st.Uid, st.Gid)
			if !d.IsDir() {
				if !info.Mode().IsRegular() || info.Size() > 64<<10 {
					return errors.New("unexpected fixture object")
				}
				digest := sha256.Sum256(rebootRead(t, path))
				_, _ = h.Write(digest[:])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return [32]byte(h.Sum(nil))
}

func readRebootFacts(t *testing.T) rebootFacts {
	t.Helper()
	var facts rebootFacts
	if err := json.Unmarshal(rebootRead(t, filepath.Join(rebootState, "ready.json")), &facts); err != nil {
		t.Fatal(err)
	}
	return facts
}

func writeRebootFacts(t *testing.T, facts rebootFacts) {
	t.Helper()
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(rebootState, "ready.tmp")
	rebootWrite(t, temp, raw)
	if err := os.Rename(temp, filepath.Join(rebootState, "ready.json")); err != nil {
		t.Fatal(err)
	}
	rebootSync(t, rebootState)
	rebootSync(t, filepath.Dir(rebootState))
}

func provisionRebootWitness(t *testing.T, path string) {
	t.Helper()
	dir := filepath.Dir(path)
	rebootMkdir(t, dir)
	key := make([]byte, 32)
	rand.Read(key)
	rebootWrite(t, path, append([]byte(hex.EncodeToString(key)), '\n'))
	rebootSync(t, dir)
}

func rebootRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func rebootMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
}

func rebootAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected absence: %s: %v", path, err)
	}
}

func rebootWrite(t *testing.T, path string, raw []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if n, err := f.Write(raw); err != nil || n != len(raw) {
		t.Fatalf("fixture write len=%s: %v", strconv.Itoa(n), err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}

func rebootSync(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}
