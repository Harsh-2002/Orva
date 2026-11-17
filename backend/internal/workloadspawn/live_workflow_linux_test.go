//go:build linux

package workloadspawn_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
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
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/trace"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/Harsh-2002/Orva/internal/ids"
)

type Factory = workloadspawn.Factory
type FactoryLimits = workloadspawn.FactoryLimits

var NewFactory = workloadspawn.NewFactory
var newQualificationStore = workloadspawn.QualificationStoreForTest
var reclaimQualified = workloadspawn.ReclaimForTest
var sealQualificationCode = workloadspawn.SealCodeForTest
var verifiedRuntimePackage = workloadspawn.VerifiedPackageForTest

func sealQualificationRuntime(t *testing.T, s *artifactfs.Store, dir, lang string, _ bool) string {
	return workloadspawn.SealRuntimeForTest(t, s, dir, lang)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// This fixture is opt-in and never imported by the serving binary.
func TestLiveWorkflow(t *testing.T) {
	if os.Getenv("ORVA_LIVE_WORKFLOW") != "disposable-vm" {
		t.Skip("explicit disposable VM only")
	}
	evidence := os.Getenv("ORVA_RUNTIME_RECIPE_OUTPUT")
	if evidence == "" {
		t.Fatal("missing evidence directory")
	}
	path := filepath.Join(t.TempDir(), "scratch.db")
	db, err := database.New(path)
	liveMust(t, err)
	defer db.Close()
	liveMust(t, db.Migrate())
	liveSchema(t, path)
	keys := map[string]string{}
	for name, perms := range map[string]string{"admin": `["read","invoke","admin"]`, "reader": `["read"]`, "writer": `["write"]`} {
		raw := make([]byte, 32)
		_, err := rand.Read(raw)
		liveMust(t, err)
		key := "orva_" + hex.EncodeToString(raw)
		hash := sha256.Sum256([]byte(key))
		liveMust(t, db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: "scratch-" + name, KeyHash: hex.EncodeToString(hash[:]), Prefix: "scratch", Permissions: perms}))
		keys[name] = key
	}
	keyJSON, _ := json.Marshal(keys)
	liveMust(t, os.WriteFile(filepath.Join(evidence, "credentials.json"), keyJSON, 0600))
	resolve := func(r *http.Request) (*auth.Principal, error) {
		key := r.Header.Get("X-Orva-API-Key")
		if !strings.HasPrefix(key, "orva_") || strings.HasPrefix(key, "orva_chn_") || len(key) > 128 {
			return nil, errors.New("invalid key")
		}
		hash := sha256.Sum256([]byte(key))
		row, err := db.GetAPIKeyByHash(hex.EncodeToString(hash[:]))
		if err != nil || (row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now())) {
			return nil, errors.New("invalid key")
		}
		perms := auth.PermSet{}
		for _, p := range row.PermissionsList() {
			perms[p] = true
		}
		return &auth.Principal{Kind: auth.KindAPIKey, ID: row.ID, Perms: perms}, nil
	}
	type liveFunction struct {
		fn      *database.Function
		factory *Factory
		worker  *sandbox.Worker
	}
	functions := map[string]*liveFunction{}
	initial := []*workloadiam.Evaluator{}
	callsEnabled := os.Getenv("ORVA_LIVE_CALLS") == "1"
	for _, lang := range []string{"node", "python"} {
		store := newQualificationStore(t)
		rid := sealQualificationRuntime(t, store, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), lang, true)
		defer reclaimQualified(t, store, rid)
		id := ids.New()
		entry, source := liveSource(lang)
		if callsEnabled {
			entry, source = liveCallsSource(lang)
		}
		cid, _ := sealQualificationCode(t, store, id, entry, source, "fixture.txt", "live-kv")
		defer reclaimQualified(t, store, cid)
		fn := &database.Function{ID: id, Name: "scratch-" + lang, Runtime: lang, Status: "active", AuthMode: "platform_key", Entrypoint: entry, CodeHash: digest(source),
			Version: 1, MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, MaxConcurrency: 1, NetworkMode: "none", SDK: true,
			EnvVars: map[string]string{"SCRATCH_CONFIG": "frozen-config"}}
		liveMust(t, db.InsertFunction(fn))
		liveMust(t, db.KVPut(id, "demo", []byte(`"scratch-value"`), nil))
		record, err := db.NewIAMPolicy(t.Context(), id, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
		liveMust(t, err)
		e, err := record.AdmissionEvaluator()
		liveMust(t, err)
		initial = append(initial, e)
		input := &secrets.PreparedArtifactSnapshot{
			Artifact: database.WorkloadArtifactRecord{OwnerID: id, Generation: ids.New(), Revision: 1, Epoch: 1},
			Function: fn.Clone(), Secrets: map[string]string{"SCRATCH_SECRET": "frozen-secret"},
		}
		factory, err := NewFactory(t.Context(), input, store, cid, store, rid, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
		liveMust(t, err)
		input.Function.EnvVars["SCRATCH_CONFIG"] = "changed"
		input.Secrets["SCRATCH_SECRET"] = "changed"
		input.Function.MemoryMB, input.Function.CPUs, input.Function.MaxConcurrency = 1, 99, 99
		input.Function.SDK, input.Function.NetworkMode = false, "egress"
		defer factory.Close()
		functions[id] = &liveFunction{fn: fn, factory: factory}
	}
	authority, err := workloadiam.NewAuthority(32, initial)
	liveMust(t, err)
	var traceMu sync.Mutex
	events := []any{}
	sdkExecutions := map[string]sandbox.WorkloadIdentity{}
	recordEvent := func(v any) {
		traceMu.Lock()
		defer traceMu.Unlock()
		events = append(events, v)
		raw, _ := json.MarshalIndent(events, "", "  ")
		if err := os.WriteFile(filepath.Join(evidence, "events.json"), raw, 0600); err != nil {
			t.Error(err)
		}
	}
	jail := filepath.Join(os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "nsjail-linux-amd64")
	checked, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
	liveMust(t, err)
	liveMust(t, checked.Close())
	reg := registry.New(db)
	targetWorkers := map[string]*atomic.Pointer[sandbox.Worker]{}
	for id := range functions {
		targetWorkers[id] = &atomic.Pointer[sandbox.Worker]{}
	}
	var manager *pool.Manager
	host := sdkhost.New(db, sdkhost.WithIAM(authority),
		sdkhost.WithIAMCurrentArtifact(func(owner, artifact string) bool { return manager.IsCurrentArtifact(owner, artifact) }),
		sdkhost.WithIAMTargetState(func(owner string, w *sandbox.Worker) workloadiam.TargetState {
			state := manager.TargetState(owner, w)
			if slot := targetWorkers[owner]; slot != nil && state.Active {
				slot.Store(w)
			}
			return state
		}))
	manager = pool.NewManager(pool.ManagerConfig{ManagedAuthority: authority, DefaultMax: 1, DefaultMaxUses: 0, ReapInterval: time.Hour},
		pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/absent-legacy-rootfs", DefaultSeccomp: "default", DataDir: t.TempDir(),
			SecretsLookup: func(string) map[string]string { t.Error("managed spawn consulted mutable legacy secrets"); return nil },
			SDKHandler: func(ctx context.Context, caller sandbox.SDKCaller, op string, args json.RawMessage) (any, *sandbox.SDKError) {
				state, ok := caller.WorkloadState()
				result, sdkErr := host.Handle(ctx, caller, op, args)
				code := "OK"
				if sdkErr != nil {
					code = sdkErr.Code
				}
				traceMu.Lock()
				sdkExecutions[state.ExecutionID] = state.Identity
				traceMu.Unlock()
				event := map[string]any{"kind": "sdk", "op": op, "code": code, "known": ok, "identity": state.Identity,
					"execution": state.ExecutionID, "phase": state.Phase, "ready": state.Ready, "alive": state.Alive, "has_lease": state.Lease != ""}
				if caller.Exec != nil {
					event["trace"], event["span"], event["depth"] = caller.Exec.TraceID, caller.Exec.SpanID, caller.Exec.Depth
					event["has_deadline"] = !caller.Exec.Deadline.IsZero()
				}
				if op == "invoke" {
					if sdkErr == nil {
						event["reply"] = result
					} else {
						event["message"] = sdkErr.Message
					}
					observed := []any{}
					for id, slot := range targetWorkers {
						if worker := slot.Load(); worker != nil {
							s, _ := worker.WorkloadState()
							observed = append(observed, map[string]any{"owner": id, "identity": s.Identity, "ready": s.Ready, "alive": s.Alive})
						}
					}
					event["acquired_targets"] = observed
				}
				recordEvent(event)
				return result, sdkErr
			}}, db, reg, sandbox.NewLimiter(8))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		liveMust(t, manager.Shutdown(ctx))
	}()
	service, err := workloadpolicy.New(db, reg, authority, func(id string) workloadpolicy.ArtifactState {
		artifact := manager.CurrentArtifact(id)
		return workloadpolicy.ArtifactState{OwnerID: id, Artifact: artifact, Known: artifact != ""}
	})
	liveMust(t, err)
	for _, f := range functions {
		code, runtime := f.factory.Identities()
		recordEvent(map[string]any{"kind": "factory", "owner": f.factory.Owner(), "artifact": f.factory.ArtifactRevision(),
			"code": code, "runtime": runtime, "generation": f.factory.Generation()})
		liveMust(t, manager.InstallFactory(t.Context(), f.factory))
	}
	invokeProxy := proxy.New()
	invokeProxy.Pool, invokeProxy.DB, invokeProxy.SDKAuth = manager, db, sdkauth.New()
	invokeHandler := &handlers.InvokeHandler{Registry: reg, Proxy: invokeProxy, DB: db, Metrics: metrics.New(), DataDir: t.TempDir()}
	if callsEnabled {
		host.SetInvoker(&handlers.InternalInvokeHandler{DB: db, Registry: reg, Pool: manager, Metrics: invokeHandler.Metrics, SDKAuth: invokeProxy.SDKAuth})
	}
	adapter := &workloadpolicy.HTTPAdapter{Service: service, Authenticate: resolve}
	if callsEnabled {
		adapter.Targets = func(ctx context.Context, p *auth.Principal) ([]workloadpolicy.FunctionTarget, error) {
			if p == nil || !p.Perms.Has("read") {
				return nil, errors.New("read required")
			}
			rows := []workloadpolicy.FunctionTarget{}
			for id := range functions {
				fn, e := db.GetFunction(id)
				if e != nil {
					return nil, e
				}
				current := false
				if e = authority.ChangeHostState(func() { current = manager.CurrentArtifact(id) != "" }); e != nil {
					return nil, e
				}
				if fn.Status == "active" && current {
					rows = append(rows, workloadpolicy.FunctionTarget{ID: fn.ID, Label: fn.Name})
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Label < rows[j].Label })
			return rows, nil
		}
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/functions/{id}/permissions", adapter)
	mux.Handle("PUT /api/v1/functions/{id}/permissions", adapter)
	mux.Handle("POST /api/v1/functions/{id}/permissions/review", adapter)
	mux.HandleFunc("GET /api/scratch/functions", func(w http.ResponseWriter, r *http.Request) {
		p, err := resolve(r)
		if err != nil || !p.Perms.Has("read") {
			w.WriteHeader(403)
			return
		}
		items := []any{}
		for _, f := range functions {
			items = append(items, map[string]string{"id": f.fn.ID, "name": f.fn.Name, "runtime": f.fn.Runtime})
		}
		respond.JSON(w, 200, items)
	})
	mux.HandleFunc("/fn/", func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		if callsEnabled {
			r = r.WithContext(trace.WithTraceID(r.Context(), trace.NewTraceID()))
		}
		invokeHandler.ServeHTTP(rec, r)
		execution := rec.Header().Get("X-Orva-Execution-ID")
		traceMu.Lock()
		identity, observed := sdkExecutions[execution]
		traceMu.Unlock()
		if observed {
			var row *database.Execution
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				var readErr error
				row, readErr = db.GetExecution(execution)
				if readErr == nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if row == nil || row.ID != execution || row.FunctionID != identity.FunctionID {
				t.Error("public execution record mismatch")
			}
			recordEvent(map[string]any{"kind": "invoke", "execution": execution, "worker": identity, "response": json.RawMessage(rec.Body.Bytes()), "record": row})
			if callsEnabled && row != nil {
				children, e := db.ListByTraceID(row.TraceID)
				if e != nil {
					t.Error(e)
				}
				recordEvent(map[string]any{"kind": "trace", "execution": execution, "rows": children})
			}
			rec.Header().Set("X-Scratch-Worker", identity.WorkerID)
			rec.Header().Set("X-Scratch-Artifact", identity.ArtifactRevision)
		}
		for key, values := range rec.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
	listener, err := net.Listen("tcp", "127.0.0.1:8443")
	liveMust(t, err)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "https://sandbox.l3b.cc.cd" && origin != "http://127.0.0.1:4300" {
			w.WriteHeader(403)
			return
		}
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	client := &http.Client{Timeout: 20 * time.Second}
	call := func(method, path, key string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, err := http.NewRequest(method, "http://127.0.0.1:8443"+path, bytes.NewReader(raw))
		liveMust(t, err)
		req.Header.Set("X-Orva-API-Key", key)
		response, err := client.Do(req)
		liveMust(t, err)
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		liveMust(t, err)
		if response.StatusCode != want {
			t.Fatalf("%s %s: got %d expected %d", method, path, response.StatusCode, want)
		}
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		recordEvent(map[string]any{"kind": "http", "method": method, "path": path, "status": response.StatusCode, "body": out})
		return out
	}
	for id, f := range functions {
		path := "/api/v1/functions/" + id + "/permissions"
		snapshot := call("GET", path, keys["admin"], nil, 200)
		call("GET", path, "", nil, 401)
		call("GET", path, "orva_chn_invalid", nil, 401)
		call("POST", "/fn/"+id+"/demo", "", nil, 401)
		call("POST", "/fn/"+id+"/demo", keys["reader"], nil, 403)
		host := snapshot["host"].(map[string]any)
		payload := func(policy any) map[string]any {
			return map[string]any{"expected": map[string]any{"revision": host["revision"], "current_artifact": host["currentArtifact"], "approved_artifact": host["approvedArtifact"]}, "policy": policy}
		}
		empty := json.RawMessage(`{"schema_version":1,"allow":[],"inbound":[]}`)
		allow := json.RawMessage(`{"schema_version":1,"allow":[{"actions":["kv.get"],"context":"execution","resource":{"own_kv":{"key":"demo"}}}],"inbound":[]}`)
		for _, key := range []string{keys["reader"], keys["writer"]} {
			call("PUT", path, key, payload(allow), 403)
		}
		assertDecision := func(want string) {
			t.Helper()
			out := call("POST", "/fn/"+id+"/demo", keys["admin"], nil, 200)
			if out["decision"] != want {
				t.Fatalf("%s expected actual SDK %s, got %v", f.fn.Runtime, want, out)
			}
		}
		assertDecision("IAM_DENIED")
		old := payload(allow)
		snapshot = call("PUT", path, keys["admin"], old, 200)
		assertDecision("ALLOWED")
		call("PUT", path, keys["admin"], old, 409)
		host = snapshot["host"].(map[string]any)
		call("PUT", path, keys["admin"], map[string]any{"actor": "author"}, 400)
		call("PUT", path, keys["admin"], payload(empty), 200)
		assertDecision("IAM_DENIED")
		t.Logf("%s: public InvokeHandler/Proxy -> real Manager -> Factory worker -> private SDK deny/allow/revoke; execution DB correlation PASS", f.fn.Runtime)
	}
	if callsEnabled {
		owners := map[string]string{}
		for id, f := range functions {
			owners[f.fn.Runtime] = id
		}
		liveCallsChecks(t, call, keys, owners)
	}
	liveMust(t, os.WriteFile(filepath.Join(evidence, "ready"), []byte("backend self-checks passed\n"), 0600))
	if os.Getenv("ORVA_LIVE_HOLD") == "1" {
		deadline := time.After(30 * time.Minute)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-deadline:
				t.Fatal("scratch QA window expired")
			case <-tick.C:
				if _, err := os.Stat(filepath.Join(evidence, "stop")); err == nil {
					return
				}
			}
		}
	}
}

func liveMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func liveSchema(t *testing.T, path string) {
	t.Helper()
	source, err := parser.ParseFile(token.NewFileSet(), "../database/workload_iam_schema.go", nil, 0)
	liveMust(t, err)
	var schema string
	ast.Inspect(source, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		name, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || name.Sel.Name != "ExecContext" {
			return true
		}
		if text, ok := call.Args[1].(*ast.BasicLit); ok {
			schema, _ = strconv.Unquote(text.Value)
		}
		return true
	})
	if schema == "" {
		t.Fatal("canonical staged schema absent")
	}
	conn, err := sql.Open("sqlite", path)
	liveMust(t, err)
	defer conn.Close()
	_, err = conn.Exec(schema)
	liveMust(t, err)
}

func liveSource(lang string) (string, string) {
	if lang == "node" {
		return "handler.js", `const {kv} = require('orva');
exports.handler = async event => {
 const proof={method:event.method,path:event.path,body:event.body,configOK:process.env.SCRATCH_CONFIG==='frozen-config',secretOK:process.env.SCRATCH_SECRET==='frozen-secret',credentialAbsent:!event.headers['x-orva-api-key']};
 try { const value = await kv.get('demo'); return {statusCode:200, body:JSON.stringify({...proof,decision:'ALLOWED',value})}; }
 catch (e) { return {statusCode:200,body:JSON.stringify({...proof,decision:String(e.message).includes('IAM_DENIED')?'IAM_DENIED':'SDK_ERROR',status:e.status})}; }
};`
	}
	return "handler.py", `import json,os
from orva import kv
def handler(event):
    proof={"method":event["method"],"path":event["path"],"body":event["body"],"configOK":os.getenv("SCRATCH_CONFIG")=="frozen-config","secretOK":os.getenv("SCRATCH_SECRET")=="frozen-secret","credentialAbsent":"x-orva-api-key" not in event["headers"]}
    try:
        value = kv.get("demo")
        return {"statusCode":200,"body":json.dumps({**proof,"decision":"ALLOWED","value":value})}
    except Exception as e:
        return {"statusCode":200,"body":json.dumps({**proof,"decision":"IAM_DENIED" if "IAM_DENIED" in str(e) else "SDK_ERROR","status":getattr(e,"status",0)})}
`
}
