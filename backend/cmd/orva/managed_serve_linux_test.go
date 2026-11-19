package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
)

func TestManagedShutdownRunsEveryStage(t *testing.T) {
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
		if !slices.Equal(got, []int{1, 2, 3}) || (failed > 0) != errors.Is(err, wantErr) {
			t.Fatalf("failure at %d: ran %v, err %v", failed, got, err)
		}
	}
}

func TestManagedInstallerUnit(t *testing.T) {
	installer, err := filepath.Abs("../../../scripts/install.sh")
	mustManagedBoot(t, err)
	root := t.TempDir()
	cmd := exec.Command("sh", "-ec", `
		ORVA_INSTALL_LIB=1 . "$1"
		DRYRUN=0 SERVICE_DISABLE_USERNS=0
		PREFIX=$2 INSTALL_RECORD=$2/install.conf
		write_service_files`, "sh", installer, root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate installer units: %v\n%s", err, output)
	}
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(root, "share", "orva", "scripts", name))
		mustManagedBoot(t, err)
		return string(raw)
	}
	unit := read("orva.service")
	if !strings.Contains(unit, "RestartForceExitStatus=70\nRestartPreventExitStatus=78\n") ||
		!strings.Contains(unit, "\nDelegate=yes\n") || strings.Contains(unit, "orva-workloads.slice") ||
		strings.Contains(unit, "ORVA_SERVING_MODE=") {
		t.Fatal("daemon unit must delegate only its own subtree and never select managed mode")
	}
	_, err = os.Stat(filepath.Join(root, "share", "orva", "scripts", "orva-workloads.slice"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installer generated the retired slice: %v", err)
	}
	uninstall := read("uninstall.sh")
	if !strings.Contains(uninstall, `rm -f "/etc/orva/host-witness/witness"`) ||
		!strings.Contains(uninstall, "sealed stores will be unrecoverable on this host") ||
		!strings.Contains(uninstall, "systemctl disable --now orva-workloads.slice 2>/dev/null || true\n") ||
		!strings.Contains(uninstall, "systemctl daemon-reload 2>/dev/null || true\n") {
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
	managedDir := filepath.Join(data, servingmode.Managed)
	const blockedIP = "192.0.2.10"
	func() {
		db, err := database.New(filepath.Join(managedDir, "orva.db"))
		mustManagedBoot(t, err)
		defer func() { mustManagedBoot(t, db.Close()) }()
		mustManagedBoot(t, db.Migrate())
		_, err = db.InsertCustomBlocklistRule("cidr", blockedIP+"/32", "fixture", true)
		mustManagedBoot(t, err)
	}()
	// Prevent initial publication without changing the database or the daemon's dependencies.
	fwDir := filepath.Dir(firewall.PolicyDir(managedDir))
	mustManagedBoot(t, os.WriteFile(fwDir, nil, 0600))
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
	checkChat := managedDaemonChat(t, "http://"+addr, blockedIP)
	checkChat(false)
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
	mustManagedBoot(t, os.Remove(fwDir))
	second := start(daemonLeaf, "serve")
	awaitHTTP()
	checkChat(true)
	after, err := os.Stat(old)
	mustManagedBoot(t, err)
	if !os.SameFile(before, after) || epochGroup() == old {
		t.Fatal("restart did not keep the exact old cgroup and commit a fresh epoch")
	}
	populated(old, false)
	assertExit(child, -1)
	oldParent, err := os.Stat(parent)
	mustManagedBoot(t, err)
	mustManagedBoot(t, second.Process.Kill())
	assertExit(second, -1)
	var remove func(string)
	remove = func(path string) {
		t.Helper()
		populated(path, false)
		entries, err := os.ReadDir(path)
		mustManagedBoot(t, err)
		for _, e := range entries {
			if e.IsDir() {
				remove(filepath.Join(path, e.Name()))
			}
		}
		mustManagedBoot(t, os.Remove(path))
	}
	remove(budget)
	mustManagedBoot(t, os.Mkdir(budget, 0700))
	mustManagedBoot(t, os.Unsetenv(managedParentEnv)) // Exercise service-cgroup autodetection.
	third := start(budget, "serve")
	awaitHTTP()
	newParent, err := os.Stat(parent)
	mustManagedBoot(t, err)
	if os.SameFile(oldParent, newParent) {
		t.Fatal("service restart did not replace the worker parent")
	}
	mustManagedBoot(t, third.Process.Signal(syscall.SIGTERM))
	mustManagedBoot(t, third.Wait())
	t.Setenv(managedParentEnv, parent)
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

// Exercise the dashboard's session-authenticated API against ordinary daemon
// startup; both the model catalog and the turn use only this offline fixture.
func managedDaemonChat(t *testing.T, origin, blockedIP string) func(bool) {
	t.Helper()
	var modelRequests, turnRequests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			modelRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"daemon-fixture"}]}`)
		case "/v1/chat/completions":
			turnRequests.Add(1)
			var req struct {
				Tools []struct{ Function struct{ Name string } }
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
				t.Error(err)
				http.Error(w, "invalid fixture request", http.StatusBadRequest)
				return
			}
			var tools []string
			for _, tool := range req.Tools {
				tools = append(tools, tool.Function.Name)
			}
			if !slices.Contains(tools, "create_iam_policy") || !slices.Contains(tools, "install_managed_deployment") ||
				slices.Contains(tools, "update_function") || slices.Contains(tools, "invoke_function") {
				t.Errorf("unexpected Chat tool catalog: %v", tools)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"daemon-fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"managed daemon fixture reply\"},\"finish_reason\":null}]}\n\n"+
				"data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"daemon-fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"+
				"data: [DONE]\n\n")
		default:
			t.Errorf("unexpected provider request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	jar, err := cookiejar.New(nil)
	mustManagedBoot(t, err)
	client := &http.Client{Jar: jar, Timeout: 12 * time.Second}
	call := func(method, path, body string, status int) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, origin+path, strings.NewReader(body))
		mustManagedBoot(t, err)
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		mustManagedBoot(t, err)
		defer res.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		mustManagedBoot(t, err)
		if res.StatusCode != status {
			t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, res.StatusCode, status, raw)
		}
		return string(raw)
	}
	call("GET", "/api/v1/ai/settings", "", 401)
	call("POST", "/api/v1/auth/onboard", `{"username":"fixture","password":"scratch-only"}`, 200)
	call("GET", "/api/v1/ai/settings", "", 200)
	call("GET", "/api/v1/ai/conversations", "", 200)
	save := func(endpoint string) string {
		t.Helper()
		raw := call("POST", "/api/v1/ai/providers", fmt.Sprintf(`{"provider":"openai","api_key":"synthetic-loopback-only","base_url":%q}`, endpoint), 200)
		var result struct{ Provider struct{ ID string } }
		mustManagedBoot(t, json.Unmarshal([]byte(raw), &result))
		if result.Provider.ID == "" || strings.Contains(raw, "synthetic-loopback-only") {
			t.Fatal("provider configuration missing ID or exposed key")
		}
		return "/api/v1/ai/providers/" + result.Provider.ID + "/models"
	}
	models := save(provider.URL + "/v1")
	call("PUT", "/api/v1/ai/settings", `{"provider":"openai","model":"daemon-fixture","approval_policy":"all_writes"}`, 200)
	turn := func() string { return call("POST", "/api/v1/ai/chat", `{"content":"Say hello"}`, 200) }
	expectFailure := func(needle string) {
		t.Helper()
		var result struct {
			Error  string
			Models []json.RawMessage
		}
		mustManagedBoot(t, json.Unmarshal([]byte(call("GET", models, "", 200)), &result))
		if !strings.Contains(result.Error, needle) || result.Models == nil || len(result.Models) != 0 {
			t.Fatalf("model probe was not refused for %q: %+v", needle, result)
		}
		if raw := turn(); !strings.Contains(raw, "event: error\n") || !strings.Contains(raw, needle) {
			t.Fatalf("Chat turn was not refused for %q: %s", needle, raw)
		}
	}
	return func(available bool) {
		t.Helper()
		if !available {
			expectFailure(firewall.ErrPolicyUnavailable.Error())
			if modelRequests.Load() != 0 || turnRequests.Load() != 0 {
				t.Fatal("unavailable policy still contacted provider")
			}
			return
		}
		if raw := call("GET", models, "", 200); !strings.Contains(raw, `"id":"daemon-fixture"`) {
			t.Fatalf("allowed model probe: %s", raw)
		}
		if raw := turn(); !strings.Contains(raw, "managed daemon fixture reply") || strings.Contains(raw, "event: error\n") {
			t.Fatalf("allowed Chat turn: %s", raw)
		}
		models = save("http://" + blockedIP + ":8080/v1")
		expectFailure(firewall.ErrBlockedByEgressPolicy.Error())
		if modelRequests.Load() != 1 || turnRequests.Load() != 1 {
			t.Fatalf("provider requests: models=%d, turns=%d; want one of each", modelRequests.Load(), turnRequests.Load())
		}
	}
}
