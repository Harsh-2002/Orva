//go:build linux

package workloadspawn

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const dummySecret = "fixture-secret-never-in-public-output"

type fixture struct {
	store             *artifactfs.Store
	codeID, runtimeID string
	code, runtime     string
	prepared          *secrets.PreparedArtifactSnapshot
	paths             *artifactfs.PreparedPaths
	reclaimed         bool
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Only fixture-authored bytes and metadata become manifest expectations.
func sealTree(t *testing.T, store *artifactfs.Store, limits artifactfs.Limits, kind artifactfs.Kind, owner, lang, content string) string {
	t.Helper()
	stage, err := store.Begin(context.Background(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	root, err := stage.Path()
	if err != nil {
		t.Fatal(err)
	}
	m := artifactfs.Manifest{Version: 1}
	add := func(name string, typ artifactfs.EntryType, mode uint32, data string) {
		t.Helper()
		e := artifactfs.Entry{Path: name, Type: typ, Mode: mode, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
		if typ == artifactfs.TypeDirectory {
			if name != "." {
				if err := os.Mkdir(filepath.Join(root, name), os.FileMode(mode)); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			if err := os.WriteFile(filepath.Join(root, name), []byte(data), os.FileMode(mode)); err != nil {
				t.Fatal(err)
			}
			e.Size, e.SHA256 = int64(len(data)), digest(data)
		}
		if err := os.Chmod(filepath.Join(root, name), os.FileMode(mode)); err != nil {
			t.Fatal(err)
		}
		m.Entries = append(m.Entries, e)
	}
	add(".", artifactfs.TypeDirectory, 0700, "")
	if kind == artifactfs.KindCode {
		add("handler.js", artifactfs.TypeFile, 0644, content)
	} else {
		for _, dir := range []string{"usr", "usr/local", "usr/local/bin", "opt", "opt/orva"} {
			add(dir, artifactfs.TypeDirectory, 0755, "")
		}
		bin, adapter := "node", "adapter.js"
		if lang == "python" {
			bin, adapter = "python3", "adapter.py"
		}
		add("usr/local/bin/"+bin, artifactfs.TypeFile, 0755, content)
		add("opt/orva/"+adapter, artifactfs.TypeFile, 0644, "// orva-sdk-proto:1")
	}
	hash, err := artifactfs.Digest(m, limits)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := stage.Seal(context.Background(), kind, owner, m, hash)
	if err != nil {
		t.Fatal(err)
	}
	return generation
}

func newFixture(t *testing.T, lang string) *fixture {
	t.Helper()
	f := &fixture{}
	data := t.TempDir()
	dbPath := filepath.Join(data, "scratch.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	fn := &database.Function{ID: ids.New(), Name: "frozen-name", Runtime: lang,
		Entrypoint: "handler.js", RunEntrypoint: "handler.js", Status: "active", Version: 1,
		CodeHash: digest("source archive, not manifest"), MemoryMB: 96, CPUs: 0.5, TimeoutMS: 1234,
		NetworkMode: "none", EnvVars: map[string]string{"CONFIG": "original", "OVERLAY": "config"}}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(artifactSchema(t)); err != nil {
		t.Fatal(err)
	}
	record, err := db.NewWorkloadArtifact(context.Background(), fn.ID, database.IAMActorReference{Kind: "system", ID: ids.New()})
	if err != nil {
		t.Fatal(err)
	}
	manager := secrets.New(db, data)
	encrypted, err := manager.EncryptValue(dummySecret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.UpsertWorkloadArtifactSecret(context.Background(), database.WorkloadArtifactChange{
		OwnerID: fn.ID, ExpectedRevision: record.Revision, ExpectedEpoch: record.Epoch,
		ExpectedGeneration: record.Generation, Actor: record.Actor,
	}, "OVERLAY", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	f.prepared, err = manager.PrepareArtifactSnapshot(context.Background(), fn.ID,
		secrets.SecretSnapshotLimits{MaxSecrets: 1024, MaxTotalBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	limits := artifactfs.Limits{Generations: 8, Leases: 16, Staging: 2, Entries: 32,
		PathBytes: 512, LinkBytes: 512, ManifestBytes: 1 << 20, Bytes: 1 << 16, FileBytes: 4096}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	f.store, err = artifactfs.New(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	f.codeID = sealTree(t, f.store, limits, artifactfs.KindCode, fn.ID, lang, "code fixture")
	f.runtimeID = sealTree(t, f.store, limits, artifactfs.KindRuntime, lang, lang, "runtime fixture")
	f.paths, err = artifactfs.PreparePaths(f.store, f.codeID, f.store, f.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	f.code, f.runtime = f.paths.Paths()
	t.Cleanup(func() {
		f.paths.Release()
		if !f.reclaimed {
			f.reclaim(t)
		}
		if err := f.store.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

// Extract the canonical inert schema without exporting or boot-wiring it.
func artifactSchema(t *testing.T) string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "../database/workload_artifact_schema.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var statements []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "workloadArtifactSchema" {
			return true
		}
		if len(spec.Values) != 1 {
			t.Fatal("expected one canonical schema initializer")
		}
		lit, ok := spec.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Fatal("canonical schema changed")
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatal(err)
		}
		statements = append(statements, text)
		return false
	})
	if len(statements) != 1 {
		t.Fatal("expected one canonical schema statement")
	}
	return statements[0]
}

func (f *fixture) pair(t *testing.T) *artifactfs.PreparedPaths {
	t.Helper()
	paths, err := artifactfs.PreparePaths(f.store, f.codeID, f.store, f.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.Release)
	return paths
}

func (f *fixture) plan(t *testing.T) *SpawnPlan {
	t.Helper()
	p, err := NewPlan(f.prepared, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func (f *fixture) reclaim(t *testing.T) {
	t.Helper()
	for _, id := range []string{f.codeID, f.runtimeID} {
		if err := f.store.Retire(id); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			err := f.store.Reclaim(id)
			if err == nil {
				break
			}
			if !errors.Is(err, artifactfs.ErrBusy) || time.Now().After(deadline) {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
	}
	f.reclaimed = true
}

type jailReply struct {
	Args []string
	Env  map[string]string
}

func fakeJail(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fake-jail")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestFakeJail$ -- --workloadspawn-jail \"$@\"\n", exe)), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func frame(w io.Writer, data []byte) {
	_ = binary.Write(w, binary.BigEndian, uint32(len(data)))
	_, _ = w.Write(data)
}

func fakeSDKCall(sdk io.ReadWriter, lease string, id uint64) {
	payload, _ := json.Marshal(map[string]any{
		"id": id, "op": "kv.get", "lease": lease,
		"function_id": "forged-frame", "artifact_revision": "forged-frame",
		"trace_id": "forged-frame", "span_id": "forged-frame", "depth": 999,
		"a": map[string]string{"key": "fixture", "function_id": "forged-argument"},
	})
	frame(sdk, payload)
	var size uint32
	if binary.Read(sdk, binary.BigEndian, &size) != nil || size == 0 || size > 1<<20 {
		os.Exit(10)
	}
	reply := make([]byte, size)
	if _, err := io.ReadFull(sdk, reply); err != nil {
		os.Exit(11)
	}
	var result struct {
		ID uint64
		OK bool
	}
	if json.Unmarshal(reply, &result) != nil || result.ID != id || !result.OK {
		os.Exit(12)
	}
}

// This child exercises wire/argv/lifetime behavior, not nsjail isolation.
func TestFakeJail(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "--workloadspawn-jail") {
		return
	}
	reply := jailReply{Args: os.Args, Env: map[string]string{}}
	for _, entry := range os.Environ() {
		key, val, _ := strings.Cut(entry, "=")
		reply.Env[key] = val
	}
	channel := os.Getenv("ORVA_SDK_PROTO") == "1"
	var sdk *os.File
	if channel {
		sdk = os.NewFile(3, "sdk")
		defer sdk.Close()
		fakeSDKCall(sdk, "", 1)
	}
	frame(os.Stdout, []byte(`{"type":"ready"}`))
	lease := ""
	for {
		var size uint32
		if binary.Read(os.Stdin, binary.BigEndian, &size) != nil || size > 1<<20 {
			os.Exit(0)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(os.Stdin, data); err != nil {
			os.Exit(0)
		}
		var request struct{ Type, Lease string }
		_ = json.Unmarshal(data, &request)
		if request.Type == "lease" {
			lease = request.Lease
		}
		if request.Type == "quit" {
			os.Exit(0)
		}
		if request.Type == "request" {
			if channel {
				fakeSDKCall(sdk, lease, 2)
			}
			body, _ := json.Marshal(reply)
			payload, _ := json.Marshal(map[string]any{"type": "response", "statusCode": 200, "body": string(body)})
			frame(os.Stdout, payload)
		}
	}
}

func TestFrozenSpawn(t *testing.T) {
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			f := newFixture(t, lang)
			fn := f.prepared.Function
			fn.EnvVars["ORVA_FUNCTION_ID"] = "forged-config"
			fn.EnvVars["orva_sdk_fd"] = "777"
			f.prepared.Secrets["ORVA_FUNCTION_NAME"] = "forged-secret"
			f.prepared.Secrets["ORVA_MEMORY_MB"] = "1"
			f.prepared.Secrets["ORVA_TIMEOUT_MS"] = "1"
			f.prepared.Secrets["ORVA_ENTRYPOINT"] = "../escape"
			f.prepared.Secrets["ORVA_INTERNAL_TOKEN"] = dummySecret
			f.prepared.Secrets["orva_api_base"] = "http://invalid"
			f.prepared.Secrets["Orva_Sdk_Proto"] = "999"
			p := f.plan(t)
			owner, artifact, gen := p.Owner(), p.ArtifactRevision(), p.Generation()
			fn.ID, fn.Name, fn.Runtime = ids.New(), "changed", "unsupported"
			fn.MemoryMB, fn.CPUs, fn.TimeoutMS = 1, 99, 99
			fn.EnvVars["CONFIG"], f.prepared.Secrets["OVERLAY"] = "changed", "changed"
			f.prepared.Artifact.Generation = ids.New()
			f.paths.Release()
			handler := func(context.Context, sandbox.SDKCaller, string, json.RawMessage) (any, *sandbox.SDKError) {
				t.Error("SDK false must not bind a handler")
				return nil, nil
			}
			w, err := p.Spawn(context.Background(), sandbox.ExecConfig{
				NsjailBin: fakeJail(t), FunctionID: "forged", ArtifactRevision: "forged",
				Language: "unsupported", CodeDir: "/legacy/current", RootfsDir: "/legacy/rootfs",
				Env:      map[string]string{"CONFIG": "template", "ORVA_FUNCTION_ID": "template"},
				MemoryMB: 1, MaxCPUs: 99, Timeout: time.Hour, NetworkMode: "egress",
				SDKHandler: handler, VerifiedPaths: &artifactfs.PreparedPaths{},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = w.Kill() })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := w.AwaitReady(ctx); err != nil {
				t.Fatal(err)
			}
			p.Close()
			result, err := w.DispatchEx(ctx, []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			var reply jailReply
			if err := json.Unmarshal([]byte(result.Body), &reply); err != nil {
				t.Fatal(err)
			}
			for key, expected := range map[string]string{"CONFIG": "original", "OVERLAY": dummySecret,
				"ORVA_FUNCTION_ID": owner, "ORVA_FUNCTION_NAME": "frozen-name",
				"ORVA_MEMORY_MB": "96", "ORVA_TIMEOUT_MS": "1234", "ORVA_ENTRYPOINT": "handler.js"} {
				if reply.Env[key] != expected {
					t.Errorf("frozen environment mismatch at %s", key)
				}
			}
			for key := range reply.Env {
				switch strings.ToUpper(key) {
				case "ORVA_INTERNAL_TOKEN", "ORVA_API_BASE", "ORVA_SDK_FD", "ORVA_SDK_PROTO":
					t.Error("reserved environment survived")
				}
			}
			args := strings.Join(reply.Args, "\n")
			if !strings.Contains(args, f.code+":/code") || !strings.Contains(args, f.runtime) ||
				strings.Contains(args, "/legacy/") || strings.Contains(args, dummySecret) || strings.Contains(args, "--user_net") {
				t.Error("wrong concrete arguments or secret in argv")
			}
			st, ok := w.WorkloadState()
			if !ok || st.Identity.FunctionID != owner || st.Identity.ArtifactRevision != artifact || st.Phase != sandbox.WorkloadNoChannel {
				t.Error("wrong host-owned identity")
			}
			if p.Generation() != gen {
				t.Error("mutable generation")
			}
			if _, err := p.Spawn(ctx, sandbox.ExecConfig{}); !errors.Is(err, ErrUsed) {
				t.Error("plan reused")
			}
			for _, id := range []string{f.codeID, f.runtimeID} {
				if err := f.store.Retire(id); err != nil {
					t.Fatal(err)
				}
				if err := f.store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
					t.Error("worker lease released early")
				}
			}
			if err := w.Kill(); err != nil {
				t.Fatal(err)
			}
			f.reclaim(t)
		})
	}
}

func TestValidationPreservesPair(t *testing.T) {
	f := newFixture(t, "node")
	cases := map[string]func(*secrets.PreparedArtifactSnapshot){
		"owner":             func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.OwnerID = ids.New() },
		"owner canonical":   func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.OwnerID = "not-a-uuid" },
		"generation":        func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.Generation = "" },
		"revision":          func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.Revision = 0 },
		"revision overflow": func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.Revision = math.MaxUint64 },
		"epoch":             func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.Epoch = math.MaxUint64 },
		"status":            func(p *secrets.PreparedArtifactSnapshot) { p.Function.Status = "created" },
		"version":           func(p *secrets.PreparedArtifactSnapshot) { p.Function.Version = 0 },
		"runtime":           func(p *secrets.PreparedArtifactSnapshot) { p.Function.Runtime = "ruby" },
		"source hash":       func(p *secrets.PreparedArtifactSnapshot) { p.Function.CodeHash = "" },
		"source canonical":  func(p *secrets.PreparedArtifactSnapshot) { p.Function.CodeHash = strings.Repeat("A", 64) },
		"entrypoint":        func(p *secrets.PreparedArtifactSnapshot) { p.Function.Entrypoint = "../" + dummySecret },
		"run entrypoint":    func(p *secrets.PreparedArtifactSnapshot) { p.Function.RunEntrypoint = "/bad" },
		"memory":            func(p *secrets.PreparedArtifactSnapshot) { p.Function.MemoryMB = math.MaxInt64 },
		"timeout":           func(p *secrets.PreparedArtifactSnapshot) { p.Function.TimeoutMS = math.MaxInt64 },
		"cpu nan":           func(p *secrets.PreparedArtifactSnapshot) { p.Function.CPUs = math.NaN() },
		"cpu inf":           func(p *secrets.PreparedArtifactSnapshot) { p.Function.CPUs = math.Inf(1) },
		"cpu negative":      func(p *secrets.PreparedArtifactSnapshot) { p.Function.CPUs = -1 },
		"cpu overflow":      func(p *secrets.PreparedArtifactSnapshot) { p.Function.CPUs = math.MaxFloat64 },
		"network":           func(p *secrets.PreparedArtifactSnapshot) { p.Function.NetworkMode = "host" },
		"env name":          func(p *secrets.PreparedArtifactSnapshot) { p.Function.EnvVars["bad=name"] = dummySecret },
		"secret name":       func(p *secrets.PreparedArtifactSnapshot) { p.Secrets["bad\x00name"] = dummySecret },
		"secret value":      func(p *secrets.PreparedArtifactSnapshot) { p.Secrets["bad"] = "\xff" },
		"env value":         func(p *secrets.PreparedArtifactSnapshot) { p.Function.EnvVars["bad"] = "\x00" },
		"map bytes":         func(p *secrets.PreparedArtifactSnapshot) { p.Secrets["large"] = strings.Repeat("x", 8<<20) },
		"map count": func(p *secrets.PreparedArtifactSnapshot) {
			for i := range 1025 {
				p.Secrets[fmt.Sprint(i)] = ""
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := *f.prepared
			p.Function = p.Function.Clone()
			p.Secrets = map[string]string{"OVERLAY": dummySecret}
			change(&p)
			paths := f.pair(t)
			plan, err := NewPlan(&p, paths)
			if plan != nil || !errors.Is(err, ErrInvalid) || strings.Contains(fmt.Sprint(err), dummySecret) {
				t.Fatal("invalid input accepted or disclosed")
			}
			if code, runtime := paths.Paths(); code == "" || runtime == "" {
				t.Fatal("constructor failure consumed pair")
			}
			paths.Release()
		})
	}
	for _, paths := range []*artifactfs.PreparedPaths{nil, {}} {
		if p, err := NewPlan(f.prepared, paths); p != nil || err == nil {
			t.Fatal("empty paths accepted")
		}
	}
	if p, err := NewPlan(nil, f.paths); p != nil || err == nil {
		t.Fatal("nil snapshot accepted")
	}
}

func TestFingerprint(t *testing.T) {
	f := newFixture(t, "node")
	makePlan := func(prepared *secrets.PreparedArtifactSnapshot, paths *artifactfs.PreparedPaths) *SpawnPlan {
		p, err := NewPlan(prepared, paths)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	base := makePlan(f.prepared, f.pair(t))
	baseHash := base.ArtifactRevision()
	p := *f.prepared
	p.Function = p.Function.Clone()
	p.Secrets = map[string]string{"DIFFERENT": "different-secret"}
	p.Function.EnvVars = map[string]string{"DIFFERENT": "different-environment"}
	same := makePlan(&p, f.pair(t))
	if same.ArtifactRevision() != baseHash {
		t.Fatal("environment or secrets fingerprinted")
	}
	for _, change := range []func(*database.Function){
		func(fn *database.Function) { fn.Version++ },
		func(fn *database.Function) { fn.Description = "cosmetic description"; fn.Version++ },
	} {
		cosmetic := *f.prepared
		cosmetic.Function = cosmetic.Function.Clone()
		change(cosmetic.Function)
		got := makePlan(&cosmetic, f.pair(t))
		if got.ArtifactRevision() != baseHash {
			t.Fatal("cosmetic metadata or its row mutation counter invalidated artifact identity")
		}
		got.Close()
	}
	for name, change := range map[string]func(*secrets.PreparedArtifactSnapshot){
		"generation":          func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.Generation = ids.New() },
		"revision":            func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.Revision++ },
		"epoch":               func(p *secrets.PreparedArtifactSnapshot) { p.Artifact.Epoch++ },
		"code hash":           func(p *secrets.PreparedArtifactSnapshot) { p.Function.CodeHash = digest("another source") },
		"name":                func(p *secrets.PreparedArtifactSnapshot) { p.Function.Name = "another-name" },
		"memory":              func(p *secrets.PreparedArtifactSnapshot) { p.Function.MemoryMB++ },
		"cpu":                 func(p *secrets.PreparedArtifactSnapshot) { p.Function.CPUs++ },
		"timeout":             func(p *secrets.PreparedArtifactSnapshot) { p.Function.TimeoutMS++ },
		"sdk":                 func(p *secrets.PreparedArtifactSnapshot) { p.Function.SDK = true },
		"network":             func(p *secrets.PreparedArtifactSnapshot) { p.Function.NetworkMode = "egress" },
		"entrypoint":          func(p *secrets.PreparedArtifactSnapshot) { p.Function.RunEntrypoint = "other.js" },
		"authored entrypoint": func(p *secrets.PreparedArtifactSnapshot) { p.Function.Entrypoint = "authored.js" },
		"concurrency":         func(p *secrets.PreparedArtifactSnapshot) { p.Function.MaxConcurrency++ },
		"auth mode":           func(p *secrets.PreparedArtifactSnapshot) { p.Function.AuthMode = "platform_key" },
		"deployment":          func(p *secrets.PreparedArtifactSnapshot) { p.Function.ActiveDeploymentID = ids.New() },
	} {
		t.Run(name, func(t *testing.T) {
			p := *f.prepared
			p.Function = p.Function.Clone()
			change(&p)
			got := makePlan(&p, f.pair(t))
			if got.ArtifactRevision() == baseHash {
				t.Fatal("fingerprint unchanged")
			}
			got.Close()
		})
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, base), dummySecret) {
			t.Fatal("format leaked secret")
		}
	}
	raw, err := json.Marshal(base)
	if err != nil || strings.Contains(string(raw), dummySecret) {
		t.Fatal("JSON leaked secret")
	}
	base.Close()
	same.Close()
	f.paths.Release()
}

func TestFailureAndConsumption(t *testing.T) {
	for _, mode := range []string{"missing jail", "sdk missing", "cancel", "close", "nil context"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, "node")
			if mode == "sdk missing" {
				f.prepared.Function.SDK = true
			}
			p := f.plan(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			if mode == "close" {
				p.Close()
				p.Close()
			}
			if mode == "nil context" {
				ctx = nil
			}
			w, err := p.Spawn(ctx, sandbox.ExecConfig{NsjailBin: "/nonexistent/" + dummySecret})
			if w != nil || err == nil || strings.Contains(fmt.Sprint(err), dummySecret) {
				t.Fatal("failure did not fail closed")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := p.Spawn(context.Background(), sandbox.ExecConfig{}); !errors.Is(err, ErrUsed) {
				t.Fatal("failed plan reused")
			}
			f.reclaim(t)
		})
	}
	var nilPlan *SpawnPlan
	for _, p := range []*SpawnPlan{nilPlan, {}} {
		p.Close()
		if w, err := p.Spawn(context.Background(), sandbox.ExecConfig{}); w != nil || !errors.Is(err, ErrUsed) {
			t.Fatal("empty plan accepted")
		}
	}
}

func TestConcurrentConsumeAndClose(t *testing.T) {
	for _, closeRace := range []bool{false, true} {
		t.Run(fmt.Sprint(closeRace), func(t *testing.T) {
			f := newFixture(t, "node")
			p := f.plan(t)
			copy := *p
			template := sandbox.ExecConfig{NsjailBin: fakeJail(t)}
			start := make(chan struct{})
			var wg sync.WaitGroup
			var winners atomic.Int32
			for i := range 16 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					if closeRace && i%2 == 0 {
						copy.Close()
						return
					}
					w, err := copy.Spawn(context.Background(), template)
					if err == nil {
						winners.Add(1)
						_ = w.Kill()
					} else if !errors.Is(err, ErrUsed) {
						t.Error(err)
					}
				}()
			}
			close(start)
			wg.Wait()
			if winners.Load() > 1 || (!closeRace && winners.Load() != 1) {
				t.Fatal("not one-use")
			}
			p.Close()
			f.reclaim(t)
		})
	}
}

func (f *fixture) extraGeneration(t *testing.T, kind artifactfs.Kind, owner, lang, content string) string {
	t.Helper()
	limits := artifactfs.Limits{Generations: 8, Leases: 16, Staging: 2, Entries: 32,
		PathBytes: 512, LinkBytes: 512, ManifestBytes: 1 << 20, Bytes: 1 << 16, FileBytes: 4096}
	id := sealTree(t, f.store, limits, kind, owner, lang, content)
	t.Cleanup(func() {
		if err := f.store.Retire(id); err != nil {
			t.Error(err)
		} else if err := f.store.Reclaim(id); err != nil {
			t.Error(err)
		}
	})
	return id
}

// A v2 wire-format oracle catches omitted digests even though resealing also changes generation.
func fixtureFingerprint(t *testing.T, f *fixture, code, runtime artifactfs.Identity) string {
	t.Helper()
	fn, artifact := f.prepared.Function, f.prepared.Artifact
	fields := []string{
		"Domain", "Owner", "Generation", "SourceCodeHash", "Code", "Runtime",
		"Name", "Language", "Authored", "Run", "Network", "Memory", "Timeout", "CPUs",
		"SDK", "MaxConcurrency", "RateLimit", "ConcurrencyPolicy", "AuthMode", "Deployment",
	}
	values := []any{
		"orva.workloadspawn.v2", fn.ID, Generation{artifact.Generation, artifact.Revision, artifact.Epoch},
		fn.CodeHash, code, runtime, fn.Name, fn.Runtime, fn.Entrypoint, fn.RunEntrypoint,
		fn.NetworkMode, fn.MemoryMB, fn.TimeoutMS, fn.CPUs, fn.SDK, fn.MaxConcurrency,
		fn.RateLimitPerMin, fn.ConcurrencyPolicy, fn.AuthMode, fn.ActiveDeploymentID,
	}
	var members []string
	for i, field := range fields {
		value, err := json.Marshal(values[i])
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, strconv.Quote(field)+":"+string(value))
	}
	return digest("{" + strings.Join(members, ",") + "}")
}

func TestFingerprintStoreInputs(t *testing.T) {
	for _, kind := range []artifactfs.Kind{artifactfs.KindCode, artifactfs.KindRuntime} {
		for _, changedContent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/content_changed=%t", kind, changedContent), func(t *testing.T) {
				f := newFixture(t, "node")
				base := f.plan(t)
				stable, err := NewPlan(f.prepared, f.pair(t))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(stable.Close)
				if stable.ArtifactRevision() != base.ArtifactRevision() {
					t.Fatal("same immutable inputs and leased generations changed fingerprint")
				}
				codeID, runtimeID := f.codeID, f.runtimeID
				owner, content := f.prepared.Function.ID, "code fixture"
				if kind == artifactfs.KindRuntime {
					owner, content = "node", "runtime fixture"
				}
				if changedContent {
					content += " changed"
				}
				id := f.extraGeneration(t, kind, owner, "node", content)
				if kind == artifactfs.KindCode {
					codeID = id
				} else {
					runtimeID = id
				}
				paths, err := artifactfs.PreparePaths(f.store, codeID, f.store, runtimeID)
				if err != nil {
					t.Fatal(err)
				}
				defer paths.Release()
				code, runtime := paths.Identities()
				original, err := artifactfs.PreparePaths(f.store, f.codeID, f.store, f.runtimeID)
				if err != nil {
					t.Fatal(err)
				}
				defer original.Release()
				oldCode, oldRuntime := original.Identities()
				if base.ArtifactRevision() != fixtureFingerprint(t, f, oldCode, oldRuntime) {
					t.Fatal("base fingerprint omitted or changed v2 immutable inputs")
				}
				before, after := oldCode, code
				if kind == artifactfs.KindRuntime {
					before, after = oldRuntime, runtime
					if code != oldCode {
						t.Fatal("unrelated code identity changed")
					}
				} else if runtime != oldRuntime {
					t.Fatal("unrelated runtime identity changed")
				}
				if before.Generation == after.Generation || (before.Digest != after.Digest) != changedContent {
					t.Fatal("fixture did not vary actual store generation/manifest as intended")
				}
				variant, err := NewPlan(f.prepared, paths)
				if err != nil {
					t.Fatal(err)
				}
				defer variant.Close()
				if variant.ArtifactRevision() == base.ArtifactRevision() {
					t.Fatal("actual store generation/manifest change did not change fingerprint")
				}
				if variant.ArtifactRevision() != fixtureFingerprint(t, f, code, runtime) {
					t.Fatal("fingerprint omitted actual store generation/manifest inputs")
				}
			})
		}
	}
}

func TestMismatchedStoreOwnersPreservePair(t *testing.T) {
	for _, kind := range []artifactfs.Kind{artifactfs.KindCode, artifactfs.KindRuntime} {
		t.Run(string(kind), func(t *testing.T) {
			f := newFixture(t, "node")
			codeID, runtimeID, owner, lang := f.codeID, f.runtimeID, ids.New(), "node"
			if kind == artifactfs.KindRuntime {
				owner, lang = "python", "python"
			}
			id := f.extraGeneration(t, kind, owner, lang, "wrong-owner fixture")
			if kind == artifactfs.KindCode {
				codeID = id
			} else {
				runtimeID = id
			}
			paths, err := artifactfs.PreparePaths(f.store, codeID, f.store, runtimeID)
			if err != nil {
				t.Fatal(err)
			}
			defer paths.Release()
			code, runtime := paths.Identities()
			codePath, runtimePath := paths.Paths()
			p, err := NewPlan(f.prepared, paths)
			if p != nil || !errors.Is(err, ErrInvalid) {
				if p != nil {
					p.Close()
				}
				t.Fatal("mismatched store owner accepted")
			}
			if gotCode, gotRuntime := paths.Identities(); gotCode != code || gotRuntime != runtime ||
				gotCode.Generation == "" || gotRuntime.Generation == "" {
				t.Fatal("rejection consumed caller identities")
			}
			if gotCode, gotRuntime := paths.Paths(); gotCode != codePath || gotRuntime != runtimePath {
				t.Fatal("rejection consumed caller paths")
			}
			for _, path := range []string{codePath, runtimePath} {
				if info, err := os.Stat(path); err != nil || !info.IsDir() {
					t.Fatal("rejection invalidated a leased tree")
				}
			}
			if err := f.store.Retire(id); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
				t.Fatal("rejection released caller's lease")
			}
			transferred, err := paths.Transfer()
			if err != nil {
				t.Fatal("rejection prevented caller transfer:", err)
			}
			defer transferred.Release()
		})
	}
}

func TestSDKSpawnHostAttribution(t *testing.T) {
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			f := newFixture(t, lang)
			f.prepared.Function.SDK = true
			f.prepared.Function.EnvVars["ORVA_FUNCTION_ID"] = "forged-env"
			f.prepared.Function.EnvVars["ORVA_SDK_FD"] = "777"
			f.prepared.Function.EnvVars["ORVA_TRACE_ID"] = "forged-env"
			f.prepared.Secrets["ORVA_SDK_PROTO"] = "999"
			f.prepared.Secrets["ARTIFACT_REVISION"] = "forged-secret"
			p := f.plan(t)
			type observation struct {
				caller sandbox.SDKCaller
				state  sandbox.WorkloadState
				ok     bool
			}
			calls := make(chan observation, 2)
			w, err := p.Spawn(context.Background(), sandbox.ExecConfig{
				NsjailBin: fakeJail(t), FunctionID: "forged-template", ArtifactRevision: "forged-template",
				Env: map[string]string{"ORVA_FUNCTION_ID": "forged-template", "ORVA_TRACE_ID": "forged-template"},
				SDKHandler: func(_ context.Context, caller sandbox.SDKCaller, op string, _ json.RawMessage) (any, *sandbox.SDKError) {
					if op != "kv.get" {
						t.Error("unexpected SDK operation")
					}
					state, ok := caller.WorkloadState()
					calls <- observation{caller, state, ok}
					return map[string]string{"value": "fixture"}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = w.Kill() })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			next := func() observation {
				t.Helper()
				select {
				case got := <-calls:
					return got
				case <-ctx.Done():
					t.Fatal("no private-channel SDK call")
					return observation{}
				}
			}
			bootstrap := next()
			if err := w.AwaitReady(ctx); err != nil {
				t.Fatal(err)
			}
			p.Close()
			f.paths.Release()
			trusted := sandbox.ExecInfo{ExecutionID: ids.New(), TraceID: "host-trace", SpanID: "host-span", Depth: 2}
			result, err := w.DispatchEx(sandbox.WithExecution(ctx, trusted), []byte(`{"trace_id":"forged-request"}`))
			if err != nil {
				t.Fatal(err)
			}
			active := next()
			for _, got := range []observation{bootstrap, active} {
				if !got.ok || got.caller.FunctionID != p.Owner() ||
					got.state.Identity.FunctionID != p.Owner() || got.state.Identity.ArtifactRevision != p.ArtifactRevision() ||
					!ids.IsUUID(got.state.Identity.WorkerID) || !got.state.Alive {
					t.Fatal("private SDK call lost plan's host-owned identity")
				}
			}
			if bootstrap.state.Phase != sandbox.WorkloadBootstrap || bootstrap.caller.Exec != nil ||
				active.state.Phase != sandbox.WorkloadExecution || active.state.Identity != bootstrap.state.Identity ||
				active.state.ExecutionID != trusted.ExecutionID || active.state.Lease == "" {
				t.Fatal("wrong bootstrap/execution attribution")
			}
			info := active.caller.Exec
			if info == nil || info.ExecutionID != trusted.ExecutionID || info.TraceID != trusted.TraceID ||
				info.SpanID != trusted.SpanID || info.Depth != trusted.Depth || info.Lease != active.state.Lease {
				t.Fatal("user claims replaced host execution/trace identity")
			}
			var reply jailReply
			if err := json.Unmarshal([]byte(result.Body), &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Env["ORVA_FUNCTION_ID"] != p.Owner() || reply.Env["ORVA_SDK_FD"] != "3" ||
				reply.Env["ORVA_SDK_PROTO"] != "1" || !strings.Contains(strings.Join(reply.Args, "\n"), "--pass_fd\n3") {
				t.Fatal("private channel was not host configured")
			}
			for _, id := range []string{f.codeID, f.runtimeID} {
				if err := f.store.Retire(id); err != nil {
					t.Fatal(err)
				}
				if err := f.store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
					t.Fatal("SDK worker lost concrete tree leases")
				}
			}
			if err := w.Kill(); err != nil {
				t.Fatal(err)
			}
			f.reclaim(t)
		})
	}
}

func TestCloseSpawnRaceRetainsWorkerLeases(t *testing.T) {
	for i := range 32 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := newFixture(t, "node")
			p := f.plan(t)
			copy := *p
			reaped, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(resume) }) }
			t.Cleanup(unblock)
			template := sandbox.ExecConfig{NsjailBin: fakeJail(t), OnExit: func() {
				close(reaped)
				<-resume
			}}
			start, closed := make(chan struct{}), make(chan struct{})
			go func() {
				<-start
				copy.Close()
				close(closed)
			}()
			close(start)
			w, err := p.Spawn(context.Background(), template)
			<-closed
			if errors.Is(err, ErrUsed) {
				if w != nil {
					t.Fatal("closed plan returned a worker")
				}
				f.reclaim(t)
				return
			}
			if err != nil || w == nil {
				t.Fatal("Close/Spawn produced a half-consumed plan:", err)
			}
			t.Cleanup(func() { unblock(); _ = w.Kill() })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := w.AwaitReady(ctx); err != nil {
				t.Fatal(err)
			}
			p.Close()
			copy.Close()
			f.paths.Release()
			for _, id := range []string{f.codeID, f.runtimeID} {
				if err := f.store.Retire(id); err != nil {
					t.Fatal(err)
				}
				if err := f.store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
					t.Fatal("Close released live worker leases")
				}
			}
			if _, err := w.DispatchEx(ctx, []byte(`{}`)); err != nil {
				t.Fatal("race winner is not usable:", err)
			}
			if _, err := copy.Spawn(ctx, template); !errors.Is(err, ErrUsed) {
				t.Fatal("race winner left a reusable plan")
			}
			if err := w.Kill(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-reaped:
			case <-ctx.Done():
				t.Fatal("worker did not reach post-Wait cleanup")
			}
			for _, id := range []string{f.codeID, f.runtimeID} {
				if err := f.store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
					t.Fatal("leases released before post-Wait cleanup finished")
				}
			}
			unblock()
			f.reclaim(t)
		})
	}
}
