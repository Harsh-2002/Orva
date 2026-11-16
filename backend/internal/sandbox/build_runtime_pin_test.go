//go:build linux

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

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
)

func TestBuildRuntimePinChild(t *testing.T) {
	if os.Getenv("BUILD_PIN_CHILD") != "1" {
		return
	}
	var root string
	for i, arg := range os.Args {
		if arg == "--chroot" && i+1 < len(os.Args) {
			root = os.Args[i+1]
		}
	}
	entered := os.Getenv("BUILD_PIN_ENTERED")
	if entered != "" {
		if err := os.WriteFile(entered, []byte(root), 0600); err != nil {
			os.Exit(3)
		}
		for {
			if _, err := os.Stat(entered + ".release"); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	fmt.Print(root)
	os.Exit(0)
}

func buildPinJail(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "jail")
	if err := os.WriteFile(p, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestBuildRuntimePinChild$ -- \"$@\"\n", exe)), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILD_PIN_CHILD", "1")
	return p
}

func TestRunBuildRuntimePinLifetime(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelRun), func(t *testing.T) {
			f := newVerifiedFixture(t, Node, nil)
			f.prepared.Release()
			cfg := BuildConfig{Runtime: &BuildRuntime{Store: f.store, Generation: f.runtimeID}, Language: Node,
				CodeDir: t.TempDir(), Argv: []string{"/usr/local/bin/node"}, NsjailBin: buildPinJail(t), RootfsDir: "/never-fallback",
				ScratchBase: t.TempDir()}
			entered := filepath.Join(t.TempDir(), "entered")
			t.Setenv("BUILD_PIN_ENTERED", entered)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				out, err := RunBuild(ctx, cfg)
				if err == nil && string(out) != f.runtime {
					done <- fmt.Errorf("runtime mixed: %q", out)
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
					t.Fatal("child did not start:", err)
				case <-deadline:
					t.Fatal("child start timeout")
				case <-time.After(time.Millisecond):
				}
			}
			if err := f.store.Retire(f.runtimeID); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Reclaim(f.runtimeID); !errors.Is(err, artifactfs.ErrBusy) {
				t.Fatal("command lease released before Wait:", err)
			}
			if cancelRun {
				cancel()
			} else if err := os.WriteFile(entered+".release", nil, 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if cancelRun && err == nil {
					t.Fatal("canceled command succeeded")
				}
				if !cancelRun && err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Wait did not finish")
			}
			entries, err := os.ReadDir(cfg.ScratchBase)
			if err != nil || len(entries) != 0 {
				t.Fatal("scratch cleanup not complete:", err)
			}
			if err := f.store.Reclaim(f.runtimeID); err != nil {
				t.Fatal("command lease leaked:", err)
			}
			f.reclaimed[f.runtimeID] = true
		})
	}
}

func TestRunBuildRuntimePinFailures(t *testing.T) {
	f := newVerifiedFixture(t, Node, nil)
	f.prepared.Release()
	for _, tc := range []struct {
		name    string
		binding *BuildRuntime
		lang    Language
		bin     string
	}{
		{"zero", &BuildRuntime{}, Node, "/usr/local/bin/node"},
		{"missing-store", &BuildRuntime{Generation: f.runtimeID}, Node, "/usr/local/bin/node"},
		{"missing-id", &BuildRuntime{Store: f.store}, Node, "/usr/local/bin/node"},
		{"wrong-kind", &BuildRuntime{Store: f.store, Generation: f.codeID}, Node, "/usr/local/bin/node"},
		{"wrong-language", &BuildRuntime{Store: f.store, Generation: f.runtimeID}, Python, "/usr/local/bin/node"},
		{"missing-tool", &BuildRuntime{Store: f.store, Generation: f.runtimeID}, Node, "/missing"},
		{"relative-tool", &BuildRuntime{Store: f.store, Generation: f.runtimeID}, Node, "usr/local/bin/node"},
		{"failed-start", &BuildRuntime{Store: f.store, Generation: f.runtimeID}, Node, "/usr/local/bin/node"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RunBuild(t.Context(), BuildConfig{Runtime: tc.binding, Language: tc.lang, Argv: []string{tc.bin}, NsjailBin: "/missing-jail"})
			if err == nil {
				t.Fatal("invalid binding/start accepted")
			}
		})
	}
	if err := f.store.Retire(f.runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Reclaim(f.runtimeID); err != nil {
		t.Fatal("failure lease leaked:", err)
	}
	f.reclaimed[f.runtimeID] = true
}

func TestRunBuildRuntimePinContainedToolLink(t *testing.T) {
	for _, absolute := range []bool{false, true} {
		t.Run(fmt.Sprint(absolute), func(t *testing.T) {
			f := newVerifiedFixture(t, Node, func(files []verifiedFile) []verifiedFile {
				target := "node"
				if absolute {
					target = "/usr/local/bin/node"
				}
				return append(files, verifiedFile{path: "usr/local/bin/npm", link: target})
			})
			_, err := RunBuild(t.Context(), BuildConfig{Runtime: &BuildRuntime{Store: f.store, Generation: f.runtimeID}, Language: Node,
				Argv: []string{"/usr/local/bin/npm"}, NsjailBin: "/missing-jail"})
			if err == nil || (absolute && !strings.Contains(err.Error(), "endpoint")) || (!absolute && !os.IsNotExist(err)) {
				t.Fatal("tool link resolution:", err)
			}
		})
	}
}
