//go:build linux

package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedOutputDeniesMissingOwner(t *testing.T) {
	b := New(WithManagedOutput(nil))
	if result, err := b.Build(t.Context(), &database.Function{}, "/not-opened"); err == nil || result != nil {
		t.Fatal("missing owner bypassed")
	}
	if _, err := NewOutputOwner(OutputConfig{}); err == nil {
		t.Fatal("unbounded config accepted")
	}
	if err := (&OutputOwner{}).RetryCleanup(t.Context()); err == nil {
		t.Fatal("unowned cleanup accepted")
	}
	if err := (&OutputOwner{}).Close(); err == nil {
		t.Fatal("unowned Close accepted")
	}
	if _, _, err := (&outputJob{}).produce(t.Context()); err == nil {
		t.Fatal("unowned/fenceless producer accepted")
	}
}

func TestManagedOutputExtractionBounds(t *testing.T) {
	for _, which := range []string{"file", "total", "entries", "path", "archive", "canceled"} {
		t.Run(which, func(t *testing.T) {
			s, _, limits := outputTestStore(t)
			cfg := OutputConfig{CodeStore: s, Limits: limits, ReservedBytes: 1024, ArchiveBytes: 1 << 20}
			switch which {
			case "file":
				cfg.Limits.FileBytes = 1
			case "total":
				cfg.ReservedBytes = 1
			case "entries":
				cfg.Limits.Entries = 1
			case "path":
				cfg.Limits.PathBytes = 3
			case "archive":
				cfg.ArchiveBytes = 1
			}
			stage, err := s.Begin(t.Context(), cfg.ReservedBytes)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stage.Abort(); err != nil {
					t.Error(err)
				}
			}()
			path, err := stage.Path()
			if err != nil {
				t.Fatal(err)
			}
			cfg.MetadataRoot = t.TempDir()
			j := &outputJob{owner: &outputState{cfg: cfg}, stage: stage, path: path}
			archive := createTestArchive(t, map[string]string{"handler.js": "plain text"})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if which == "canceled" {
				cancel()
			}
			if _, err := j.extract(ctx, archive); err == nil {
				t.Fatal("source bound ignored:", which)
			}
			if _, _, err := j.produce(t.Context()); err == nil {
				t.Fatal("unfenced stage admitted to manifest production")
			}
		})
	}
}

func TestAttachSourceVerifiesExtractedArchive(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			const input = "original archive"
			j := &outputJob{owner: &outputState{cfg: OutputConfig{Limits: artifactfs.Limits{FileBytes: 1024}}},
				path: t.TempDir(), source: filepath.Join(t.TempDir(), "archive")}
			body := input
			if changed {
				body = "modified archive"
			}
			if err := os.WriteFile(j.source, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			err := j.attachSource(t.Context(), fmt.Sprintf("%x", sha256.Sum256([]byte(input))))
			if changed {
				if !errors.Is(err, artifactfs.ErrIntegrity) {
					t.Fatalf("attached changed input: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(j.path, artifactfs.BuildSourceName))
			if err != nil || string(got) != input {
				t.Fatalf("attached archive: %q, %v", got, err)
			}
		})
	}
}

func TestManagedOutputArchiveTypeConflict(t *testing.T) {
	s, _, limits := outputTestStore(t)
	cfg := OutputConfig{CodeStore: s, Limits: limits, ReservedBytes: 1024, ArchiveBytes: 1 << 20}
	stage, err := s.Begin(t.Context(), cfg.ReservedBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stage.Abort(); err != nil {
			t.Error(err)
		}
	}()
	path, err := stage.Path()
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for _, h := range []*tar.Header{{Name: "same", Typeflag: tar.TypeReg, Mode: 0644}, {Name: "same", Typeflag: tar.TypeDir, Mode: 0755}} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "duplicate.tar.gz")
	if err := os.WriteFile(archive, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.MetadataRoot = t.TempDir()
	j := &outputJob{owner: &outputState{cfg: cfg}, stage: stage, path: path}
	if _, err := j.extract(t.Context(), archive); !errors.Is(err, artifactfs.ErrUnsupported) {
		t.Fatal("archive metadata conflict rewritten:", err)
	}
}

func TestManagedOutputRejectsRawParentComponents(t *testing.T) {
	for _, name := range []string{"../handler.js", "foo/../handler.js", "foo/bar/../../handler.js", "./foo/../handler.js", "foo/..//handler.js"} {
		t.Run(name, func(t *testing.T) {
			s, _, limits := outputTestStore(t)
			stage, err := s.Begin(t.Context(), 1024)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stage.Abort(); err != nil {
					t.Error(err)
				}
			}()
			path, err := stage.Path()
			if err != nil {
				t.Fatal(err)
			}
			j := &outputJob{owner: &outputState{cfg: OutputConfig{Limits: limits, ReservedBytes: 1024, ArchiveBytes: 1 << 20,
				MetadataRoot: t.TempDir()}}, stage: stage, path: path}
			if _, err := j.extract(t.Context(), createTestArchive(t, map[string]string{name: "plain"})); !errors.Is(err, artifactfs.ErrUnsupported) {
				t.Fatalf("raw parent component normalized rather than rejected: %v", err)
			}
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected header created staging entries: count=%d, %v", len(entries), err)
			}
		})
	}
}

func outputTestBudget(t *testing.T) (*workloadfence.Budget, string) {
	t.Helper()
	if os.Getenv("ORVA_OUTPUT_KERNEL_TEST") != "disposable-vm" {
		t.Skip("requires explicitly provisioned disposable guest budget")
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	rel := strings.TrimSpace(strings.TrimPrefix(string(raw), "0::"))
	base := "/sys/fs/cgroup" + rel
	if base != os.Getenv("ORVA_OUTPUT_TEST_BUDGET") || base == "/sys/fs/cgroup" {
		t.Fatal("not in explicit owned test budget")
	}
	budget, err := workloadfence.CaptureBudget()
	if err != nil {
		t.Fatal(err)
	}
	daemon, parent := filepath.Join(base, "daemon"), filepath.Join(base, "domains")
	for _, p := range []string{daemon, parent} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(daemon, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	remaining, err := os.ReadFile(filepath.Join(base, "cgroup.procs"))
	if err != nil || strings.TrimSpace(string(remaining)) != "" {
		t.Fatal("fixture refuses to move other processes")
	}
	for _, p := range []string{base, parent} {
		if err := os.WriteFile(filepath.Join(p, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		entries, err := os.ReadDir(parent)
		if err != nil {
			t.Error(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				t.Errorf("unremoved domain parent: %s", e.Name())
			}
		}
		if err := os.Remove(parent); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(filepath.Join(base, "cgroup.subtree_control"), []byte("-cpu -memory -pids"), 0600); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(filepath.Join(base, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Error(err)
		}
		if err := os.Remove(daemon); err != nil {
			t.Error(err)
		}
		if err := budget.Close(); err != nil {
			t.Error(err)
		}
	})
	return budget, parent
}

func outputTestStore(t *testing.T) (*artifactfs.Store, string, artifactfs.Limits) {
	t.Helper()
	l := artifactfs.Limits{Generations: 32, Leases: 32, Staging: 8, Entries: 64, PathBytes: 512, LinkBytes: 512, ManifestBytes: 1 << 20, Bytes: 2 << 20, FileBytes: 1 << 16}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := artifactfs.New(root, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, root, l
}

func outputTestConfig(t *testing.T, budget *workloadfence.Budget, parent string, s *artifactfs.Store, l artifactfs.Limits) OutputConfig {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return OutputConfig{CodeStore: s, Budget: budget, Parent: parent, MetadataRoot: root, Limits: l,
		ReservedBytes: 1 << 16, MaxReservedBytes: 1 << 16, ArchiveBytes: 1 << 20, MaxBuilds: 1, BuildTimeout: 5 * time.Second, CleanupTimeout: time.Second}
}

func outputTestJail(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "jail")
	if err := os.WriteFile(p, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestOutputJailChild$ -- \"$@\"\n", exe)), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OUTPUT_CHILD", "1")
	return p
}

func TestOutputJailChild(t *testing.T) {
	if os.Getenv("OUTPUT_CHILD") != "1" {
		return
	}
	mode := os.Getenv("OUTPUT_CHILD_MODE")
	event := os.Getenv("OUTPUT_EVENT")
	code := os.Getenv("OUTPUT_CODE")
	mount := ""
	for i, arg := range os.Args {
		if arg == "-B" && i+1 < len(os.Args) && strings.HasSuffix(os.Args[i+1], ":/code") {
			code = strings.TrimSuffix(os.Args[i+1], ":/code")
		}
		if arg == "--cgroupv2_mount" && i+1 < len(os.Args) {
			mount = os.Args[i+1]
		}
	}
	membership, _ := os.ReadFile("/proc/self/cgroup")
	if event != "" {
		suffix := ".membership"
		if mode == "grandchild" {
			suffix = ".grandchild.membership"
		}
		_ = os.WriteFile(event+suffix, membership, 0600)
		if mount != "" {
			_ = os.WriteFile(event+".mount", []byte(mount), 0600)
		}
	}
	if mode == "grandchild" {
		_ = os.WriteFile(event+".grandchild", []byte(strconv.Itoa(os.Getpid())), 0600)
		time.Sleep(2 * time.Second)
		_ = os.WriteFile(filepath.Join(code, "late-write"), []byte("must not appear after fence"), 0600)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if mode == "delayed" || mode == "fail" || mode == "cancel" || mode == "quarantine" {
		child := exec.Command(os.Args[0], "-test.run=^TestOutputJailChild$")
		child.Env = append(os.Environ(), "OUTPUT_CHILD_MODE=grandchild", "OUTPUT_CODE="+code)
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		for i := 0; i < 5000; i++ {
			if _, err := os.Stat(event + ".grandchild"); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	if mode == "link" {
		_ = os.Mkdir(filepath.Join(code, "bin"), 0755)
		_ = os.Symlink("../handler.js", filepath.Join(code, "bin", "cmd"))
	}
	if mode == "xattr" {
		_ = syscallSetOutputXattr(filepath.Join(code, "handler.js"))
	}
	if event != "" {
		_ = os.WriteFile(event, []byte(code), 0600)
	}
	fmt.Println("OUTPUT CHILD FINISHED")
	if mode == "cancel" {
		time.Sleep(time.Minute)
	}
	if mode == "fail" {
		os.Exit(9)
	}
	os.Exit(0)
}

func awaitOutputFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			return string(raw)
		}
		select {
		case <-deadline:
			t.Fatal("fixture did not become ready:", path)
		case <-time.After(time.Millisecond):
		}
	}
}
func outputChildDead(t *testing.T, path string) {
	t.Helper()
	pid := strings.TrimSpace(awaitOutputFile(t, path+".grandchild"))
	raw, err := os.ReadFile("/proc/" + pid + "/stat")
	if err == nil {
		tail := raw[strings.LastIndex(string(raw), ")")+1:]
		if !strings.HasPrefix(strings.TrimSpace(string(tail)), "Z") {
			t.Fatalf("descendant still live: %s", pid)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestManagedOutputKernel(t *testing.T) {
	budget, parent := outputTestBudget(t)
	for _, mode := range []string{"normal", "capture", "delayed", "fail", "cancel", "quarantine", "link", "xattr", "capacity", "profiles", "bindings"} {
		t.Run(mode, func(t *testing.T) {
			s, storeRoot, l := outputTestStore(t)
			rs, rid, _ := builderRuntime(t, artifactfs.KindRuntime, "node", "node fixture")
			cfg := outputTestConfig(t, budget, parent, s, l)
			owner, err := NewOutputOwner(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := owner.Close(); err != nil {
					t.Error(err)
				}
			})
			b := New(WithRuntimeBinding(func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
				return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: rs, Generation: rid}}, nil
			}), WithManagedOutput(owner))
			b.DataDir, b.RootfsDir, b.NsjailBin = t.TempDir(), "/never-legacy", outputTestJail(t)
			event := filepath.Join(t.TempDir(), "event")
			t.Setenv("OUTPUT_EVENT", event)
			actualMode := mode
			if mode == "capacity" {
				actualMode = "cancel"
			}
			t.Setenv("OUTPUT_CHILD_MODE", actualMode)
			fn := &database.Function{ID: ids.New(), Name: "managed", Runtime: "node", Entrypoint: "handler.js"}
			originalID := fn.ID
			files := map[string]string{"handler.js": "exports.handler = () => 42"}
			archive := createTestArchive(t, files)
			// A legacy ready tree is not inspected, replaced or adopted.
			hash, _ := hashFile(archive)
			legacy := filepath.Join(b.DataDir, "functions", fn.ID, "versions", versionHash(hash, "node fixture"))
			if err := os.MkdirAll(legacy, 0755); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(legacy, ".orva-ready")
			if err := os.WriteFile(sentinel, []byte("legacy sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			var saved, record string
			if mode == "capture" {
				b.runtimeProvider = func(_ context.Context, req RuntimeRequest) (RuntimeBinding, error) {
					if req.FunctionID != originalID || req.Language != sandbox.Node {
						t.Fatal("managed identity not captured")
					}
					fn.ID, fn.Runtime, fn.Entrypoint = ids.New(), "python", "../changed"
					b.DataDir, b.RootfsDir, b.NsjailBin = "/changed", "/changed", "/changed"
					b.outputOwner, b.runtimeProvider = nil, nil
					return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: rs, Generation: rid}}, nil
				}
			}
			if mode == "quarantine" {
				b.Logger = runtimeLog(func(_, line string) {
					if strings.Contains(line, "OUTPUT CHILD FINISHED") {
						dirs, _ := os.ReadDir(cfg.MetadataRoot)
						for _, d := range dirs {
							if d.IsDir() {
								record = filepath.Join(cfg.MetadataRoot, d.Name(), "epoch.json")
								saved = record + ".saved"
								if err := os.Rename(record, saved); err != nil {
									t.Error(err)
								}
							}
						}
					}
				})
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var result *BuildResult
			if mode == "cancel" || mode == "capacity" {
				done := make(chan error, 1)
				go func() { _, e := b.Build(ctx, fn, archive); done <- e }()
				awaitOutputFile(t, event)
				if mode == "capacity" {
					if _, e := b.Build(t.Context(), fn, archive); !errors.Is(e, ErrOutputCapacity) {
						t.Fatal("parallel capacity bypass:", e)
					}
				}
				cancel()
				select {
				case err = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("canceled build did not fence")
				}
			} else if mode == "profiles" {
				for _, name := range []string{"requirements.txt", "tsconfig.json"} {
					a := createTestArchive(t, map[string]string{"handler.js": "exports.handler = () => 42", name: "{}"})
					if _, e := b.Build(t.Context(), fn, a); !errors.Is(e, ErrOutputProfile) {
						t.Fatal("unsupported build profile admitted:", name, e)
					}
				}
				a := createTestArchive(t, map[string]string{"handler.js": "exports.handler = () => 42", "package.json": "{}"})
				if _, e := b.Build(t.Context(), fn, a); !errors.Is(e, sandbox.ErrEgressPolicyMissing) {
					t.Fatal("installer did not fail closed:", e)
				}
				b.BuildFunc = func(context.Context, string, string, string) (int64, error) {
					t.Fatal("host hook invoked")
					return 0, nil
				}
				if _, e := b.Build(t.Context(), fn, archive); e == nil {
					t.Fatal("host build hook admitted")
				}
				b.BuildFunc = nil
				return
			} else if mode == "bindings" {
				original := b.runtimeProvider
				for _, which := range []string{"domain", "wrong-runtime", "provider-error"} {
					b.runtimeProvider = func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
						if which == "provider-error" {
							return RuntimeBinding{}, errors.New("host declined")
						}
						binding := RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: rs, Generation: rid}}
						if which == "domain" {
							binding.Domain = &workloadfence.Domain{}
						} else {
							binding.Runtime.Generation = ids.New()
						}
						return binding, nil
					}
					if result, e := b.Build(t.Context(), fn, archive); e == nil || result != nil {
						t.Fatal("bad binding accepted:", which)
					}
				}
				b.runtimeProvider = original
				owner.s.cfg.Budget = &workloadfence.Budget{} // exclusively owned test configuration, no active jobs
				if result, e := b.Build(t.Context(), fn, archive); e == nil || result != nil {
					t.Fatal("unminted budget accepted")
				}
				return
			} else {
				result, err = b.Build(ctx, fn, archive)
			}
			membership := strings.TrimSpace(awaitOutputFile(t, event+".membership"))
			mount := strings.TrimSpace(awaitOutputFile(t, event+".mount"))
			if !strings.HasPrefix(mount, parent+"/build-") || !strings.HasSuffix(mount, "/jails") ||
				!strings.Contains(membership, strings.TrimPrefix(filepath.Dir(mount), "/sys/fs/cgroup")+"/supervisors") {
				t.Fatal("host jail not enrolled in the same private epoch as relocation arguments:", membership, mount)
			}
			t.Logf("private epoch enrollment: %s; jail parent=%s", membership, mount)
			unchanged, e := os.ReadFile(sentinel)
			if e != nil || string(unchanged) != "legacy sentinel" {
				t.Fatal("legacy overwritten:", e)
			}
			if mode == "quarantine" {
				if !errors.Is(err, ErrOutputPending) || result != nil || owner.Usage().Pending != 1 {
					t.Fatal("failed fence escaped quarantine:", err)
				}
				stage := awaitOutputFile(t, event)
				if _, e := os.Stat(stage); e != nil {
					t.Fatal("unfenced stage removed:", e)
				}
				if e := rs.Retire(rid); e != nil {
					t.Fatal(e)
				}
				if e := rs.Reclaim(rid); !errors.Is(e, artifactfs.ErrBusy) {
					t.Fatal("runtime anchor lost on fence failure:", e)
				}
				if _, e := b.Build(t.Context(), fn, archive); !errors.Is(e, ErrOutputCapacity) {
					t.Fatal("quarantine stopped charging capacity:", e)
				}
				if e := os.Rename(saved, record); e != nil {
					t.Fatal(e)
				}
				var wg sync.WaitGroup
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if e := owner.RetryCleanup(t.Context()); e != nil {
							t.Error("explicit concurrent retry failed:", e)
						}
					}()
				}
				wg.Wait()
				if _, e := os.Stat(stage); !os.IsNotExist(e) {
					t.Fatal("retry did not abort stage")
				}
				outputChildDead(t, event)
			} else if mode == "normal" || mode == "capture" || mode == "delayed" {
				if err != nil || result == nil {
					t.Fatal("managed build failed:", err)
				}
				if result.CodeArtifact.Kind != artifactfs.KindCode || result.CodeArtifact.Owner != originalID || result.RuntimeArtifact.Generation != rid ||
					result.VersionDir != "" || result.Cached || result.SourceArchiveSHA256 != hash || result.AuthoredEntrypoint != "handler.js" {
					t.Fatal("incorrect managed result")
				}
				lease, e := s.Acquire(result.CodeArtifact.Generation)
				if e != nil {
					t.Fatal(e)
				}
				if mode == "delayed" {
					outputChildDead(t, event)
					time.Sleep(2200 * time.Millisecond)
					if _, e := os.Stat(filepath.Join(lease.Path(), "late-write")); !os.IsNotExist(e) {
						t.Fatal("late writer survived Seal")
					}
				}
				raw, e := os.ReadFile(filepath.Join(lease.Path(), "handler.js"))
				if e != nil || string(raw) != files["handler.js"] {
					t.Fatal("sealed output changed:", e)
				}
				if e := s.Retire(result.CodeArtifact.Generation); e != nil {
					t.Fatal(e)
				}
				if e := s.Reclaim(result.CodeArtifact.Generation); !errors.Is(e, artifactfs.ErrBusy) {
					t.Fatal("sealed consumer lease not retained:", e)
				}
				lease.Release()
				if e := s.Reclaim(result.CodeArtifact.Generation); e != nil {
					t.Fatal(e)
				}
			} else {
				if err == nil || result != nil {
					t.Fatal("failed build returned code generation")
				}
				if mode == "fail" || mode == "cancel" || mode == "capacity" {
					outputChildDead(t, event)
				}
			}
			if usage := owner.Usage(); usage.Jobs != 0 || usage.ReservedBytes != 0 {
				t.Fatal("capacity leaked:", usage)
			}
			entries, e := os.ReadDir(storeRoot)
			if e != nil || len(entries) != 0 {
				t.Fatal("stage/generation leaked:", entries, e)
			}
			t.Logf("%s: same-epoch Fence/Check before Seal/Abort; usage=%+v", mode, owner.Usage())
		})
	}
}

// All operations below run only in the disposable guest's test process.
func syscallSetOutputXattr(path string) error {
	return syscall.Setxattr(path, "user.fixture", []byte("deny"), 0)
}
