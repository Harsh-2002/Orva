//go:build linux

package workloadspawn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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

func TestFactoryRuntimeQualification(t *testing.T) {
	if os.Getenv("ORVA_FACTORY_RUNTIME_TEST") != "1" {
		t.Skip("scratch VM real-runtime factory qualification only; see FACTORY.md")
	}
	dir := os.Getenv("ORVA_RUNTIME_PACKAGE_DIR")
	jail := filepath.Join(dir, "nsjail-linux-amd64")
	f, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) { qualifyFactoryRuntime(t, dir, jail, lang) })
	}
}

type heldFactoryRuntimeWorker struct {
	worker         *sandbox.Worker
	reaped, resume chan struct{}
	once           sync.Once
}

func (w *heldFactoryRuntimeWorker) unblock() { w.once.Do(func() { close(w.resume) }) }

func qualifyFactoryRuntime(t *testing.T, dir, jail, lang string) {
	t.Helper()
	store := newQualificationStore(t)
	runtimeID := sealQualificationRuntime(t, store, dir, lang, true)
	defer reclaimQualified(t, store, runtimeID)
	owner := ids.New()
	entrypoint, source, lazyName, lazy := factoryRuntimeSources(lang)
	codeID, codeManifestSHA := sealQualificationCode(t, store, owner, entrypoint, source, lazyName, lazy)
	defer reclaimQualified(t, store, codeID)
	prepared := &secrets.PreparedArtifactSnapshot{
		Artifact: database.WorkloadArtifactRecord{OwnerID: owner, Generation: ids.New(), Revision: 1, Epoch: 1},
		Function: &database.Function{ID: owner, Name: "factory-runtime-qualification", Runtime: lang,
			Entrypoint: entrypoint, CodeHash: digest(source), Status: "active", Version: 1,
			MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, MaxConcurrency: 2, NetworkMode: "none", SDK: true,
			EnvVars: map[string]string{"FACTORY_CONFIG": "original-config"}},
		Secrets: map[string]string{"FACTORY_SECRET": "original-fixture-secret"},
	}
	factory, err := NewFactory(t.Context(), prepared, store, codeID, store, runtimeID,
		FactoryLimits{MaxPreparedPlans: 2, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer factory.Close()
	wantFacts := ResourceFacts{MemoryMB: 256, CPUs: 1, Timeout: 10 * time.Second, MaxConcurrency: 2, NetworkMode: "none", SDK: true}
	facts, generation, artifact := factory.Resources(), factory.Generation(), factory.ArtifactRevision()
	if facts != wantFacts {
		t.Fatal("resource facts differ from frozen function")
	}
	first, err := factory.Prepare(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	codePath, runtimePath := first.state.input.paths.Paths()
	prepared.Function.EnvVars["FACTORY_CONFIG"] = "changed-config"
	prepared.Function.EnvVars["FACTORY_EXTRA"] = "must-not-appear"
	prepared.Secrets["FACTORY_SECRET"] = "changed-secret"
	prepared.Secrets["FACTORY_EXTRA_SECRET"] = "must-not-appear"
	prepared.Function.ID, prepared.Function.Name, prepared.Function.Runtime = ids.New(), "changed-name", "unsupported"
	prepared.Function.Entrypoint, prepared.Function.Status = "../changed", "created"
	prepared.Function.MemoryMB, prepared.Function.TimeoutMS, prepared.Function.CPUs = 1, 1, 99
	prepared.Function.MaxConcurrency, prepared.Function.SDK, prepared.Function.NetworkMode = 99, false, "egress"
	prepared.Artifact.OwnerID, prepared.Artifact.Generation = prepared.Function.ID, ids.New()
	prepared.Artifact.Revision, prepared.Artifact.Epoch = 99, 99
	second, err := factory.Prepare(t.Context())
	if err != nil {
		t.Fatal("source mutation contaminated repeated preparation:", err)
	}
	defer second.Close()
	plans := []*SpawnPlan{first, second}
	for _, plan := range plans {
		if plan.ArtifactRevision() != artifact || plan.Generation() != generation || plan.Owner() != owner {
			t.Fatal("repeated plan mixed identity/generation")
		}
		code, runtime := plan.state.input.paths.Paths()
		wantCode, wantRuntime := factory.Identities()
		gotCode, gotRuntime := plan.state.input.paths.Identities()
		if code != codePath || runtime != runtimePath || gotCode != wantCode || gotRuntime != wantRuntime {
			t.Fatal("repeated plan changed concrete code/runtime generation")
		}
	}
	if extra, err := factory.Prepare(t.Context()); extra != nil || !errors.Is(err, ErrFactoryCapacity) {
		if extra != nil {
			extra.Close()
		}
		t.Fatal("real-runtime preparation bypassed pending-plan limit")
	}
	if usage := factory.Usage(); usage.Plans != 2 || usage.Bytes != 2*usage.PlanBytes {
		t.Fatal("repeated preparations were not charged")
	}
	t.Logf("factory artifact=%s generation=%s code=%s runtime=%s", artifact, generation.Token, codePath, runtimePath)
	writeFactoryRuntimeCodeEvidence(t, lang, owner, artifact, entrypoint, source, lazyName, lazy, codeManifestSHA)
	calls := make(chan sandbox.WorkloadState, 4)
	var workers []*heldFactoryRuntimeWorker
	defer func() {
		for _, w := range workers {
			w.unblock()
		}
		for _, w := range workers {
			_ = w.worker.Kill()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for index, plan := range plans {
		held := &heldFactoryRuntimeWorker{reaped: make(chan struct{}), resume: make(chan struct{})}
		worker, err := plan.Spawn(ctx, sandbox.ExecConfig{
			NsjailBin: jail, CodeDir: "/nonexistent-legacy-code", RootfsDir: "/nonexistent-legacy-runtime",
			MemoryMB: 1, MaxCPUs: 99, Timeout: time.Hour, NetworkMode: "egress",
			Env:           map[string]string{"FACTORY_CONFIG": "template", "FACTORY_SECRET": "template"},
			SeccompPolicy: sandbox.BuildSeccompPolicy("default", nil, nil),
			OnExit:        func() { close(held.reaped); <-held.resume },
			SDKHandler: func(_ context.Context, caller sandbox.SDKCaller, op string, _ json.RawMessage) (any, *sandbox.SDKError) {
				state, ok := caller.WorkloadState()
				if !ok || caller.FunctionID != owner || state.Identity.ArtifactRevision != artifact ||
					caller.Exec == nil || state.ExecutionID != caller.Exec.ExecutionID || op != "kv.get" {
					t.Error("private SDK call lost frozen host identity")
				}
				select {
				case calls <- state:
				default:
					t.Error("unexpected excess private SDK calls")
				}
				return map[string]any{"value": "private-channel", "found": true}, nil
			},
		})
		if err != nil {
			t.Fatal("real factory Spawn failed:", err)
		}
		held.worker = worker
		workers = append(workers, held)
		if usage := factory.Usage(); usage.Plans != 1-index || usage.Bytes != int64(1-index)*usage.PlanBytes {
			t.Fatal("Spawn return did not release exactly one prepared reservation")
		}
		if err := worker.AwaitReady(ctx); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			factory.Retire()
			factory.Close()
			if !factory.Retired() || factory.Resources() != facts || factory.Generation() != generation {
				t.Fatal("retirement changed immutable observations")
			}
			if extra, err := factory.Prepare(ctx); extra != nil || !errors.Is(err, ErrFactoryRetired) {
				if extra != nil {
					extra.Close()
				}
				t.Fatal("retired factory admitted a new preparation")
			}
			for _, id := range []string{codeID, runtimeID} {
				if err := store.Retire(id); err != nil {
					t.Fatal(err)
				}
				if err := store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
					t.Fatal("factory retirement released worker/issued-plan leases:", err)
				}
			}
		}
		executionID := ids.New()
		result, err := worker.DispatchEx(sandbox.WithExecution(ctx, sandbox.ExecInfo{ExecutionID: executionID}), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Version, SDK, Lazy, Config, Secret, Extra, ExtraSecret, Owner, Name, Entrypoint, SDKProto string
			MemoryMB, TimeoutMS                                                                       int64
		}
		if err := json.Unmarshal([]byte(result.Body), &body); err != nil || result.StatusCode != 200 {
			t.Fatal("invalid real-runtime response:", err)
		}
		if body.SDK != "private-channel" || body.Lazy != "lazy-file" || body.Config != "original-config" ||
			body.Secret != "original-fixture-secret" || body.Extra != "" || body.ExtraSecret != "" ||
			body.Owner != owner || body.Name != "factory-runtime-qualification" || body.Entrypoint != entrypoint ||
			body.MemoryMB != facts.MemoryMB || body.TimeoutMS != facts.Timeout.Milliseconds() || body.SDKProto != "1" {
			t.Fatal("actual handler environment/SDK/lazy imports differ from frozen resources and inputs")
		}
		prefix := sandbox.NodeMajor + "."
		if lang == "python" {
			prefix = sandbox.PythonMinor + "."
		}
		if !strings.HasPrefix(body.Version, prefix) {
			t.Fatal("wrong interpreter version:", body.Version)
		}
		identity, ok := worker.WorkloadState()
		if !ok || !identity.Alive || !identity.Ready || !canonicalUUID(identity.Identity.WorkerID) ||
			identity.Identity.ArtifactRevision != artifact || identity.Identity.FunctionID != owner {
			t.Fatal("real worker lost factory identity/readiness")
		}
		select {
		case state := <-calls:
			if state.Identity != identity.Identity || state.ExecutionID != executionID || state.Lease == "" ||
				state.Phase != sandbox.WorkloadExecution || !state.Ready || !state.Alive {
				t.Fatal("private SDK frame was not attributed to the live host execution/worker")
			}
		default:
			t.Fatal("handler did not use the private SDK channel")
		}
		t.Logf("worker[%d] id=%s interpreter=%s artifact=%s prepared=%d", index, identity.Identity.WorkerID,
			body.Version, artifact, factory.Usage().Plans)
	}
	firstState, _ := workers[0].worker.WorkloadState()
	secondState, _ := workers[1].worker.WorkloadState()
	if firstState.Identity.WorkerID == secondState.Identity.WorkerID ||
		workers[0].worker.Cmd.Process.Pid == workers[1].worker.Cmd.Process.Pid ||
		workers[0].worker.IsDead() || workers[1].worker.IsDead() {
		t.Fatal("repeated Spawn did not retain two distinct live workers")
	}
	if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
		t.Fatal("live workers were charged as prepared plans")
	}
	for _, w := range workers {
		if err := w.worker.Kill(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-w.reaped:
		case <-ctx.Done():
			t.Fatal("real worker did not reach post-Wait callback")
		}
		for _, id := range []string{codeID, runtimeID} {
			if err := store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
				t.Fatal("tree lease released before post-Wait cleanup:", err)
			}
		}
	}
	for _, w := range workers {
		w.unblock()
	}
	t.Logf("qualified %s: two real workers, one frozen generation, retired anchors, SDK/lazy imports, quota and post-Wait leases; no IAM admission asserted", lang)
}

func writeFactoryRuntimeCodeEvidence(t *testing.T, lang, owner, artifact, entrypoint, source, lazyName, lazy, manifest string) {
	t.Helper()
	evidence := struct {
		Language, Owner, Artifact, Entrypoint, SourceSHA256, LazyName, LazySHA256, ManifestSHA256 string
	}{lang, owner, artifact, entrypoint, digest(source), lazyName, digest(lazy), manifest}
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(os.Getenv("ORVA_RUNTIME_RECIPE_OUTPUT"), lang, "factory-code.json"),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.Write(raw)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("factory code evidence write failed")
	}
}

func factoryRuntimeSources(lang string) (string, string, string, string) {
	entrypoint, _, lazyName, lazy := qualificationSources(lang)
	if lang == "node" {
		return entrypoint, `const {kv} = require('orva');
exports.handler = async () => ({statusCode:200, body:JSON.stringify({
version:process.versions.node, sdk:await kv.get('factory-qualification'), lazy:require('./lazy.js')(),
config:process.env.FACTORY_CONFIG, secret:process.env.FACTORY_SECRET,
extra:process.env.FACTORY_EXTRA || '', extraSecret:process.env.FACTORY_EXTRA_SECRET || '',
owner:process.env.ORVA_FUNCTION_ID, name:process.env.ORVA_FUNCTION_NAME, entrypoint:process.env.ORVA_ENTRYPOINT,
memoryMB:Number(process.env.ORVA_MEMORY_MB), timeoutMS:Number(process.env.ORVA_TIMEOUT_MS), sdkProto:process.env.ORVA_SDK_PROTO})});
`, lazyName, lazy
	}
	return entrypoint, `import sys, os
from orva import kv
def handler(event):
    import lazy
    return {"statusCode": 200, "body": __import__("json").dumps({
        "version": sys.version.split()[0], "sdk": kv.get("factory-qualification"), "lazy": lazy.load(),
        "config": os.environ.get("FACTORY_CONFIG"), "secret": os.environ.get("FACTORY_SECRET"),
        "extra": os.environ.get("FACTORY_EXTRA", ""), "extraSecret": os.environ.get("FACTORY_EXTRA_SECRET", ""),
        "owner": os.environ.get("ORVA_FUNCTION_ID"), "name": os.environ.get("ORVA_FUNCTION_NAME"),
        "entrypoint": os.environ.get("ORVA_ENTRYPOINT"), "memoryMB": int(os.environ["ORVA_MEMORY_MB"]),
        "timeoutMS": int(os.environ["ORVA_TIMEOUT_MS"]), "sdkProto": os.environ.get("ORVA_SDK_PROTO")})}
`, lazyName, lazy
}
