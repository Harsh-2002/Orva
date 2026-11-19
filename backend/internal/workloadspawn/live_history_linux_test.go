//go:build linux

package workloadspawn_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
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
	"github.com/Harsh-2002/Orva/backend/internal/workloadhistory"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func historySource(lang string) (string, string) {
	if lang == "node" {
		return "handler.js", `
const {jobs,kv}=require('orva');
exports.handler=async e=>{
 const x=JSON.parse(e.body||'{}');
 const trigger=e.headers['x-orva-trigger'];
 if(trigger==='job'||trigger==='cron'){
   await kv.put('completed/'+trigger,{id:e.headers['x-orva-'+trigger+'-id'],tag:x.tag});
   return {statusCode:200,body:'done'};
 }
 const j=await jobs.enqueue(x.target,{tag:'old-private-payload'},{scheduledAt:x.at,idempotencyKey:x.key});
 return {statusCode:200,body:JSON.stringify(j)};
};`
	}
	return "handler.py", `
import json
from orva import jobs,kv
def handler(e):
    x=json.loads(e.get("body") or "{}")
    trigger=e["headers"].get("x-orva-trigger")
    if trigger in ("job","cron"):
        kv.put("completed/"+trigger,{"id":e["headers"]["x-orva-"+trigger+"-id"],"tag":x.get("tag")})
        return {"statusCode":200,"body":"done"}
    j=jobs.enqueue(x["target"],{"tag":"old-private-payload"},scheduled_at=x["at"],idempotency_key=x["key"])
    return {"statusCode":200,"body":json.dumps(j)}
`
}

func TestLiveHistoryLifecycle(t *testing.T) {
	if os.Getenv("ORVA_LIVE_HISTORY") != "disposable-vm" {
		t.Skip("explicit bounded disposable VM only")
	}
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "history.db")
			db, err := database.New(databasePath)
			liveMust(t, err)
			defer db.Close()
			liveMust(t, db.Migrate())
			liveMust(t, db.InitAsyncSchemaForFixture(t.Context()))
			liveMust(t, db.InitCronSchemaForFixture(t.Context()))
			liveMust(t, db.InitHistorySchemaForFixture(t.Context()))
			liveMust(t, db.InitIAMSchemaForFixture(t.Context()))
			store := newQualificationStore(t)
			runtimeID := sealQualificationRuntime(t, store, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), lang, true)
			defer reclaimQualified(t, store, runtimeID)
			finalizers := []func(){}
			defer func() {
				for i := len(finalizers) - 1; i >= 0; i-- {
					finalizers[i]()
				}
			}()
			entry, source := historySource(lang)
			name := "history-" + lang
			reg := registry.New(db)
			a, err := workloadiam.NewAuthority(32, nil)
			liveMust(t, err)
			var pm *pool.Manager
			async := &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: func(owner string) string { return pm.CurrentArtifact(owner) }}
			history := &workloadhistory.Service{DB: db, Authority: a, CurrentArtifact: async.CurrentArtifact}
			reg.DeletionHook = history
			host := sdkhost.New(db, sdkhost.WithIAM(a), sdkhost.WithAsyncIAM(async), sdkhost.WithCronIAM(async),
				sdkhost.WithIAMCurrentArtifact(func(owner, artifact string) bool { return pm.IsCurrentArtifact(owner, artifact) }))
			jail := filepath.Join(os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "nsjail-linux-amd64")
			verified, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
			liveMust(t, err)
			liveMust(t, verified.Close())
			pm = pool.NewManager(pool.ManagerConfig{ManagedAuthority: a, DefaultMax: 1, ReapInterval: time.Hour},
				pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/absent", DefaultSeccomp: "default", DataDir: t.TempDir(), SDKHandler: host.Handle},
				db, reg, sandbox.NewLimiter(4))
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				liveMust(t, pm.Shutdown(ctx))
			}()
			install := func(old bool) *database.Function {
				t.Helper()
				id := ids.New()
				codeStore := newQualificationStore(t)
				codeID, _ := sealQualificationCode(t, codeStore, id, entry, source, "fixture.txt", "history")
				// Pool shutdown and factory close precede store reclamation.
				finalizers = append(finalizers, func() { reclaimQualified(t, codeStore, codeID) })
				fn := &database.Function{ID: id, Name: name, Runtime: lang, Status: "active", AuthMode: "none", Entrypoint: entry, CodeHash: digest(source), Version: 1,
					MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, MaxConcurrency: 1, NetworkMode: "none", SDK: true, EnvVars: map[string]string{}}
				liveMust(t, reg.Set(fn))
				factory, err := NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{Artifact: database.WorkloadArtifactRecord{OwnerID: id, Generation: ids.New(), Revision: 1, Epoch: 1},
					Function: fn.Clone(), Secrets: map[string]string{}}, codeStore, codeID, store, runtimeID, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
				liveMust(t, err)
				finalizers = append(finalizers, func() { factory.Close() })
				prefix := "completed/"
				policy := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{
					{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.KVPut}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Prefix: &prefix}}},
				}, Inbound: []workloadiam.Acceptance{}}
				if old {
					policy.Allow = append(policy.Allow, workloadiam.Statement{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.JobsEnqueue}, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: id}}})
					policy.Inbound = []workloadiam.Acceptance{{CallerID: id, Triggers: []workloadiam.Trigger{workloadiam.Job}}}
				}
				e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: factory.ArtifactRevision()}, policy)
				liveMust(t, err)
				liveMust(t, a.Publish(0, e, func() error { return nil }))
				liveMust(t, pm.InstallFactory(t.Context(), factory))
				return fn
			}
			old := install(true)
			authenticator := sdkauth.New()
			p := proxy.New()
			p.Pool = pm
			p.DB = db
			p.SDKAuth = authenticator
			invoke := &handlers.InvokeHandler{Registry: reg, Proxy: p, DB: db, Metrics: metrics.New(), DataDir: t.TempDir()}
			token := "orva_" + strings.ReplaceAll(ids.New(), "-", "")
			hash := sha256.Sum256([]byte(token))
			ref := auth.OperatorReference{Kind: "api_key", ID: ids.New()}
			liveMust(t, db.InsertAPIKey(&database.APIKey{ID: ref.ID, Name: "history-reviewer", KeyHash: hex.EncodeToString(hash[:]), Prefix: "fixture", Permissions: `["read","admin"]`}))
			operatorCtx := auth.WithOperator(t.Context(), &auth.Principal{Kind: ref.Kind, ID: ref.ID})
			// Bind the existing explicit runtime policy to canonical source storage
			// before enabling managed deletion; no serving loader is involved.
			row, err := db.NewIAMPolicy(operatorCtx, old.ID, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
			liveMust(t, err)
			prefix := "completed/"
			initialPolicy := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{
				{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.KVPut}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Prefix: &prefix}}},
				{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.JobsEnqueue}, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: old.ID}}},
			}, Inbound: []workloadiam.Acceptance{{CallerID: old.ID, Triggers: []workloadiam.Trigger{workloadiam.Job}}}}
			initialJSON, err := json.Marshal(initialPolicy)
			liveMust(t, err)
			row, err = db.SaveIAMPolicyAsOperator(operatorCtx, ref, database.IAMPolicyChange{OwnerID: old.ID, ExpectedRevision: row.Revision,
				ExpectedApprovedArtifact: row.ApprovedArtifact, ApprovedArtifact: pm.CurrentArtifact(old.ID), PolicyJSON: initialJSON})
			liveMust(t, err)
			evaluator, err := row.AdmissionEvaluator()
			liveMust(t, err)
			liveMust(t, a.Publish(1, evaluator, func() error { return nil }))
			stageHistoryManaged(t, databasePath, "workload_managed_schema.go", "CREATE TABLE IF NOT EXISTS workload_iam_inline")
			managed, err := workloadpolicy.NewManaged(db, reg, a, func(owner string) workloadpolicy.ArtifactState {
				artifact := pm.CurrentArtifact(owner)
				return workloadpolicy.ArtifactState{OwnerID: owner, Artifact: artifact, Known: artifact != ""}
			})
			liveMust(t, err)
			history.Managed = managed
			history.Registry = reg
			definition, err := managed.MutateManaged(operatorCtx, ref, database.IAMManagedMutation{Operation: "create", Name: "history-reader",
				PolicyJSON: []byte(`{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"state"}},"context":"execution"}],"inbound":[]}`)})
			liveMust(t, err)
			attached, err := managed.MutateManaged(operatorCtx, ref, database.IAMManagedMutation{Operation: "attach", PolicyID: definition.Policy.ID,
				MembershipRevision: definition.Policy.MembershipRevision, Version: 1,
				Owners: []database.IAMOwnerExpectation{{OwnerID: old.ID, Revision: row.Revision, ApprovedArtifact: row.ApprovedArtifact, CurrentArtifact: pm.CurrentArtifact(old.ID)}}})
			liveMust(t, err)
			// Finish fixture DDL/source staging before invocation starts async execution writes.
			body, _ := json.Marshal(map[string]string{"target": old.Name, "at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano), "key": ids.New()})
			rec := httptest.NewRecorder()
			invoke.ServeHTTP(rec, httptest.NewRequest("POST", "/fn/"+old.ID, bytes.NewReader(body)))
			if rec.Code != 200 {
				t.Fatalf("real SDK enqueue status=%d", rec.Code)
			}
			var enqueued struct {
				ID string `json:"id"`
			}
			liveMust(t, json.Unmarshal(rec.Body.Bytes(), &enqueued))
			original, err := db.GetAsyncJob(t.Context(), enqueued.ID)
			liveMust(t, err)
			if original.OriginKind != "function" || original.CreatorID != old.ID {
				t.Fatal("SDK provenance was not host-owned")
			}
			cron := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: old.ID, Name: "old-clock", CronExpr: "@every 1h", Timezone: "UTC", Enabled: true, Payload: `{"tag":"old-private-cron"}`}}
			liveMust(t, async.CreateCronOperator(operatorCtx, ref, cron))
			authenticate := func(r *http.Request) (*auth.Principal, error) {
				credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				if credential == "" {
					credential = r.Header.Get("X-Orva-API-Key")
				}
				digest := sha256.Sum256([]byte(credential))
				key, err := db.GetAPIKeyByHash(hex.EncodeToString(digest[:]))
				if err != nil {
					return nil, err
				}
				perms := auth.PermSet{}
				for _, p := range key.PermissionsList() {
					perms[p] = true
				}
				return &auth.Principal{Kind: "api_key", ID: key.ID, Perms: perms}, nil
			}
			deleteHandler := &handlers.FunctionHandler{Registry: reg, DB: db, DataDir: t.TempDir(), FnLock: pm.FunctionLock, PoolDrain: pm.DrainAndRemove}
			mux := http.NewServeMux()
			mux.HandleFunc("DELETE /api/v1/functions/{id}", func(w http.ResponseWriter, r *http.Request) {
				principal, err := authenticate(r)
				if err != nil {
					http.Error(w, "unauthorized", 401)
					return
				}
				deleteHandler.Delete(w, r.WithContext(auth.WithOperator(r.Context(), principal)))
			})
			(&workloadhistory.HTTPAdapter{Service: history, Authenticate: authenticate}).Register(mux)
			if historyLiveControlsEnabled() {
				registerHistoryLiveControls(t, mux, db, databasePath, reg, async, history, authenticate)
			}
			server := httptest.NewServer(mux)
			defer server.Close()
			request := func(method, path string, in any, out any) {
				t.Helper()
				var raw []byte
				if in != nil {
					var err error
					raw, err = json.Marshal(in)
					liveMust(t, err)
				}
				req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
				liveMust(t, err)
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				res, err := server.Client().Do(req)
				liveMust(t, err)
				defer res.Body.Close()
				response, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
				liveMust(t, err)
				if res.StatusCode != 200 {
					t.Fatalf("%s %s status=%d", method, path, res.StatusCode)
				}
				if out != nil {
					liveMust(t, json.Unmarshal(response, out))
				}
			}
			request("DELETE", "/api/v1/functions/"+old.ID, nil, nil)
			remaining, err := db.ListIAMAttachedOwners(t.Context(), definition.Policy.ID)
			liveMust(t, err)
			counter, err := db.GetIAMManagedPolicy(t.Context(), definition.Policy.ID)
			liveMust(t, err)
			memberAudit, err := db.ListIAMManagedAudit(t.Context(), definition.Policy.ID)
			liveMust(t, err)
			if len(remaining) != 0 || counter.MembershipRevision != attached.Policy.MembershipRevision+1 ||
				memberAudit[0].Operation != "owner_delete" || memberAudit[0].Actor.ID != ref.ID {
				t.Fatal("actual function deletion left stale named membership/audit")
			}
			if _, err := async.BeginJob(t.Context(), original); !errors.Is(err, workloadiam.ErrDenied) {
				t.Fatal("old standing origin survived target delete", err)
			}
			for _, action := range []string{"resume", "retry"} {
				if err := async.MutateJob(operatorCtx, ref, original.ID, action); err == nil {
					t.Fatal("old archive resumed as live work")
				}
			}
			var entries []database.HistoryEntry
			request("POST", "/api/v1/work-history/list", map[string]any{"target_id": old.ID, "limit": 100}, &entries)
			if len(entries) != 3 {
				t.Fatalf("expected job/cron/deletion metadata, got %d", len(entries))
			}
			for _, entry := range entries {
				if entry.Reserved || bytes.Contains(entry.Metadata, []byte("old-private")) || bytes.Contains(entry.Metadata, []byte(original.IdempotencyKey)) {
					t.Fatal("history retained payload/key or pending state")
				}
				if entry.Kind == "delete" {
					var root struct {
						Sources []database.IAMOwnerDeletionAudit `json:"managed_sources"`
					}
					liveMust(t, json.Unmarshal(entry.Metadata, &root))
					if len(root.Sources) != 1 || root.Sources[0].OwnerID != old.ID || root.Sources[0].PolicyID != definition.Policy.ID ||
						root.Sources[0].Digest != memberAudit[0].Digest {
						t.Fatal("retained deletion identity/audit mismatch")
					}
					continue
				}
				var h database.WorkHistory
				liveMust(t, json.Unmarshal(entry.Metadata, &h))
				if h.Executable || h.PayloadAvailable || h.TargetID != old.ID || h.PayloadBytes < 1 {
					t.Fatal("history was not metadata-only")
				}
			}
			fresh := install(false)
			if fresh.Name != old.Name || fresh.ID == old.ID {
				t.Fatal("same-name recreation did not use a fresh UUID")
			}
			var jobReply, cronReply struct {
				NewID  string `json:"new_id"`
				Origin string `json:"origin"`
			}
			request("POST", "/api/v1/work-history/reissue", database.HistoryReissue{Source: database.HistorySelector{Kind: "job", ID: original.ID}, TargetID: fresh.ID,
				Payload: json.RawMessage(`{"tag":"fresh-job"}`), ScheduledAt: time.Now().UTC(), MaxAttempts: 2, Confirm: true, Reviewed: true}, &jobReply)
			request("POST", "/api/v1/work-history/reissue", database.HistoryReissue{Source: database.HistorySelector{Kind: "cron", ID: cron.ID}, TargetID: fresh.ID,
				Payload: json.RawMessage(`{"tag":"fresh-cron"}`), Name: "new-clock", Expression: "@every 2s", Timezone: "UTC", Enabled: true, Confirm: true, Reviewed: true}, &cronReply)
			if jobReply.NewID == original.ID || cronReply.NewID == cron.ID || jobReply.Origin != "operator" || cronReply.Origin != "operator" {
				t.Fatal("reissue adopted an original work ID/origin")
			}
			reissued, err := db.GetAsyncJob(t.Context(), jobReply.NewID)
			liveMust(t, err)
			reissuedCron, err := db.GetAsyncCron(t.Context(), cronReply.NewID)
			liveMust(t, err)
			if reissued.FunctionID != fresh.ID || reissued.CreatorID != "" || reissued.OperatorID != ref.ID || reissued.IdempotencyKey != "" ||
				reissuedCron.FunctionID != fresh.ID || reissuedCron.CreatorID != "" || reissuedCron.OperatorID != ref.ID {
				t.Fatal("new work provenance/target mismatch")
			}
			s := scheduler.New(db, pm, t.TempDir(), nil, scheduler.WithAsyncIAM(async), scheduler.WithCronIAM(async), scheduler.WithCronPollIntervalForFixture(25*time.Millisecond))
			s.SetSDKAuth(authenticator)
			ctx, cancel := context.WithCancel(t.Context())
			s.Start(ctx)
			defer func() { cancel(); s.Stop(5 * time.Second) }()
			for _, expected := range []struct{ trigger, id, tag string }{{"job", jobReply.NewID, "fresh-job"}, {"cron", cronReply.NewID, "fresh-cron"}} {
				success := false
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					if value, err := db.KVGet(fresh.ID, "completed/"+expected.trigger); err == nil {
						var got struct{ ID, Tag string }
						liveMust(t, json.Unmarshal(value.Value, &got))
						if got.ID != expected.id || got.Tag != expected.tag {
							t.Fatal("actual SDK observed wrong work/payload")
						}
						success = true
						break
					}
					time.Sleep(25 * time.Millisecond)
				}
				if !success {
					t.Fatalf("real sealed managed %s did not complete", expected.trigger)
				}
				var audit database.HistoryEntry
				request("POST", "/api/v1/work-history/get", database.HistorySelector{Kind: "reissue", ID: expected.id}, &audit)
				var change struct {
					NewID, TargetID, NewOrigin string
					Actor                      database.IAMActorReference `json:"actor"`
				}
				// Wire names are intentionally explicit; no old payload is used to reconstruct work.
				var fields map[string]json.RawMessage
				liveMust(t, json.Unmarshal(audit.Metadata, &fields))
				liveMust(t, json.Unmarshal(fields["new_id"], &change.NewID))
				liveMust(t, json.Unmarshal(fields["target_id"], &change.TargetID))
				liveMust(t, json.Unmarshal(fields["new_origin"], &change.NewOrigin))
				liveMust(t, json.Unmarshal(fields["actor"], &change.Actor))
				if change.NewID != expected.id || change.TargetID != fresh.ID || change.NewOrigin != "operator" || change.Actor.ID != ref.ID {
					t.Fatal("runtime result/audit not correlated")
				}
			}
			t.Log("actual private SDK original job; function DELETE HTTP atomically removes named attachment/advances membership/audits old UUID -> metadata-only history -> old IDs deny -> reviewed new-UUID operator reissue -> real sealed managed job+cron SDK effects/audit correlate")
			if historyLiveControlsEnabled() {
				liveHistoryControls(t, db, databasePath, server, async, ref, token, lang, old.ID, fresh.ID, original.ID, cron.ID, reissuedCron.ID)
			}
		})
	}
}

const historyStagePragmas = "_pragma=foreign_keys(ON)&_pragma=busy_timeout(10000)"

func stageHistoryManaged(t *testing.T, path, name, marker string) {
	t.Helper()
	tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "database", name), nil, 0)
	liveMust(t, err)
	var schema string
	ast.Inspect(tree, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			value, _ := strconv.Unquote(lit.Value)
			if strings.Contains(value, marker) {
				schema = value
			}
		}
		return true
	})
	if schema == "" {
		t.Fatal("canonical source schema absent")
	}
	conn, err := sql.Open("sqlite", path+"?"+historyStagePragmas)
	liveMust(t, err)
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	_, err = conn.ExecContext(t.Context(), schema)
	liveMust(t, err)
}
