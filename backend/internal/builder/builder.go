package builder

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
)

// BuildResult holds the output of a successful build.
type BuildResult struct {
	ImageTag   string        `json:"image_tag"`
	ImageSize  int64         `json:"image_size"`
	Duration   time.Duration `json:"duration"`
	CodeHash   string        `json:"code_hash"`
	VersionDir string        `json:"version_dir"` // absolute path to versions/<hash>; activate sets `current` to point at this
	Cached     bool          `json:"cached"`      // true when an identical-hash version already existed and we skipped extract+install
}

// ErrInsufficientDisk is returned by Build when the data directory has
// less free space than system_config.min_free_disk_mb. The handler maps
// this to HTTP 503 with code INSUFFICIENT_DISK.
var ErrInsufficientDisk = errors.New("insufficient free disk space for deploy")

// Builder handles building function code for nsjail execution.
type Builder struct {
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
}

// New creates a new Builder.
func New() *Builder {
	return &Builder{}
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
	if _, err := os.Stat(filepath.Join(versionDir, ".orva-ready")); err == nil {
		slog.Info("build cache hit", "fn", fn.ID, "hash", codeHash[:12])
		return &BuildResult{
			ImageTag:   imageTag,
			Duration:   time.Since(start),
			CodeHash:   codeHash,
			VersionDir: versionDir,
			Cached:     true,
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
	if err := b.installDependencies(ctx, scratchDir, fn.Runtime); err != nil {
		return nil, fmt.Errorf("install dependencies: %w", err)
	}
	if err := installAdapter(scratchDir, fn.Runtime, fn.Entrypoint); err != nil {
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
	}, nil
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

// isNodeRuntime / isPythonRuntime / pythonVersion are thin helpers so
// version bumps only need to add new strings in one place. Latest two
// stable LTS / stable only.
func isNodeRuntime(r string) bool   { return r == "node22" || r == "node24" }
func isPythonRuntime(r string) bool { return r == "python313" || r == "python314" }

// pythonVersionFor returns the pip --python-version flag value for the
// runtime. Used so wheels resolve for the right interpreter.
func pythonVersionFor(r string) string {
	switch r {
	case "python313":
		return "3.13"
	case "python314":
		return "3.14"
	default:
		return "3.13" // fallback — should never hit because validation rejects it
	}
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
// available at /code/<pkgs> inside the nsjail sandbox.
//
//   - node22: if package.json is present, runs `npm install --prefix <codeDir>`.
//     node_modules/ lands at /code/node_modules and require() finds it automatically.
//
//   - python313: if requirements.txt is present, runs `pip install -t <codeDir>`.
//     Packages land at /code/<pkg> and the Python adapter adds /code to sys.path.
//
// Both commands run on the host (not inside nsjail) during the build phase.
func (b *Builder) installDependencies(ctx context.Context, codeDir, runtime string) error {
	switch {
	case isNodeRuntime(runtime):
		pkgJSON := filepath.Join(codeDir, "package.json")
		if _, err := os.Stat(pkgJSON); os.IsNotExist(err) {
			return nil // no deps file → nothing to install
		}
		slog.Info("installing node dependencies", "dir", codeDir)
		cmd := exec.CommandContext(ctx, "npm", "install", "--prefix", codeDir, "--no-audit", "--no-fund")
		cmd.Dir = codeDir
		out, err := cmd.CombinedOutput()
		logLines(b, "npm", out)
		if err != nil {
			return fmt.Errorf("npm install failed: %w\n%s", err, string(out))
		}
		slog.Info("npm install complete", "output", strings.TrimSpace(string(out)))

	case isPythonRuntime(runtime):
		reqTxt := filepath.Join(codeDir, "requirements.txt")
		if _, err := os.Stat(reqTxt); os.IsNotExist(err) {
			return nil
		}
		slog.Info("installing python dependencies", "dir", codeDir, "runtime", runtime)
		// Cross-install wheels for the sandbox's Python version (not the
		// host's). --only-binary=:all: forces wheels so we never execute
		// setup.py with the wrong interpreter.
		pyVer := pythonVersionFor(runtime)
		baseArgs := []string{
			"install", "-r", reqTxt, "-t", codeDir,
			"--python-version", pyVer,
			"--platform", "manylinux2014_x86_64",
			"--implementation", "cp",
			"--only-binary=:all:",
			"--quiet",
			// Suppress pip's "Running pip as the 'root' user" warning.
			// We're inside a single-tenant container — there's no
			// alternative user to switch to and the warning was the
			// only line showing up in the deploy progress UI.
			"--root-user-action=ignore",
		}
		cmd := exec.CommandContext(ctx, "pip", baseArgs...)
		cmd.Env = append(os.Environ(), "PIP_DISABLE_PIP_VERSION_CHECK=1")
		cmd.Dir = codeDir
		out, err := cmd.CombinedOutput()
		logLines(b, "pip", out)
		if err != nil {
			return fmt.Errorf("pip install failed: %w\n%s", err, string(out))
		}
		slog.Info("pip install complete")
	}
	return nil
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
