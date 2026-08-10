package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// argIndex returns the position of s in args, or -1.
func argIndex(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

// hasPair reports whether args contains flag immediately followed by value.
func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func testBuildConfig(t *testing.T) BuildConfig {
	t.Helper()
	return BuildConfig{
		Language:         Node,
		CodeDir:          "/data/functions/fn/versions/abc.tmp",
		Argv:             []string{"/usr/local/bin/npm", "install"},
		NetworkMode:      "egress",
		EgressPolicyPath: testPolicyFile(t),
	}
}

// TestBuildJailArgs_CodeDirIsBoundReadWrite is the difference that makes a
// build jail a build jail: npm writes node_modules/ and pip writes its target
// directory, so the -R (read-only) bind a worker gets would fail every install.
func TestBuildJailArgs_CodeDirIsBoundReadWrite(t *testing.T) {
	cfg := testBuildConfig(t)
	args, err := buildJailArgs(cfg, "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("buildJailArgs: %v", err)
	}
	if !hasPair(args, "-B", cfg.CodeDir+":"+BuildCodeDir) {
		t.Fatalf("code dir must be bound read-write, got: %s", strings.Join(args, " "))
	}
	if hasPair(args, "-R", cfg.CodeDir+":"+BuildCodeDir) {
		t.Fatalf("code dir must not be bound read-only: %s", strings.Join(args, " "))
	}
}

// TestBuildJailArgs_WritableHomeAndCwd covers the other two things npm and pip
// refuse to run without: a writable HOME/cache and a working directory that
// exists inside the jail.
func TestBuildJailArgs_WritableHomeAndCwd(t *testing.T) {
	args, err := buildJailArgs(testBuildConfig(t), "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("buildJailArgs: %v", err)
	}
	if !hasPair(args, "-B", "/scratch:"+buildTmpDir) {
		t.Fatalf("scratch dir must be bound read-write at %s: %s", buildTmpDir, strings.Join(args, " "))
	}
	if !hasPair(args, "--env", "HOME="+buildHomeDir) {
		t.Fatalf("HOME must point into the writable scratch mount: %s", strings.Join(args, " "))
	}
	if !hasPair(args, "--env", "TMPDIR="+buildTmpDir) {
		t.Fatalf("TMPDIR must point into the writable scratch mount: %s", strings.Join(args, " "))
	}
	if !hasPair(args, "--cwd", BuildCodeDir) {
		t.Fatalf("build must run with cwd inside the jail: %s", strings.Join(args, " "))
	}
}

// countPairs returns how many times flag appears immediately followed by a
// value satisfying match.
func countPairs(args []string, flag string, match func(string) bool) int {
	n := 0
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && match(args[i+1]) {
			n++
		}
	}
	return n
}

// TestBuildJailArgs_CacheBindIsSingleAndFunctionScoped is the regression guard
// on the whole point of the per-function cache: EXACTLY ONE directory is
// mounted at /cache, and it is the one the caller named for the function being
// built. Anything that turns this into a second, or a shared, mount
// reintroduces the cross-function poisoning channel — npm's packument lives in
// the same URL-keyed cacache as the tarball behind an unkeyed checksum, pip's
// http cache has no content verification at all, and `npm install` runs
// postinstall scripts.
func TestBuildJailArgs_CacheBindIsSingleAndFunctionScoped(t *testing.T) {
	cfg := testBuildConfig(t)
	cfg.CacheDir = "/data/build-cache/019df200-7b00-7e00-9c00-aab1cd2e3f40"
	args, err := buildJailArgs(cfg, "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("buildJailArgs: %v", err)
	}

	mounts := countPairs(args, "-B", func(v string) bool {
		return strings.HasSuffix(v, ":"+buildCacheDir)
	})
	if mounts != 1 {
		t.Fatalf("want exactly 1 bind at %s, got %d: %s", buildCacheDir, mounts, strings.Join(args, " "))
	}
	if !hasPair(args, "-B", cfg.CacheDir+":"+buildCacheDir) {
		t.Fatalf("the cache mount must be the caller's per-function dir: %s", strings.Join(args, " "))
	}
	// Read-write, or npm cannot populate it.
	if hasPair(args, "-R", cfg.CacheDir+":"+buildCacheDir) {
		t.Error("the cache must be bound read-write")
	}
	// HOME stays throwaway: persisting it would also persist npm's logs, its
	// update-notifier stamp, and whatever a postinstall script drops in ~.
	if !hasPair(args, "--env", "HOME="+buildHomeDir) {
		t.Error("HOME must stay inside the throwaway scratch mount")
	}
	for _, want := range [][2]string{
		{"--env", "npm_config_cache=" + buildCacheDir + "/npm"},
		{"--env", "PIP_CACHE_DIR=" + buildCacheDir + "/pip"},
		// npm derives logs-dir from ${cache}/_logs, which would accumulate one
		// debug log per build inside a directory we keep forever.
		{"--env", "npm_config_logs_dir=" + buildNpmLogsDir},
	} {
		if !hasPair(args, want[0], want[1]) {
			t.Errorf("want %s %s, got: %s", want[0], want[1], strings.Join(args, " "))
		}
	}
}

// TestBuildJailArgs_NoCacheDirIsThrowaway: an empty CacheDir must behave
// exactly as before the cache existed — no extra mount, and both installers
// pointed inside the scratch directory that is deleted after the build.
func TestBuildJailArgs_NoCacheDirIsThrowaway(t *testing.T) {
	args, err := buildJailArgs(testBuildConfig(t), "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("buildJailArgs: %v", err)
	}
	if n := countPairs(args, "-B", func(v string) bool { return strings.HasSuffix(v, ":"+buildCacheDir) }); n != 0 {
		t.Fatalf("no CacheDir must mean no cache mount, got %d: %s", n, strings.Join(args, " "))
	}
	for _, want := range []string{
		"npm_config_cache=" + buildCacheDir + "/npm",
		"PIP_CACHE_DIR=" + buildCacheDir + "/pip",
	} {
		if !hasPair(args, "--env", want) {
			t.Errorf("want --env %s, got: %s", want, strings.Join(args, " "))
		}
		// With nothing bound over it, the cache path is an ordinary directory
		// in the scratch tree and dies with it.
		if !strings.HasPrefix(strings.SplitN(want, "=", 2)[1], buildTmpDir+"/") {
			t.Errorf("the fallback cache must live inside the throwaway mount: %s", want)
		}
	}
}

// TestBuildJailArgs_CacheMountPointIsInsideTheScratchMount pins the mechanical
// constraint the live run found: nsjail creates each mount point inside the
// chroot, and a runtime rootfs is read-only and owned by another user. The
// image pre-creates exactly /code and /tmp (scripts/build-rootfs.sh), so a
// top-level /cache fails with "mkdir(...): Permission denied" on every rootfs
// already deployed. The mount point must therefore live under /tmp, which is
// our own world-writable scratch directory.
func TestBuildJailArgs_CacheMountPointIsInsideTheScratchMount(t *testing.T) {
	if !strings.HasPrefix(buildCacheDir, buildTmpDir+"/") {
		t.Fatalf("cache mount point %q must live under the scratch mount %q", buildCacheDir, buildTmpDir)
	}
	cfg := testBuildConfig(t)
	cfg.CacheDir = "/data/build-cache/fn"
	args, err := buildJailArgs(cfg, "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("buildJailArgs: %v", err)
	}
	// Order matters: the scratch mount has to be in place before something is
	// mounted inside it.
	scratchAt, cacheAt := -1, -1
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-B" {
			continue
		}
		switch args[i+1] {
		case "/scratch:" + buildTmpDir:
			scratchAt = i
		case cfg.CacheDir + ":" + buildCacheDir:
			cacheAt = i
		}
	}
	if scratchAt < 0 || cacheAt < 0 {
		t.Fatalf("both mounts must be present: %s", strings.Join(args, " "))
	}
	if scratchAt > cacheAt {
		t.Errorf("the scratch mount must precede the cache mount nested inside it")
	}
}

// TestBuildJailArgs_OrvaOwnsTheCacheEnv: these variables name paths that exist
// only inside the jail, so a value forwarded from orvad's environment cannot
// be right. An operator with npm_config_cache=/root/.npm set on the daemon
// would otherwise have npm pointed at an unmounted path on the read-only
// chroot, and the build dies with a bare EROFS.
func TestBuildJailArgs_OrvaOwnsTheCacheEnv(t *testing.T) {
	cfg := testBuildConfig(t)
	cfg.CacheDir = "/data/build-cache/fn"
	cfg.Env = map[string]string{
		"npm_config_cache":    "/root/.npm",
		"NPM_CONFIG_CACHE":    "/root/.npm",
		"PIP_CACHE_DIR":       "/root/.cache/pip",
		"pip_cache_dir":       "/root/.cache/pip",
		"npm_config_logs_dir": "/root/.npm/_logs",
		// A legitimately forwarded setting must still survive.
		"NPM_CONFIG_REGISTRY": "https://registry.internal/",
	}
	args, err := buildJailArgs(cfg, "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("buildJailArgs: %v", err)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "/root/") || strings.Contains(arg, "=/root/") {
			t.Fatalf("a host cache path leaked into the jail env: %q", arg)
		}
	}
	if !hasPair(args, "--env", "npm_config_cache="+buildCacheDir+"/npm") {
		t.Errorf("Orva's cache path must win: %s", strings.Join(args, " "))
	}
	if !hasPair(args, "--env", "NPM_CONFIG_REGISTRY=https://registry.internal/") {
		t.Error("forwarding a private registry must still work")
	}
}

// TestBuildJailArgs_RaisesInstallLimits pins the limits an install needs and an
// invocation does not. nsjail's defaults (RLIMIT_FSIZE 1 MB, RLIMIT_NOFILE 32,
// time_limit 600s) each break a real dependency install on their own.
func TestBuildJailArgs_RaisesInstallLimits(t *testing.T) {
	args, err := buildJailArgs(testBuildConfig(t), "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("buildJailArgs: %v", err)
	}
	for _, want := range [][2]string{
		{"--rlimit_fsize", "max"},
		{"--rlimit_nofile", "max"},
		{"--rlimit_as", "max"},
		{"--time_limit", "0"},
	} {
		if !hasPair(args, want[0], want[1]) {
			t.Errorf("want %s %s, got: %s", want[0], want[1], strings.Join(args, " "))
		}
	}
}

// TestBuildJailArgs_ConfigFlagIsFirst pins the same argv ordering the worker
// path depends on: nsjail's config loader CopyFroms onto the config message and
// clears everything already set, so a flag before --config is silently
// discarded. For a build that would drop --cwd (cwd resets to "/", which makes
// npm fail with a confusing "Tracker idealTree already exists") and every
// --env, --seccomp_string and bind mount with it.
func TestBuildJailArgs_ConfigFlagIsFirst(t *testing.T) {
	cfg := testBuildConfig(t)
	cfg.Env = map[string]string{"NPM_CONFIG_REGISTRY": "https://registry.example"}
	args, err := buildJailArgs(cfg, "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("buildJailArgs: %v", err)
	}
	if len(args) < 2 || args[0] != "--config" || args[1] != cfg.EgressPolicyPath {
		t.Fatalf("--config must be argv[0..1], got: %s", strings.Join(args, " "))
	}
	for _, flag := range []string{"--env", "--seccomp_string", "-B", "--chroot", "--cwd"} {
		if argIndex(args, flag) == 0 {
			t.Errorf("%s must not precede --config", flag)
		}
	}
}

// TestBuildJailArgs_EgressWithoutPolicyIsAnError is the fail-closed guard for
// dependency installs. NSTUN allows every destination no rule matches, so an
// install started without a compiled policy would fetch from anywhere the
// operator had blocked — the exact hole this jail exists to close.
func TestBuildJailArgs_EgressWithoutPolicyIsAnError(t *testing.T) {
	t.Run("no policy path", func(t *testing.T) {
		cfg := testBuildConfig(t)
		cfg.EgressPolicyPath = ""
		if _, err := buildJailArgs(cfg, "/rootfs/node", "/scratch"); !errors.Is(err, ErrEgressPolicyMissing) {
			t.Fatalf("want ErrEgressPolicyMissing, got %v", err)
		}
	})

	t.Run("policy file vanished", func(t *testing.T) {
		cfg := testBuildConfig(t)
		cfg.EgressPolicyPath = filepath.Join(t.TempDir(), "gone.cfg")
		_, err := buildJailArgs(cfg, "/rootfs/node", "/scratch")
		if !errors.Is(err, ErrEgressPolicyMissing) {
			t.Fatalf("want ErrEgressPolicyMissing, got %v", err)
		}
		if !strings.Contains(err.Error(), "gone.cfg") {
			t.Errorf("error should name the missing path: %v", err)
		}
	})
}

// TestBuildJailArgs_NoNetworkStepIsOffline covers the compile step. tsc needs
// no registry, so it gets no network stack — and therefore needs no policy,
// which must not be mistaken for the fail-closed case above.
func TestBuildJailArgs_NoNetworkStepIsOffline(t *testing.T) {
	cfg := testBuildConfig(t)
	cfg.NetworkMode = ""
	cfg.EgressPolicyPath = ""
	cfg.Argv = []string{"/usr/local/bin/npx", "tsc"}
	args, err := buildJailArgs(cfg, "/rootfs/node", "/scratch")
	if err != nil {
		t.Fatalf("a no-network build step must not require a policy: %v", err)
	}
	if argIndex(args, "--user_net") >= 0 || argIndex(args, "--config") >= 0 {
		t.Fatalf("offline step must have neither --user_net nor --config: %s", strings.Join(args, " "))
	}
}

func TestBuildJailArgs_DisableUsernsTracksTheEnvVar(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		t.Setenv("ORVA_DISABLE_USERNS", "1")
		args, err := buildJailArgs(testBuildConfig(t), "/rootfs/node", "/scratch")
		if err != nil {
			t.Fatalf("buildJailArgs: %v", err)
		}
		if argIndex(args, "--disable_clone_newuser") < 0 {
			t.Fatalf("ORVA_DISABLE_USERNS=1 must emit --disable_clone_newuser: %s", strings.Join(args, " "))
		}
	})
	for _, value := range []string{"", "0", "true"} {
		t.Run("disabled/"+value, func(t *testing.T) {
			t.Setenv("ORVA_DISABLE_USERNS", value)
			args, err := buildJailArgs(testBuildConfig(t), "/rootfs/node", "/scratch")
			if err != nil {
				t.Fatalf("buildJailArgs: %v", err)
			}
			if argIndex(args, "--disable_clone_newuser") >= 0 {
				t.Fatalf("ORVA_DISABLE_USERNS=%q must keep the user namespace", value)
			}
		})
	}
}

// TestBuildJailArgs_UsesTheRootfsToolchain guards against reintroducing the
// host-npm/rootfs-node skew: argv[0] is resolved inside the runtime image.
func TestRunBuildResolvesToolFromRootfs(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "node", "usr", "local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := BuildConfig{Language: Node, RootfsDir: root, Argv: []string{"/usr/local/bin/npm"}}

	if _, err := resolveBuildRuntime(cfg); err == nil {
		t.Fatal("a rootfs without npm must be rejected")
	}
	if err := os.WriteFile(filepath.Join(binDir, "npm"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveBuildRuntime(cfg); err != nil {
		t.Fatalf("rootfs with npm was rejected: %v", err)
	}

	cfg.Argv = []string{"npm"}
	if _, err := resolveBuildRuntime(cfg); err == nil {
		t.Fatal("a host-relative command must be rejected: argv is resolved inside the jail")
	}
}

// TestBuildSeccompProfilePermitsInstallSyscalls is the regression guard for the
// profile. Each name below was added because a real install failed with EPERM
// without it, or because glibc reaches the same operation through it on the
// other release architecture.
func TestBuildSeccompProfilePermitsInstallSyscalls(t *testing.T) {
	allowed := policySyscalls(BuildJailSeccompPolicy("egress"))

	mutation := []string{
		// proven by a failing install
		"symlink", "listxattr", "utimensat", "fsync",
		// aarch64 reaches the same operations through these
		"symlinkat", "linkat", "renameat",
		// already in the base set, listed so a base change cannot silently
		// take file mutation away from builds
		"mkdir", "mkdirat", "rename", "renameat2", "unlink", "unlinkat",
		"rmdir", "chmod", "fchmod", "fchmodat", "ftruncate", "statfs",
	}
	for _, name := range mutation {
		if !allowed[name] {
			t.Errorf("build profile must permit %s", name)
		}
	}
	// The registry fetch itself.
	if !allowed["connect"] {
		t.Error("an egress build step must be able to connect()")
	}
}

// TestBuildSeccompProfileWithoutNetworkWithholdsConnect: the compile step gets
// the file-mutation set but no sockets.
func TestBuildSeccompProfileWithoutNetworkWithholdsConnect(t *testing.T) {
	allowed := policySyscalls(BuildJailSeccompPolicy(""))
	if allowed["connect"] {
		t.Error("an offline build step must not be able to connect()")
	}
	if !allowed["symlink"] {
		t.Error("an offline build step still writes files")
	}
}

// TestBuildProfileDoesNotWidenWorkerSandboxes: the build additions must reach
// build jails only. A worker keeps the read-mostly default it always had.
func TestBuildProfileDoesNotWidenWorkerSandboxes(t *testing.T) {
	worker := policySyscalls(BuildSeccompPolicy("default", SeccompAllowForNetworkMode("egress"), nil))
	for _, name := range []string{"symlink", "listxattr", "utimensat", "fsync", "fork", "vfork", "kill"} {
		if worker[name] {
			t.Errorf("default worker policy must not gain %s from the build profile", name)
		}
	}
}

// TestBuildProfileCompilesOnArm64 guards the one-architecture footgun: Kafel
// treats an unknown syscall name as a compilation error, so shipping a name
// aarch64 does not have would take every build on arm64 down at once.
func TestBuildProfileCompilesOnArm64(t *testing.T) {
	allowed := policySyscalls(buildSeccompPolicyForArch(seccompBaseBuild,
		SeccompAllowForNetworkMode("egress"), nil, "arm64"))
	for _, name := range []string{"fork", "vfork", "link", "symlink"} {
		if allowed[name] {
			t.Errorf("%s is absent from the aarch64 syscall table and must be filtered", name)
		}
	}
	for _, name := range []string{"linkat", "symlinkat", "renameat", "utimensat", "listxattr"} {
		if !allowed[name] {
			t.Errorf("arm64 build profile is missing %s, the form glibc uses there", name)
		}
	}
}

// TestBuildProfileIsNotOperatorSelectable: "build" is an internal base. The
// operator-facing surface must keep advertising exactly the four public names,
// so nobody can point a worker sandbox at the install profile.
func TestBuildProfileIsNotOperatorSelectable(t *testing.T) {
	if err := ValidatePolicy(seccompBaseBuild); err == nil {
		t.Fatal("ValidatePolicy must reject the internal build profile name")
	}
	if _, ok := ListPolicies()[seccompBaseBuild]; ok {
		t.Fatal("the build profile must not appear in the public policy list")
	}
}

func TestRunBuildRejectsEmptyCommand(t *testing.T) {
	if _, err := RunBuild(t.Context(), BuildConfig{Language: Node}); err == nil {
		t.Fatal("an empty argv must be rejected")
	}
}
