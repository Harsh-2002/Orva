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
	for k, v := range env {
		args = append(args, "--env", k+"="+v)
	}

	args = append(args, "--")
	args = append(args, cfg.Argv...)
	return args, nil
}
