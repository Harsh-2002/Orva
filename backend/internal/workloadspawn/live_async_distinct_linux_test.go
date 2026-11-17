//go:build linux

package workloadspawn_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/proxy"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/scheduler"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestLiveAsyncDistinctJobs(t *testing.T) {
	if os.Getenv("ORVA_LIVE_ASYNC") != "disposable-vm" {
		t.Skip("explicit bounded disposable VM only")
	}
	db, err := database.New(filepath.Join(t.TempDir(), "cross-jobs.db"))
	liveMust(t, err)
	defer db.Close()
	liveMust(t, db.Migrate())
	liveMust(t, db.InitAsyncSchemaForFixture(t.Context()))
	type owner struct {
		fn      *database.Function
		factory *Factory
		meta    workloadiam.Metadata
		policy  workloadiam.Policy
		replace func() *Factory
	}
	owners := []*owner{}
	initial := []*workloadiam.Evaluator{}
	for _, lang := range []string{"node", "python"} {
		store := newQualificationStore(t)
		rid := sealQualificationRuntime(t, store, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), lang, true)
		defer reclaimQualified(t, store, rid)
		id := ids.New()
		entry, source := asyncSource(lang)
		cid, _ := sealQualificationCode(t, store, id, entry, source, "fixture.txt", "cross-jobs")
		defer reclaimQualified(t, store, cid)
		fn := &database.Function{ID: id, Name: "cross-" + lang, Runtime: lang, Status: "active", AuthMode: "none",
			Entrypoint: entry, CodeHash: digest(source), Version: 1, MemoryMB: 256, CPUs: 1, TimeoutMS: 10000,
			MaxConcurrency: 1, NetworkMode: "none", SDK: true, EnvVars: map[string]string{}}
		liveMust(t, db.InsertFunction(fn))
		factory, err := NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{Artifact: database.WorkloadArtifactRecord{
			OwnerID: id, Generation: ids.New(), Revision: 1, Epoch: 1}, Function: fn.Clone(), Secrets: map[string]string{}},
			store, cid, store, rid, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
		liveMust(t, err)
		defer factory.Close()
		key := "completed"
		policy := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{{
			Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.KVPut},
			Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}},
		}}, Inbound: []workloadiam.Acceptance{}}
		meta := workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: factory.ArtifactRevision()}
		e, err := workloadiam.Compile(meta, policy)
		liveMust(t, err)
		initial = append(initial, e)
		replacement := func() *Factory {
			next, err := NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{Artifact: database.WorkloadArtifactRecord{
				OwnerID: id, Generation: ids.New(), Revision: 2, Epoch: 2}, Function: fn.Clone(), Secrets: map[string]string{}},
				store, cid, store, rid, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
			liveMust(t, err)
			return next
		}
		owners = append(owners, &owner{fn, factory, meta, policy, replacement})
	}
	a, err := workloadiam.NewAuthority(32, initial)
	liveMust(t, err)
	var pm *pool.Manager
	service := &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: func(id string) string { return pm.CurrentArtifact(id) }}
	host := sdkhost.New(db, sdkhost.WithIAM(a), sdkhost.WithAsyncIAM(service),
		sdkhost.WithIAMCurrentArtifact(func(id, artifact string) bool { return pm.IsCurrentArtifact(id, artifact) }))
	jail := filepath.Join(os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "nsjail-linux-amd64")
	verified, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
	liveMust(t, err)
	liveMust(t, verified.Close())
	reg := registry.New(db)
	var deniedJobs atomic.Int64
	pm = pool.NewManager(pool.ManagerConfig{ManagedAuthority: a, DefaultMax: 1, ReapInterval: time.Hour},
		pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/absent", DefaultSeccomp: "default", DataDir: t.TempDir(),
			SDKHandler: func(ctx context.Context, caller sandbox.SDKCaller, op string, args json.RawMessage) (any, *sandbox.SDKError) {
				result, sdkErr := host.Handle(ctx, caller, op, args)
				if op == "jobs.enqueue" && sdkErr != nil && sdkErr.Code == "IAM_DENIED" {
					deniedJobs.Add(1)
				}
				return result, sdkErr
			}}, db, reg, sandbox.NewLimiter(4))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		liveMust(t, pm.Shutdown(ctx))
	}()
	for _, o := range owners {
		liveMust(t, pm.InstallFactory(t.Context(), o.factory))
	}
	authenticator := sdkauth.New()
	p := proxy.New()
	p.Pool, p.DB, p.SDKAuth = pm, db, authenticator
	invoke := &handlers.InvokeHandler{Registry: reg, Proxy: p, DB: db, Metrics: metrics.New(), DataDir: t.TempDir()}
	publish := func(o *owner, target, caller string) {
		t.Helper()
		o.meta.Revision++
		policy := o.policy
		policy.Allow = append([]workloadiam.Statement{}, o.policy.Allow...)
		if target != "" {
			policy.Allow = append(policy.Allow, workloadiam.Statement{Context: workloadiam.Execution,
				Actions:  []workloadiam.Action{workloadiam.JobsEnqueue},
				Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: target}}})
		}
		if caller != "" {
			policy.Inbound = []workloadiam.Acceptance{{CallerID: caller, Triggers: []workloadiam.Trigger{workloadiam.Job}}}
		}
		e, err := workloadiam.Compile(o.meta, policy)
		liveMust(t, err)
		liveMust(t, a.Publish(o.meta.Revision-1, e, func() error { return nil }))
	}
	snapshotKV := func(id string) []byte {
		t.Helper()
		value, err := db.KVGet(id, "completed")
		if err == database.ErrKVNotFound {
			return nil
		}
		liveMust(t, err)
		return append([]byte{}, value.Value...)
	}
	enqueue := func(caller, target *owner, delay time.Duration, allowed bool) *database.AsyncJob {
		t.Helper()
		targetBefore := snapshotKV(target.fn.ID)
		before, err := db.ListJobs("", "", 100)
		liveMust(t, err)
		body, _ := json.Marshal(map[string]any{"target": target.fn.Name, "at": time.Now().Add(delay).UTC().Format(time.RFC3339Nano), "key": ids.New()})
		rec := httptest.NewRecorder()
		invoke.ServeHTTP(rec, httptest.NewRequest("POST", "/fn/"+caller.fn.ID, bytes.NewReader(body)))
		if !allowed {
			if rec.Code == 200 {
				t.Fatal("one-sided SDK enqueue succeeded")
			}
			after, err := db.ListJobs("", "", 100)
			liveMust(t, err)
			if len(after) != len(before) {
				t.Fatal("one-sided denial persisted a job")
			}
			if !bytes.Equal(snapshotKV(target.fn.ID), targetBefore) {
				t.Fatal("one-sided denial executed target")
			}
			return nil
		}
		if rec.Code != 200 {
			t.Fatalf("actual distinct SDK enqueue status=%d", rec.Code)
		}
		var reply struct {
			ID string `json:"id"`
		}
		liveMust(t, json.Unmarshal(rec.Body.Bytes(), &reply))
		j, err := db.GetAsyncJob(t.Context(), reply.ID)
		liveMust(t, err)
		if j.OriginKind != "function" || j.CreatorID != caller.fn.ID || j.FunctionID != target.fn.ID ||
			j.CreatorID == j.FunctionID || j.IdempotencyKey == "" {
			t.Fatal("distinct function provenance lost")
		}
		return j
	}
	waitState := func(id, authority, status string) *database.AsyncJob {
		t.Helper()
		deadline := time.Now().Add(18 * time.Second)
		for time.Now().Before(deadline) {
			j, err := db.GetAsyncJob(t.Context(), id)
			if err == nil && j.AuthorityState == authority && j.Status == status {
				return j
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatalf("job failed to reach %s/%s", authority, status)
		return nil
	}
	s := scheduler.New(db, pm, t.TempDir(), nil, scheduler.WithAsyncIAM(service))
	s.SetSDKAuth(authenticator)
	ctx, cancel := context.WithCancel(t.Context())
	s.Start(ctx)
	defer func() { cancel(); s.Stop(5 * time.Second) }()
	controls := newAsyncControls(t, db, reg, service)
	type browserCase struct {
		CallerID string `json:"caller_id"`
		TargetID string `json:"target_id"`
		Runtime  string `json:"runtime"`
		JobID    string `json:"job_id"`
	}
	cases := []browserCase{}
	for i, caller := range owners {
		target := owners[1-i]
		t.Run(caller.fn.Runtime+"-to-"+target.fn.Runtime, func(t *testing.T) {
			callerBefore := snapshotKV(caller.fn.ID)
			publish(caller, target.fn.ID, "")
			publish(target, "", "")
			enqueue(caller, target, 0, false)
			publish(caller, "", "")
			publish(target, "", caller.fn.ID)
			enqueue(caller, target, 0, false)
			publish(caller, target.fn.ID, "")
			j := enqueue(caller, target, 2*time.Second, true)
			done := waitState(j.ID, "active", "succeeded")
			if done.Attempts != 1 || done.CreatorID != caller.fn.ID || done.IdempotencyKey != j.IdempotencyKey {
				t.Fatal("dispatch changed origin/raw key or attempts")
			}
			value, err := db.KVGet(target.fn.ID, "completed")
			liveMust(t, err)
			var completed string
			liveMust(t, json.Unmarshal(value.Value, &completed))
			if completed != j.ID {
				t.Fatal("target SDK wrote the wrong namespace/job")
			}
			if !bytes.Equal(snapshotKV(caller.fn.ID), callerBefore) {
				t.Fatal("cross-runtime target wrote caller namespace")
			}
			held, err := pm.Acquire(t.Context(), target.fn.ID)
			liveMust(t, err)
			waiting := enqueue(caller, target, 0, true)
			waitState(waiting.ID, "active", "running")
			publish(caller, "", "")
			pm.Release(held, nil)
			blocked := waitState(waiting.ID, "suspended", "pending")
			if blocked.Attempts != 0 {
				t.Fatal("cross-runtime pool wait revoke spent attempt")
			}
			value, err = db.KVGet(target.fn.ID, "completed")
			liveMust(t, err)
			liveMust(t, json.Unmarshal(value.Value, &completed))
			if completed != j.ID {
				t.Fatal("revoked waiting target executed")
			}
			publish(caller, target.fn.ID, "")
			time.Sleep(6 * time.Second)
			waitState(waiting.ID, "suspended", "pending")
			controls.denyChecks(t, blocked)
			if i == 0 {
				controls.cli(t, "admin", waiting.ID, "resume", true)
			} else {
				reply := controls.rpc(t, "admin", "tools/call", map[string]any{"name": "resume_job", "arguments": map[string]string{"id": waiting.ID}})
				if len(reply.Error) != 0 || reply.Result.IsError {
					t.Fatal("admin MCP cross-runtime resume failed")
				}
			}
			resumed := waitState(waiting.ID, "active", "succeeded")
			if resumed.CreatorID != caller.fn.ID || resumed.OriginKind != "function" || resumed.IdempotencyKey != waiting.IdempotencyKey {
				t.Fatal("resume changed immutable origin/raw key")
			}
			if !bytes.Equal(snapshotKV(caller.fn.ID), callerBefore) {
				t.Fatal("resumed target changed caller namespace")
			}
			t.Log("distinct real SDK, both-sided admission, target namespace, occupied-pool revoke, zero attempts, no replay, CLI/MCP resume and immutable origin/raw key PASS")
		})
	}
	for i, caller := range owners {
		target := owners[1-i]
		publish(caller, target.fn.ID, target.fn.ID)
		publish(target, caller.fn.ID, caller.fn.ID)
		held, err := pm.Acquire(t.Context(), caller.fn.ID)
		liveMust(t, err)
		before, err := db.ListJobs("", "", 100)
		liveMust(t, err)
		deniedBefore := deniedJobs.Load()
		replacement := caller.replace()
		defer replacement.Close()
		if replacement.ArtifactRevision() == caller.meta.ApprovedArtifact {
			t.Fatal("replacement did not change artifact identity")
		}
		liveMust(t, pm.InstallFactory(t.Context(), replacement))
		body, _ := json.Marshal(map[string]any{"target": target.fn.Name, "key": ids.New()})
		event, _ := json.Marshal(map[string]any{"method": "POST", "path": "/", "headers": map[string]string{}, "body": string(body)})
		_, _ = held.Worker.DispatchEx(sandbox.WithExecution(t.Context(), sandbox.ExecInfo{ExecutionID: ids.New()}), event)
		pm.Release(held, nil)
		after, err := db.ListJobs("", "", 100)
		liveMust(t, err)
		if deniedJobs.Load() != deniedBefore+1 || len(after) != len(before) {
			t.Fatal("real retained old worker did not deny jobs enqueue without inserting a row")
		}
		caller.factory = replacement
		caller.meta.ApprovedArtifact = replacement.ArtifactRevision()
		publish(caller, target.fn.ID, target.fn.ID)
		t.Log(caller.fn.Runtime + " real held old-worker jobs.enqueue denied after Factory replacement with retained old policy; no row")
	}
	// Prepare one actual suspended function-origin row per direction for browser Resume.
	for i, caller := range owners {
		target := owners[1-i]
		publish(caller, target.fn.ID, "")
		publish(target, "", caller.fn.ID)
		j := enqueue(caller, target, 2*time.Second, true)
		publish(caller, "", "")
		waitState(j.ID, "suspended", "pending")
		publish(caller, target.fn.ID, "")
		cases = append(cases, browserCase{caller.fn.ID, target.fn.ID, target.fn.Runtime, j.ID})
	}
	// Preserve both sides while the two browser-resume cases are pending.
	for i, o := range owners {
		publish(o, owners[1-i].fn.ID, owners[1-i].fn.ID)
	}
	legacy := &database.Job{FunctionID: owners[0].fn.ID, Payload: []byte(`{}`), ScheduledAt: time.Now().UTC().Add(time.Hour)}
	liveMust(t, db.EnqueueJob(legacy))
	q, err := db.GetAsyncJob(t.Context(), legacy.ID)
	liveMust(t, err)
	if q.OriginKind != "legacy_unknown" || q.AuthorityState != "quarantined" {
		t.Fatal("historical unknown row was not actually quarantined")
	}
	if os.Getenv("ORVA_ASYNC_BROWSER_HOLD") == "1" {
		evidence := os.Getenv("ORVA_ASYNC_EVIDENCE")
		credentials, _ := json.Marshal(controls.keys)
		liveMust(t, os.WriteFile(filepath.Join(evidence, "credentials.json"), credentials, 0600))
		ready, _ := json.Marshal(map[string]any{"backend": controls.server.URL, "cases": cases, "quarantined_job": q.ID})
		liveMust(t, os.WriteFile(filepath.Join(evidence, "ready.json"), ready, 0600))
		deadline := time.Now().Add(6 * time.Minute)
		for {
			if _, err := os.Stat(filepath.Join(evidence, "stop")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("bounded browser hold expired")
			}
			time.Sleep(100 * time.Millisecond)
		}
		for _, c := range cases {
			j := waitState(c.JobID, "active", "succeeded")
			value, err := db.KVGet(c.TargetID, "completed")
			liveMust(t, err)
			var id string
			liveMust(t, json.Unmarshal(value.Value, &id))
			if j.CreatorID != c.CallerID || j.OriginKind != "function" || id != j.ID {
				t.Fatal("browser-resumed target/origin/SDK effect mismatch")
			}
		}
		t.Log("real authenticated browser resumed both distinct function-origin targets; SDK target effects verified")
	}
}
