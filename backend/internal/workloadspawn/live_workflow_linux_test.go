//go:build linux

package workloadspawn

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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

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
	for _, lang := range []string{"node", "python"} {
		store := newQualificationStore(t)
		rid := sealQualificationRuntime(t, store, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), lang, true)
		defer reclaimQualified(t, store, rid)
		id := ids.New()
		entry, source := liveSource(lang)
		cid, _ := sealQualificationCode(t, store, id, entry, source, "fixture.txt", "live-kv")
		defer reclaimQualified(t, store, cid)
		fn := &database.Function{ID: id, Name: "scratch-" + lang, Runtime: lang, Status: "active", Entrypoint: entry, CodeHash: digest(source),
			Version: 1, MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, MaxConcurrency: 1, NetworkMode: "none", SDK: true}
		liveMust(t, db.InsertFunction(fn))
		liveMust(t, db.KVPut(id, "demo", []byte(`"scratch-value"`), nil))
		record, err := db.NewIAMPolicy(t.Context(), id, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
		liveMust(t, err)
		e, err := record.AdmissionEvaluator()
		liveMust(t, err)
		initial = append(initial, e)
		factory, err := NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{
			Artifact: database.WorkloadArtifactRecord{OwnerID: id, Generation: ids.New(), Revision: 1, Epoch: 1},
			Function: fn, Secrets: map[string]string{},
		}, store, cid, store, rid, FactoryLimits{MaxPreparedPlans: 1, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
		liveMust(t, err)
		defer factory.Close()
		functions[id] = &liveFunction{fn: fn, factory: factory}
	}
	authority, err := workloadiam.NewAuthority(32, initial)
	liveMust(t, err)
	service, err := workloadpolicy.New(db, registry.New(db), authority, func(id string) workloadpolicy.ArtifactState {
		f := functions[id]
		if f == nil || f.factory.Retired() {
			return workloadpolicy.ArtifactState{}
		}
		return workloadpolicy.ArtifactState{OwnerID: f.factory.Owner(), Artifact: f.factory.ArtifactRevision(), Known: true}
	})
	liveMust(t, err)
	host := sdkhost.New(db, sdkhost.WithIAM(authority))
	var traceMu sync.Mutex
	events := []any{}
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
	for _, f := range functions {
		code, runtime := f.factory.Identities()
		recordEvent(map[string]any{"kind": "factory", "owner": f.factory.Owner(), "artifact": f.factory.ArtifactRevision(),
			"code": code, "runtime": runtime, "generation": f.factory.Generation()})
		plan, err := f.factory.Prepare(t.Context())
		liveMust(t, err)
		w, err := plan.Spawn(t.Context(), sandbox.ExecConfig{NsjailBin: jail, RootfsDir: "/absent", CodeDir: "/absent",
			SeccompPolicy:          sandbox.BuildSeccompPolicy("default", nil, nil),
			WorkloadTransitionHook: func(change func()) { liveMust(t, authority.ChangeHostState(change)) },
			SDKHandler: func(ctx context.Context, caller sandbox.SDKCaller, op string, args json.RawMessage) (any, *sandbox.SDKError) {
				state, ok := caller.WorkloadState()
				result, sdkErr := host.Handle(ctx, caller, op, args)
				code := "OK"
				if sdkErr != nil {
					code = sdkErr.Code
				}
				recordEvent(map[string]any{"kind": "sdk", "op": op, "code": code, "known": ok,
					"identity": state.Identity, "execution": state.ExecutionID, "phase": state.Phase,
					"ready": state.Ready, "alive": state.Alive, "has_lease": state.Lease != ""})
				return result, sdkErr
			}})
		plan.Close()
		liveMust(t, err)
		f.worker = w
		defer w.Kill()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		err = w.AwaitReady(ctx)
		cancel()
		liveMust(t, err)
	}
	adapter := &workloadpolicy.HTTPAdapter{Service: service, Authenticate: resolve}
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
	mux.HandleFunc("POST /api/scratch/invoke/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, err := resolve(r)
		if err != nil || !p.Perms.Has("invoke") {
			w.WriteHeader(403)
			return
		}
		f := functions[r.PathValue("id")]
		if f == nil {
			w.WriteHeader(404)
			return
		}
		execution := ids.New()
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		result, err := f.worker.DispatchEx(sandbox.WithExecution(ctx, sandbox.ExecInfo{ExecutionID: execution}), []byte(`{}`))
		if err != nil {
			respond.Error(w, 500, "DISPATCH", "Scratch dispatch failed", "")
			return
		}
		state, _ := f.worker.WorkloadState()
		recordEvent(map[string]any{"kind": "invoke", "execution": execution, "worker": state.Identity, "response": json.RawMessage(result.Body)})
		respond.JSON(w, 200, map[string]any{"execution": execution, "worker": state.Identity.WorkerID, "artifact": state.Identity.ArtifactRevision,
			"response": json.RawMessage(result.Body)})
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
			out := call("POST", "/api/scratch/invoke/"+id, keys["admin"], nil, 200)
			if out["response"].(map[string]any)["decision"] != want {
				t.Fatalf("%s expected actual SDK %s, got %v", f.fn.Runtime, want, out["response"])
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
		t.Logf("%s: real HTTP authenticated CAS -> Authority -> same Factory worker private SDK deny/allow/revoke PASS", f.fn.Runtime)
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
exports.handler = async () => {
 try { const value = await kv.get('demo'); return {statusCode:200, body:JSON.stringify({decision:'ALLOWED',value})}; }
 catch (e) { return {statusCode:200,body:JSON.stringify({decision:String(e.message).includes('IAM_DENIED')?'IAM_DENIED':'SDK_ERROR',status:e.status})}; }
};`
	}
	return "handler.py", `import json
from orva import kv
def handler(event):
    try:
        value = kv.get("demo")
        return {"statusCode":200,"body":json.dumps({"decision":"ALLOWED","value":value})}
    except Exception as e:
        return {"statusCode":200,"body":json.dumps({"decision":"IAM_DENIED" if "IAM_DENIED" in str(e) else "SDK_ERROR","status":getattr(e,"status",0)})}
`
}
