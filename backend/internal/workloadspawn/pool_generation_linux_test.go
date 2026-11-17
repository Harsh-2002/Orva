//go:build linux

package workloadspawn_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestLiveManagedPoolReplacement(t *testing.T) {
	if os.Getenv("ORVA_LIVE_WORKFLOW") != "disposable-vm" {
		t.Skip("actual nsjail in explicit disposable guest only")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "generation.db")
	db, err := database.New(dbPath)
	liveMust(t, err)
	defer db.Close()
	liveMust(t, db.Migrate())
	liveSchema(t, dbPath)
	owner := ids.New()
	fn := &database.Function{ID: owner, Name: "generation-replacement", Runtime: "node", Status: "active", Version: 1, Entrypoint: "handler.js",
		CodeHash: digest("owned"), MemoryMB: 96, CPUs: 1, TimeoutMS: 5000, MaxConcurrency: 1, SDK: true, NetworkMode: "none",
		EnvVars: map[string]string{"SCRATCH_CONFIG": "frozen-config"}}
	liveMust(t, db.InsertFunction(fn))
	liveMust(t, db.KVPut(owner, "demo", []byte(`"scratch-value"`), nil))
	record, err := db.NewIAMPolicy(ctx, owner, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
	liveMust(t, err)
	evaluator, err := record.AdmissionEvaluator()
	liveMust(t, err)
	authority, err := workloadiam.NewAuthority(8, []*workloadiam.Evaluator{evaluator})
	liveMust(t, err)
	ref := database.OperatorCredentialReference{Kind: "api_key", ID: ids.New()}
	liveMust(t, db.InsertAPIKey(&database.APIKey{ID: ref.ID, Name: "generation-admin", KeyHash: "unused-fixture-hash", Prefix: "fixture", Permissions: `["admin"]`}))
	// The runtime's near-1GiB reservation must not consume the code-store budget.
	store, runtimeStore := newQualificationStore(t), newQualificationStore(t)
	runtime := sealQualificationRuntime(t, runtimeStore, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "node", true)
	defer reclaimQualified(t, runtimeStore, runtime)
	_, source := liveSource("node")
	slow := `const boot=require('orva').kv.get('demo').catch(()=>{}); Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,1800);` + source
	slowID, _ := sealQualificationCode(t, store, owner, "handler.js", slow, "fixture.txt", "late")
	defer reclaimQualified(t, store, slowID)
	fastID, _ := sealQualificationCode(t, store, owner, "handler.js", source, "fixture.txt", "current")
	defer reclaimQualified(t, store, fastID)
	makeFactory := func(code string, memory int64, cpu float64) *Factory {
		t.Helper()
		input := &secrets.PreparedArtifactSnapshot{Artifact: database.WorkloadArtifactRecord{OwnerID: owner, Generation: ids.New(), Revision: 1, Epoch: 1},
			Function: fn.Clone(), Secrets: map[string]string{"SCRATCH_SECRET": "frozen-secret"}}
		input.Function.MemoryMB, input.Function.CPUs = memory, cpu
		f, err := NewFactory(ctx, input, store, code, runtimeStore, runtime, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
		liveMust(t, err)
		t.Cleanup(f.Close)
		input.Function.EnvVars["SCRATCH_CONFIG"] = "polluted"
		input.Secrets["SCRATCH_SECRET"] = "polluted"
		input.Function.MemoryMB, input.Function.CPUs, input.Function.MaxConcurrency = 1, 99, 99
		input.Function.SDK, input.Function.NetworkMode = false, "egress"
		return f
	}
	first, second, third := makeFactory(slowID, 96, 1), makeFactory(fastID, 96, 1), makeFactory(fastID, 128, .5)
	bootstrap := make(chan sandbox.WorkloadIdentity, 1)
	var manager *pool.Manager
	reg := registry.New(db)
	host := sdkhost.New(db, sdkhost.WithIAM(authority),
		sdkhost.WithIAMCurrentArtifact(func(id, artifact string) bool { return manager.IsCurrentArtifact(id, artifact) }),
		sdkhost.WithIAMTargetState(func(id string, w *sandbox.Worker) workloadiam.TargetState { return manager.TargetState(id, w) }))
	jail := filepath.Join(os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "nsjail-linux-amd64")
	checked, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
	liveMust(t, err)
	liveMust(t, checked.Close())
	manager = pool.NewManager(pool.ManagerConfig{ManagedAuthority: authority, DefaultMax: 1, ReapInterval: time.Hour},
		pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/absent-legacy-rootfs", DefaultSeccomp: "default",
			SecretsLookup: func(string) map[string]string { t.Error("mutable legacy secrets lookup used"); return nil },
			SDKHandler: func(ctx context.Context, caller sandbox.SDKCaller, op string, args json.RawMessage) (any, *sandbox.SDKError) {
				state, _ := caller.WorkloadState()
				if state.Phase == sandbox.WorkloadBootstrap {
					select {
					case bootstrap <- state.Identity:
					default:
					}
				}
				return host.Handle(ctx, caller, op, args)
			}}, db, reg, sandbox.NewLimiter(8))
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		liveMust(t, manager.Shutdown(stop))
	}()
	service, err := workloadpolicy.New(db, reg, authority, func(id string) workloadpolicy.ArtifactState {
		artifact := manager.CurrentArtifact(id)
		return workloadpolicy.ArtifactState{OwnerID: id, Artifact: artifact, Known: artifact != ""}
	})
	liveMust(t, err)
	liveMust(t, manager.InstallFactory(ctx, first))
	type result struct {
		acq *pool.AcquireResult
		err error
	}
	acquired := make(chan result, 1)
	go func() { a, e := manager.Acquire(ctx, owner); acquired <- result{a, e} }()
	var stale sandbox.WorkloadIdentity
	select {
	case stale = <-bootstrap:
	case <-ctx.Done():
		t.Fatal("real old import did not reach private SDK")
	}
	liveMust(t, manager.InstallFactory(ctx, second))
	var old *pool.AcquireResult
	select {
	case r := <-acquired:
		liveMust(t, r.err)
		old = r.acq
	case <-ctx.Done():
		t.Fatal("replacement acquire did not finish")
	}
	state, _ := old.Worker.WorkloadState()
	if state.Identity.ArtifactRevision != second.ArtifactRevision() || state.Identity.WorkerID == stale.WorkerID || !first.Retired() {
		t.Fatal("late retired spawn escaped into acquisition")
	}
	allow := []byte(`{"schema_version":1,"allow":[{"actions":["kv.get"],"context":"execution","resource":{"own_kv":{"key":"demo"}}}],"inbound":[]}`)
	record, err = service.Replace(ctx, ref, owner, workloadpolicy.Expected{Revision: record.Revision, CurrentArtifact: second.ArtifactRevision(), ApprovedArtifact: record.ApprovedArtifact}, allow)
	liveMust(t, err)
	dispatch := func(a *pool.AcquireResult, want string) {
		t.Helper()
		reply, e := a.Worker.DispatchEx(sandbox.WithExecution(ctx, sandbox.ExecInfo{ExecutionID: ids.New()}), []byte(`{"method":"POST","path":"/demo","headers":{},"body":"{}"}`))
		liveMust(t, e)
		var body map[string]any
		liveMust(t, json.Unmarshal([]byte(reply.Body), &body))
		if body["decision"] != want || body["configOK"] != true || body["secretOK"] != true {
			t.Fatalf("actual SDK/frozen-input proof failed: %v", body)
		}
	}
	dispatch(old, "ALLOWED")
	liveMust(t, manager.InstallFactory(ctx, third))
	unchanged, err := db.GetIAMPolicy(ctx, owner)
	liveMust(t, err)
	if unchanged.Revision != record.Revision || unchanged.ApprovedArtifact != second.ArtifactRevision() {
		t.Fatal("replacement silently changed approval")
	}
	dispatch(old, "IAM_DENIED")
	if manager.TargetState(owner, old.Worker).Active {
		t.Fatal("old acquired target still current")
	}
	manager.Release(old, nil)
	manager.Release(old, nil)
	current, err := manager.Acquire(ctx, owner)
	liveMust(t, err)
	defer manager.Release(current, nil)
	facts, ok := current.ManagedResources()
	if !ok || facts != third.Resources() || facts.MemoryMB != 128 || facts.CPUs != .5 || facts.MaxConcurrency != 1 || !facts.SDK || facts.NetworkMode != "none" {
		t.Fatal("pool/acquisition resource facts diverged from concrete spawn Factory")
	}
	if target := manager.TargetState(owner, current.Worker); !target.Active || target.Principal.ArtifactRevision != third.ArtifactRevision() {
		t.Fatal("acquired current target observer denied")
	}
	dispatch(current, "IAM_DENIED")
	record, err = service.Replace(ctx, ref, owner, workloadpolicy.Expected{Revision: record.Revision, CurrentArtifact: third.ArtifactRevision(), ApprovedArtifact: record.ApprovedArtifact}, allow)
	liveMust(t, err)
	dispatch(current, "ALLOWED")
	raw, _ := json.MarshalIndent(map[string]any{"late_worker": stale, "acquired_second": state.Identity, "third_artifact": third.ArtifactRevision(), "frozen_resources": facts,
		"policy_revision": record.Revision, "old_policy_retained_through_replacement": true, "old_release_twice_safe": true}, "", "  ")
	liveMust(t, os.WriteFile(filepath.Join(os.Getenv("ORVA_RUNTIME_RECIPE_OUTPUT"), "pool-replacement.json"), raw, 0600))
	t.Log("PASS actual delayed nsjail import rejected after replacement; held old worker denied with retained policy; release ABA safe; frozen resources/config/secrets; explicit new approval allowed")
}
