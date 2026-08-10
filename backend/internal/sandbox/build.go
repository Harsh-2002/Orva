package sandbox

// Build jails. A dependency install is the one place Orva executes arbitrary
// third-party code with a reason to reach the network: `npm install` runs
// whatever postinstall script a package ships, and both npm and pip fetch from
// a registry. Running that on the host left it outside every control the
// operator configured — the compiled NSTUN egress policy is attached per
// nsjail spawn, so a host process is not covered by it at all.
//
// RunBuild puts those commands in the same kind of jail the workers get, under
// the same policy generation, with the same fail-closed rule: no compiled
// policy, no install. The differences from a worker jail are all consequences
// of a build needing to WRITE:
//
//   - the code directory is bound read-write (-B, not -R): npm writes
//     node_modules/, pip writes packages into the target dir
//   - a throwaway host directory is bound read-write at /tmp and carries HOME,
//     because npm and pip both insist on a writable cache. It lives under the
//     data dir rather than in /tmp so it shares a filesystem with the code dir
//     (no EXDEV on rename) and does not consume RAM on a tmpfs.
//   - optionally a SECOND writable mount at /cache holding the installer
//     caches, which unlike /tmp survives the build (see BuildConfig.CacheDir).
//     HOME stays throwaway on purpose: persisting it would also persist npm's
//     logs, its update-notifier stamp, and whatever a postinstall script chose
//     to drop in ~.
//   - --cwd is inside the jail, so argv must name in-jail paths (BuildCodeDir)
//   - the limits are an order of magnitude above an invoke's

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Paths inside a build jail. Callers compose argv from these: the code
// directory is bind-mounted, so a host path would not resolve in the jail.
const (
	// BuildCodeDir is where the version scratch directory is mounted.
	BuildCodeDir = "/code"
	// buildTmpDir is a writable scratch mount; also TMPDIR for the build.
	buildTmpDir = "/tmp"
	// buildHomeDir lives inside buildTmpDir so one mount covers both.
	buildHomeDir = buildTmpDir + "/home"
	// buildCacheDir is where the installer caches live inside the jail.
	//
	// It sits UNDER the scratch mount for a mechanical reason: nsjail has to
	// create each mount point inside the chroot, and a runtime rootfs is
	// read-only and owned by another user — the image pre-creates exactly
	// /code and /tmp for this (scripts/build-rootfs.sh), so a top-level
	// /cache cannot be mounted on any rootfs already deployed. The scratch
	// directory is ours and world-writable, so a mount point inside it always
	// works.
	//
	// Whether this path persists is decided entirely on the host side: with
	// BuildConfig.CacheDir set, a host directory is bound OVER it and its
	// contents outlive the build; without, it is an ordinary directory in the
	// scratch tree and is deleted with it. HOME is throwaway either way.
	buildCacheDir = buildTmpDir + "/cache"
	// buildNpmLogsDir keeps npm's debug logs OUT of the cache root. npm derives
	// logs-dir from ${cache}/_logs by default, which against a persistent cache
	// would accumulate one log per build forever.
	buildNpmLogsDir = buildTmpDir + "/npm-logs"
)

// Build jail resource defaults. An install is nothing like an invocation:
// npm routinely peaks in the hundreds of MB and forks a process per package
// lifecycle script, so the invoke defaults (64 MB / 32 pids) would kill it.
const (
	defaultBuildMemoryMB = 2048
	defaultBuildMaxPids  = 256
)

// BuildConfig describes one build step to run inside nsjail.
type BuildConfig struct {
	Language Language

	// CodeDir is the host directory bind-mounted READ-WRITE at BuildCodeDir.
	// This is the version scratch dir; the step is expected to mutate it.
	CodeDir string

	// Argv is the command to run, as absolute in-jail paths. It must come
	// from the runtime's own rootfs (/usr/local/bin/npm, /usr/local/bin/pip)
	// so a build uses the same toolchain the function will run under.
	Argv []string

	// Env is added to (and overrides) the jail's structural environment.
	Env map[string]string

	// NetworkMode is "egress" for steps that must reach a package registry and
	// "" / "none" for steps that must not (the TypeScript compile). "egress"
	// requires EgressPolicyPath.
	NetworkMode string

	// EgressPolicyPath is the compiled NSTUN policy generation, REQUIRED when
	// NetworkMode == "egress". Empty is a hard error, never a silent
	// fall-through to an unfiltered install.
	EgressPolicyPath string
	// EgressPolicyGen identifies that generation, for logs.
	EgressPolicyGen string

	// ResolvConfPath / HostsPath are the firewall-managed DNS files, bound
	// read-only when present. Same skip-if-missing posture as a worker.
	ResolvConfPath string
	HostsPath      string

	// ScratchBase is the directory the throwaway HOME/TMPDIR is created under.
	// Empty falls back to the OS temp dir.
	ScratchBase string

	// CacheDir is a host directory bound read-write at buildCacheDir and NOT
	// deleted afterwards, holding npm's cacache and pip's http cache so a
	// redeploy does not re-download every dependency.
	//
	// It MUST be private to the function being built. npm caches the packument
	// alongside the tarball in the same cacache, keyed by URL and protected
	// only by an unkeyed corruption checksum, and pip's http cache is
	// URL-keyed with no content verification at all — so a shared cache would
	// let one function's dependency poison every later build of every other
	// function, and `npm install` runs postinstall scripts.
	//
	// Empty means "no persistent cache": the installers get a throwaway cache
	// inside the scratch mount, which is exactly the pre-cache behaviour.
	CacheDir string

	// Timeout bounds the step. Zero means "no deadline of our own" — the
	// caller's context still applies.
	Timeout time.Duration

	MemoryMB int
	MaxPids  int

	NsjailBin string
	RootfsDir string
}

// ErrBuildTimedOut means the step hit its own deadline (not the caller's).
var ErrBuildTimedOut = errors.New("build step timed out")

// RunBuild executes one build step inside nsjail and returns its combined
// stdout+stderr. The output is returned on both the success and failure paths
// so the caller can stream it into build_logs either way.
func RunBuild(ctx context.Context, cfg BuildConfig) ([]byte, error) {
	if len(cfg.Argv) == 0 {
		return nil, errors.New("build step has no command")
	}
	if cfg.MemoryMB <= 0 {
		cfg.MemoryMB = defaultBuildMemoryMB
	}
	if cfg.MaxPids <= 0 {
		cfg.MaxPids = defaultBuildMaxPids
	}

	rootfs, err := resolveBuildRuntime(cfg)
	if err != nil {
		return nil, err
	}

	// One throwaway directory serves as both /tmp and HOME for the step. It is
	// removed afterwards: npm's cache alone can outweigh the deployed code, and
	// nothing downstream reads it.
	scratchBase := cfg.ScratchBase
	if scratchBase != "" {
		if err := os.MkdirAll(scratchBase, 0o755); err != nil {
			return nil, fmt.Errorf("create build scratch base: %w", err)
		}
	}
	scratch, err := os.MkdirTemp(scratchBase, "build-")
	if err != nil {
		return nil, fmt.Errorf("create build scratch dir: %w", err)
	}
	defer os.RemoveAll(scratch)
	// 0777: with a user namespace the jailed process keeps the daemon's uid,
	// but a bare-metal install that sets ORVA_DISABLE_USERNS runs nsjail
	// without one, and nsjail may then drop to a different uid inside.
	if err := os.Chmod(scratch, 0o777); err != nil {
		return nil, fmt.Errorf("prepare build scratch dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(scratch, "home"), 0o777); err != nil {
		return nil, fmt.Errorf("create build home dir: %w", err)
	}
	// The mount point for buildCacheDir has to exist before nsjail runs: it
	// cannot create one itself inside the read-only rootfs. Created
	// unconditionally so the in-jail path is the same whether or not a
	// persistent cache is bound over it.
	if err := os.MkdirAll(filepath.Join(scratch, "cache"), 0o777); err != nil {
		return nil, fmt.Errorf("create build cache mount point: %w", err)
	}

	// A cache is an optimisation, never a precondition: if it cannot be
	// prepared, drop it and build with a throwaway one rather than failing a
	// deploy over a directory nothing depends on.
	if cfg.CacheDir != "" {
		if err := prepareCacheDirs(cfg.CacheDir); err != nil {
			slog.Warn("build cache unusable, falling back to a throwaway cache",
				"dir", cfg.CacheDir, "err", err)
			cfg.CacheDir = ""
		}
	}

	args, err := buildJailArgs(cfg, rootfs, scratch)
	if err != nil {
		return nil, err
	}

	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	slog.Info("build step starting",
		"cmd", cfg.Argv[0],
		"language", string(cfg.Language),
		"network_mode", cfg.NetworkMode,
		"egress_policy_gen", cfg.EgressPolicyGen)

	out, runErr := exec.CommandContext(ctx, cfg.NsjailBin, args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%w after %s", ErrBuildTimedOut, cfg.Timeout)
	}
	return out, runErr
}

// prepareCacheDirs creates the per-installer cache subdirectories. Same 0777
// as the scratch dir and for the same reason: with ORVA_DISABLE_USERNS=1 there
// is no user namespace, so nsjail may drop to a different uid inside the jail
// and would otherwise be unable to write here.
func prepareCacheDirs(cacheDir string) error {
	for _, dir := range []string{cacheDir, filepath.Join(cacheDir, "npm"), filepath.Join(cacheDir, "pip")} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return fmt.Errorf("create build cache dir: %w", err)
		}
		// MkdirAll applies the process umask, so the mode above is a request,
		// not a guarantee — chmod is what actually makes it group/other
		// writable.
		if err := os.Chmod(dir, 0o777); err != nil {
			return fmt.Errorf("prepare build cache dir: %w", err)
		}
	}
	return nil
}

// resolveBuildRuntime validates the rootfs and that argv[0] actually exists in
// it. Without this check a missing toolchain surfaces as nsjail exit 255 with
// no explanation of what was missing.
func resolveBuildRuntime(cfg BuildConfig) (string, error) {
	switch cfg.Language {
	case Node, Python:
	default:
		return "", fmt.Errorf("unsupported language: %s", cfg.Language)
	}
	rootfs := filepath.Join(cfg.RootfsDir, string(cfg.Language))
	info, err := os.Stat(rootfs)
	if err != nil {
		return "", fmt.Errorf("rootfs not found at %s: %w", rootfs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("rootfs path is not a directory: %s", rootfs)
	}
	bin := cfg.Argv[0]
	if !strings.HasPrefix(bin, "/") {
		return "", fmt.Errorf("build command must be an absolute in-jail path, got %q", bin)
	}
	binPath := filepath.Join(rootfs, filepath.FromSlash(strings.TrimPrefix(bin, "/")))
	binInfo, err := os.Stat(binPath)
	if err != nil || binInfo.IsDir() || binInfo.Mode()&0o111 == 0 {
		return "", fmt.Errorf("build tool is missing or not executable at %s", binPath)
	}
	return rootfs, nil
}

func buildJailArgs(cfg BuildConfig, rootfs, scratch string) ([]string, error) {
	// --config must lead; see egressConfigArgs. A build step that needs a
	// registry and has no compiled policy stops here.
	args, err := egressConfigArgs(cfg.NetworkMode, cfg.EgressPolicyPath)
	if err != nil {
		return nil, err
	}

	args = append(args,
		"-Mo",
		"--chroot", rootfs,
		// Read-WRITE, unlike a worker: the whole point of the step is to
		// populate node_modules/ or the pip target directory.
		"-B", cfg.CodeDir+":"+BuildCodeDir,
		"-B", scratch+":"+buildTmpDir,
	)
	// Exactly one cache mount, and only when the caller named one. Any change
	// here that mounts a directory shared between functions reintroduces the
	// cross-function poisoning channel BuildConfig.CacheDir exists to avoid.
	if cfg.CacheDir != "" {
		args = append(args, "-B", cfg.CacheDir+":"+buildCacheDir)
	}
	args = append(args,
		"--cwd", BuildCodeDir,
		"--rlimit_as", "max",
		// nsjail defaults RLIMIT_FSIZE to 1 MB and RLIMIT_NOFILE to 32. Both
		// are fatal for an install: npm writes tarballs far past 1 MB and
		// keeps hundreds of descriptors open.
		"--rlimit_fsize", "max",
		"--rlimit_nofile", "max",
		// nsjail's own 600 s default would cut a large install off with no
		// diagnostic. Deadlines belong to the caller's context, which reports
		// a real error.
		"--time_limit", "0",
		// -q (WARNING), not -Q (FATAL): nsjail gates rule-compilation errors
		// behind ERROR, and NSTUN is default-allow, so a silently dropped rule
		// is a security failure we must be able to see.
		"-q",
	)
	if os.Getenv("ORVA_DISABLE_USERNS") == "1" {
		args = append(args, "--disable_clone_newuser")
	}

	args = append(args, egressNetArgs(cfg.NetworkMode, cfg.ResolvConfPath, cfg.HostsPath)...)

	if mount := cgroupv2Delegate(); mount != "" {
		memMaxBytes := int64(cfg.MemoryMB) * 1024 * 1024 * 3 / 2
		args = append(args,
			"--use_cgroupv2",
			"--cgroupv2_mount", mount,
			"--cgroup_mem_max", fmt.Sprintf("%d", memMaxBytes),
			"--cgroup_pids_max", fmt.Sprintf("%d", cfg.MaxPids),
		)
	}

	args = append(args, "--seccomp_string", BuildJailSeccompPolicy(cfg.NetworkMode))

	// Structural environment first so a caller-supplied value wins.
	env := map[string]string{
		"HOME":   buildHomeDir,
		"TMPDIR": buildTmpDir,
		"PATH":   "/usr/local/bin:/usr/local/sbin:/usr/bin:/bin:/usr/sbin:/sbin",
	}
	for k, v := range cfg.Env {
		if k == "" {
			continue
		}
		env[k] = v
	}

	// ...except the cache locations, which Orva owns outright. These name
	// paths that exist only inside the jail, so a forwarded value cannot be
	// right: an operator with npm_config_cache=/root/.npm in orvad's
	// environment would otherwise point npm at a path that is not mounted and
	// sits on the read-only chroot, and the build dies with a bare EROFS that
	// says nothing about why.
	setOwnedEnv(env, "npm_config_cache", buildCacheDir+"/npm")
	setOwnedEnv(env, "PIP_CACHE_DIR", buildCacheDir+"/pip")
	setOwnedEnv(env, "npm_config_logs_dir", buildNpmLogsDir)

	// Env goes in as one of two forms, and the choice is a disclosure
	// boundary, not a style preference.
	//
	// "--env KEY=VALUE" puts the value in the nsjail process's argv, i.e.
	// /proc/<pid>/cmdline — mode 0444, world-readable, and readable across the
	// whole host because docker-compose runs with pid: host and the image sets
	// no USER. The forwarded set is exactly the credential-bearing one
	// (NPM_CONFIG_*, npm_config_*, PIP_*, and the proxy vars all routinely
	// carry registry tokens or user:pass in a URL). Before builds were jailed,
	// npm and pip ran with an inherited environment, so those values only ever
	// sat in /proc/<pid>/environ at mode 0400 — owner-only. Passing them as
	// arguments would be a real widening introduced by jailing the build.
	//
	// "--env KEY" (no '=') tells nsjail to forward the value from its OWN
	// environment, which it inherits from orvad. Same value in the jail, never
	// in anyone's argv. Use it whenever the value is one we forwarded from our
	// environment in the first place; Orva-computed values (cache paths) are
	// not secret and take the explicit form.
	for k, v := range env {
		if cur, ok := os.LookupEnv(k); ok && cur == v {
			args = append(args, "--env", k)
			continue
		}
		args = append(args, "--env", k+"="+v)
	}

	args = append(args, "--")
	args = append(args, cfg.Argv...)
	return args, nil
}

// setOwnedEnv sets key, first removing every case-insensitive spelling of it
// already in the map. npm folds NPM_CONFIG_CACHE and npm_config_cache onto the
// same config key and resolves a collision by environment iteration order, and
// pip is equally case-insensitive about PIP_*, so deleting our own value's
// variants is the only way "Orva wins" is deterministic.
func setOwnedEnv(env map[string]string, key, value string) {
	for k := range env {
		if strings.EqualFold(k, key) {
			delete(env, k)
		}
	}
	env[key] = value
}
