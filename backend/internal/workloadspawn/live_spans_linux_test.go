//go:build linux

package workloadspawn_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/proxy"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/trace"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestLiveIAMSpans(t *testing.T) {
	if os.Getenv("ORVA_LIVE_SPANS") != "disposable-vm" {
		t.Skip("explicit disposable VM only")
	}
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			db, err := database.New(filepath.Join(t.TempDir(), "spans.db"))
			liveMust(t, err)
			defer db.Close()
			liveMust(t, db.Migrate())
			store := newQualificationStore(t)
			rid := sealQualificationRuntime(t, store, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), lang, true)
			defer reclaimQualified(t, store, rid)
			id := ids.New()
			entry, source := "handler.js", `
const orva=require('orva');
const boot=orva.trace.span('bootstrap', async()=>{});
exports.handler=async e=>{
 await boot;
 const x=JSON.parse(e.body||'{}');
 await orva.trace.span(x.name,async()=>{}, {synthetic:true});
 return {statusCode:200,body:'ok'};
};`
			if lang == "python" {
				entry, source = "handler.py", `
import json
from orva import trace
with trace.span("bootstrap"):
    pass
def handler(e):
    with trace.span(json.loads(e.get("body") or "{}")["name"], attributes={"synthetic":True}):
        pass
    return {"statusCode":200,"body":"ok"}
`
			}
			cid, _ := sealQualificationCode(t, store, id, entry, source, "fixture.txt", "spans")
			defer reclaimQualified(t, store, cid)
			fn := &database.Function{ID: id, Name: "spans-" + lang, Runtime: lang, Status: "active", AuthMode: "none", Entrypoint: entry, CodeHash: digest(source), Version: 1,
				MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, MaxConcurrency: 1, NetworkMode: "none", SDK: true, EnvVars: map[string]string{}}
			reg := registry.New(db)
			liveMust(t, reg.Set(fn))
			makeFactory := func(rev uint64) *Factory {
				f, err := NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{Artifact: database.WorkloadArtifactRecord{OwnerID: id, Generation: ids.New(), Revision: rev, Epoch: 1}, Function: fn.Clone(), Secrets: map[string]string{}},
					store, cid, store, rid, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
				liveMust(t, err)
				return f
			}
			factory := makeFactory(1)
			defer factory.Close()
			meta := workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: factory.ArtifactRevision()}
			policy := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}}
			e, err := workloadiam.Compile(meta, policy)
			liveMust(t, err)
			a, err := workloadiam.NewAuthority(16, []*workloadiam.Evaluator{e})
			liveMust(t, err)
			var pm *pool.Manager
			host := sdkhost.New(db, sdkhost.WithIAM(a), sdkhost.WithIAMCurrentArtifact(func(owner, artifact string) bool { return pm.IsCurrentArtifact(owner, artifact) }))
			type observation struct {
				name   string
				status int
				caller sandbox.SDKCaller
			}
			var mu sync.Mutex
			var observed []observation
			retained, release := make(chan struct{}), make(chan struct{})
			bootstrapDone := make(chan struct{})
			var bootstrapOnce sync.Once
			var once sync.Once
			defer once.Do(func() { close(release) })
			jail := filepath.Join(os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "nsjail-linux-amd64")
			verified, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
			liveMust(t, err)
			liveMust(t, verified.Close())
			pm = pool.NewManager(pool.ManagerConfig{ManagedAuthority: a, DefaultMax: 1, ReapInterval: time.Hour},
				pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/absent", DefaultSeccomp: "default", DataDir: t.TempDir(),
					SDKHandler: func(ctx context.Context, c sandbox.SDKCaller, op string, raw json.RawMessage) (any, *sandbox.SDKError) {
						var req struct {
							Name string `json:"name"`
						}
						_ = json.Unmarshal(raw, &req)
						if req.Name == "retained" {
							close(retained)
							select {
							case <-release:
							case <-ctx.Done():
							}
						}
						result, err := host.Handle(ctx, c, op, raw)
						status := 200
						if err != nil {
							status = err.Status
						}
						mu.Lock()
						observed = append(observed, observation{req.Name, status, c})
						mu.Unlock()
						if req.Name == "bootstrap" {
							bootstrapOnce.Do(func() { close(bootstrapDone) })
						}
						return result, err
					}}, db, reg, sandbox.NewLimiter(4))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				liveMust(t, pm.Shutdown(ctx))
			}()
			liveMust(t, pm.InstallFactory(t.Context(), factory))
			warm, err := pm.Acquire(t.Context(), id)
			liveMust(t, err)
			select {
			case <-bootstrapDone:
			case <-time.After(5 * time.Second):
				t.Fatal("bootstrap emission missing")
			}
			pm.Release(warm, nil)
			p := proxy.New()
			p.Pool = pm
			p.DB = db
			p.SDKAuth = sdkauth.New()
			invoke := &handlers.InvokeHandler{Registry: reg, Proxy: p, DB: db, Metrics: metrics.New(), DataDir: t.TempDir()}
			request := func(name string) *httptest.ResponseRecorder {
				r := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/fn/"+id, bytes.NewBufferString(`{"name":"`+name+`"}`))
				req = req.WithContext(trace.WithTraceID(req.Context(), trace.NewTraceID()))
				invoke.ServeHTTP(r, req)
				return r
			}
			check := func(name string, status int) observation {
				t.Helper()
				mu.Lock()
				defer mu.Unlock()
				for _, o := range observed {
					if o.name == name {
						if o.status != status {
							t.Fatalf("%s SDK status=%d want=%d", name, o.status, status)
						}
						return o
					}
				}
				t.Fatalf("no actual SDK emission for %s", name)
				return observation{}
			}
			setGrant := func(allow bool) {
				meta.Revision++
				policy.Allow = []workloadiam.Statement{}
				if allow {
					policy.Allow = append(policy.Allow, workloadiam.Statement{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.SpansEmit}, Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}})
				}
				next, err := workloadiam.Compile(meta, policy)
				liveMust(t, err)
				liveMust(t, a.Publish(meta.Revision-1, next, func() error { return nil }))
			}
			if request("denied").Code != 200 {
				t.Fatal("public invoke failed")
			}
			check("bootstrap", 403)
			check("denied", 403)
			setGrant(true)
			response := request("granted")
			if response.Code != 200 {
				t.Fatal("granted public invoke failed")
			}
			accepted := check("granted", 200)
			x, ok := accepted.caller.WorkloadExecution()
			if !ok || x.ExecutionID != response.Header().Get("X-Orva-Execution-ID") {
				t.Fatal("host execution mismatch")
			}
			if _, err := host.Handle(t.Context(), accepted.caller, "spans.emit", json.RawMessage(`{"name":"stale"}`)); err == nil || err.Status != 403 {
				t.Fatal("ended dispatch lease admitted")
			}
			setGrant(false)
			if request("revoked").Code != 200 {
				t.Fatal("revoke public invoke failed")
			}
			check("revoked", 403)
			setGrant(true)
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() { finished <- request("retained") }()
			select {
			case <-retained:
			case <-time.After(10 * time.Second):
				t.Fatal("retained worker did not emit")
			}
			replacement := makeFactory(2)
			defer replacement.Close()
			if replacement.ArtifactRevision() == meta.ApprovedArtifact {
				t.Fatal("replacement not distinct")
			}
			liveMust(t, pm.InstallFactory(t.Context(), replacement))
			once.Do(func() { close(release) })
			select {
			case r := <-finished:
				if r.Code != 200 {
					t.Fatal("retained invoke failed")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("retained invoke blocked")
			}
			check("retained", 403)
			deadline := time.Now().Add(5 * time.Second)
			for {
				rows, err := db.ListUserSpansByExecution(x.ExecutionID)
				liveMust(t, err)
				if len(rows) == 1 && db.WriterStats().TelemetryBytes == 0 {
					s := rows[0]
					if s.Name != "granted" || s.TraceID != x.TraceID || s.ParentSpanID != x.SpanID || s.ExecutionID != x.ExecutionID {
						t.Fatal("DB span lost host attribution")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("admitted span did not persist")
				}
				time.Sleep(20 * time.Millisecond)
			}
			// Inspect every actual dispatch, not just the successful execution.
			mu.Lock()
			copyObserved := append([]observation(nil), observed...)
			mu.Unlock()
			for _, o := range copyObserved {
				if o.name == "granted" {
					continue
				}
				info, ok := o.caller.WorkloadExecution()
				if ok {
					rows, err := db.ListUserSpansByExecution(info.ExecutionID)
					liveMust(t, err)
					if len(rows) != 0 {
						t.Fatal("denied emission wrote optional span")
					}
				}
			}
			t.Log("real public invoke + private trace.span: bootstrap deny, no-grant deny, explicit grant row with host trace/parent/execution, stale lease deny, revoke deny, actual retained old Factory worker deny; SDK wrapped result preserved")
		})
	}
}
