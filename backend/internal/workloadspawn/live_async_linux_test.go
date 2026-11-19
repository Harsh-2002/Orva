//go:build linux

package workloadspawn_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func asyncSource(lang string) (string, string) {
	if lang == "node" {
		return "handler.js", `
const {jobs,kv}=require('orva');
exports.handler=async e=>{
 const x=JSON.parse(e.body||'{}');
 if(e.headers['x-orva-trigger']==='job'){
   await kv.put('completed',e.headers['x-orva-job-id']);
   return {statusCode:200,body:'done'};
 }
 const j=await jobs.enqueue(x.target,{}, {scheduledAt:x.at,idempotencyKey:x.key});
 return {statusCode:200,body:JSON.stringify(j)};
};`
	}
	return "handler.py", `
import json
from orva import jobs,kv
def handler(e):
    x=json.loads(e.get("body") or "{}")
    if e["headers"].get("x-orva-trigger")=="job":
        kv.put("completed",e["headers"]["x-orva-job-id"])
        return {"statusCode":200,"body":"done"}
    j=jobs.enqueue(x["target"],{},scheduled_at=x.get("at"),idempotency_key=x.get("key"))
    return {"statusCode":200,"body":json.dumps(j)}
`
}

func TestLiveAsyncJobs(t *testing.T) {
	if os.Getenv("ORVA_LIVE_ASYNC") != "disposable-vm" {
		t.Skip("explicit bounded disposable VM only")
	}
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			db, err := database.New(filepath.Join(t.TempDir(), "async.db"))
			liveMust(t, err)
			defer db.Close()
			liveMust(t, db.Migrate())
			liveMust(t, db.InitAsyncSchemaForFixture(t.Context()))
			store := newQualificationStore(t)
			rid := sealQualificationRuntime(t, store, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), lang, true)
			defer reclaimQualified(t, store, rid)
			id := ids.New()
			entry, source := asyncSource(lang)
			cid, _ := sealQualificationCode(t, store, id, entry, source, "fixture.txt", "async")
			defer reclaimQualified(t, store, cid)
			fn := &database.Function{ID: id, Name: "async-" + lang, Runtime: lang, Status: "active", AuthMode: "none",
				Entrypoint: entry, CodeHash: digest(source), Version: 1, MemoryMB: 256, CPUs: 1, TimeoutMS: 10000,
				MaxConcurrency: 1, NetworkMode: "none", SDK: true, EnvVars: map[string]string{}}
			liveMust(t, db.InsertFunction(fn))
			factory, err := NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{Artifact: database.WorkloadArtifactRecord{OwnerID: id,
				Generation: ids.New(), Revision: 1, Epoch: 1}, Function: fn.Clone(), Secrets: map[string]string{}}, store, cid, store, rid,
				FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
			liveMust(t, err)
			defer factory.Close()
			key := "completed"
			policy := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{
				{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.JobsEnqueue}, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: id}}},
				{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.KVPut}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}},
			}, Inbound: []workloadiam.Acceptance{{CallerID: id, Triggers: []workloadiam.Trigger{workloadiam.Job}}}}
			meta := workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: factory.ArtifactRevision()}
			e, err := workloadiam.Compile(meta, policy)
			liveMust(t, err)
			a, err := workloadiam.NewAuthority(16, []*workloadiam.Evaluator{e})
			liveMust(t, err)
			var pm *pool.Manager
			service := &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: func(owner string) string { return pm.CurrentArtifact(owner) }}
			host := sdkhost.New(db, sdkhost.WithIAM(a), sdkhost.WithAsyncIAM(service),
				sdkhost.WithIAMCurrentArtifact(func(owner, artifact string) bool { return pm.IsCurrentArtifact(owner, artifact) }))
			jail := filepath.Join(os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "nsjail-linux-amd64")
			verified, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
			liveMust(t, err)
			liveMust(t, verified.Close())
			reg := registry.New(db)
			pm = pool.NewManager(pool.ManagerConfig{ManagedAuthority: a, DefaultMax: 1, ReapInterval: time.Hour},
				pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/absent", DefaultSeccomp: "default", DataDir: t.TempDir(), SDKHandler: host.Handle}, db, reg, sandbox.NewLimiter(4))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				liveMust(t, pm.Shutdown(ctx))
			}()
			liveMust(t, pm.InstallFactory(t.Context(), factory))
			authenticator := sdkauth.New()
			p := proxy.New()
			p.Pool = pm
			p.DB = db
			p.SDKAuth = authenticator
			invoke := &handlers.InvokeHandler{Registry: reg, Proxy: p, DB: db, Metrics: metrics.New(), DataDir: t.TempDir()}
			enqueue := func(delay time.Duration) *database.AsyncJob {
				t.Helper()
				body, _ := json.Marshal(map[string]any{"target": fn.Name, "at": time.Now().Add(delay).UTC().Format(time.RFC3339Nano), "key": ids.New()})
				req := httptest.NewRequest("POST", "/fn/"+id, bytes.NewReader(body))
				rec := httptest.NewRecorder()
				invoke.ServeHTTP(rec, req)
				if rec.Code != 200 {
					t.Fatalf("real SDK enqueue HTTP status %d", rec.Code)
				}
				var reply struct {
					ID string `json:"id"`
				}
				liveMust(t, json.Unmarshal(rec.Body.Bytes(), &reply))
				j, err := db.GetAsyncJob(t.Context(), reply.ID)
				liveMust(t, err)
				if j.OriginKind != "function" || j.CreatorID != id || j.FunctionID != id || j.CreationCallerRevision == 0 {
					t.Fatal("SDK origin not host-bound")
				}
				return j
			}
			setGrant := func(grant bool) {
				t.Helper()
				meta.Revision++
				next := policy
				if !grant {
					next.Allow = next.Allow[1:]
					next.Inbound = []workloadiam.Acceptance{}
				}
				e, err := workloadiam.Compile(meta, next)
				liveMust(t, err)
				liveMust(t, a.Publish(meta.Revision-1, e, func() error { return nil }))
			}
			waitState := func(id, state, status string) *database.AsyncJob {
				t.Helper()
				deadline := time.Now().Add(16 * time.Second)
				for time.Now().Before(deadline) {
					j, err := db.GetAsyncJob(t.Context(), id)
					if err == nil && j.AuthorityState == state && j.Status == status {
						return j
					}
					time.Sleep(50 * time.Millisecond)
				}
				j, _ := db.GetAsyncJob(t.Context(), id)
				t.Fatalf("job did not reach %s/%s; got %+v", state, status, j)
				return nil
			}
			j := enqueue(7 * time.Second)
			setGrant(false)
			s := scheduler.New(db, pm, t.TempDir(), nil, scheduler.WithAsyncIAM(service))
			s.SetSDKAuth(authenticator)
			ctx, cancel := context.WithCancel(t.Context())
			s.Start(ctx)
			defer func() { cancel(); s.Stop(5 * time.Second) }()
			suspended := waitState(j.ID, "suspended", "pending")
			if suspended.Attempts != 0 {
				t.Fatal("denial consumed an attempt")
			}
			setGrant(true)
			time.Sleep(6 * time.Second)
			suspended = waitState(j.ID, "suspended", "pending")
			controls := newAsyncControls(t, db, reg, service)
			controls.denyChecks(t, suspended)
			controls.cli(t, "admin", j.ID, "resume", true)
			done := waitState(j.ID, "active", "succeeded")
			if done.Attempts != 1 {
				t.Fatal("wrong admitted attempts", done.Attempts)
			}
			value, err := db.KVGet(id, "completed")
			liveMust(t, err)
			var completed string
			liveMust(t, json.Unmarshal(value.Value, &completed))
			if completed != j.ID {
				t.Fatal("managed target SDK did not observe actual job id")
			}
			// Hold the real only worker so revocation occurs after claim/pool wait.
			waiting := enqueue(0)
			acq, err := pm.Acquire(t.Context(), id)
			liveMust(t, err)
			defer func() {
				if acq != nil {
					pm.Release(acq, nil)
				}
			}()
			waitState(waiting.ID, "active", "running")
			setGrant(false)
			pm.Release(acq, nil)
			acq = nil
			blocked := waitState(waiting.ID, "suspended", "pending")
			if blocked.Attempts != 0 {
				t.Fatal("revoked pool wait spent attempt")
			}
			value, err = db.KVGet(id, "completed")
			liveMust(t, err)
			liveMust(t, json.Unmarshal(value.Value, &completed))
			if completed != j.ID {
				t.Fatal("revoked queued job dispatched")
			}
			setGrant(true)
			time.Sleep(6 * time.Second)
			waitState(waiting.ID, "suspended", "pending")
			reply := controls.rpc(t, "admin", "tools/call", map[string]any{"name": "resume_job", "arguments": map[string]string{"id": waiting.ID}})
			if len(reply.Error) > 0 || reply.Result.IsError {
				t.Fatal("external MCP admin resume failed")
			}
			waitState(waiting.ID, "active", "succeeded")
			t.Log("real private SDK enqueue -> SQLite delayed job -> revoke/suspend -> regrant stays suspended -> actual admin CLI resume -> managed pool SDK target PASS; real pool wait revoke/no attempt and external MCP admin resume PASS; reader/write REST/CLI/MCP resume denied")
		})
	}
}
