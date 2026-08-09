package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// containsArg returns true if any element in args equals s — useful for
// asserting flag presence regardless of position.
func containsArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// TestBuildArgs_NoEgressByDefault asserts that the default (and explicit
// "none") network mode does NOT add --user_net. The sandbox stays in
// nsjail's default loopback-only net namespace.
func TestBuildArgs_NoEgressByDefault(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{"unset", ""},
		{"explicit-none", "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ExecConfig{
				Language:    Python,
				CodeDir:     "/tmp/code",
				NetworkMode: tc.mode,
			}
			args, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.py")
			if err != nil {
				t.Fatalf("buildArgs: %v", err)
			}
			if containsArg(args, "--user_net") {
				t.Fatalf("expected no --user_net with NetworkMode=%q, got: %s",
					tc.mode, strings.Join(args, " "))
			}
			// A non-egress sandbox must never be handed an egress policy.
			if containsArg(args, "--config") {
				t.Fatalf("expected no --config with NetworkMode=%q, got: %s",
					tc.mode, strings.Join(args, " "))
			}
		})
	}
}

// TestBuildArgs_EgressAddsUserNet asserts that NetworkMode == "egress"
// adds the --user_net flag, which lights up nsjail's userspace TCP/UDP
// stack (the nstun backend) and lets the sandbox reach external APIs.
func TestBuildArgs_EgressAddsUserNet(t *testing.T) {
	cfg := ExecConfig{
		Language:         Node,
		CodeDir:          "/tmp/code",
		NetworkMode:      "egress",
		EgressPolicyPath: "/var/lib/orva/firewall/policy/egress-abc.cfg",
	}
	args, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if !containsArg(args, "--user_net") {
		t.Fatalf("expected --user_net in args for egress mode, got: %s",
			strings.Join(args, " "))
	}
}

// TestBuildArgs_EgressWithoutPolicyPathIsAnError is the fail-closed guard.
// NSTUN allows every destination no rule matches, so starting an egress
// sandbox with no policy would run the function completely unfiltered. That
// must be an error, NOT the skip-if-missing behaviour used for DNS files.
func TestBuildArgs_EgressWithoutPolicyPathIsAnError(t *testing.T) {
	cfg := ExecConfig{
		Language:    Node,
		CodeDir:     "/tmp/code",
		NetworkMode: "egress",
	}
	if _, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js"); !errors.Is(err, ErrEgressPolicyMissing) {
		t.Fatalf("want ErrEgressPolicyMissing, got %v", err)
	}
}

// TestBuildArgs_ConfigFlagIsFirst pins argv ordering, which is load-bearing
// rather than cosmetic: nsjail's config loader CopyFroms onto the config
// message and clears everything already set, so any flag before --config is
// silently discarded — including --env, i.e. every secret and ORVA_API_BASE.
func TestBuildArgs_ConfigFlagIsFirst(t *testing.T) {
	cfg := ExecConfig{
		Language:         Node,
		CodeDir:          "/tmp/code",
		NetworkMode:      "egress",
		EgressPolicyPath: "/var/lib/orva/firewall/policy/egress-abc.cfg",
		Env:              map[string]string{"SECRET": "value"},
		SeccompPolicy:    "POLICY x { ALLOW { read } }",
	}
	args, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if len(args) < 2 || args[0] != "--config" || args[1] != cfg.EgressPolicyPath {
		t.Fatalf("--config must be argv[0..1], got: %s", strings.Join(args, " "))
	}
	// Everything that would be wiped by a late --config must come after it.
	for _, flag := range []string{"--env", "--seccomp_string", "-R", "--chroot"} {
		idx := -1
		for i, a := range args {
			if a == flag {
				idx = i
				break
			}
		}
		if idx == 0 {
			t.Fatalf("%s must not precede --config", flag)
		}
	}
}

// TestBuildArgs_UsesWarningLogLevel guards the observability fix: nsjail gates
// rule-compilation errors behind ERROR, and -Q (FATAL) suppressed them
// entirely, making a silently-dropped policy rule invisible.
func TestBuildArgs_UsesWarningLogLevel(t *testing.T) {
	cfg := ExecConfig{Language: Python, CodeDir: "/tmp/code"}
	args, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.py")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if containsArg(args, "-Q") {
		t.Fatalf("-Q hides nsjail rule-parse errors; want -q. got: %s", strings.Join(args, " "))
	}
	if !containsArg(args, "-q") {
		t.Fatalf("expected -q in args, got: %s", strings.Join(args, " "))
	}
}

// TestBuildArgs_AlwaysHasOnceMode is a smoke check that the new branch
// didn't accidentally drop the -Mo flag (one-shot mode is mandatory).
func TestBuildArgs_AlwaysHasOnceMode(t *testing.T) {
	cfg := ExecConfig{
		Language: Python,
		CodeDir:  "/tmp/code",
	}
	args, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.py")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if !containsArg(args, "-Mo") {
		t.Fatalf("expected -Mo in args, got: %s", strings.Join(args, " "))
	}
}

func TestApplyExecDefaultsSetsProcessLimit(t *testing.T) {
	cfg := applyExecDefaults(ExecConfig{})
	if cfg.MaxPids != 32 {
		t.Fatalf("MaxPids default: want 32, got %d", cfg.MaxPids)
	}
	if cfg.MemoryMB != 64 {
		t.Fatalf("MemoryMB default: want 64, got %d", cfg.MemoryMB)
	}
	if cfg.Timeout <= 0 {
		t.Fatalf("Timeout default must be positive, got %s", cfg.Timeout)
	}
}

func TestApplyExecDefaultsPreservesExplicitProcessLimit(t *testing.T) {
	cfg := applyExecDefaults(ExecConfig{MaxPids: 7})
	if cfg.MaxPids != 7 {
		t.Fatalf("explicit MaxPids: want 7, got %d", cfg.MaxPids)
	}
}

func TestARM64SeccompFiltersUnsupportedLegacySyscalls(t *testing.T) {
	policy := buildSeccompPolicyForArch("default", nil, nil, "arm64")
	allowed := policySyscalls(policy)
	for name := range arm64UnsupportedSyscalls {
		if syscallSupportedOnArch(name, "arm64") {
			t.Errorf("%s must be filtered on arm64", name)
		}
		if !syscallSupportedOnArch(name, "amd64") {
			t.Errorf("%s should remain available on amd64", name)
		}
		if allowed[name] {
			t.Errorf("ARM64 policy still contains unsupported syscall %s", name)
		}
	}
	if !syscallSupportedOnArch("openat", "arm64") {
		t.Fatal("openat must remain available on arm64")
	}
}

func policySyscalls(policy string) map[string]bool {
	result := make(map[string]bool)
	const prefix = "POLICY orva { ALLOW { "
	body := strings.TrimPrefix(policy, prefix)
	if end := strings.Index(body, " } }"); end >= 0 {
		body = body[:end]
	}
	for _, name := range strings.Split(body, ",") {
		if name = strings.TrimSpace(name); name != "" {
			result[name] = true
		}
	}
	return result
}

func TestSeccompPolicyIsAnEnforcingAllowlist(t *testing.T) {
	policy := buildSeccompPolicyForArch("default", nil, nil, "amd64")
	if !strings.HasSuffix(policy, "DEFAULT ERRNO(1)") {
		t.Fatalf("policy must deny unknown syscalls, got: %s", policy)
	}
	if strings.Contains(policy, "DEFAULT LOG") {
		t.Fatal("DEFAULT LOG observes syscalls but does not restrict them")
	}
}

func TestDefaultSeccompBlocksNestedNamespaceCreation(t *testing.T) {
	policy := buildSeccompPolicyForArch("default", nil, nil, "amd64")
	want := "clone(orva_clone_flags) { (orva_clone_flags & " + cloneNamespaceFlags + ") == 0 }"
	if !strings.Contains(policy, want) {
		t.Fatalf("default policy must restrict clone namespace flags: %s", policy)
	}
	if !strings.Contains(policy, "ERRNO(38) { clone3 }") {
		t.Fatalf("default policy must deny clone3 with ENOSYS so runtimes fall back safely: %s", policy)
	}
	if policySyscalls(policy)["clone3"] {
		t.Fatalf("default policy cannot allow clone3's pointer arguments: %s", policy)
	}
}

func TestSeccompUsesKafelRuntimeAliasesOnSupportedArchitectures(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		allowed := policySyscalls(buildSeccompPolicyForArch("default", nil, nil, arch))
		for _, name := range kafelRuntimeAliases {
			if !allowed[name] {
				t.Errorf("%s policy is missing Kafel runtime alias %s", arch, name)
			}
		}
	}
}

func TestStrictSeccompFiltersUnknownKafelIdentifiers(t *testing.T) {
	allowed := policySyscalls(buildSeccompPolicyForArch("strict", nil, nil, "amd64"))
	if allowed["uname"] {
		t.Fatal("strict policy contains uname, which is absent from the Kafel catalog")
	}
	if !allowed["newuname"] {
		t.Fatal("strict policy is missing Kafel's newuname runtime alias")
	}
}

func TestDeadWorkerReturnsStartupStderr(t *testing.T) {
	w := &Worker{errBuf: newRingBuffer(1024)}
	w.dead.Store(true)
	_, _ = w.errBuf.Write([]byte("nsjail startup failed"))

	result, err := w.DispatchEx(context.Background(), []byte(`{}`))
	if !errors.Is(err, ErrWorkerExited) {
		t.Fatalf("expected ErrWorkerExited, got %v", err)
	}
	if got := string(result.Stderr()); got != "nsjail startup failed" {
		t.Fatalf("startup stderr: want %q, got %q", "nsjail startup failed", got)
	}
}

func TestFastProcessExitDrainsStartupStderr(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		t.Run(fmt.Sprintf("iteration-%03d", iteration), testFastProcessExitDrainsStartupStderr)
	}
}

func testFastProcessExitDrainsStartupStderr(t *testing.T) {
	root := t.TempDir()
	nodeRoot := filepath.Join(root, "rootfs", "node")
	if err := os.MkdirAll(filepath.Join(nodeRoot, "usr/local/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(nodeRoot, "opt/orva"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeRoot, "usr/local/bin/node"), []byte("node"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeRoot, "opt/orva/adapter.js"), []byte("adapter"), 0o644); err != nil {
		t.Fatal(err)
	}
	codeDir := filepath.Join(root, "code")
	if err := os.MkdirAll(codeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeNsjail := filepath.Join(root, "fake-nsjail")
	if err := os.WriteFile(fakeNsjail, []byte("#!/bin/sh\nprintf 'kafel startup failed\\n' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := Spawn(context.Background(), ExecConfig{
		Language: Node, CodeDir: codeDir, NsjailBin: fakeNsjail,
		RootfsDir: filepath.Join(root, "rootfs"), Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn fast-failing process: %v", err)
	}
	select {
	case <-w.waitDone:
	case <-time.After(time.Second):
		t.Fatal("fast-failing process did not exit")
	}
	result, err := w.DispatchEx(context.Background(), []byte(`{}`))
	if !errors.Is(err, ErrWorkerExited) {
		t.Fatalf("expected ErrWorkerExited, got %v", err)
	}
	if got := string(result.Stderr()); !strings.Contains(got, "kafel startup failed") {
		t.Fatalf("startup stderr was not drained: %q", got)
	}
}

func TestResolveRuntimeRejectsIncompleteRootfs(t *testing.T) {
	root := t.TempDir()
	nodeRoot := filepath.Join(root, "node")
	if err := os.MkdirAll(filepath.Join(nodeRoot, "usr/local/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveRuntime(ExecConfig{Language: Node, RootfsDir: root}); err == nil {
		t.Fatal("incomplete rootfs without node executable was accepted")
	}
	if err := os.WriteFile(filepath.Join(nodeRoot, "usr/local/bin/node"), []byte("node"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveRuntime(ExecConfig{Language: Node, RootfsDir: root}); err == nil {
		t.Fatal("rootfs without embedded adapter was accepted")
	}
	if err := os.MkdirAll(filepath.Join(nodeRoot, "opt/orva"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeRoot, "opt/orva/adapter.js"), []byte("adapter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveRuntime(ExecConfig{Language: Node, RootfsDir: root}); err != nil {
		t.Fatalf("complete rootfs was rejected: %v", err)
	}
}

// TestEgressGrantsOutboundSocketSyscalls guards the footgun that made
// network_mode=egress a toggle with no effect.
//
// The base policies withhold `connect` on purpose (the default set permits only
// the socket calls Node's internal IPC needs). Before this, those syscalls
// reached a sandbox only by switching the whole instance to the `permissive`
// base, so an operator could flip a function to egress, see it accepted, and
// have every outbound call killed by seccomp before the egress policy was ever
// consulted.
func TestEgressGrantsOutboundSocketSyscalls(t *testing.T) {
	for _, base := range []string{"default", "strict", "permissive"} {
		t.Run(base, func(t *testing.T) {
			egress := BuildSeccompPolicy(base, SeccompAllowForNetworkMode("egress"), nil)
			if !strings.Contains(egress, "connect") {
				t.Errorf("base %q + egress must allow connect, policy was:\n%s", base, egress)
			}
		})
	}
}

// TestNonEgressWithholdsConnect is the other half: granting the network
// syscalls per function must not leak them to functions that were never given
// network access.
func TestNonEgressWithholdsConnect(t *testing.T) {
	for _, mode := range []string{"", "none"} {
		if got := SeccompAllowForNetworkMode(mode); len(got) != 0 {
			t.Errorf("network mode %q must grant no extra syscalls, got %v", mode, got)
		}
		policy := BuildSeccompPolicy("default", SeccompAllowForNetworkMode(mode), nil)
		if strings.Contains(policy, "connect") {
			t.Errorf("network mode %q must not allow connect", mode)
		}
	}
}
