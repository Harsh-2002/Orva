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
			args, _, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.py")
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
		EgressPolicyPath: testPolicyFile(t),
	}
	args, _, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js")
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
	if _, _, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js"); !errors.Is(err, ErrEgressPolicyMissing) {
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
		EgressPolicyPath: testPolicyFile(t),
		Env:              map[string]string{"SECRET": "value"},
		SeccompPolicy:    "POLICY x { ALLOW { read } }",
	}
	args, _, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js")
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

// TestBuildArgs_DisableUsernsTracksTheEnvVar pins the one deployment
// combination that silently breaks egress: a container WITHOUT NET_ADMIN plus
// ORVA_DISABLE_USERNS=1.
//
// Why it matters: nsjail creates each sandbox's TAP device inside its own user
// namespace, so the kernel checks ns_capable() there instead of the container's
// capability set — that is the whole reason the image needs no NET_ADMIN
// (docker-compose.yml "cap_add" comment, README.md's runc note). Setting
// ORVA_DISABLE_USERNS=1 emits --disable_clone_newuser, which removes that user
// namespace; TUNSETIFF is then charged against the INITIAL namespace and needs
// real CAP_NET_ADMIN (the bare-metal installer instead grants file caps on the
// nsjail binary — see scripts/install.sh setcap and the ci.yml e2e job, which
// runs exactly this configuration).
//
// HONEST SCOPE: this pins the arg-building contract only. It proves that the
// flag which removes the user namespace is emitted exactly when the env var
// asks for it — it does NOT execute nsjail and therefore does not prove the
// kernel's capability check. The capability evidence lives in the docs above
// and in the privileged e2e runs; this test exists so the code cannot drift
// away from what those docs promise without a test going red.
func TestBuildArgs_DisableUsernsTracksTheEnvVar(t *testing.T) {
	newCfg := func() ExecConfig {
		return ExecConfig{
			Language:         Node,
			CodeDir:          "/tmp/code",
			NetworkMode:      "egress",
			EgressPolicyPath: testPolicyFile(t),
		}
	}

	t.Run("enabled", func(t *testing.T) {
		t.Setenv("ORVA_DISABLE_USERNS", "1")
		args, _, err := buildArgs(newCfg(), "/tmp/rootfs", "/tmp/code/handler.js")
		if err != nil {
			t.Fatalf("buildArgs: %v", err)
		}
		if !containsArg(args, "--disable_clone_newuser") {
			t.Fatalf("ORVA_DISABLE_USERNS=1 must emit --disable_clone_newuser, got: %s",
				strings.Join(args, " "))
		}
		// The combination is the invariant: this is still an egress sandbox
		// (--user_net opens /dev/net/tun) and it is now doing so with no user
		// namespace to be privileged in. Deployments in this state need
		// CAP_NET_ADMIN from the initial namespace, or file caps on nsjail.
		if !containsArg(args, "--user_net") {
			t.Fatalf("egress mode must still emit --user_net, got: %s", strings.Join(args, " "))
		}
	})

	// Values other than "1" are not a partial opt-in: the userns path stays on,
	// which is what lets the shipped compose file drop NET_ADMIN.
	for _, value := range []string{"", "0", "true", "yes"} {
		t.Run("disabled/"+value, func(t *testing.T) {
			t.Setenv("ORVA_DISABLE_USERNS", value)
			args, _, err := buildArgs(newCfg(), "/tmp/rootfs", "/tmp/code/handler.js")
			if err != nil {
				t.Fatalf("buildArgs: %v", err)
			}
			if containsArg(args, "--disable_clone_newuser") {
				t.Fatalf("ORVA_DISABLE_USERNS=%q must keep the user namespace, got: %s",
					value, strings.Join(args, " "))
			}
		})
	}
}

// TestBuildArgs_UsesWarningLogLevel guards the observability fix: nsjail gates
// rule-compilation errors behind ERROR, and -Q (FATAL) suppressed them
// entirely, making a silently-dropped policy rule invisible.
func TestBuildArgs_UsesWarningLogLevel(t *testing.T) {
	cfg := ExecConfig{Language: Python, CodeDir: "/tmp/code"}
	args, _, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.py")
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
	args, _, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.py")
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
	// The policy opens with an ERRNO block before ALLOW, so anchor on the
	// ALLOW list itself rather than a fixed prefix — otherwise the first
	// syscall in the list is silently swallowed by the header.
	const marker = "ALLOW { "
	body := policy
	if start := strings.Index(body, marker); start >= 0 {
		body = body[start+len(marker):]
	}
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

// testPolicyFile writes a throwaway policy file so buildArgs' on-disk check
// passes. buildArgs verifies the generation file still exists, so a fabricated
// path would now be rejected as a missing policy.
func testPolicyFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "egress-test.cfg")
	if err := os.WriteFile(p, []byte("mount_proc: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestBuildArgs_EgressPolicyFileMustExist covers the second fail-closed arm.
//
// A path can be non-empty and still be gone: the manager hands out a concrete
// generation path, and the file behind it can be deleted or a volume can go
// missing. nsjail would fail closed anyway (exit 255 on an unreadable
// --config), but it surfaces as a generic worker crash whose operator hint
// points at the wrong thing. Catching it here maps both arms — no policy
// compiled, and a compiled policy whose file vanished — onto one actionable
// error.
func TestBuildArgs_EgressPolicyFileMustExist(t *testing.T) {
	cfg := ExecConfig{
		Language:         Node,
		CodeDir:          "/tmp/code",
		NetworkMode:      "egress",
		EgressPolicyPath: filepath.Join(t.TempDir(), "does-not-exist.cfg"),
	}
	_, _, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js")
	if !errors.Is(err, ErrEgressPolicyMissing) {
		t.Fatalf("want ErrEgressPolicyMissing for a vanished policy file, got %v", err)
	}
	if !strings.Contains(err.Error(), "does-not-exist.cfg") {
		t.Errorf("error should name the missing path so an operator can act on it: %v", err)
	}
}

// TestKafelAliasesRespectTheArchFilter guards a structural hole rather than a
// current bug: the Kafel runtime aliases are appended after the arch filter
// because they deliberately bypass ValidSyscallName (they are Kafel-internal
// names, absent from the user-facing catalog). Bypassing the ARCH check too
// would mean a future alias missing from one architecture's Kafel catalog
// fails policy compilation — and an unknown name is a compile error, so every
// sandbox on that arch would fail to start rather than merely lose a syscall.
func TestKafelAliasesRespectTheArchFilter(t *testing.T) {
	// Both current aliases are valid on both targets, so the policy must carry
	// them on each. This is the baseline the guard protects.
	for _, arch := range []string{"amd64", "arm64"} {
		policy := buildSeccompPolicyForArch("default", nil, nil, arch)
		for _, alias := range kafelRuntimeAliases {
			if !strings.Contains(policy, alias) {
				t.Errorf("%s: policy is missing Kafel alias %q", arch, alias)
			}
		}
	}

	// And an alias that an architecture does not support must be dropped for
	// that arch instead of reaching Kafel. "open" stands in for a future alias
	// on aarch64's unsupported list.
	prev := kafelRuntimeAliases
	kafelRuntimeAliases = append(append([]string(nil), prev...), "open")
	t.Cleanup(func() { kafelRuntimeAliases = prev })

	arm := buildSeccompPolicyForArch("strict", nil, nil, "arm64")
	for _, tok := range strings.Split(arm, ",") {
		if strings.TrimSpace(tok) == "open" {
			t.Fatal("an arm64-unsupported alias reached the compiled policy; " +
				"Kafel would fail to compile it and no sandbox would start")
		}
	}
}

// TestBuildArgs_SecretsNeverAppearInArgv pins the fix for secrets leaking via
// /proc/<pid>/cmdline. That file is world-readable and the shipped compose runs
// `pid: host`, so a value in argv was readable by any local user for the life of
// the call. Values now travel in the child's environment; argv carries only the
// NAME, which nsjail resolves from its own env.
//
// Fails on the pre-fix code: argv contained "SECRET=hunter2".
func TestBuildArgs_SecretsNeverAppearInArgv(t *testing.T) {
	const secret = "hunter2-do-not-leak"
	cfg := ExecConfig{
		Language:    Node,
		CodeDir:     "/tmp/code",
		NetworkMode: "none",
		Env: map[string]string{
			"SECRET":              secret,
			"ORVA_INTERNAL_TOKEN": "tok-" + secret,
			"ORVA_FUNCTION_ID":    "fn-1",
		},
	}
	args, childEnv, err := buildArgs(cfg, "/tmp/rootfs", "/tmp/code/handler.js")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}

	joined := strings.Join(args, " ")
	if strings.Contains(joined, secret) {
		t.Errorf("secret value leaked into argv: %s", joined)
	}
	// The names must still be forwarded, or the function loses its env.
	for _, name := range []string{"SECRET", "ORVA_INTERNAL_TOKEN", "ORVA_FUNCTION_ID"} {
		if !hasPair(args, "--env", name) {
			t.Errorf("--env %s missing from argv", name)
		}
	}
	// ...and the values must be in the child env exactly once.
	want := map[string]bool{
		"SECRET=" + secret:                  false,
		"ORVA_INTERNAL_TOKEN=tok-" + secret: false,
		"ORVA_FUNCTION_ID=fn-1":             false,
	}
	for _, kv := range childEnv {
		if _, ok := want[kv]; ok {
			want[kv] = true
		}
	}
	for kv, seen := range want {
		if !seen {
			t.Errorf("child env missing %q; got %v", kv, childEnv)
		}
	}
}
