package builder

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

// BuildResult holds the output of a successful build.
type BuildResult struct {
	ImageTag   string        `json:"image_tag"`
	ImageSize  int64         `json:"image_size"`
	Duration   time.Duration `json:"duration"`
	CodeHash   string        `json:"code_hash"`
	VersionDir string        `json:"version_dir"` // absolute path to versions/<hash>; activate sets `current` to point at this
	Cached     bool          `json:"cached"`      // true when an identical-hash version already existed and we skipped extract+install
	// Entrypoint is the resolved relative path of the file the sandbox
	// adapter should require/import. For Python and plain JS it equals
	// fn.Entrypoint as submitted. For TypeScript builds it is rewritten
	// to the compiled `<outDir>/<stem>.js` (e.g. "dist/handler.js") so
	// the worker process can find the emitted artifact at runtime. The
	// queue worker persists this back onto the function row so the
	// pool's buildEnv can publish it as ORVA_ENTRYPOINT to the sandbox.
	Entrypoint string `json:"entrypoint"`
}

// ErrInsufficientDisk is returned by Build when the data directory has
// less free space than system_config.min_free_disk_mb. The handler maps
// this to HTTP 503 with code INSUFFICIENT_DISK.
var ErrInsufficientDisk = errors.New("insufficient free disk space for deploy")

// Builder handles building function code for nsjail execution.
type Builder struct {
	// FnLock is the per-function build mutex, shared with the build queue and
	// the GC. Optional: nil means no concurrent build can exist (unit tests).
	FnLock func(fnID string) *sync.Mutex

	// BuildFunc can be injected to override behavior. For nsjail this is
	// typically a no-op since code just needs to be extracted, not built.
	BuildFunc func(ctx context.Context, dockerfilePath, contextDir, imageTag string) (int64, error)

	// DataDir is the persistent directory where function code is stored.
	DataDir string

	// DB is consulted at build time for tuning knobs (min_free_disk_mb).
	// Optional — nil means skip the pre-flight disk check, which is fine
	// for unit tests but production wires this up.
	DB *database.Database

	// Logger, when non-nil, receives per-line stdout/stderr from pip/npm
	// and validator output so build progress can stream into build_logs.
	// Set by the Queue worker right before calling Build and cleared after.
	Logger interface {
		Append(stream, line string)
	}

	// NsjailBin / RootfsDir locate the sandbox every dependency install runs
	// in. Same values the pool uses for workers, so a function is built with
	// the very toolchain it will later run under.
	NsjailBin string
	RootfsDir string

	// EgressPolicy resolves the compiled NSTUN policy a dependency install
	// runs under, exactly as pool.Manager does for workers. Late-bound from
	// the firewall manager in server.New (see SetEgressPolicy).
	//
	// Unlike the pool's, a nil accessor here is NOT "no policy layer wired,
	// carry on": an install reaches a package registry, so running it without
	// a policy is precisely the unfiltered egress this exists to prevent.
	// Builds that install nothing never consult it.
	EgressPolicy func() (path, gen string, err error)
}

// New creates a new Builder.
func New() *Builder {
	return &Builder{}
}

// SetEgressPolicy wires the compiled-policy accessor in after construction,
// mirroring pool.Manager.SetEgressPolicy: the firewall manager is built later
// than the builder in server.New.
func (b *Builder) SetEgressPolicy(fn func() (string, string, error)) {
	b.EgressPolicy = fn
}

// Build extracts the code archive into a content-addressed version directory,
// validates it, installs dependencies, and writes a `.orva-ready` marker.
// The version directory is built atomically via a scratch dir + rename so
// failed builds never leave a half-installed `versions/<hash>/` for the GC
// or rollback to find. Activation (the symlink retarget) is the caller's
// responsibility — Build only produces the artifact.
//
// If a `versions/<hash>/.orva-ready` already exists Build short-circuits
// and returns the existing path with Cached=true. This makes redeploys of
// identical content nearly free.
func (b *Builder) Build(ctx context.Context, fn *database.Function, codeArchivePath string) (*BuildResult, error) {
	start := time.Now()

	// Pre-flight: refuse to start an extract that won't finish.
	if err := b.checkFreeDisk(); err != nil {
		return nil, err
	}

	// Compute code hash.
	codeHash, err := hashFile(codeArchivePath)
	if err != nil {
		return nil, fmt.Errorf("hash archive: %w", err)
	}

	fnDir := filepath.Join(b.DataDir, "functions", fn.ID)
	versionDir := filepath.Join(fnDir, "versions", codeHash)
	imageTag := fmt.Sprintf("nsjail/%s:%s", fn.Name, codeHash[:12])

	// Idempotent fast path: same hash, fully published before. Skip
	// extract + install. Activation still happens upstream so a redeploy
	// of identical content can re-point `current` if it had drifted.
	//
	// Entrypoint resolution still has to happen on this path: the function
	// row may have been migrated from a pre-fix version where
	// fn.Entrypoint is still the .ts source, while the cached version dir
	// already contains the compiled dist/. Re-resolve from disk so the
	// queue worker writes the correct value back.
	if _, err := os.Stat(filepath.Join(versionDir, ".orva-ready")); err == nil {
		slog.Info("build cache hit", "fn", fn.ID, "hash", codeHash[:12])
		return &BuildResult{
			ImageTag:   imageTag,
			Duration:   time.Since(start),
			CodeHash:   codeHash,
			VersionDir: versionDir,
			Cached:     true,
			Entrypoint: resolveCachedEntrypoint(versionDir, fn.Entrypoint),
		}, nil
	}

	// Build into a scratch dir adjacent to the final dir; atomically rename
	// on success. Defer cleanup so any error path leaves no debris.
	if err := os.MkdirAll(filepath.Join(fnDir, "versions"), 0755); err != nil {
		return nil, fmt.Errorf("create versions dir: %w", err)
	}
	scratchDir := versionDir + ".tmp." + randSuffix()
	defer os.RemoveAll(scratchDir)

	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	if err := extractTarGz(codeArchivePath, scratchDir); err != nil {
		return nil, fmt.Errorf("extract archive: %w", err)
	}
	if err := ValidateArchive(scratchDir, fn.Runtime, fn.Entrypoint); err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}
	resolvedEntrypoint, err := b.installDependencies(ctx, fn.ID, scratchDir, fn.Runtime, fn.Entrypoint)
	if err != nil {
		return nil, fmt.Errorf("install dependencies: %w", err)
	}
	if err := installAdapter(scratchDir, fn.Runtime, resolvedEntrypoint); err != nil {
		return nil, fmt.Errorf("install adapter: %w", err)
	}
	if err := os.WriteFile(filepath.Join(scratchDir, ".orva-ready"), []byte(codeHash), 0644); err != nil {
		return nil, fmt.Errorf("write ready marker: %w", err)
	}

	// Optional BuildFunc hook (Go compile, image bake, etc.) — runs against
	// the scratch dir before publish.
	if b.BuildFunc != nil {
		if _, err := b.BuildFunc(ctx, "", scratchDir, imageTag); err != nil {
			return nil, fmt.Errorf("build: %w", err)
		}
	}

	// Atomic publish. If a parallel build raced us to the same hash, drop
	// theirs — both produced identical content so this is safe.
	if err := os.RemoveAll(versionDir); err != nil {
		return nil, fmt.Errorf("clear stale version dir: %w", err)
	}
	if err := os.Rename(scratchDir, versionDir); err != nil {
		return nil, fmt.Errorf("publish version dir: %w", err)
	}

	return &BuildResult{
		ImageTag:   imageTag,
		Duration:   time.Since(start),
		CodeHash:   codeHash,
		VersionDir: versionDir,
		Entrypoint: resolvedEntrypoint,
	}, nil
}

// resolveCachedEntrypoint inspects an already-published version dir to
// pick the correct entrypoint when the cache short-circuit fires. For TS
// deploys this means returning "<outDir>/<stem>.js" if the compiled file
// exists; otherwise we trust the function row's original entrypoint.
func resolveCachedEntrypoint(versionDir, original string) string {
	tsConfig := filepath.Join(versionDir, "tsconfig.json")
	if _, err := os.Stat(tsConfig); err != nil {
		return original
	}
	outDir := readTSConfigOutDir(tsConfig)
	// `original` may already be the post-compile path
	// (`dist/handler.js`) on the second build of a function — strip the
	// outDir prefix so we don't end up with `dist/dist/handler.js`.
	stem := tsSourceStem(original, outDir)
	candidate := filepath.Join(outDir, stem+".js")
	if _, err := os.Stat(filepath.Join(versionDir, candidate)); err == nil {
		return candidate
	}
	return original
}

// tsSourceStem returns the source-file stem (no directory, no extension)
// for a TS entrypoint that may have already been rewritten to its
// post-compile form. e.g.
//
//	("handler.ts",        "dist") → "handler"
//	("dist/handler.js",   "dist") → "handler"
//	("src/handler.ts",    "dist") → "src/handler"
//
// We only strip the leading outDir prefix; nested dirs under an outDir
// like dist/sub/handler.js are preserved as `sub/handler` so the caller
// can still locate the right compiled file.
func tsSourceStem(entrypoint, outDir string) string {
	noExt := strings.TrimSuffix(entrypoint, filepath.Ext(entrypoint))
	prefix := outDir + string(filepath.Separator)
	noExt = strings.TrimPrefix(noExt, prefix)
	return noExt
}

// checkFreeDisk returns ErrInsufficientDisk when the data dir has less
// free space than the configured floor. Optional — skipped when DB is
// unwired (unit tests).
func (b *Builder) checkFreeDisk() error {
	if b.DB == nil {
		return nil
	}
	floorMB := b.DB.GetSystemConfigInt("min_free_disk_mb", 500)
	if floorMB <= 0 {
		return nil
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(b.DataDir, &st); err != nil {
		// If we can't even stat the data dir, let the build try and surface
		// the real error rather than masquerading as INSUFFICIENT_DISK.
		return nil
	}
	freeMB := int64(st.Bavail) * int64(st.Bsize) / (1024 * 1024)
	if freeMB >= int64(floorMB) {
		return nil
	}

	// Below the floor. Before refusing the deploy, reclaim the build caches:
	// they are a pure optimisation, so a rebuildable cache must never be the
	// reason a deploy fails for want of space. Caches a build may still be
	// using are left alone (EvictIdleBuildCaches).
	cacheMB := BuildCacheUsageBytes(b.DataDir) / (1024 * 1024)
	slog.Warn("free disk below floor for deploy; reclaiming build caches",
		"free_mb", freeMB, "floor_mb", floorMB, "build_cache_mb", cacheMB, "dir", b.DataDir)
	if freed := EvictIdleBuildCaches(b.DataDir, b.tryFnLock); freed > 0 {
		if err := syscall.Statfs(b.DataDir, &st); err == nil {
			freeMB = int64(st.Bavail) * int64(st.Bsize) / (1024 * 1024)
		}
	}
	if freeMB < int64(floorMB) {
		slog.Warn("insufficient free disk for deploy", "free_mb", freeMB, "floor_mb", floorMB, "dir", b.DataDir)
		return ErrInsufficientDisk
	}
	return nil
}

// randSuffix returns 8 random hex chars, used to build scratch dir names
// that won't collide with concurrent builds of the same hash.
func randSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// installAdapter writes a main.py/main.js wrapper that imports the user's
// handler with the correct entrypoint name.
//
// Historical note: the wrapper used to be the canonical way to feed
// ORVA_ENTRYPOINT to the worker, but the nsjail argv in
// internal/sandbox.buildArgs hardcodes /opt/orva/adapter.{js,py} as the
// entrypoint — this wrapper is therefore never invoked by the runtime
// today. ORVA_ENTRYPOINT is now plumbed through internal/pool.buildEnv
// onto the sandbox env map at spawn time. We keep emitting the wrapper
// because (a) it's a tiny file, (b) the builder unit tests assert on
// its presence, and (c) it's a useful artifact for ad-hoc debugging
// (`node /code/main.js` outside nsjail). Treat as no-op for routing.
func installAdapter(codeDir, runtime, entrypoint string) error {
	switch {
	case isNodeRuntime(runtime):
		adapter := fmt.Sprintf(`// Auto-generated Orva adapter wrapper
process.env.ORVA_ENTRYPOINT = %q;
require('/opt/orva/adapter.js');
`, entrypoint)
		return os.WriteFile(filepath.Join(codeDir, "main.js"), []byte(adapter), 0644)

	case isPythonRuntime(runtime):
		adapter := fmt.Sprintf(`# Auto-generated Orva adapter wrapper
import os
os.environ["ORVA_ENTRYPOINT"] = %q
exec(open("/opt/orva/adapter.py").read())
`, entrypoint)
		return os.WriteFile(filepath.Join(codeDir, "main.py"), []byte(adapter), 0644)

	default:
		return fmt.Errorf("unsupported runtime: %s", runtime)
	}
}

// isNodeRuntime / isPythonRuntime / pythonVersionFor are thin helpers so a
// version bump only changes one place. Orva offers two generic runtimes
// (node = Node.js 24, python = Python 3.14).
func isNodeRuntime(r string) bool   { return r == "node" }
func isPythonRuntime(r string) bool { return r == "python" }

// pythonVersionFor returns the pip --python-version flag value for the python
// runtime. Used so wheels resolve for the right interpreter; bump alongside the
// python rootfs base image.
func pythonVersionFor(r string) string {
	return "3.14"
}

// hashFile computes the SHA256 hash of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractTarGz extracts a .tar.gz file into the destination directory.
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		// Sanitize: prevent path traversal.
		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") || strings.HasPrefix(cleanName, "/") {
			return fmt.Errorf("path traversal in archive: %s", hdr.Name)
		}

		target := filepath.Join(destDir, cleanName)
		// Defense-in-depth: the joined path must still live under destDir even
		// after Clean (mirrors the backup extractor).
		if target != filepath.Clean(destDir) &&
			!strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("path traversal in archive: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outFile, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
			if err := os.Chmod(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		}
	}
	return nil
}

// installDependencies installs packages into the code directory so they are
// available at /code/<pkgs> inside the nsjail sandbox. It returns the
// (possibly rewritten) entrypoint path that should be baked into the
// adapter wrapper — for a TypeScript build that's the compiled `.js` under
// the resolved `outDir`; for everything else it's the unchanged
// `entrypoint` argument.
//
//   - node: if package.json is present, runs `npm install --prefix
//     <codeDir>`. node_modules/ lands at /code/node_modules and require()
//     finds it automatically. If a tsconfig.json is *also* present, runs
//     `npx --no-install tsc --project tsconfig.json` after the install
//     completes — gated on `typescript` appearing in package.json's
//     dependencies / devDependencies. The resolved entrypoint becomes
//     `<outDir>/<stem>.js`.
//
//   - python: if requirements.txt is present, runs `pip install -t <codeDir>`.
//     Packages land at /code/<pkg> and the Python adapter adds /code to sys.path.
//
// Every one of those commands runs inside nsjail, under the same compiled NSTUN
// egress policy a function worker gets (see sandbox.RunBuild). This is not
// decoration: `npm install` executes whatever postinstall script a package
// ships, and both installers fetch from a registry, so a host-side build was
// the one path out of the box the operator's blocklist could not touch. The
// installs therefore fail closed — no compiled policy, no install — while a
// function with no dependencies never invokes an installer at all and deploys
// exactly as before.
//
// The toolchain comes from the runtime's own rootfs, not the host, which also
// closes a latent skew: Orva used to build with the host's npm/pip and run the
// result under the rootfs's node/python.
func (b *Builder) installDependencies(ctx context.Context, fnID, codeDir, runtime, entrypoint string) (string, error) {
	switch {
	case isNodeRuntime(runtime):
		pkgJSON := filepath.Join(codeDir, "package.json")
		if _, err := os.Stat(pkgJSON); os.IsNotExist(err) {
			// No package.json → nothing to install. We still allow a
			// tsconfig.json-only directory through to the TS step so a
			// user with a globally available tsc can be told clearly
			// that they need to declare typescript as a dep.
			return b.maybeCompileTypeScript(ctx, fnID, codeDir, entrypoint)
		}
		slog.Info("installing node dependencies", "dir", codeDir)
		out, err := b.runBuildStep(ctx, buildStep{
			language: sandbox.Node,
			fnID:     fnID,
			codeDir:  codeDir,
			stream:   "npm",
			// --prefix is redundant now that the jail's cwd is the code dir,
			// but it keeps the resolved install root explicit and matches
			// what operators see in the build log.
			argv: []string{nodeNpmBin, "install", "--prefix", sandbox.BuildCodeDir, "--no-audit", "--no-fund"},
			env: map[string]string{
				// The "New major version of npm available" banner is pure
				// noise in a deploy log the user is watching. It only showed
				// up once builds moved into the jail, because npm's notifier
				// state lives in HOME and HOME is now thrown away each time.
				"NO_UPDATE_NOTIFIER":         "1",
				"npm_config_update_notifier": "false",
			},
			network: buildNetworkEgress,
		})
		if err != nil {
			return entrypoint, fmt.Errorf("npm install failed: %w\n%s", err, string(out))
		}
		slog.Info("npm install complete", "output", strings.TrimSpace(string(out)))
		return b.maybeCompileTypeScript(ctx, fnID, codeDir, entrypoint)

	case isPythonRuntime(runtime):
		reqTxt := filepath.Join(codeDir, "requirements.txt")
		if _, err := os.Stat(reqTxt); os.IsNotExist(err) {
			return entrypoint, nil
		}
		slog.Info("installing python dependencies", "dir", codeDir, "runtime", runtime)
		// Wheels are still resolved explicitly for the sandbox's Python
		// version and platform. The build now runs on that interpreter, so
		// this is belt-and-braces rather than a cross-install — but pinning
		// it keeps wheel selection identical to what shipped before.
		// --only-binary=:all: forces wheels so we never execute a setup.py.
		pyVer := pythonVersionFor(runtime)
		out, err := b.runBuildStep(ctx, buildStep{
			language: sandbox.Python,
			fnID:     fnID,
			codeDir:  codeDir,
			stream:   "pip",
			argv: []string{
				pythonPipBin,
				"install", "-r", sandbox.BuildCodeDir + "/requirements.txt",
				"-t", sandbox.BuildCodeDir,
				"--python-version", pyVer,
				"--platform", pipPlatformTag(),
				"--implementation", "cp",
				"--only-binary=:all:",
				"--quiet",
				// Suppress pip's "Running pip as the 'root' user" warning.
				// We're inside a single-tenant container — there's no
				// alternative user to switch to and the warning was the
				// only line showing up in the deploy progress UI.
				"--root-user-action=ignore",
			},
			env:     map[string]string{"PIP_DISABLE_PIP_VERSION_CHECK": "1"},
			network: buildNetworkEgress,
		})
		if err != nil {
			return entrypoint, fmt.Errorf("pip install failed: %w\n%s", err, string(out))
		}
		slog.Info("pip install complete")
	}
	return entrypoint, nil
}

// Toolchain paths inside the runtime rootfs images.
const (
	nodeNpmBin   = "/usr/local/bin/npm"
	nodeNpxBin   = "/usr/local/bin/npx"
	pythonPipBin = "/usr/local/bin/pip"
)

// buildNetworkEgress marks a step that must reach a package registry. Note it
// is a property of the STEP, not of the function: a function with
// network_mode=none still has to download its dependencies to exist at all.
// What that download may reach is the operator's egress policy to decide.
const buildNetworkEgress = "egress"

// buildStep is one jailed command in the deploy pipeline.
type buildStep struct {
	language sandbox.Language
	fnID     string // scopes the persistent installer cache; "" = no cache
	codeDir  string
	argv     []string
	env      map[string]string
	stream   string // build_logs label: "npm" / "pip" / "tsc"
	network  string // buildNetworkEgress, or "" for no network
	timeout  time.Duration
}

// runBuildStep resolves the egress policy (when the step needs one), runs the
// command in nsjail, and streams its output into build_logs — preserving the
// old behaviour of logging everything the subprocess printed on both the
// success and failure paths.
func (b *Builder) runBuildStep(ctx context.Context, step buildStep) ([]byte, error) {
	cfg := b.buildConfigFor(step)
	if step.network == buildNetworkEgress {
		path, gen, err := b.resolveEgressPolicy()
		if err != nil {
			return nil, err
		}
		cfg.EgressPolicyPath, cfg.EgressPolicyGen = path, gen
	}

	out, err := sandbox.RunBuild(ctx, cfg)
	logLines(b, step.stream, out)
	return out, err
}

// buildConfigFor translates a step into the jail configuration. Split out from
// runBuildStep so the cache-scoping invariant is testable without nsjail: the
// cache directory handed to a step is derived from the id of the function
// being built and from nothing else.
func (b *Builder) buildConfigFor(step buildStep) sandbox.BuildConfig {
	cfg := sandbox.BuildConfig{
		Language:    step.language,
		CodeDir:     step.codeDir,
		Argv:        step.argv,
		Env:         withRegistryEnv(step.env),
		NetworkMode: step.network,
		Timeout:     step.timeout,
		NsjailBin:   b.NsjailBin,
		RootfsDir:   b.RootfsDir,
	}
	if b.DataDir == "" {
		return cfg
	}
	// The jail's HOME/TMPDIR lives beside the code it is building so the
	// two share a filesystem (no EXDEV on rename, no tmpfs RAM cost).
	cfg.ScratchBase = filepath.Join(b.DataDir, buildScratchDirName)
	cfg.ResolvConfPath = filepath.Join(b.DataDir, "firewall", "resolv.conf")
	cfg.HostsPath = filepath.Join(b.DataDir, "firewall", "hosts")

	// The installer cache is private to this function — see buildcache.go for
	// why sharing one is a code-execution channel between functions. Failure
	// to prepare it degrades to the throwaway cache; it never fails a build.
	cacheDir, err := PrepareBuildCache(b.DataDir, step.fnID)
	if err != nil {
		slog.Warn("build cache unavailable for this step", "fn", step.fnID, "err", err)
		return cfg
	}
	cfg.CacheDir = cacheDir
	return cfg
}

// resolveEgressPolicy is the fail-closed gate on every dependency install.
func (b *Builder) resolveEgressPolicy() (path, gen string, err error) {
	if b.EgressPolicy == nil {
		return "", "", fmt.Errorf("%w: builder has no egress policy source", sandbox.ErrEgressPolicyMissing)
	}
	return b.EgressPolicy()
}

// registryEnvPrefixes are the environment variables forwarded from orvad's own
// environment into a build jail. A jailed build gets a fresh HOME, so an
// operator's ~/.npmrc or pip.conf no longer applies; the env-var form of the
// same settings (the one the Docker deployment uses) still does. Proxy vars are
// included because a private registry usually sits behind one.
var registryEnvPrefixes = []string{"NPM_CONFIG_", "npm_config_", "PIP_"}

var registryEnvNames = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
}

// withRegistryEnv layers the forwarded registry/proxy configuration under the
// step's own environment (the step wins on collision).
func withRegistryEnv(stepEnv map[string]string) map[string]string {
	env := make(map[string]string, len(stepEnv)+8)
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		forward := false
		for _, name := range registryEnvNames {
			if k == name {
				forward = true
				break
			}
		}
		for _, prefix := range registryEnvPrefixes {
			if strings.HasPrefix(k, prefix) {
				forward = true
				break
			}
		}
		if forward {
			env[k] = v
		}
	}
	for k, v := range stepEnv {
		env[k] = v
	}
	return env
}

// maybeCompileTypeScript runs `tsc --project tsconfig.json` when the code
// directory contains a tsconfig.json. The user must declare typescript in
// their package.json (deps OR devDeps) — otherwise we fail loudly so the
// hint is in build_logs rather than a node_modules-not-found 500 at
// invocation time. Returns the post-compile entrypoint
// (`<outDir>/<stem>.js`); when no tsconfig.json is present it returns the
// original entrypoint unchanged so existing .js-only deploys are untouched.
func (b *Builder) maybeCompileTypeScript(ctx context.Context, fnID, codeDir, entrypoint string) (string, error) {
	tsConfigPath := filepath.Join(codeDir, "tsconfig.json")
	if _, err := os.Stat(tsConfigPath); os.IsNotExist(err) {
		return entrypoint, nil
	}

	// Hard requirement: typescript must be declared in package.json. We
	// don't fall back to a host-installed tsc because that produces a
	// version skew between dev (whatever the box has) and prod (whatever
	// the operator's box has) that's painful to debug.
	pkgJSON := filepath.Join(codeDir, "package.json")
	if err := verifyTypeScriptDeclared(pkgJSON); err != nil {
		return entrypoint, err
	}

	// Honor the existing build_timeout_seconds knob. Default 5 min — long
	// enough for a real typed app, short enough that a runaway build
	// can't hang the queue worker forever.
	timeoutSec := 300
	if b.DB != nil {
		timeoutSec = b.DB.GetSystemConfigInt("build_timeout_seconds", 300)
		if timeoutSec <= 0 {
			timeoutSec = 300
		}
	}
	slog.Info("compiling typescript", "dir", codeDir, "timeout_sec", timeoutSec)
	// `--no-install` keeps us from quietly fetching tsc when it's missing —
	// npm install above should have placed it. If the operator has air-
	// gapped their box this preserves the failure mode.
	//
	// No network: tsc compiles what npm already put on disk, so this step gets
	// no --user_net and needs no egress policy. It is still jailed, because
	// tsc and everything tsconfig.json pulls in is third-party code.
	out, runErr := b.runBuildStep(ctx, buildStep{
		language: sandbox.Node,
		fnID:     fnID,
		codeDir:  codeDir,
		stream:   "tsc",
		argv:     []string{nodeNpxBin, "--no-install", "tsc", "--project", "tsconfig.json"},
		timeout:  time.Duration(timeoutSec) * time.Second,
	})
	if errors.Is(runErr, sandbox.ErrBuildTimedOut) {
		return entrypoint, fmt.Errorf("tsc timed out after %ds", timeoutSec)
	}
	if runErr != nil {
		// tsc prints diagnostics to stdout (TS6059, TS2322, …) — surface the
		// captured output so the user sees the actual type error in the
		// build log.
		return entrypoint, fmt.Errorf("tsc failed: %w\n%s", runErr, strings.TrimSpace(string(out)))
	}

	outDir := readTSConfigOutDir(tsConfigPath)
	// On a re-deploy `entrypoint` may already carry the `dist/` prefix
	// from the previous build's persisted entrypoint — strip it so we
	// don't double-nest the outDir.
	stem := tsSourceStem(entrypoint, outDir)
	resolved := filepath.Join(outDir, stem+".js")
	compiled := filepath.Join(codeDir, resolved)
	if _, err := os.Stat(compiled); err != nil {
		return entrypoint, fmt.Errorf("tsc succeeded but compiled entrypoint not found at %s: %w", resolved, err)
	}
	// Successful tsc runs silently — surface a single line into build_logs
	// so operators can see "tsc ran" in the deploy progress UI without
	// having to grep for absence-of-error. We use slog as well as the
	// build_log sink so the line shows up regardless of whether a Logger
	// is wired (legacy synchronous deploys, unit tests).
	logTSCCompiled(b, resolved)
	slog.Info("typescript compile complete", "entrypoint", resolved)
	return resolved, nil
}

// logTSCCompiled emits the post-tsc summary line. Split out so the call
// site stays compact and so the always-fires guarantee is in one place:
// (a) we always log to slog, (b) we always append to build_logs when a
// Logger is attached. The previous inline `b != nil && b.Logger != nil`
// guard meant a nil Builder (theoretical) could swallow the line; this
// helper keeps the same nil-safety while making the intent obvious.
func logTSCCompiled(b *Builder, resolved string) {
	line := fmt.Sprintf("compiled to %s", resolved)
	if b != nil && b.Logger != nil {
		b.Logger.Append("tsc", line)
	}
	slog.Info("tsc compiled", "entrypoint", resolved)
}

// verifyTypeScriptDeclared reads package.json and confirms `typescript`
// appears in either dependencies or devDependencies. Anything else (a
// missing package.json, malformed JSON, missing key) is converted into a
// build-time error with a paste-ready hint so users don't have to dig.
func verifyTypeScriptDeclared(pkgJSONPath string) error {
	raw, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return fmt.Errorf("tsconfig.json present but typescript not in package.json — add \"typescript\": \"^5.4\" to your dependencies")
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return fmt.Errorf("tsconfig.json present but package.json is invalid JSON — add \"typescript\": \"^5.4\" to your dependencies")
	}
	if _, ok := pkg.Dependencies["typescript"]; ok {
		return nil
	}
	if _, ok := pkg.DevDependencies["typescript"]; ok {
		return nil
	}
	return fmt.Errorf("tsconfig.json present but typescript not in package.json — add \"typescript\": \"^5.4\" to your dependencies")
}

// readTSConfigOutDir extracts compilerOptions.outDir from a tsconfig.json,
// falling back to "dist" when the file is missing the field, has comments
// (the TS spec allows JSON-with-comments), or is otherwise unparseable. We
// deliberately don't pull in a JSON-with-comments parser — operators who
// need a non-default outDir can supply a comment-free tsconfig, and the
// fallback is safe (the post-compile stat() will fail loudly if outDir
// actually differs from `dist`).
func readTSConfigOutDir(tsConfigPath string) string {
	const fallback = "dist"
	raw, err := os.ReadFile(tsConfigPath)
	if err != nil {
		return fallback
	}
	var cfg struct {
		CompilerOptions struct {
			OutDir string `json:"outDir"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fallback
	}
	out := strings.TrimSpace(cfg.CompilerOptions.OutDir)
	if out == "" {
		return fallback
	}
	// Strip a leading "./" so filepath.Join produces a clean relative path.
	out = strings.TrimPrefix(out, "./")
	if out == "" {
		return fallback
	}
	return out
}

// logLines pipes a captured stdout/stderr blob into the Builder's Logger
// (one call per line). Silently dropped when no Logger is attached, which
// is the case for legacy synchronous deploys via the old API.
func logLines(b *Builder, stream string, out []byte) {
	if b == nil || b.Logger == nil || len(out) == 0 {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		b.Logger.Append(stream, line)
	}
}

// tryFnLock takes the per-function build lock without blocking, mirroring the
// GC. Returns a no-op release and true when no lock source is wired (unit
// tests), since there is no concurrent build to protect against there.
func (b *Builder) tryFnLock(fnID string) (func(), bool) {
	if b.FnLock == nil {
		return func() {}, true
	}
	lk := b.FnLock(fnID)
	if lk == nil || !lk.TryLock() {
		return nil, false
	}
	return lk.Unlock, true
}

// pipPlatformTag is the manylinux wheel platform pip resolves against.
//
// It must track the host architecture. This was hardcoded to
// manylinux2014_x86_64, which combined with --only-binary=:all: meant an
// arm64 host silently installed x86_64 wheels — any package with a compiled
// extension would then fail to import at invoke time, long after the deploy
// reported success.
func pipPlatformTag() string {
	if goruntime.GOARCH == "arm64" {
		return "manylinux2014_aarch64"
	}
	return "manylinux2014_x86_64"
}
