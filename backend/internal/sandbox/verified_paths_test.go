//go:build linux

package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/internal/ids"
)

type verifiedFile struct {
	path, data string
	mode       uint32
	link       string
}

type verifiedFixture struct {
	store                    *artifactfs.Store
	codeID, runtimeID, owner string
	code, runtime, source    string
	prepared                 *artifactfs.PreparedPaths
	reclaimed                map[string]bool
}

// Expectations describe only fixture-owned bytes; they are not a runtime importer.
func verifiedTree(t *testing.T, s *artifactfs.Store, limits artifactfs.Limits, kind artifactfs.Kind, owner string, dirs []string, files []verifiedFile) (string, string) {
	t.Helper()
	stage, err := s.Begin(context.Background(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	source, err := stage.Path()
	if err != nil {
		t.Fatal(err)
	}
	m := artifactfs.Manifest{Version: 1}
	add := func(path string, mode uint32, typ artifactfs.EntryType, data string) {
		e := artifactfs.Entry{Path: path, Mode: mode, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid()), Type: typ}
		if typ == artifactfs.TypeFile {
			sum := sha256.Sum256([]byte(data))
			e.Size, e.SHA256 = int64(len(data)), hex.EncodeToString(sum[:])
		}
		m.Entries = append(m.Entries, e)
	}
	add(".", 0700, artifactfs.TypeDirectory, "")
	for _, dir := range dirs {
		path := filepath.Join(source, dir)
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
		add(dir, 0755, artifactfs.TypeDirectory, "")
	}
	for _, file := range files {
		path := filepath.Join(source, file.path)
		if file.link != "" {
			if err := os.Symlink(file.link, path); err != nil {
				t.Fatal(err)
			}
			add(file.path, 0777, artifactfs.TypeSymlink, "")
			m.Entries[len(m.Entries)-1].LinkTarget = file.link
			continue
		}
		if err := os.WriteFile(path, []byte(file.data), os.FileMode(file.mode)); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, os.FileMode(file.mode)); err != nil {
			t.Fatal(err)
		}
		add(file.path, file.mode, artifactfs.TypeFile, file.data)
	}
	digest, err := artifactfs.Digest(m, limits)
	if err != nil {
		t.Fatal(err)
	}
	id, err := stage.Seal(context.Background(), kind, owner, m, digest)
	if err != nil {
		t.Fatal(err)
	}
	return id, source
}

func newVerifiedFixture(t *testing.T, lang Language, change func([]verifiedFile) []verifiedFile) *verifiedFixture {
	t.Helper()
	limits := artifactfs.Limits{Generations: 4, Leases: 8, Staging: 2, Entries: 32, PathBytes: 512, LinkBytes: 512, ManifestBytes: 1 << 20, Bytes: 1 << 16, FileBytes: 4096}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := artifactfs.New(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	f := &verifiedFixture{store: s, owner: ids.New(), reclaimed: make(map[string]bool)}
	f.codeID, f.source = verifiedTree(t, s, limits, artifactfs.KindCode, f.owner, nil,
		[]verifiedFile{{path: "handler.js", data: "trusted code", mode: 0644}, {path: "lazy.js", data: "lazy original", mode: 0644}})
	bin, adapter := "node", "adapter.js"
	if lang == Python {
		bin, adapter = "python3", "adapter.py"
	}
	files := []verifiedFile{{path: "usr/local/bin/" + bin, data: "fixture interpreter", mode: 0755}, {path: "opt/orva/" + adapter, data: "// orva-sdk-proto:1", mode: 0644}}
	if change != nil {
		files = change(files)
	}
	f.runtimeID, _ = verifiedTree(t, s, limits, artifactfs.KindRuntime, string(lang),
		[]string{"usr", "usr/local", "usr/local/bin", "opt", "opt/orva"}, files)
	f.prepared, err = artifactfs.PreparePaths(s, f.codeID, s, f.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	f.code, f.runtime = f.prepared.Paths()
	t.Cleanup(func() {
		f.prepared.Release()
		for _, id := range []string{f.codeID, f.runtimeID} {
			if f.reclaimed[id] {
				continue
			}
			if err := s.Retire(id); err != nil {
				t.Error(err)
			}
			if err := s.Reclaim(id); err != nil {
				t.Error(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func verifiedJail(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fake-jail")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestVerifiedPathsFakeJail$ -- --verified-fake-jail \"$@\"\n", exe)), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

type verifiedReply struct {
	Code, Runtime, Lazy string
	Args                []string
}

// This process exercises argv and lifetime only, never real nsjail isolation.
func TestVerifiedPathsFakeJail(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "--verified-fake-jail") {
		return
	}
	reply := verifiedReply{Args: os.Args}
	for i, arg := range os.Args {
		if arg == "--chroot" && i+1 < len(os.Args) {
			reply.Runtime = os.Args[i+1]
		}
		if arg == "-R" && i+1 < len(os.Args) && strings.HasSuffix(os.Args[i+1], ":/code") {
			reply.Code = strings.TrimSuffix(os.Args[i+1], ":/code")
		}
	}
	if os.Getenv("VERIFIED_ONESHOT") == "1" {
		_ = json.NewEncoder(os.Stdout).Encode(reply)
		os.Exit(0)
	}
	if writeFrame(os.Stdout, []byte(`{"type":"ready"}`)) != nil {
		os.Exit(10)
	}
	if os.Getenv("VERIFIED_CRASH") == "1" {
		os.Exit(17)
	}
	for {
		b, err := readFrame(os.Stdin)
		if err != nil {
			os.Exit(0)
		}
		var frame struct{ Type string }
		if json.Unmarshal(b, &frame) != nil {
			os.Exit(11)
		}
		if frame.Type == "quit" {
			os.Exit(0)
		}
		if frame.Type == "request" {
			lazy, err := os.ReadFile(filepath.Join(reply.Code, "lazy.js"))
			if err != nil {
				os.Exit(12)
			}
			reply.Lazy = string(lazy)
			body, _ := json.Marshal(reply)
			payload, _ := json.Marshal(frameResponse{Type: "response", StatusCode: 200, Body: string(body)})
			if writeFrame(os.Stdout, payload) != nil {
				os.Exit(13)
			}
		}
	}
}

func (f *verifiedFixture) config(t *testing.T, lang Language) ExecConfig {
	t.Helper()
	return ExecConfig{FunctionID: f.owner, Language: lang, VerifiedPaths: f.prepared,
		CodeDir: "/unused/attacker/current", RootfsDir: "/unused/legacy-rootfs", NsjailBin: verifiedJail(t)}
}

func retireVerified(t *testing.T, f *verifiedFixture) {
	t.Helper()
	for _, id := range []string{f.codeID, f.runtimeID} {
		if f.reclaimed[id] {
			continue
		}
		if err := f.store.Retire(id); err != nil {
			t.Fatal(err)
		}
	}
}

func busyVerified(t *testing.T, f *verifiedFixture) {
	t.Helper()
	for _, id := range []string{f.codeID, f.runtimeID} {
		if err := f.store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
			t.Fatalf("leased generation reclaim: %v", err)
		}
	}
}

func waitVerifiedRelease(t *testing.T, f *verifiedFixture) {
	t.Helper()
	for _, id := range []string{f.codeID, f.runtimeID} {
		if f.reclaimed[id] {
			continue
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := f.store.Reclaim(id)
			if err == nil {
				f.reclaimed[id] = true
				break
			}
			if !errors.Is(err, artifactfs.ErrBusy) || time.Now().After(deadline) {
				t.Fatalf("release/reclaim: %v", err)
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func TestVerifiedPathsWorkerLifetime(t *testing.T) {
	for _, lang := range []Language{Node, Python} {
		t.Run(string(lang), func(t *testing.T) {
			f := newVerifiedFixture(t, lang, func(files []verifiedFile) []verifiedFile {
				if lang == Node {
					files[1].link = "adapter.real"
					files = append(files, verifiedFile{path: "opt/orva/adapter.real", data: "// orva-sdk-proto:1", mode: 0644})
				}
				return files
			})
			cfg := f.config(t, lang)
			cfg.SDKHandler = okHandler
			exited, allowCleanup := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(allowCleanup) }) }
			cfg.OnExit = func() { close(exited); <-allowCleanup }
			w, err := Spawn(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				unblock()
				_ = w.Kill()
				retireVerified(t, f)
				waitVerifiedRelease(t, f)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := w.AwaitReady(ctx); err != nil {
				t.Fatal(err)
			}
			f.prepared.Release()
			if _, err := f.prepared.Transfer(); !errors.Is(err, artifactfs.ErrInvalid) {
				t.Fatal("caller retained ownership")
			}
			retireVerified(t, f)
			busyVerified(t, f)
			// Seal renamed the source; replacing its old name cannot redirect lazy reads.
			if err := os.Mkdir(f.source, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.source, "lazy.js"), []byte("attacker"), 0644); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(f.source) })
			res, err := w.DispatchEx(ctx, []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			var reply verifiedReply
			if err := json.Unmarshal([]byte(res.Body), &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Code != f.code || reply.Runtime != f.runtime || reply.Lazy != "lazy original" {
				t.Fatalf("wrong concrete binding: %+v", reply)
			}
			state, ok := w.WorkloadState()
			if !ok || state.Identity.ArtifactRevision != "" {
				t.Fatal("lease proof fabricated artifact approval")
			}
			busyVerified(t, f)
			end := w.Kill
			if lang == Python {
				end = func() error { return w.Quit(100 * time.Millisecond) }
			}
			if err := end(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-exited:
			case <-ctx.Done():
				t.Fatal("worker not reaped")
			}
			// Even a confirmed Wait does not release ahead of post-reap cleanup.
			busyVerified(t, f)
			unblock()
			waitVerifiedRelease(t, f)
		})
	}
}

func TestVerifiedPathsUnexpectedExit(t *testing.T) {
	f := newVerifiedFixture(t, Node, nil)
	cfg := f.config(t, Node)
	cfg.Env = map[string]string{"VERIFIED_CRASH": "1"}
	cfg.ArtifactRevision = "host-supplied-revision"
	exited, allowCleanup := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(allowCleanup) }) }
	cfg.OnExit = func() { close(exited); <-allowCleanup }
	w, err := Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unblock()
		_ = w.Kill()
		retireVerified(t, f)
		waitVerifiedRelease(t, f)
	})
	f.prepared.Release()
	retireVerified(t, f)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("unexpected exit was not reaped")
	}
	busyVerified(t, f)
	if state, ok := w.WorkloadState(); !ok || state.Alive || state.Identity.ArtifactRevision != cfg.ArtifactRevision {
		t.Fatal("reap lost the independently supplied workload identity")
	}
	unblock()
	waitVerifiedRelease(t, f)
}

func TestVerifiedPathsSpawnFailuresRelease(t *testing.T) {
	tests := []struct {
		name   string
		change func([]verifiedFile) []verifiedFile
		config func(*ExecConfig)
	}{
		{"owner", nil, func(c *ExecConfig) { c.FunctionID = ids.New() }},
		{"noncanonical owner", nil, func(c *ExecConfig) { c.FunctionID = "A" + c.FunctionID[1:] }},
		{"language", nil, func(c *ExecConfig) { c.Language = Python }},
		{"unsupported language", nil, func(c *ExecConfig) { c.Language = "ruby" }},
		{"interpreter", func(files []verifiedFile) []verifiedFile { files[0].mode = 0644; return files }, nil},
		{"adapter", func(files []verifiedFile) []verifiedFile { return files[:1] }, nil},
		{"chroot absolute adapter", func(files []verifiedFile) []verifiedFile {
			files[1].link = "/usr/local/bin/node"
			return files
		}, func(c *ExecConfig) { c.SDKHandler = okHandler }},
		{"SDK marker", func(files []verifiedFile) []verifiedFile { files[1].data = "// no channel"; return files }, func(c *ExecConfig) { c.SDKHandler = okHandler }},
		{"egress", nil, func(c *ExecConfig) { c.NetworkMode = "egress" }},
		{"start", nil, func(c *ExecConfig) { c.NsjailBin = "/nonexistent/fake-jail" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newVerifiedFixture(t, Node, tt.change)
			cfg := f.config(t, Node)
			if tt.config != nil {
				tt.config(&cfg)
			}
			if w, err := Spawn(context.Background(), cfg); err == nil || w != nil {
				if w != nil {
					_ = w.Kill()
				}
				t.Fatalf("spawn should fail: %v", err)
			}
			if c, r := f.prepared.Paths(); c != "" || r != "" {
				t.Fatal("failed spawn did not consume caller handle")
			}
			retireVerified(t, f)
			waitVerifiedRelease(t, f)
		})
	}
}

func TestVerifiedPathsZeroAndLegacy(t *testing.T) {
	f := newVerifiedFixture(t, Node, nil)
	cfg := f.config(t, Node)
	cfg.VerifiedPaths = &artifactfs.PreparedPaths{}
	if w, err := Spawn(context.Background(), cfg); w != nil || !errors.Is(err, artifactfs.ErrInvalid) {
		t.Fatalf("zero handle accepted: %v", err)
	}
	root := t.TempDir()
	if err := os.Symlink(f.runtime, filepath.Join(root, "node")); err != nil {
		t.Fatal(err)
	}
	cfg.VerifiedPaths, cfg.RootfsDir, cfg.CodeDir = nil, root, f.code
	w, err := Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Kill(); <-w.waitDone }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.AwaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if c, r := f.prepared.Paths(); c != f.code || r != f.runtime || w.verifiedPaths != nil {
		t.Fatal("nil option changed legacy ownership")
	}
}

func TestVerifiedPathsExecute(t *testing.T) {
	f := newVerifiedFixture(t, Node, nil)
	cfg := f.config(t, Node)
	cfg.Env = map[string]string{"VERIFIED_ONESHOT": "1"}
	res := Execute(context.Background(), cfg)
	if res.Error != nil || res.ExitCode != 0 {
		t.Fatalf("execute: %+v", res)
	}
	var reply verifiedReply
	if err := json.Unmarshal(res.Stdout, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Code != f.code || reply.Runtime != f.runtime {
		t.Fatalf("execute used legacy paths: %+v", reply)
	}
	retireVerified(t, f)
	waitVerifiedRelease(t, f)
}
