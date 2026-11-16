//go:build linux

package workloadspawn

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestPackagedRuntimeQualification(t *testing.T) {
	if os.Getenv("ORVA_RUNTIME_PACKAGE_TEST") != "1" {
		t.Skip("scratch VM only; see RUNTIME_QUALIFICATION.md")
	}
	runRuntimeQualification(t, false)
}

func TestRuntimeRecipeQualification(t *testing.T) {
	if os.Getenv("ORVA_RUNTIME_RECIPE_TEST") != "1" {
		t.Skip("scratch VM build-time recipe only; see RUNTIME_QUALIFICATION.md")
	}
	runRuntimeQualification(t, true)
}

func runRuntimeQualification(t *testing.T, recipe bool) {
	dir := os.Getenv("ORVA_RUNTIME_PACKAGE_DIR")
	jail := filepath.Join(dir, "nsjail-linux-amd64")
	f, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			qualifyRuntime(t, dir, jail, lang, recipe)
		})
	}
}

func qualifyRuntime(t *testing.T, dir, jail, lang string, recipe bool) {
	t.Helper()
	limits := runtimePackageLimits()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := artifactfs.New(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	stage, err := store.Begin(t.Context(), (1<<30)-4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Abort(); err != nil {
			t.Error(err)
		}
	})
	dest, _ := stage.Path()
	expected := os.Getenv("ORVA_RUNTIME_" + strings.ToUpper(lang) + "_SHA256")
	pkg, err := verifiedRuntimePackage(filepath.Join(dir, "rootfs-"+lang+"-amd64.tar.zst"), expected)
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "zstd", "-dc")
	cmd.Stdin, cmd.Stderr = pkg, os.Stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	m, produceErr := unpackRuntimePackage(dest, pipe, limits)
	if produceErr == nil {
		n, drainErr := io.Copy(io.Discard, io.LimitReader(pipe, (1<<20)+1))
		if drainErr != nil || n > 1<<20 {
			produceErr = errors.New("package trailing-data budget exceeded")
		}
	}
	if produceErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err := pkg.Close(); err != nil {
		t.Fatal(err)
	}
	if produceErr != nil || waitErr != nil {
		t.Fatalf("trusted package production failed: producer=%v decompressor=%v", produceErr, waitErr)
	}
	if recipe {
		m, err = applyRuntimeRecipe(dest, lang, expected, "../../runtimes", runtimeRecipeSourceRevision, m)
		if err != nil {
			t.Fatal(err)
		}
	}
	hash, err := artifactfs.Digest(m, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("authenticated %s package sha256=%s entries=%d manifest=%s", lang, expected, len(m.Entries), hash)
	if recipe {
		output := os.Getenv("ORVA_RUNTIME_RECIPE_OUTPUT")
		if output == "" {
			t.Fatal("missing fresh recipe output parent")
		}
		archiveSHA, err := writeRuntimeRecipeEvidence(filepath.Join(output, lang), dest, lang, expected, m, hash)
		if err != nil {
			t.Fatal(err)
		}
		// Consume the built artifact in a new stage, keeping the producer's
		// independently derived manifest, not authority learned from extraction.
		if err := stage.Abort(); err != nil {
			t.Fatal(err)
		}
		stage, err = store.Begin(t.Context(), (1<<30)-4096)
		if err != nil {
			t.Fatal(err)
		}
		dest, _ = stage.Path()
		archive, err := verifiedRuntimePackage(filepath.Join(output, lang, "runtime.tar"), archiveSHA)
		if err != nil {
			t.Fatal(err)
		}
		observed, produceErr := unpackRuntimePackage(dest, archive, limits)
		closeErr := archive.Close()
		if produceErr != nil || closeErr != nil {
			t.Fatalf("built artifact import: %v %v", produceErr, closeErr)
		}
		observedSHA, err := artifactfs.Digest(observed, limits)
		if err != nil || observedSHA != hash {
			t.Fatal("built artifact differs from trusted producer expectations:", err)
		}
		t.Logf("consuming trusted recipe archive sha256=%s", archiveSHA)
	}
	runtimeID, err := stage.Seal(t.Context(), artifactfs.KindRuntime, lang, m, hash)
	if runtimeID != "" {
		defer reclaimQualified(t, store, runtimeID)
	}
	if err != nil {
		logMissingPackageTargets(t, m)
		t.Fatalf("runtime rejected by strict Seal (recipe=%t): %v", recipe, err)
	}
	owner := ids.New()
	entrypoint, source, lazyName, lazy := qualificationSources(lang)
	codeStage, err := store.Begin(t.Context(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := codeStage.Abort(); err != nil {
			t.Error(err)
		}
	})
	codeRoot, _ := codeStage.Path()
	raw := packageFixture(t, []*tar.Header{
		{Name: ".", Mode: 0755, Typeflag: tar.TypeDir},
		{Name: entrypoint, Mode: 0644, Typeflag: tar.TypeReg, Size: int64(len(source))},
		{Name: lazyName, Mode: 0644, Typeflag: tar.TypeReg, Size: int64(len(lazy))},
	}, []string{"", source, lazy})
	codeManifest, err := unpackRuntimePackage(codeRoot, strings.NewReader(string(raw)), limits)
	if err != nil {
		t.Fatal(err)
	}
	codeHash, err := artifactfs.Digest(codeManifest, limits)
	if err != nil {
		t.Fatal(err)
	}
	codeID, err := codeStage.Seal(t.Context(), artifactfs.KindCode, owner, codeManifest, codeHash)
	if codeID != "" {
		defer reclaimQualified(t, store, codeID)
	}
	if err != nil {
		t.Fatal(err)
	}
	paths, err := artifactfs.PreparePaths(store, codeID, store, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	defer paths.Release()
	codePath, runtimePath := paths.Paths()
	t.Logf("concrete code=%s runtime=%s", codePath, runtimePath)
	// Synthetic owned metadata isolates runtime qualification from DB/IAM claims.
	prepared := &secrets.PreparedArtifactSnapshot{
		Artifact: database.WorkloadArtifactRecord{OwnerID: owner, Generation: ids.New(), Revision: 1, Epoch: 1},
		Function: &database.Function{ID: owner, Name: "runtime-qualification", Runtime: lang,
			Entrypoint: entrypoint, CodeHash: digest(source), Status: "active", Version: 1,
			MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, NetworkMode: "none", SDK: true},
	}
	plan, err := NewPlan(prepared, paths)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	reaped, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(resume) }) }
	defer unblock()
	calls := make(chan sandbox.WorkloadState, 8)
	worker, err := plan.Spawn(t.Context(), sandbox.ExecConfig{
		NsjailBin: jail, CodeDir: "/nonexistent-legacy-code", RootfsDir: "/nonexistent-legacy-runtime",
		SeccompPolicy: sandbox.BuildSeccompPolicy("default", nil, nil),
		OnExit:        func() { close(reaped); <-resume },
		SDKHandler: func(_ context.Context, caller sandbox.SDKCaller, op string, _ json.RawMessage) (any, *sandbox.SDKError) {
			state, ok := caller.WorkloadState()
			if !ok || caller.FunctionID != owner || state.Identity.ArtifactRevision != plan.ArtifactRevision() || op != "kv.get" {
				t.Error("lost host identity/private channel")
			}
			select {
			case calls <- state:
			default:
				t.Error("unexpected excess SDK calls")
			}
			return map[string]any{"value": "private-channel", "found": true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unblock(); _ = worker.Kill() }()
	readyCtx, readyCancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer readyCancel()
	if err := worker.AwaitReady(readyCtx); err != nil {
		t.Fatal(err)
	}
	plan.Close()
	paths.Release()
	for _, id := range []string{codeID, runtimeID} {
		if err := store.Retire(id); err != nil {
			t.Fatal(err)
		}
		if err := store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
			t.Fatal("ready worker lost concrete leases:", err)
		}
	}
	execID := ids.New()
	result, err := worker.DispatchEx(sandbox.WithExecution(readyCtx, sandbox.ExecInfo{ExecutionID: execID}), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Version, SDK, Lazy string }
	if err := json.Unmarshal([]byte(result.Body), &body); err != nil || result.StatusCode != 200 ||
		body.SDK != "private-channel" || body.Lazy != "lazy-file" {
		t.Fatalf("runtime response mismatch: status=%d body=%q err=%v stderr=%s", result.StatusCode, result.Body, err, result.Stderr())
	}
	prefix := sandbox.NodeMajor + "."
	if lang == "python" {
		prefix = sandbox.PythonMinor + "."
	}
	if !strings.HasPrefix(body.Version, prefix) {
		t.Fatalf("unexpected interpreter version: %s", body.Version)
	}
	select {
	case state := <-calls:
		if state.Phase != sandbox.WorkloadExecution || state.ExecutionID != execID || state.Lease == "" {
			t.Fatal("SDK call not bound to host execution")
		}
	default:
		t.Fatal("no private-channel SDK call")
	}
	if err := worker.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reaped:
	case <-readyCtx.Done():
		t.Fatal("no post-Wait callback")
	}
	for _, id := range []string{codeID, runtimeID} {
		if err := store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
			t.Fatal("lease released before post-Wait cleanup:", err)
		}
	}
	unblock()
	t.Logf("qualified %s %s: ready, execution SDK, lazy code/runtime import, post-Wait leases", lang, body.Version)
}

func reclaimQualified(t *testing.T, store *artifactfs.Store, id string) {
	t.Helper()
	if err := store.Retire(id); err != nil {
		t.Error(err)
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := store.Reclaim(id)
		if err == nil {
			return
		}
		if !errors.Is(err, artifactfs.ErrBusy) || time.Now().After(deadline) {
			t.Error("reclaim:", err)
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func logMissingPackageTargets(t *testing.T, m artifactfs.Manifest) {
	t.Helper()
	entries := map[string]artifactfs.Entry{}
	for _, e := range m.Entries {
		entries[e.Path] = e
	}
	missing := 0
	for _, e := range m.Entries {
		if e.Type != artifactfs.TypeSymlink {
			continue
		}
		target := strings.TrimPrefix(e.LinkTarget, "/")
		if !path.IsAbs(e.LinkTarget) {
			target = path.Join(path.Dir(e.Path), e.LinkTarget)
		}
		if target == "" || target == "." || target != path.Clean(target) || target == ".." || strings.HasPrefix(target, "../") {
			continue
		}
		if _, exists := entries[target]; exists {
			continue
		}
		// Only report proven missing direct targets, not symlink-parent guesses.
		parent := path.Dir(target)
		for parent != "." && entries[parent].Type == artifactfs.TypeDirectory {
			parent = path.Dir(parent)
		}
		if parent == "." {
			missing++
			if missing <= 16 {
				t.Logf("packaged dangling link: %s -> %s", e.Path, e.LinkTarget)
			}
		}
	}
	t.Logf("proven missing direct package targets: %d (at most 16 shown)", missing)
}

func qualificationSources(lang string) (string, string, string, string) {
	if lang == "node" {
		return "handler.js", `const {kv} = require('orva');
exports.handler = async () => ({statusCode:200, body:JSON.stringify({
version:process.versions.node, sdk:await kv.get('qualification'), lazy:require('./lazy.js')()})});
`, "lazy.js", `module.exports = () => {
if (require('/usr/local/lib/node_modules/npm/package.json').name !== 'npm') throw Error('runtime lazy import');
return 'lazy-file';
};`
	}
	return "handler.py", `import sys
from orva import kv
def handler(event):
    import lazy
    return {"statusCode": 200, "body": __import__("json").dumps({
        "version": sys.version.split()[0], "sdk": kv.get("qualification"), "lazy": lazy.load()})}
`, "lazy.py", `def load():
    import fractions
    assert fractions.Fraction(1, 2) + fractions.Fraction(1, 2) == 1
    return "lazy-file"
`
}
