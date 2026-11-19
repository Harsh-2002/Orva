package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
)

func TestManagedShutdownOrder(t *testing.T) {
	for failed := range 4 {
		var got []int
		wantErr := errors.New("cannot close")
		step := func(n int) func(context.Context) error {
			return func(context.Context) error {
				got = append(got, n)
				if n == failed {
					return wantErr
				}
				return nil
			}
		}
		err := closeManaged(t.Context(), step(1), step(2), step(3))
		expected := []int{1, 2, 3}
		if failed > 0 {
			expected = expected[:failed]
		}
		if !slices.Equal(got, expected) || (failed > 0) != errors.Is(err, wantErr) {
			t.Fatalf("failure at %d: ran %v, err %v", failed, got, err)
		}
	}
}

func TestManagedInstallerUnit(t *testing.T) {
	installer, err := filepath.Abs("../../../scripts/install.sh")
	mustManagedBoot(t, err)
	root := t.TempDir()
	legacy, managed := filepath.Join(root, "legacy"), filepath.Join(root, "managed")
	cmd := exec.Command("sh", "-ec", `
		ORVA_INSTALL_LIB=1 . "$1"
		DRYRUN=0 SERVICE_DISABLE_USERNS=0
		PREFIX=$2 INSTALL_RECORD=$2/install.conf
		write_service_files
		PREFIX=$3 INSTALL_RECORD=$3/install.conf
		parse_args --managed-workload-slice
		write_service_files`, "sh", installer, legacy, managed)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate installer units: %v\n%s", err, output)
	}
	read := func(prefix, name string) string {
		raw, err := os.ReadFile(filepath.Join(prefix, "share", "orva", "scripts", name))
		mustManagedBoot(t, err)
		return string(raw)
	}
	legacyUnit := read(legacy, "orva.service")
	managedUnit := strings.ReplaceAll(read(managed, "orva.service"), managed, legacy)
	if legacyUnit != managedUnit || !strings.Contains(legacyUnit, "RestartForceExitStatus=70\nRestartPreventExitStatus=78\n") {
		t.Fatal("opt-in provisioning changed the original daemon unit")
	}
	_, err = os.Stat(filepath.Join(legacy, "share", "orva", "scripts", "orva-workloads.slice"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy install generated the managed slice: %v", err)
	}
	slice := read(managed, "orva-workloads.slice")
	if !strings.Contains(slice, "StopWhenUnneeded=no\n") || strings.Contains(slice, "Delegate=") {
		t.Fatal("persistent slice lifetime or delegation is wrong")
	}
	uninstall := read(managed, "uninstall.sh")
	if !strings.Contains(uninstall, `rm -f "/etc/orva/host-witness/witness"`) ||
		!strings.Contains(uninstall, "sealed stores will be unrecoverable on this host") {
		t.Fatal("uninstall does not clearly destroy the managed host witness")
	}
}

func mustManagedBoot(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Run only from an owned, offline VM. Its external launcher, not either
// daemon, creates and retains the delegated budget and moves the new process.
func TestManagedDaemonKernel(t *testing.T) {
	if os.Getenv("ORVA_OWNER_KERNEL_TEST") != "disposable-vm" || os.Getenv("ORVA_MANAGED_BOOT_BUDGET") == "" {
		t.Skip("requires explicit disposable VM and owned launch budget")
	}
	if args := os.Getenv("ORVA_MANAGED_BOOT_CHILD"); args != "" {
		if args == "serve" {
			os.Exit(runServe(newServeCmd(), nil))
		}
		cmd := newSetupCmd()
		cmd.SetArgs(strings.Fields(args))
		if err := cmd.Execute(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	budget := os.Getenv("ORVA_MANAGED_BOOT_BUDGET")
	parent := filepath.Join(budget, "orva.workers")
	root := t.TempDir()
	data := filepath.Join(root, "data")
	witness := filepath.Join(root, "host", "witness")
	mustManagedBoot(t, os.Mkdir(data, 0700))
	choose, err := net.Listen("tcp", "127.0.0.1:0")
	mustManagedBoot(t, err)
	addr := choose.Addr().String()
	mustManagedBoot(t, choose.Close())
	t.Setenv("ORVA_DATA_DIR", data)
	t.Setenv(managedWitnessEnv, witness)
	t.Setenv(managedParentEnv, parent)
	t.Setenv("ORVA_HOST", "127.0.0.1")
	_, port, err := net.SplitHostPort(addr)
	mustManagedBoot(t, err)
	t.Setenv("ORVA_PORT", port)
	bin, err := os.Executable()
	mustManagedBoot(t, err)
	start := func(group, args string) *exec.Cmd {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/bin/sh", "-ec",
			`printf '%s' "$$" > "$1/cgroup.procs"; shift; exec "$@"`, "sh", group, bin,
			"-test.run=^TestManagedDaemonKernel$")
		cmd.Env = append(os.Environ(), "ORVA_MANAGED_BOOT_CHILD="+args)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		mustManagedBoot(t, cmd.Start())
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			if cmd.ProcessState == nil {
				_ = cmd.Wait()
			}
		})
		return cmd
	}
	inPlace := func(args ...string) {
		t.Helper()
		cmd := newSetupCmd()
		cmd.SetArgs(args)
		mustManagedBoot(t, cmd.Execute())
	}
	inPlace("--managed-witness", witness)
	inPlace("--managed-layout", "provision")
	t.Setenv(servingmode.Env, servingmode.Provisioning)
	mustManagedBoot(t, start(budget, "serve").Wait())
	assertExit := func(cmd *exec.Cmd, want int) {
		t.Helper()
		var status *exec.ExitError
		if err := cmd.Wait(); !errors.As(err, &status) || status.ExitCode() != want {
			t.Fatalf("expected exit %d, got %v", want, err)
		}
	}
	daemonLeaf := filepath.Join(budget, "orva.daemon")
	assertExit(start(daemonLeaf, "serve"), exitConfig) // Nonempty roots cannot be provisioned anew.
	mustManagedBoot(t, start(daemonLeaf, "--managed-layout publish").Wait())
	t.Setenv(servingmode.Env, servingmode.Managed)
	client := &http.Client{Timeout: 250 * time.Millisecond}
	awaitHTTP := func() {
		t.Helper()
		for until := time.Now().Add(8 * time.Second); time.Now().Before(until); {
			r, err := client.Get("http://" + addr + "/api/v1/auth/status")
			if err == nil {
				r.Body.Close()
				if r.StatusCode != 200 {
					t.Fatalf("managed auth/status HTTP %d", r.StatusCode)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("managed listener did not open")
	}
	epochGroup := func() string {
		t.Helper()
		var a struct {
			Epochs []struct{ Reference workloadfence.EpochReference }
		}
		raw, err := os.ReadFile(filepath.Join(data, "sealed-owner", "owner.json"))
		mustManagedBoot(t, err)
		mustManagedBoot(t, json.Unmarshal(raw, &a))
		if len(a.Epochs) == 0 {
			t.Fatal("no durable epochs")
		}
		return filepath.Join(parent, "epoch-"+a.Epochs[len(a.Epochs)-1].Reference.Epoch)
	}
	populated := func(group string, want bool) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(group, "cgroup.events"))
		mustManagedBoot(t, err)
		if !strings.Contains(string(raw), fmt.Sprintf("populated %d\n", map[bool]int{false: 0, true: 1}[want])) {
			t.Fatalf("cgroup %s population not %t", group, want)
		}
	}
	first := start(daemonLeaf, "serve")
	awaitHTTP()
	old := epochGroup()
	child := exec.Command("/bin/sleep", "120")
	child.Stdout, child.Stderr = io.Discard, io.Discard
	mustManagedBoot(t, child.Start())
	t.Cleanup(func() {
		_ = child.Process.Kill()
		if child.ProcessState == nil {
			_ = child.Wait()
		}
	})
	mustManagedBoot(t, os.WriteFile(filepath.Join(old, "supervisors", "cgroup.procs"), []byte(strconv.Itoa(child.Process.Pid)), 0600))
	populated(old, true)
	before, err := os.Stat(old)
	mustManagedBoot(t, err)
	mustManagedBoot(t, first.Process.Kill())
	assertExit(first, -1)
	populated(old, true)
	second := start(daemonLeaf, "serve")
	awaitHTTP()
	after, err := os.Stat(old)
	mustManagedBoot(t, err)
	if !os.SameFile(before, after) || epochGroup() == old {
		t.Fatal("restart did not keep the exact old cgroup and commit a fresh epoch")
	}
	populated(old, false)
	assertExit(child, -1)
	mustManagedBoot(t, second.Process.Signal(syscall.SIGTERM))
	mustManagedBoot(t, second.Wait())
	ownerRecord := filepath.Join(data, "sealed-owner", "owner.json")
	prior, err := os.ReadFile(ownerRecord)
	mustManagedBoot(t, err)
	foreign := filepath.Join(root, "foreign", "witness")
	inPlace("--managed-witness", foreign)
	t.Setenv(managedWitnessEnv, foreign)
	assertExit(start(daemonLeaf, "serve"), exitConfig)
	unchanged, err := os.ReadFile(ownerRecord)
	mustManagedBoot(t, err)
	if !slices.Equal(prior, unchanged) {
		t.Fatal("witness mismatch changed durable owner")
	}
	t.Setenv(managedWitnessEnv, witness)
	t.Setenv(managedParentEnv, filepath.Join(budget, "wrong"))
	assertExit(start(daemonLeaf, "serve"), exitConfig)
	t.Setenv(managedParentEnv, parent)
	t.Setenv(managedNodeRuntimeEnv, "not-a-sealed-generation")
	assertExit(start(daemonLeaf, "serve"), exitConfig)
	mustManagedBoot(t, os.Unsetenv(managedNodeRuntimeEnv))
	occupied, err := net.Listen("tcp", addr)
	mustManagedBoot(t, err)
	assertExit(start(daemonLeaf, "serve"), exitConfig)
	mustManagedBoot(t, occupied.Close())
	db := filepath.Join(data, "managed", "orva.db")
	mustManagedBoot(t, os.WriteFile(db, make([]byte, 8192), 0600))
	assertExit(start(daemonLeaf, "serve"), exitConfig)
	latest := epochGroup()
	for _, child := range []string{"jails", "supervisors", ""} {
		mustManagedBoot(t, os.Remove(filepath.Join(latest, child)))
	}
	assertExit(start(daemonLeaf, "serve"), exitConfig)
}
