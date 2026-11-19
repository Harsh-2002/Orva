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

func cronSource(lang string) (string, string) {
	if lang == "node" {
		return "handler.js", `
const {crons,kv}=require('orva');
exports.handler=async e=>{
 const x=JSON.parse(e.body||'{}');
 if(e.headers['x-orva-trigger']==='cron'){
   const count=await kv.incr('count',1);
   await kv.put('completed',{id:e.headers['x-orva-cron-id'],tag:x.tag,count});
   if(x.mode==='crash') process.exit(17);
   if(x.mode==='throw') throw new Error('fixture cron failure');
   return {statusCode:x.mode==='500'?500:200,body:'done'};
 }
 const c=await crons.upsert(x.name,x.schedule,{payload:x.payload});
 return {statusCode:200,body:JSON.stringify(c)};
};`
	}
	return "handler.py", `
import json, os
from orva import crons,kv
def handler(e):
    x=json.loads(e.get("body") or "{}")
    if e["headers"].get("x-orva-trigger")=="cron":
        count=kv.incr("count",1)
        kv.put("completed",{"id":e["headers"]["x-orva-cron-id"],"tag":x.get("tag"),"count":count})
        if x.get("mode")=="crash":
            os._exit(17)
        if x.get("mode")=="throw":
            raise ValueError("fixture cron failure")
        return {"statusCode":500 if x.get("mode")=="500" else 200,"body":"done"}
    c=crons.upsert(x["name"],x["schedule"],payload=x.get("payload"))
    return {"statusCode":200,"body":json.dumps(c)}
`
}

func TestLiveCronIAM(t *testing.T) {
	if os.Getenv("ORVA_LIVE_CRON") != "disposable-vm" {
		t.Skip("explicit bounded disposable VM only")
	}
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			db, err := database.New(filepath.Join(t.TempDir(), "cron.db"))
			liveMust(t, err)
			defer db.Close()
			liveMust(t, db.Migrate())
			liveMust(t, db.InitCronSchemaForFixture(t.Context()))
			store := newQualificationStore(t)
			rid := sealQualificationRuntime(t, store, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), lang, true)
			defer reclaimQualified(t, store, rid)
			id := ids.New()
			entry, source := cronSource(lang)
			cid, _ := sealQualificationCode(t, store, id, entry, source, "fixture.txt", "cron")
			defer reclaimQualified(t, store, cid)
			fn := &database.Function{ID: id, Name: "cron-" + lang, Runtime: lang, Status: "active", AuthMode: "none", Entrypoint: entry, CodeHash: digest(source), Version: 1,
				MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, MaxConcurrency: 1, NetworkMode: "none", SDK: true, EnvVars: map[string]string{}}
			liveMust(t, db.InsertFunction(fn))
			factory, err := NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{Artifact: database.WorkloadArtifactRecord{OwnerID: id, Generation: ids.New(), Revision: 1, Epoch: 1},
				Function: fn.Clone(), Secrets: map[string]string{}}, store, cid, store, rid, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
			liveMust(t, err)
			defer factory.Close()
			name, countKey, completed := "daily.one", "count", "completed"
			policy := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{
				{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.CronsUpsert}, Resource: workloadiam.Resource{OwnCron: &workloadiam.CronResource{Name: &name, TargetID: id}}},
				{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.KVIncr, workloadiam.KVGet}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &countKey}}},
				{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.KVPut}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &completed}}},
			}, Inbound: []workloadiam.Acceptance{{CallerID: id, Triggers: []workloadiam.Trigger{workloadiam.Cron}}}}
			meta := workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: factory.ArtifactRevision()}
			e, err := workloadiam.Compile(meta, policy)
			liveMust(t, err)
			a, err := workloadiam.NewAuthority(16, []*workloadiam.Evaluator{e})
			liveMust(t, err)
			var pm *pool.Manager
			service := &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: func(owner string) string { return pm.CurrentArtifact(owner) }}
			host := sdkhost.New(db, sdkhost.WithIAM(a), sdkhost.WithCronIAM(service), sdkhost.WithIAMCurrentArtifact(func(owner, artifact string) bool { return pm.IsCurrentArtifact(owner, artifact) }))
			jail := filepath.Join(os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "nsjail-linux-amd64")
			verified, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
			liveMust(t, err)
			liveMust(t, verified.Close())
			reg := registry.New(db)
			limiter := sandbox.NewLimiter(4)
			pm = pool.NewManager(pool.ManagerConfig{ManagedAuthority: a, DefaultMax: 1, ReapInterval: time.Hour},
				pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/absent", DefaultSeccomp: "default", DataDir: t.TempDir(), SDKHandler: host.Handle}, db, reg, limiter)
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
			payload, _ := json.Marshal(map[string]any{"name": name, "schedule": "@every 2s", "payload": map[string]any{"tag": "initial"}})
			req := httptest.NewRequest("POST", "/fn/"+id, bytes.NewReader(payload))
			rec := httptest.NewRecorder()
			invoke.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("real SDK upsert HTTP %d", rec.Code)
			}
			var reply struct {
				ID string `json:"id"`
			}
			liveMust(t, json.Unmarshal(rec.Body.Bytes(), &reply))
			get := func() *database.AsyncCron {
				t.Helper()
				c, err := db.GetAsyncCron(t.Context(), reply.ID)
				liveMust(t, err)
				return c
			}
			c := get()
			if c.OriginKind != "function" || c.CreatorID != id || c.FunctionID != id || c.CreationCallerRevision != 1 || c.ConfigRevision != 1 {
				t.Fatal("real SDK origin not host-bound")
			}
			admin := database.OperatorCredentialReference{Kind: "api_key", ID: ids.New()}
			liveMust(t, db.InsertAPIKey(&database.APIKey{ID: admin.ID, Name: "cron-admin", KeyHash: "fixture-tokenless-admin", Prefix: "fixture", Permissions: `["admin"]`}))
			readCount := func() int64 {
				v, err := db.KVGet(id, "count")
				if err != nil {
					return 0
				}
				var n int64
				liveMust(t, json.Unmarshal(v.Value, &n))
				return n
			}
			wait := func(what string, predicate func() bool) {
				t.Helper()
				deadline := time.Now().Add(12 * time.Second)
				for time.Now().Before(deadline) {
					if predicate() {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("timed out: " + what)
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
			mutate := func(action string, edit func(*database.AsyncCron)) {
				t.Helper()
				c := get()
				if edit != nil {
					edit(c)
				}
				liveMust(t, service.MutateCron(t.Context(), admin, c, action))
			}
			var controls *cronControlSurfaces
			if os.Getenv("ORVA_LIVE_CRON_CONTROLS") == "disposable-vm" {
				controls = newCronControlSurfaces(t, db, reg, service, id)
				liveMust(t, db.InsertCronSchedule(&database.CronSchedule{FunctionID: id, Name: "historic-unknown", CronExpr: "@daily", Timezone: "UTC", Payload: "{}"}))
			}
			s := scheduler.New(db, pm, t.TempDir(), nil, scheduler.WithCronIAM(service), scheduler.WithCronPollIntervalForFixture(25*time.Millisecond))
			s.SetSDKAuth(authenticator)
			ctx, cancel := context.WithCancel(t.Context())
			s.Start(ctx)
			stopped := false
			defer func() {
				if !stopped {
					cancel()
					s.Stop(5 * time.Second)
				}
			}()
			wait("first actual cron SDK write", func() bool { return get().LastStatus == "ok" && readCount() >= 1 })
			value, err := db.KVGet(id, "completed")
			liveMust(t, err)
			var result struct{ ID, Tag string }
			liveMust(t, json.Unmarshal(value.Value, &result))
			if result.ID != reply.ID || result.Tag != "initial" {
				t.Fatal("actual target SDK saw wrong cron/payload")
			}
			mutate("disable", nil)
			initialCount := readCount()

			queued := func() bool {
				for _, p := range pm.Stats() {
					if p.FunctionID == id && p.Queued > 0 {
						return true
					}
				}
				return false
			}
			idle := func() bool {
				for _, p := range pm.Stats() {
					if p.FunctionID == id {
						return p.Busy == 0 && p.Queued == 0
					}
				}
				return false
			}
			hold := func() func() {
				t.Helper()
				acq, err := pm.Acquire(t.Context(), id)
				liveMust(t, err)
				released := false
				release := func() {
					if !released {
						released = true
						pm.Release(acq, nil)
					}
				}
				t.Cleanup(release)
				return release
			}
			release := hold()
			mutate("update", func(c *database.AsyncCron) { c.Enabled = true })
			wait("actual occupied-pool cron wait", queued)
			active, _ := limiter.Stats()
			if active != 1 {
				t.Fatalf("waiting cron occupied host execution limiter: %d", active)
			}
			setGrant(false)
			release()
			wait("revocation suspension", func() bool { return get().AuthorityState == "suspended" && !queued() })
			blocked := get()
			if readCount() != initialCount {
				t.Fatal("revoked wait dispatched child")
			}
			setGrant(true)
			time.Sleep(2200 * time.Millisecond)
			if get().AuthorityState != "suspended" || readCount() != initialCount {
				t.Fatal("regrant replayed cron")
			}
			before := time.Now().UTC()
			if controls != nil {
				controls.negativeChecks(t, get())
				controls.resume(t, get(), "cli")
			} else {
				mutate("resume", nil)
			}
			resumed := get()
			if resumed.NextRunAt == nil || !resumed.NextRunAt.After(before) || resumed.CreationCallerRevision != 1 || resumed.AdmissionCallerRevision != 3 {
				t.Fatal("resume lost provenance or scheduled catchup")
			}
			until := time.Until(*resumed.NextRunAt) - 50*time.Millisecond
			if until > 0 {
				time.Sleep(until)
			}
			if readCount() != initialCount {
				t.Fatal("resume ran before next future scheduled time")
			}
			wait("future resumed cron", func() bool {
				return readCount() > initialCount && get().LastStatus == "ok" && get().LastRunAt.After(*blocked.LastRunAt) && idle()
			})
			mutate("disable", nil)

			if controls != nil {
				for _, surface := range []string{"mcp", "ai", "phone", "desktop"} {
					mutate("update", func(c *database.AsyncCron) { c.Enabled = true; c.CronExpr = "@every 2s" })
					priorCount := readCount()
					setGrant(false)
					wait("public control suspension", func() bool { return get().AuthorityState == "suspended" })
					setGrant(true)
					time.Sleep(100 * time.Millisecond)
					if get().AuthorityState != "suspended" || readCount() != priorCount {
						t.Fatal("public control regrant replayed")
					}
					if surface == "phone" || surface == "desktop" {
						controls.browser(t, get(), surface)
						wait("browser future SDK write", func() bool { return readCount() > priorCount && idle() })
					} else {
						controls.resume(t, get(), surface)
						next := get().NextRunAt
						if next == nil || !next.After(time.Now()) {
							t.Fatal("public resume did not select strict future")
						}
						if delay := time.Until(*next) - 50*time.Millisecond; delay > 0 {
							time.Sleep(delay)
						}
						if readCount() != priorCount {
							t.Fatal("public resume caught up before future tick")
						}
						wait("public "+surface+" future fire", func() bool { return readCount() > priorCount && idle() })
					}
					mutate("disable", nil)
				}
				t.Log("PASS: compiled admin-only CLI, external MCP and shared AI registry resume; forced read/write denials; stale HTTP 409; actual production CronJobs/ConfirmDialog browser route at 390 night and 1440 day")
			}

			for _, action := range []string{"update", "disable"} {
				release := hold()
				mutate("update", func(c *database.AsyncCron) { c.Enabled = true; c.CronExpr = "@every 2s" })
				wait("stale "+action+" pool wait", queued)
				countBefore := readCount()
				before := get()
				mutate(action, func(c *database.AsyncCron) {
					if action == "update" {
						c.CronExpr = "@hourly"
						c.Payload = `{"tag":"edited"}`
					}
				})
				release()
				wait("stale "+action+" dequeue", func() bool { return !queued() })
				time.Sleep(150 * time.Millisecond)
				after := get()
				if readCount() != countBefore || !after.LastRunAt.Equal(*before.LastRunAt) || after.ConfigRevision <= before.ConfigRevision {
					t.Fatal("stale queued " + action + " dispatched or overwrote result")
				}
				mutate("disable", nil)
			}
			for _, mode := range []string{"500", "throw", "crash"} {
				prior := get().LastRunAt
				countBefore := readCount()
				mutate("update", func(c *database.AsyncCron) {
					c.Enabled = true
					c.CronExpr = "@every 1s"
					c.Payload = `{"mode":"` + mode + `"}`
				})
				wait("actual "+mode+" result", func() bool {
					c := get()
					return c.LastStatus == "failed" && c.LastRunAt.After(*prior) && idle() && readCount() > countBefore
				})
				if mode == "crash" && get().LastError == "function returned 500" {
					t.Fatal("worker exit did not exercise dispatch-error path")
				}
				mutate("disable", nil)
			}
			cancel()
			s.Stop(5 * time.Second)
			stopped = true
			wait("writer drain", func() bool { w := db.WriterStats(); return w.CriticalDepth == 0 && w.CriticalBytes == 0 })
			if db.WriterStats().CriticalFailures != 0 {
				t.Fatal("critical completion writer failed")
			}
			reserveCtx, cancelReserve := context.WithTimeout(t.Context(), time.Second)
			defer cancelReserve()
			leases := []*database.ExecutionLease{}
			defer func() {
				for _, l := range leases {
					l.Cancel()
				}
			}()
			for range db.WriterStats().CriticalCap {
				l, err := db.ReserveExecution(reserveCtx)
				liveMust(t, err)
				leases = append(leases, l)
			}
			t.Log("PASS: real leased SDK self-upsert -> durable cron -> Scheduler -> current managed target SDK write; actual pool-wait revocation; regrant remains suspended; admin-only service resume next-future/no catchup; queued edit/disable CAS denial; 500/throw/worker-exit completion paths; all critical completion slots reclaimed")
		})
	}
}
