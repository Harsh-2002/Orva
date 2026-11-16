//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestDomainMissingBindingAndLimitsDeny(t *testing.T) {
	d := &workloadfence.Domain{}
	if _, _, err := buildArgs(ExecConfig{Domain: d, Language: Node}, "/runtime", "adapter"); !errors.Is(err, workloadfence.ErrInvalid) {
		t.Fatalf("unbound worker arguments: %v", err)
	}
	if _, err := buildJailArgs(BuildConfig{Domain: d, Argv: []string{"/bin/tool"}}, "/runtime", "/scratch"); !errors.Is(err, workloadfence.ErrInvalid) {
		t.Fatalf("unbound build arguments: %v", err)
	}
	for _, cpu := range []float64{-1, math.NaN(), math.Inf(1), .0001, .0015, .0010000000001, math.MaxFloat64} {
		if err := validateDomainLimits(d, 64, 32, cpu); err == nil {
			t.Fatalf("unrepresentable CPU limit accepted: %v", cpu)
		}
	}
	for _, pair := range [][2]int{{-1, 32}, {64, -1}, {0, 32}, {64, 0}, {math.MaxInt, 32}} {
		if err := validateDomainLimits(d, pair[0], pair[1], 0); err == nil {
			t.Fatalf("invalid memory/PID limit accepted: %v", pair)
		}
	}
	if err := validateDomainLimits(nil, -1, -1, math.NaN()); err != nil {
		t.Fatalf("nil domain changed legacy validation: %v", err)
	}
}

func TestDomainCPUQuotaPrecision(t *testing.T) {
	d := &workloadfence.Domain{}
	for _, tc := range []struct {
		cpu  float64
		want string
	}{{0, ""}, {.001, "1"}, {.009, "9"}, {.5, "500"}, {1.001, "1001"}, {float64(math.MaxUint32) / 1000, "4294967295"}} {
		got, err := cpuMillisecondsArgument(ExecConfig{Domain: d, MaxCPUs: tc.cpu})
		if err != nil || got != tc.want {
			t.Fatalf("CPU %.17g argument = %q, %v; want %q", tc.cpu, got, err, tc.want)
		}
	}
	for _, cpu := range []float64{.0009, .0015, 1.0005, math.NaN(), math.Inf(1), float64(math.MaxUint32)/1000 + .001} {
		if _, err := cpuMillisecondsArgument(ExecConfig{Domain: d, MaxCPUs: cpu}); err == nil {
			t.Fatalf("unrepresentable CPU %.17g accepted", cpu)
		}
	}
	for _, cpu := range []float64{0, .0001, .0015, 1.001} {
		want := ""
		if ms := int(cpu * 1000); ms > 0 {
			want = strconv.Itoa(ms)
		}
		got, err := cpuMillisecondsArgument(ExecConfig{MaxCPUs: cpu})
		if err != nil || got != want {
			t.Fatalf("nil-domain CPU conversion changed: %q, %v; want %q", got, err, want)
		}
	}
}

func TestDomainPreparationFailureReleasesVerifiedPaths(t *testing.T) {
	for _, mode := range []string{"worker", "execute"} {
		t.Run(mode, func(t *testing.T) {
			f := newVerifiedFixture(t, Node, nil)
			cfg := f.config(t, Node)
			cfg.Domain = &workloadfence.Domain{}
			if mode == "worker" {
				if _, err := Spawn(context.Background(), cfg); !errors.Is(err, workloadfence.ErrInvalid) {
					t.Fatal(err)
				}
			} else if result := Execute(context.Background(), cfg); !errors.Is(result.Error, workloadfence.ErrInvalid) {
				t.Fatal(result.Error)
			}
			retireVerified(t, f)
			waitVerifiedRelease(t, f)
		})
	}
}

type domainFixture struct {
	domain *workloadfence.Domain
	jails  string
	rootfs string
}

func domainContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func removeDomainGroups(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Error(err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			removeDomainGroups(t, filepath.Join(path, entry.Name()))
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Error(err)
	}
}

func newDomainFixture(t *testing.T) domainFixture {
	t.Helper()
	if os.Getenv("ORVA_SANDBOX_DOMAIN_TEST") != "1" {
		t.Skip("real nsjail epoch checks require an owned disposable VM")
	}
	if os.Getenv("ORVA_FENCE_TEST_SCRATCH") != "disposable-vm" {
		t.Fatal("refusing cgroup fixture without disposable-VM acknowledgement")
	}
	rootfs := os.Getenv("ORVA_SANDBOX_ROOTFS")
	if rootfs == "" {
		t.Fatal("explicit verified fixture rootfs required")
	}
	budget, err := workloadfence.CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	membership := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(membership, "0::/") || strings.Contains(membership, "\n") {
		t.Fatal("ambiguous test membership")
	}
	anchor := "/sys/fs/cgroup" + strings.TrimPrefix(membership, "0::")
	base := filepath.Join(anchor, fmt.Sprintf("sandbox-domain-%d", time.Now().UnixNano()))
	daemon, parent := filepath.Join(base, "daemon"), filepath.Join(base, "domains")
	for _, path := range []string{base, daemon, parent} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, path := range []string{parent, base, anchor} {
			if err := os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte("-cpu -memory -pids"), 0); err != nil {
				t.Error(err)
			}
		}
		if err := os.WriteFile(filepath.Join(anchor, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0); err != nil {
			t.Error(err)
		}
		removeDomainGroups(t, base)
		if err := budget.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := os.WriteFile(filepath.Join(daemon, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		t.Fatal(err)
	}
	if procs, err := os.ReadFile(filepath.Join(anchor, "cgroup.procs")); err != nil || len(strings.TrimSpace(string(procs))) != 0 {
		t.Fatalf("fixture cannot move other processes; anchor remains occupied: %s %v", procs, err)
	}
	for _, path := range []string{anchor, base, parent} {
		if err := os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0); err != nil {
			t.Fatal(err)
		}
	}
	meta := t.TempDir()
	if err := os.Chmod(meta, 0o700); err != nil {
		t.Fatal(err)
	}
	domain, err := workloadfence.CreateUnderBudget(budget, meta, parent)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := domain.PrepareJails(domainContext(t), "cpu", "memory", "pids")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := domain.Fence(domainContext(t)); err != nil {
			t.Error(err)
		}
		domain.Close()
		removeDomainGroups(t, filepath.Dir(binding.Path()))
	})
	return domainFixture{domain, binding.Path(), rootfs}
}

func awaitDomainChild(t *testing.T, f domainFixture) (string, []string) {
	t.Helper()
	ctx := domainContext(t)
	for {
		entries, err := os.ReadDir(f.jails)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if !strings.HasPrefix(entry.Name(), "NSJAIL.") {
				t.Fatalf("unexpected nsjail relocation: %s", entry.Name())
			}
			path := filepath.Join(f.jails, entry.Name())
			data, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
			if err == nil && len(strings.Fields(string(data))) >= 2 {
				pids := strings.Fields(string(data))
				for _, pid := range pids {
					cg, err := os.ReadFile("/proc/" + pid + "/cgroup")
					if err != nil || !strings.Contains(string(cg), strings.TrimPrefix(path, "/sys/fs/cgroup")) {
						t.Fatalf("jailed process outside epoch: %s %v", cg, err)
					}
				}
				return path, pids
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("nsjail child and grandchild did not populate owned epoch")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func checkDomainLimits(t *testing.T, path string, cpu bool) {
	t.Helper()
	expected := map[string]string{"memory.max": "201326592", "pids.max": "64"}
	if cpu {
		expected["cpu.max"] = "500000 1000000"
	}
	for name, value := range expected {
		actual, err := os.ReadFile(filepath.Join(path, name))
		if err != nil || strings.TrimSpace(string(actual)) != value {
			t.Fatalf("nsjail limit %s = %s, expected %s: %v", name, actual, value, err)
		}
	}
	t.Logf("real nsjail child=%s controls=%v", path, expected)
}

func checkDomainHost(t *testing.T, f domainFixture) []string {
	t.Helper()
	supervisors := filepath.Join(filepath.Dir(f.jails), "supervisors")
	data, err := os.ReadFile(filepath.Join(supervisors, "cgroup.procs"))
	if err != nil || len(strings.Fields(string(data))) != 1 {
		t.Fatalf("host nsjail enrollment: %s %v", data, err)
	}
	pids := strings.Fields(string(data))
	for _, pid := range pids {
		cg, err := os.ReadFile("/proc/" + pid + "/cgroup")
		if err != nil || !strings.Contains(string(cg), strings.TrimPrefix(supervisors, "/sys/fs/cgroup")) {
			t.Fatalf("host command escaped epoch: %s %v", cg, err)
		}
	}
	return pids
}

func fenceDomain(t *testing.T, f domainFixture, pids []string) {
	t.Helper()
	proof, err := f.domain.Fence(domainContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.domain.Check(domainContext(t), proof); err != nil {
		t.Fatal(err)
	}
	for _, pid := range pids {
		data, err := os.ReadFile("/proc/" + pid + "/stat")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		at := strings.LastIndexByte(string(data), ')')
		if err != nil || at < 0 || !strings.HasPrefix(string(data[at+1:]), " Z ") {
			t.Fatalf("fenced process remains alive: %s %s %v", pid, data, err)
		}
	}
	t.Log("Domain.Fence and Check verified populated=0; all sampled descendants stopped")
}

func TestDomainRealSandbox(t *testing.T) {
	t.Run("worker-sdk", func(t *testing.T) {
		f := newDomainFixture(t)
		code := t.TempDir()
		source := `const {spawn}=require('child_process'); const orva=require('orva');
spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'pipe'});
exports.handler=async()=>({statusCode:200,body:await orva.kv.get('fixture')});`
		if err := os.WriteFile(filepath.Join(code, "handler.js"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		exited := make(chan struct{})
		cfg := ExecConfig{Domain: f.domain, Language: Node, FunctionID: ids.New(), CodeDir: code,
			RootfsDir: f.rootfs, NsjailBin: "/usr/local/bin/nsjail", MemoryMB: 128, MaxPids: 64, MaxCPUs: .5,
			OnExit: func() { close(exited) },
			SDKHandler: func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) {
				calls.Add(1)
				return map[string]any{"value": "sdk-ok"}, nil
			}}
		w, err := Spawn(domainContext(t), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = w.Kill() })
		if err := w.AwaitReady(domainContext(t)); err != nil {
			t.Fatalf("ready: %v stderr=%s", err, w.errBuf.Snapshot(0))
		}
		if w.Cmd.SysProcAttr.UseCgroupFD || !w.Cmd.SysProcAttr.Setpgid || len(w.Cmd.ExtraFiles) != 1 {
			t.Fatal("domain Start changed process-group/SDK descriptor ownership")
		}
		reply, _, err := w.Dispatch(domainContext(t), []byte(`{}`))
		if err != nil || !strings.Contains(string(reply), "sdk-ok") || calls.Load() != 1 {
			t.Fatalf("real SDK channel: %s %v calls=%d stderr=%s", reply, err, calls.Load(), w.errBuf.Snapshot(0))
		}
		path, children := awaitDomainChild(t, f)
		checkDomainLimits(t, path, true)
		pids := append(checkDomainHost(t, f), children...)
		fenceDomain(t, f, pids)
		select {
		case <-exited:
		case <-domainContext(t).Done():
			t.Fatal("central worker Wait/OnExit did not complete after fence")
		}
		if w.Cmd.ProcessState == nil {
			t.Fatal("OnExit ran before authoritative Wait")
		}
		if _, err := Spawn(domainContext(t), cfg); !errors.Is(err, workloadfence.ErrClosed) {
			t.Fatalf("post-fence worker spawn: %v", err)
		}
	})
	for _, mode := range []string{"execute", "build", "execute-cancel", "build-cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newDomainFixture(t)
			code := t.TempDir()
			source := "import subprocess,sys,time\nchild=subprocess.Popen([sys.executable,'-c','import time;time.sleep(600)'],stdout=subprocess.PIPE,stderr=subprocess.PIPE)\ntime.sleep(600)\ndef handler(event): return 'unused'\n"
			if err := os.WriteFile(filepath.Join(code, "handler.py"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			var result *ExecResult
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			oneShot := strings.HasPrefix(mode, "execute")
			go func() {
				if oneShot {
					result = Execute(ctx, ExecConfig{Domain: f.domain, Language: Python, CodeDir: code,
						RootfsDir: f.rootfs, NsjailBin: "/usr/local/bin/nsjail", MemoryMB: 128, MaxPids: 64, MaxCPUs: .5})
					t.Logf("Execute completion: code=%d error=%v stderr=%s", result.ExitCode, result.Error, result.Stderr)
					done <- result.Error
				} else {
					out, err := RunBuild(ctx, BuildConfig{Domain: f.domain, Language: Python, CodeDir: code,
						RootfsDir: f.rootfs, NsjailBin: "/usr/local/bin/nsjail", MemoryMB: 128, MaxPids: 64,
						Argv: []string{"/usr/local/bin/python3", "/code/handler.py"}})
					t.Logf("RunBuild completion: error=%v output=%s", err, out)
					done <- err
				}
			}()
			path, children := awaitDomainChild(t, f)
			checkDomainLimits(t, path, oneShot)
			pids := append(checkDomainHost(t, f), children...)
			completed := false
			var runErr error
			if strings.HasSuffix(mode, "-cancel") {
				cancel()
				select {
				case runErr = <-done:
					completed = true
				case <-time.After(3 * time.Second):
					t.Error("CommandContext cancellation did not return before explicit fencing")
				}
			}
			fenceDomain(t, f, pids)
			if !completed {
				select {
				case runErr = <-done:
				case <-domainContext(t).Done():
					t.Fatal("Wait did not complete after fence")
				}
			}
			if !oneShot && runErr == nil {
				t.Fatal("killed build falsely returned success")
			}
			if oneShot && (result.ExitCode == 0 || runErr != nil) {
				t.Fatalf("one-shot exit contract changed: %+v", result)
			}
		})
	}
}

func TestDomainRealStartFailureAndUnavailableControllers(t *testing.T) {
	f := newDomainFixture(t)
	code := t.TempDir()
	if err := os.WriteFile(filepath.Join(code, "handler.js"), []byte("exports.handler=async()=>({body:'unused'});"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ExecConfig{Domain: f.domain, Language: Node, CodeDir: code, RootfsDir: f.rootfs,
		NsjailBin: "/nonexistent/nsjail", MemoryMB: 128, MaxPids: 64,
		SDKHandler: func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) { return nil, nil }}
	host, child, err := newSDKPair()
	if err != nil {
		t.Fatal(err)
	}
	host.Close()
	child.Close()
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for range 12 {
		if w, err := Spawn(domainContext(t), cfg); err == nil || w != nil {
			t.Fatalf("nonexistent nsjail started: %v", err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil || len(after) > len(before) {
		t.Fatalf("failed Start leaked SDK/stdio descriptors: before=%d after=%d err=%v", len(before), len(after), err)
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	current := "/sys/fs/cgroup" + strings.TrimPrefix(strings.TrimSpace(string(raw)), "0::")
	parent := filepath.Join(current, "undelegated")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := t.TempDir()
	if err := os.Chmod(meta, 0o700); err != nil {
		t.Fatal(err)
	}
	domain, err := workloadfence.Create(meta, parent)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := domain.PrepareJails(domainContext(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := domain.Fence(domainContext(t)); err != nil {
			t.Error(err)
		}
		domain.Close()
		removeDomainGroups(t, parent)
	})
	cfg.Domain, cfg.NsjailBin = domain, "/usr/local/bin/nsjail"
	if _, err := Spawn(domainContext(t), cfg); !errors.Is(err, workloadfence.ErrUnsupported) {
		t.Fatalf("worker silently downgraded resource controls: %v", err)
	}
	if result := Execute(domainContext(t), cfg); !errors.Is(result.Error, workloadfence.ErrUnsupported) {
		t.Fatalf("one-shot silently downgraded resource controls: %v", result.Error)
	}
	if _, err := RunBuild(domainContext(t), BuildConfig{Domain: domain, Language: Node, CodeDir: code,
		RootfsDir: f.rootfs, NsjailBin: cfg.NsjailBin, Argv: []string{"/usr/local/bin/node", "/code/handler.js"}}); !errors.Is(err, workloadfence.ErrUnsupported) {
		t.Fatalf("build silently downgraded resource controls: %v", err)
	}
	procs, err := os.ReadFile(filepath.Join(filepath.Dir(binding.Path()), "supervisors", "cgroup.procs"))
	if err != nil || strings.TrimSpace(string(procs)) != "" {
		t.Fatalf("denied opt-in created an unenrolled/fallback process: %s %v", procs, err)
	}
}
