//go:build linux

package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func builderRuntime(t *testing.T, kind artifactfs.Kind, language, stamp string) (*artifactfs.Store, string, string) {
	t.Helper()
	limits := artifactfs.Limits{Generations: 4, Leases: 8, Staging: 2, Entries: 32, PathBytes: 512,
		LinkBytes: 512, ManifestBytes: 1 << 20, Bytes: 1 << 16, FileBytes: 4096}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := artifactfs.New(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := s.Begin(t.Context(), 8192)
	if err != nil {
		t.Fatal(err)
	}
	path, err := stage.Path()
	if err != nil {
		t.Fatal(err)
	}
	m := artifactfs.Manifest{Version: 1}
	add := func(name string, mode uint32, typ artifactfs.EntryType, data string) {
		e := artifactfs.Entry{Path: name, Mode: mode, Type: typ, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
		if typ == artifactfs.TypeFile {
			sum := sha256.Sum256([]byte(data))
			e.SHA256, e.Size = hex.EncodeToString(sum[:]), int64(len(data))
			if err := os.WriteFile(filepath.Join(path, name), []byte(data), os.FileMode(mode)); err != nil {
				t.Fatal(err)
			}
		} else if name != "." {
			if err := os.Mkdir(filepath.Join(path, name), os.FileMode(mode)); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(filepath.Join(path, name), os.FileMode(mode)); err != nil {
			t.Fatal(err)
		}
		m.Entries = append(m.Entries, e)
	}
	add(".", 0700, artifactfs.TypeDirectory, "")
	for _, dir := range []string{"usr", "usr/local", "usr/local/bin"} {
		add(dir, 0755, artifactfs.TypeDirectory, "")
	}
	for _, bin := range []string{"node", "python3", "npm", "pip", "npx"} {
		add("usr/local/bin/"+bin, 0755, artifactfs.TypeFile, "fixture executable")
	}
	add(RuntimeStampFile, 0644, artifactfs.TypeFile, stamp)
	hash, err := artifactfs.Digest(m, limits)
	if err != nil {
		t.Fatal(err)
	}
	owner := language
	if kind == artifactfs.KindCode {
		owner = ids.New()
	}
	id, err := stage.Seal(t.Context(), kind, owner, m, hash)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	sealedPath := lease.Path()
	lease.Release()
	t.Cleanup(func() {
		if err := s.Retire(id); err != nil {
			t.Error(err)
		}
		if err := s.Reclaim(id); err != nil {
			t.Error(err)
		}
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, id, sealedPath
}

type runtimeLog func(string, string)

func (f runtimeLog) Append(stream, line string) { f(stream, line) }

func builderFakeJail(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "jail")
	const script = `#!/bin/sh
while [ "$#" -gt 0 ]; do
 case "$1" in
 --chroot) root="$2"; shift 2;;
 -B) case "$2" in *:/code) code="${2%:/code}";; esac; shift 2;;
 --) shift; break;;
 *) shift;;
 esac
done
printf 'PIN %s %s %s\n' "$root" "$(cat "$root/.orva-runtime")" "$1"
case "$1" in */npx) mkdir -p "$code/dist"; printf 'compiled' > "$code/dist/handler.js";; esac
`
	if err := os.WriteFile(p, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildRuntimeCapturedAcrossActualSteps(t *testing.T) {
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			s, id, path := builderRuntime(t, artifactfs.KindRuntime, lang, lang+" pinned-abi")
			files := map[string]string{"handler.js": "exports.handler = () => 1", "package.json": `{"dependencies":{"typescript":"fixture"}}`,
				"tsconfig.json": `{"compilerOptions":{"outDir":"dist"}}`}
			entry := "handler.js"
			wantSteps := 3
			if lang == "python" {
				files = map[string]string{"handler.py": "def handler(e): return 1", "requirements.txt": "fixture"}
				entry, wantSteps = "handler.py", 2
			}
			archive := createTestArchive(t, files)
			fn := &database.Function{ID: ids.New(), Name: "original", Runtime: lang, Entrypoint: entry,
				EnvVars: map[string]string{"owned": "original"}}
			originalID := fn.ID
			calls, steps := 0, 0
			var b *Builder
			b = New(WithRuntimeBinding(func(_ context.Context, req RuntimeRequest) (RuntimeBinding, error) {
				calls++
				if req.FunctionID != originalID || string(req.Language) != lang {
					t.Fatal("wrong captured request")
				}
				fn.ID, fn.Runtime, fn.Entrypoint = ids.New(), "unsupported", "../bad"
				fn.EnvVars["owned"] = "changed"
				b.DataDir, b.NsjailBin = "/must-not-use", "/must-not-run"
				return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s, Generation: id}}, nil
			}))
			dataDir := t.TempDir()
			b.DataDir, b.NsjailBin, b.RootfsDir = dataDir, builderFakeJail(t), "/missing-legacy-root"
			policy := filepath.Join(t.TempDir(), "policy")
			if err := os.WriteFile(policy, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			b.EgressPolicy = func() (string, string, error) { return policy, "test", nil }
			b.Logger = runtimeLog(func(_, line string) {
				if !strings.HasPrefix(line, "PIN ") {
					return
				}
				steps++
				if !strings.Contains(line, path+" "+lang+" pinned-abi ") {
					t.Error("step mixed runtime:", line)
				}
				b.RootfsDir = "/changed-between-steps"
				b.runtimeProvider = func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
					t.Error("provider refreshed")
					return RuntimeBinding{}, errors.New("refreshed")
				}
				if err := s.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
					t.Error("missing pipeline anchor", err)
				}
			})
			result, err := b.Build(t.Context(), fn, archive)
			if err != nil {
				t.Fatal(err)
			}
			hash, _ := hashFile(archive)
			if calls != 1 || steps != wantSteps || result.RuntimeArtifact.Generation != id ||
				result.RuntimeArtifact.Kind != artifactfs.KindRuntime || result.RuntimeArtifact.Digest == "" ||
				result.RuntimeABI != lang+" pinned-abi" || result.CodeHash != versionHash(hash, lang+" pinned-abi") ||
				!strings.HasPrefix(result.VersionDir, filepath.Join(dataDir, "functions", originalID)+"/") {
				t.Fatalf("mixed build capture: calls=%d steps=%d result=%+v", calls, steps, result)
			}
			// The existing ready marker has no full-runtime provenance and must not be overwritten.
			b.DataDir, b.NsjailBin = dataDir, builderFakeJail(t)
			b.runtimeProvider = func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
				return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s, Generation: id}}, nil
			}
			_, err = b.Build(t.Context(), &database.Function{ID: originalID, Name: "original", Runtime: lang, Entrypoint: entry}, archive)
			if err == nil || !strings.Contains(err.Error(), "cached output") {
				t.Fatal("unproven cache hit accepted", err)
			}
			if _, err := os.Stat(filepath.Join(result.VersionDir, ".orva-ready")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBuildRuntimeDenials(t *testing.T) {
	s, id, _ := builderRuntime(t, artifactfs.KindRuntime, "node", "node fixture")
	codeStore, codeID, _ := builderRuntime(t, artifactfs.KindCode, "node", "not-runtime")
	fn := &database.Function{ID: ids.New(), Name: "test", Runtime: "node", Entrypoint: "handler.js"}
	archive := createTestArchive(t, map[string]string{"handler.js": "exports.handler = () => 1"})
	for _, tc := range []struct {
		name     string
		provider RuntimeProvider
	}{
		{"nil", nil},
		{"declined", func(context.Context, RuntimeRequest) (RuntimeBinding, error) { return RuntimeBinding{}, nil }},
		{"error", func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
			return RuntimeBinding{}, fmt.Errorf("provider denied")
		}},
		{"missing-store", func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
			return RuntimeBinding{Runtime: sandbox.BuildRuntime{Generation: id}}, nil
		}},
		{"missing-id", func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
			return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s}}, nil
		}},
		{"unknown-id", func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
			return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s, Generation: ids.New()}}, nil
		}},
		{"wrong-kind", func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
			return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: codeStore, Generation: codeID}}, nil
		}},
		{"invalid-domain", func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
			return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s, Generation: id}, Domain: &workloadfence.Domain{}}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New(WithRuntimeBinding(tc.provider))
			b.DataDir, b.NsjailBin = t.TempDir(), builderFakeJail(t)
			if result, err := b.Build(t.Context(), fn, archive); err == nil || result != nil {
				t.Fatal("binding denial bypassed")
			}
		})
	}
	b := New(WithRuntimeBinding(func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
		return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s, Generation: id}}, nil
	}))
	b.DataDir = t.TempDir()
	wrong := fn.Clone()
	wrong.Runtime = "python"
	if _, err := b.Build(t.Context(), wrong, archive); err == nil {
		t.Fatal("wrong runtime language accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := b.Build(ctx, fn, archive); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost:", err)
	}
}

func TestBuildRuntimeRetirementBetweenSteps(t *testing.T) {
	s, id, _ := builderRuntime(t, artifactfs.KindRuntime, "node", "node fixture")
	b := New(WithRuntimeBinding(func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
		return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s, Generation: id}}, nil
	}))
	b.DataDir, b.NsjailBin = t.TempDir(), builderFakeJail(t)
	policy := filepath.Join(t.TempDir(), "policy")
	if err := os.WriteFile(policy, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	b.EgressPolicy = func() (string, string, error) { return policy, "test", nil }
	b.Logger = runtimeLog(func(_, line string) {
		if strings.HasPrefix(line, "PIN ") {
			if err := s.Retire(id); err != nil {
				t.Error(err)
			}
			if err := s.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
				t.Error("anchor dropped between commands:", err)
			}
		}
	})
	archive := createTestArchive(t, map[string]string{"handler.js": "exports.handler = () => 1", "package.json": `{}`})
	result, err := b.Build(t.Context(), &database.Function{ID: ids.New(), Name: "test", Runtime: "node", Entrypoint: "handler.js"}, archive)
	if result != nil || !errors.Is(err, artifactfs.ErrRetired) {
		t.Fatal("retired runtime switched/fell back:", err)
	}
}

func TestBuildRuntimeCancelDuringCommand(t *testing.T) {
	s, id, _ := builderRuntime(t, artifactfs.KindRuntime, "node", "node fixture")
	b := New(WithRuntimeBinding(func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
		return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s, Generation: id}}, nil
	}))
	b.DataDir = t.TempDir()
	entered := filepath.Join(t.TempDir(), "entered")
	jail := filepath.Join(t.TempDir(), "jail")
	if err := os.WriteFile(jail, []byte(fmt.Sprintf("#!/bin/sh\nprintf started > %q\nexec sleep 60\n", entered)), 0755); err != nil {
		t.Fatal(err)
	}
	b.NsjailBin = jail
	archive := createTestArchive(t, map[string]string{"handler.js": "exports.handler = () => 1"})
	fn := &database.Function{ID: ids.New(), Name: "cancel", Runtime: "node", Entrypoint: "handler.js"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := b.Build(ctx, fn, archive)
		if result != nil {
			done <- errors.New("canceled build returned result")
			return
		}
		done <- err
	}()
	deadline := time.After(10 * time.Second)
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatal("command did not start:", err)
		case <-deadline:
			t.Fatal("start timeout")
		case <-time.After(time.Millisecond):
		}
	}
	if err := s.Retire(id); err != nil {
		t.Fatal(err)
	}
	if err := s.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
		t.Fatal("active pipeline lost leases:", err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled build succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("canceled build did not reap")
	}
	if _, err := s.Acquire(id); !errors.Is(err, artifactfs.ErrRetired) {
		t.Fatal("retired binding revived:", err)
	}
	// Fixture cleanup reclaims the generation; any leaked command or anchor lease fails it.
}

func TestBuildRuntimePinnedStampBounds(t *testing.T) {
	for _, stamp := range []string{"", "  node fixture\n", strings.Repeat("a", 1025)} {
		t.Run(fmt.Sprint(len(stamp)), func(t *testing.T) {
			_, _, path := builderRuntime(t, artifactfs.KindRuntime, "node", stamp)
			abi, err := pinnedRuntimeABI(path, "node")
			if len(stamp) > 1024 {
				if err == nil {
					t.Fatal("unbounded stamp accepted")
				}
			} else if err != nil || (stamp == "" && abi != "node unstamped") || (stamp != "" && abi != "node fixture") {
				t.Fatal("pinned ABI:", abi, err)
			}
		})
	}
}

func TestBuildRuntimeCapturesDomain(t *testing.T) {
	s, id, _ := builderRuntime(t, artifactfs.KindRuntime, "node", "node fixture")
	domain := &workloadfence.Domain{}
	b := New(WithRuntimeBinding(func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
		return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: s, Generation: id}, Domain: domain}, nil
	}))
	fn := &database.Function{ID: ids.New(), Runtime: "node"}
	lease, _, err := b.captureRuntime(t.Context(), fn)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	for _, bin := range []string{"/usr/local/bin/node", nodeNpmBin, nodeNpxBin} {
		cfg := b.buildConfigFor(buildStep{language: sandbox.Node, argv: []string{bin}, env: map[string]string{"STEP": "owned"}})
		if cfg.Domain != domain || cfg.Runtime.Store != s || cfg.Runtime.Generation != id ||
			cfg.Env["STEP"] != "owned" {
			t.Fatal("step lost captured domain/runtime or step environment")
		}
	}
}

func TestBuildRuntimeNilLegacy(t *testing.T) {
	b := New()
	b.DataDir, b.NsjailBin, b.RootfsDir = t.TempDir(), builderFakeJail(t), t.TempDir()
	root := filepath.Join(b.RootfsDir, "node")
	if err := os.MkdirAll(filepath.Join(root, "usr", "local", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "local", "bin", "node"), []byte("fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	stamp := filepath.Join(root, RuntimeStampFile)
	if err := os.WriteFile(stamp, []byte("node legacy-a"), 0644); err != nil {
		t.Fatal(err)
	}
	fn := &database.Function{ID: ids.New(), Name: "legacy", Runtime: "node", Entrypoint: "handler.js"}
	archive := createTestArchive(t, map[string]string{"handler.js": "exports.handler = () => 1"})
	first, err := b.Build(t.Context(), fn, archive)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Build(t.Context(), fn, archive)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached || second.CodeHash != first.CodeHash || first.RuntimeArtifact != (artifactfs.Identity{}) || first.RuntimeABI != "" {
		t.Fatal("nil legacy cache/metadata behavior changed")
	}
	if err := os.WriteFile(stamp, []byte("node legacy-b"), 0644); err != nil {
		t.Fatal(err)
	}
	third, err := b.Build(t.Context(), fn, archive)
	if err != nil {
		t.Fatal(err)
	}
	if third.Cached || third.CodeHash == first.CodeHash {
		t.Fatal("nil legacy no longer observes mutable stamp")
	}
}
