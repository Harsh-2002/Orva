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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	orvamcp "github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/metrics"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/proxy"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func configurationSchema(t *testing.T, path, filename string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../database", filename), nil, 0)
	liveMust(t, err)
	conn, err := sql.Open("sqlite", path)
	liveMust(t, err)
	defer conn.Close()
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			text, _ := strconv.Unquote(lit.Value)
			if bytes.Contains([]byte(text), []byte("CREATE TABLE IF NOT EXISTS")) {
				_, err := conn.Exec(text)
				liveMust(t, err)
			}
		}
		return true
	})
}

func configurationExpected(t *testing.T, call func(string, string, any, int) map[string]any, owner string) database.WorkloadConfigurationExpected {
	t.Helper()
	snapshot := call("GET", "/api/v1/functions/"+owner+"/configuration", nil, 200)
	raw, err := json.Marshal(snapshot["expected"])
	liveMust(t, err)
	var expected database.WorkloadConfigurationExpected
	liveMust(t, json.Unmarshal(raw, &expected))
	if expected.FunctionVersion < 1 || expected.ArtifactRevision == 0 || expected.PolicyRevision == 0 {
		t.Fatal("inspection missing exact expectations")
	}
	return expected
}

func configurationWire(e database.WorkloadConfigurationExpected) map[string]string {
	return map[string]string{"function_version": strconv.Itoa(e.FunctionVersion), "artifact_revision": strconv.FormatUint(e.ArtifactRevision, 10),
		"artifact_epoch": strconv.FormatUint(e.ArtifactEpoch, 10), "generation": e.Generation, "policy_revision": strconv.FormatUint(e.PolicyRevision, 10),
		"authority_epoch": strconv.FormatUint(e.AuthorityEpoch, 10), "approved_artifact": e.ApprovedArtifact}
}

func TestLiveConfigurationEnvironment(t *testing.T) {
	if os.Getenv("ORVA_CONFIG_MUTATIONS") != "disposable-vm" {
		t.Skip("explicit disposable VM only")
	}
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "scratch.db")
			db, err := database.New(path)
			liveMust(t, err)
			defer db.Close()
			liveMust(t, db.Migrate())
			liveSchema(t, path)
			configurationSchema(t, path, "workload_artifact_schema.go")
			owner := ids.New()
			entry, source := configurationSource(lang)
			fn := &database.Function{ID: owner, Name: "config-" + lang, Runtime: lang, Status: "active", Entrypoint: entry, CodeHash: digest(source),
				Version: 1, MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, MaxConcurrency: 2, NetworkMode: "none", SDK: true, AuthMode: "platform_key",
				EnvVars: map[string]string{"SCRATCH_CONFIG": "old"}}
			liveMust(t, db.InsertFunction(fn))
			actor := database.IAMActorReference{Kind: "system", ID: ids.New()}
			_, err = db.NewWorkloadArtifact(ctx, owner, actor)
			liveMust(t, err)
			p, err := db.NewIAMPolicy(ctx, owner, false, actor)
			liveMust(t, err)
			configurationSchema(t, path, "workload_managed_schema.go")
			sm := secrets.New(db, t.TempDir())
			liveMust(t, sm.Upsert(owner, "SCRATCH_SECRET", "old"))
			snap, err := sm.PrepareArtifactSnapshot(ctx, owner, secrets.SecretSnapshotLimits{MaxSecrets: 1024, MaxTotalBytes: 8 << 20})
			liveMust(t, err)
			storeRoot := t.TempDir()
			liveMust(t, os.Chmod(storeRoot, 0700))
			store, err := artifactfs.New(storeRoot, artifactfs.Limits{Generations: 4, Leases: 64, Staging: 1, Entries: 32768,
				PathBytes: 4096, LinkBytes: 4096, ManifestBytes: 64 << 20, Bytes: 1 << 30, FileBytes: 256 << 20})
			liveMust(t, err)
			t.Cleanup(func() { liveMust(t, store.Close()) })
			rid := sealQualificationRuntime(t, store, os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), lang, true)
			defer reclaimQualified(t, store, rid)
			cid, _ := sealQualificationCode(t, store, owner, entry, source, "fixture.txt", "config-mutations")
			defer reclaimQualified(t, store, cid)
			factory, err := NewFactory(ctx, snap, store, cid, store, rid, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
			liveMust(t, err)
			evaluator, err := p.AdmissionEvaluator()
			liveMust(t, err)
			authority, err := workloadiam.NewAuthority(32, []*workloadiam.Evaluator{evaluator})
			liveMust(t, err)
			reg := registry.New(db)
			var manager *pool.Manager
			host := sdkhost.New(db, sdkhost.WithIAM(authority), sdkhost.WithIAMCurrentArtifact(func(o, a string) bool { return manager.IsCurrentArtifact(o, a) }))
			type barrier struct {
				waiting, proceed chan struct{}
				once, release    sync.Once
			}
			barriers := map[string]*barrier{}
			observedCaller := make(chan workloadiam.Context, 1)
			for _, name := range []string{"barrier-env", "barrier-upsert", "barrier-delete"} {
				b := &barrier{waiting: make(chan struct{}), proceed: make(chan struct{})}
				barriers[name] = b
				t.Cleanup(func() { b.release.Do(func() { close(b.proceed) }) })
			}
			jail := filepath.Join(os.Getenv("ORVA_RUNTIME_PACKAGE_DIR"), "nsjail-linux-amd64")
			checked, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
			liveMust(t, err)
			liveMust(t, checked.Close())
			manager = pool.NewManager(pool.ManagerConfig{ManagedAuthority: authority, DefaultMax: 2, ReapInterval: time.Hour},
				pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/absent-legacy", DataDir: t.TempDir(), DefaultSeccomp: "default",
					SecretsLookup: func(string) map[string]string { t.Error("legacy secret lookup"); return nil },
					SDKHandler: func(ctx context.Context, caller sandbox.SDKCaller, op string, args json.RawMessage) (any, *sandbox.SDKError) {
						for name, b := range barriers {
							if bytes.Contains(args, []byte(name)) {
								if name == "barrier-env" {
									state, ok := caller.WorkloadState()
									if !ok || !state.Alive || !state.Ready || state.Lease == "" {
										t.Error("missing real caller observation")
									} else {
										observedCaller <- workloadiam.Context{Principal: workloadiam.Principal{FunctionID: state.Identity.FunctionID, WorkerID: state.Identity.WorkerID, ArtifactRevision: state.Identity.ArtifactRevision},
											Ready: state.Ready, Phase: workloadiam.Execution, Lease: workloadiam.Lease{ID: state.Lease, WorkerID: state.Identity.WorkerID, ExecutionID: state.ExecutionID, Active: true}}
									}
								}
								b.once.Do(func() { close(b.waiting) })
								select {
								case <-b.proceed:
								case <-ctx.Done():
								}
							}
						}
						return host.Handle(ctx, caller, op, args)
					}}, db, reg, sandbox.NewLimiter(4))
			defer func() {
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				liveMust(t, manager.Shutdown(c))
			}()
			liveMust(t, manager.InstallFactory(ctx, factory))
			policies, err := workloadpolicy.NewManaged(db, reg, authority, func(o string) workloadpolicy.ArtifactState {
				a := manager.CurrentArtifact(o)
				return workloadpolicy.ArtifactState{OwnerID: o, Artifact: a, Known: a != ""}
			})
			liveMust(t, err)
			adminID := ids.New()
			key := "orva_" + ids.New()
			hash := sha256.Sum256([]byte(key))
			liveMust(t, db.InsertAPIKey(&database.APIKey{ID: adminID, Name: "scratch-admin", KeyHash: hex.EncodeToString(hash[:]), Prefix: "scratch", Permissions: `["admin","invoke","read","write"]`}))
			ref := database.OperatorCredentialReference{Kind: "api_key", ID: adminID}
			roleKeys, roleIDs := map[string]string{}, map[string]string{}
			for _, role := range []string{"read", "write", "admin"} {
				raw, id := "orva_"+ids.New(), ids.New()
				h := sha256.Sum256([]byte(raw))
				liveMust(t, db.InsertAPIKey(&database.APIKey{ID: id, Name: "scratch-" + role, KeyHash: hex.EncodeToString(h[:]), Prefix: "scratch", Permissions: `["` + role + `"]`}))
				roleKeys[role], roleIDs[role] = raw, id
			}
			grant := []byte(fmt.Sprintf(`{"schema_version":1,"allow":[{"actions":["kv.get"],"context":"execution","resource":{"own_kv":{"prefix":""}}},{"actions":["functions.invoke"],"context":"execution","resource":{"function":{"id":%q}}}],"inbound":[{"caller_id":%q,"triggers":["f2f"]}]}`, owner, owner))
			p, err = policies.Replace(ctx, ref, owner, workloadpolicy.Expected{Revision: p.Revision, ApprovedArtifact: p.ApprovedArtifact, CurrentArtifact: factory.ArtifactRevision()}, grant)
			liveMust(t, err)
			liveMust(t, db.KVPut(owner, "demo", []byte(`true`), nil))
			config, err := workloadconfig.New(db, reg, authority, manager, sm, map[string]workloadconfig.Binding{owner: {Code: store, Runtime: store, CodeGeneration: cid, RuntimeGeneration: rid}})
			liveMust(t, err)
			adapter := &workloadconfig.HTTPAdapter{Service: config, Authenticate: func(r *http.Request) (*auth.Principal, error) {
				h := sha256.Sum256([]byte(r.Header.Get("X-Orva-API-Key")))
				k, e := db.GetAPIKeyByHash(hex.EncodeToString(h[:]))
				if e != nil {
					return nil, errors.New("invalid operator")
				}
				return &auth.Principal{Kind: auth.KindAPIKey, ID: k.ID}, nil
			}}
			invokeProxy := proxy.New()
			invokeProxy.Pool, invokeProxy.DB, invokeProxy.SDKAuth = manager, db, sdkauth.New()
			invoke := &handlers.InvokeHandler{Registry: reg, Proxy: invokeProxy, DB: db, Metrics: metrics.New(), DataDir: t.TempDir()}
			mux := http.NewServeMux()
			adapter.Register(mux)
			mux.Handle("/fn/", invoke)
			deps := orvamcp.Deps{DB: db, Registry: reg, Configuration: config, Version: "scratch-config"}
			mux.Handle("/mcp", orvamcp.NewHandler(deps))
			server := httptest.NewServer(mux)
			defer func() {
				for _, b := range barriers {
					b.release.Do(func() { close(b.proceed) })
				}
				server.Close()
			}()
			callAs := func(credential, method, route string, body any, want int) map[string]any {
				t.Helper()
				raw, _ := json.Marshal(body)
				req, err := http.NewRequestWithContext(ctx, method, server.URL+route, bytes.NewReader(raw))
				liveMust(t, err)
				req.Header.Set("X-Orva-API-Key", credential)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				res, err := server.Client().Do(req)
				liveMust(t, err)
				defer res.Body.Close()
				data, err := io.ReadAll(res.Body)
				liveMust(t, err)
				if res.StatusCode != want {
					t.Fatalf("HTTP status %d expected %d", res.StatusCode, want)
				}
				if bytes.HasPrefix(data, []byte("event:")) {
					for _, line := range bytes.Split(data, []byte("\n")) {
						if bytes.HasPrefix(line, []byte("data:")) {
							data = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
							break
						}
					}
				}
				var out map[string]any
				liveMust(t, json.Unmarshal(data, &out))
				return out
			}
			call := func(method, route string, body any, want int) map[string]any {
				return callAs(key, method, route, body, want)
			}
			inspectPath := "/api/v1/functions/" + owner + "/configuration"
			callAs(roleKeys["read"], "GET", inspectPath, nil, 200)
			callAs(roleKeys["write"], "GET", inspectPath, nil, 403)
			callAs(roleKeys["admin"], "GET", inspectPath, nil, 403)
			assert := func(out map[string]any, decision, configValue, secretValue string) {
				t.Helper()
				if out["decision"] != decision || out["config_"+configValue] != true || out["secret_"+secretValue] != true {
					t.Fatal("runtime identity/config/secret/SDK proof mismatch")
				}
			}
			assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "ALLOWED", "old", "old")
			var reentered atomic.Bool
			reg.PublishEvent = func(string, any) {
				lock := reg.MutationLock(owner)
				if !lock.TryLock() {
					t.Error("event callback held owner lock")
					return
				}
				lock.Unlock()
				liveMust(t, authority.ChangeHostState(func() { reentered.Store(true) }))
			}
			oldResult := make(chan map[string]any, 1)
			go func() { oldResult <- call("POST", "/fn/"+owner+"/barrier-env", nil, 200) }()
			select {
			case <-barriers["barrier-env"].waiting:
			case <-time.After(10 * time.Second):
				t.Fatal("old worker did not reach SDK barrier")
			}
			caller := <-observedCaller
			registration, err := authority.BeginCall(ctx, caller, workloadiam.Request{Operation: workloadiam.OpInvoke, FunctionID: owner, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: owner}}})
			liveMust(t, err)
			defer registration.Finish()
			cancelReentered := make(chan struct{})
			stopCancelCallback := context.AfterFunc(registration.Context(), func() {
				lock := reg.MutationLock(owner)
				if !lock.TryLock() {
					t.Error("cancellation callback held owner lock")
				} else {
					lock.Unlock()
				}
				_ = authority.ChangeHostState(func() {})
				close(cancelReentered)
			})
			defer stopCancelCallback()
			expected := configurationExpected(t, call, owner)
			payload := map[string]any{"expected": configurationWire(expected), "current_artifact": factory.ArtifactRevision(), "environment": map[string]string{"SCRATCH_CONFIG": "new"}}
			callAs(roleKeys["read"], "PUT", inspectPath+"/environment", payload, 403)
			callAs(roleKeys["write"], "PUT", inspectPath+"/environment", payload, 403)
			secretRows, err := db.ListSecrets(owner)
			liveMust(t, err)
			liveMust(t, db.UpsertSecret(owner, "SCRATCH_SECRET", "corrupt-fixture-cipher"))
			call("PUT", "/api/v1/functions/"+owner+"/configuration/environment", payload, 503)
			if factory.Retired() {
				t.Fatal("cipher corruption retired current Factory")
			}
			liveMust(t, db.UpsertSecret(owner, "SCRATCH_SECRET", secretRows[0].ValueEncrypted))
			sqlConn, err := sql.Open("sqlite", path)
			liveMust(t, err)
			defer sqlConn.Close()
			_, err = sqlConn.Exec(`CREATE TABLE configuration_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
				CREATE TRIGGER configuration_fail AFTER INSERT ON workload_iam_audit BEGIN INSERT INTO configuration_guard VALUES('absent'); END`)
			liveMust(t, err)
			call("PUT", "/api/v1/functions/"+owner+"/configuration/environment", payload, 503)
			var afterFailure string
			liveMust(t, authority.ChangeHostState(func() { afterFailure = manager.CurrentArtifact(owner) }))
			cached, err := reg.Get(owner)
			liveMust(t, err)
			if afterFailure != factory.ArtifactRevision() || factory.Retired() || cached.EnvVars["SCRATCH_CONFIG"] != "old" || reentered.Load() {
				t.Fatal("failed COMMIT changed host/cache/callback state")
			}
			if registration.Context().Err() != nil {
				t.Fatal("failed COMMIT cancelled registered work")
			}
			assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "ALLOWED", "old", "old")
			_, err = sqlConn.Exec(`DROP TRIGGER configuration_fail`)
			liveMust(t, err)
			call("PUT", "/api/v1/functions/"+owner+"/configuration/environment", payload, 200)
			if !reentered.Load() {
				t.Fatal("publication event missing")
			}
			if registration.Context().Err() == nil {
				t.Fatal("registered work not cancelled before ACK")
			}
			select {
			case <-cancelReentered:
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation callback deadlocked on owner/gate")
			}
			var current string
			liveMust(t, authority.ChangeHostState(func() { current = manager.CurrentArtifact(owner) }))
			if current != "" {
				t.Fatal("old factory remained current after ACK")
			}
			call("POST", inspectPath+"/install", map[string]any{"expected": configurationWire(expected)}, 409)
			call("POST", inspectPath+"/install", map[string]any{"expected": configurationWire(expected), "current_artifact": factory.ArtifactRevision()}, 400)
			inspection := call("GET", "/api/v1/functions/"+owner+"/configuration", nil, 200)
			if inspection["current_artifact"] != "" || inspection["environment"] != nil || inspection["secrets"] != nil || inspection["code_digest"] != nil {
				t.Fatal("inspection inferred current authority or exposed private input")
			}
			if _, err := policies.Review(ctx, ref, owner, workloadpolicy.Expected{Revision: expected.PolicyRevision + 1, ApprovedArtifact: p.ApprovedArtifact, CurrentArtifact: factory.ArtifactRevision()}); err == nil {
				t.Fatal("old observer approved new durable configuration")
			}
			barriers["barrier-env"].release.Do(func() { close(barriers["barrier-env"].proceed) })
			select {
			case out := <-oldResult:
				assert(out, "IAM_DENIED", "old", "old")
			case <-time.After(10 * time.Second):
				t.Fatal("old invocation stuck")
			}
			cached, err = reg.Get(owner)
			liveMust(t, err)
			if cached.EnvVars["SCRATCH_CONFIG"] != "new" {
				t.Fatal("registry cache stale")
			}
			expected = configurationExpected(t, call, owner)
			installed := call("POST", "/api/v1/functions/"+owner+"/configuration/install", map[string]any{"expected": configurationWire(expected)}, 200)
			newArtifact, ok := installed["current_artifact"].(string)
			if !ok || newArtifact == factory.ArtifactRevision() {
				t.Fatal("new exact factory missing")
			}
			assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "IAM_DENIED", "new", "old")
			p, err = policies.Review(ctx, ref, owner, workloadpolicy.Expected{Revision: expected.PolicyRevision, ApprovedArtifact: expected.ApprovedArtifact, CurrentArtifact: newArtifact})
			liveMust(t, err)
			assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "ALLOWED", "new", "old")
			t.Log("environment HTTP commit, old busy SDK deny, new sealed Factory/config and explicit review: PASS")
			for _, op := range []string{"upsert", "delete"} {
				barrierName := "barrier-" + op
				oldSecret, newSecret, method := "old", "new", "PUT"
				secret := map[string]string{"key": "SCRATCH_SECRET", "value": "new"}
				if op == "delete" {
					oldSecret, newSecret, method = "new", "absent", "DELETE"
					delete(secret, "value")
				}
				go func() { oldResult <- call("POST", "/fn/"+owner+"/"+barrierName, nil, 200) }()
				select {
				case <-barriers[barrierName].waiting:
				case <-time.After(10 * time.Second):
					t.Fatal("old secret worker did not reach SDK barrier")
				}
				expected = configurationExpected(t, call, owner)
				call(method, "/api/v1/functions/"+owner+"/configuration/secrets", map[string]any{"expected": configurationWire(expected), "current_artifact": newArtifact, "secret": secret}, 200)
				barriers[barrierName].release.Do(func() { close(barriers[barrierName].proceed) })
				select {
				case out := <-oldResult:
					assert(out, "IAM_DENIED", "new", oldSecret)
				case <-time.After(10 * time.Second):
					t.Fatal("secret retirement stuck")
				}
				expected = configurationExpected(t, call, owner)
				installed = call("POST", "/api/v1/functions/"+owner+"/configuration/install", map[string]any{"expected": configurationWire(expected)}, 200)
				oldArtifact := newArtifact
				newArtifact = installed["current_artifact"].(string)
				if newArtifact == oldArtifact {
					t.Fatal("secret rotation reused artifact")
				}
				assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "IAM_DENIED", "new", newSecret)
				p, err = policies.Review(ctx, ref, owner, workloadpolicy.Expected{Revision: expected.PolicyRevision, ApprovedArtifact: expected.ApprovedArtifact, CurrentArtifact: newArtifact})
				liveMust(t, err)
				assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "ALLOWED", "new", newSecret)
				t.Log("secret " + op + ": old busy worker denied; new concrete Factory sees only new secret state after explicit review: PASS")
			}
			expected = configurationExpected(t, call, owner)
			settings := database.WorkloadExecutionSettings{MemoryMB: 192, CPUs: 0.75, TimeoutMS: 5000, NetworkMode: "none", SDK: true, MaxConcurrency: 1, ConcurrencyPolicy: "queue", AuthMode: "platform_key", RateLimitPerMin: 1000}
			call("PUT", "/api/v1/functions/"+owner+"/configuration/settings", map[string]any{"expected": configurationWire(expected), "current_artifact": newArtifact, "environment": map[string]string{"SCRATCH_CONFIG": "new"}, "settings": settings}, 200)
			expected = configurationExpected(t, call, owner)
			installed = call("POST", "/api/v1/functions/"+owner+"/configuration/install", map[string]any{"expected": configurationWire(expected)}, 200)
			newArtifact = installed["current_artifact"].(string)
			_, err = policies.Review(ctx, ref, owner, workloadpolicy.Expected{Revision: expected.PolicyRevision, ApprovedArtifact: expected.ApprovedArtifact, CurrentArtifact: newArtifact})
			liveMust(t, err)
			assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "ALLOWED", "new", "absent")
			var facts workloadspawnResourceProof
			liveMust(t, authority.ChangeHostState(func() {
				f := manager.CurrentFactory(owner)
				if f != nil {
					r := f.Resources()
					facts = workloadspawnResourceProof{r.MemoryMB, r.CPUs, r.MaxConcurrency}
				}
			}))
			if facts != (workloadspawnResourceProof{192, 0.75, 1}) {
				t.Fatal("new execution resources not from current Factory")
			}
			t.Log("settings HTTP publication -> new frozen resource facts and real public invocation/private SDK: PASS")
			expected = configurationExpected(t, call, owner)
			args := map[string]any{"id": owner, "request": map[string]any{"expected": configurationWire(expected), "current_artifact": newArtifact, "environment": map[string]string{"SCRATCH_CONFIG": "new"}}}
			reply := call("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "publish_workload_environment", "arguments": args}}, 200)
			result, ok := reply["result"].(map[string]any)
			if !ok || result["isError"] == true || reply["error"] != nil {
				t.Fatal("external MCP mutation failed")
			}
			expected = configurationExpected(t, call, owner)
			ai := orvamcp.BuildAgentRegistry(deps, map[string]bool{"admin": true, "write": true})
			aiCtx := auth.WithOperator(ctx, &auth.Principal{Kind: auth.KindAPIKey, ID: adminID})
			raw, _ := json.Marshal(map[string]any{"id": owner, "request": map[string]any{"expected": configurationWire(expected)}})
			value, err := ai.Dispatch(aiCtx, "install_workload_configuration", raw)
			liveMust(t, err)
			newArtifact = value.(map[string]any)["current_artifact"].(string)
			_, err = policies.Review(ctx, ref, owner, workloadpolicy.Expected{Revision: expected.PolicyRevision, ApprovedArtifact: expected.ApprovedArtifact, CurrentArtifact: newArtifact})
			liveMust(t, err)
			assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "ALLOWED", "new", "absent")
			t.Log("external MCP publication and trusted-context in-process AI registry installation share the real service: PASS")
			readArgs := map[string]any{"id": owner, "request": map[string]any{}}
			readReply := callAs(roleKeys["read"], "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "inspect_workload_configuration", "arguments": readArgs}}, 200)
			readResult, ok := readReply["result"].(map[string]any)
			if !ok || readResult["isError"] == true || readReply["error"] != nil {
				t.Fatal("external MCP inspection failed")
			}
			readContent, ok := readResult["structuredContent"].(map[string]any)
			if !ok || readContent["current_artifact"] != newArtifact || readContent["expected"] == nil || readContent["environment"] != nil || readContent["secrets"] != nil {
				t.Fatal("external inspection metadata/current Factory mismatch")
			}
			readRegistry := orvamcp.BuildAgentRegistry(deps, map[string]bool{"read": true})
			readCtx := auth.WithOperator(ctx, &auth.Principal{Kind: auth.KindAPIKey, ID: roleIDs["read"], Perms: auth.PermSet{"read": true}})
			readRaw, _ := json.Marshal(readArgs)
			_, err = readRegistry.Dispatch(readCtx, "inspect_workload_configuration", readRaw)
			liveMust(t, err)
			_, err = sqlConn.Exec(`UPDATE api_keys SET permissions='[]' WHERE id=?`, roleIDs["read"])
			liveMust(t, err)
			if _, err := readRegistry.Dispatch(readCtx, "inspect_workload_configuration", readRaw); !errors.Is(err, database.ErrOperatorDenied) {
				t.Fatal("frozen READ catalog/context bypassed live permission check")
			}
			callAs(roleKeys["read"], "GET", inspectPath, nil, 403)
			p, err = db.GetIAMPolicy(ctx, owner)
			liveMust(t, err)
			empty := []byte(`{"schema_version":1,"allow":[],"inbound":[]}`)
			p, err = policies.Replace(ctx, ref, owner, workloadpolicy.Expected{Revision: p.Revision, ApprovedArtifact: p.ApprovedArtifact, CurrentArtifact: newArtifact}, empty)
			liveMust(t, err)
			expected = configurationExpected(t, call, owner)
			writePayload := map[string]any{"expected": configurationWire(expected), "current_artifact": newArtifact, "environment": map[string]string{"SCRATCH_CONFIG": "new"}}
			callAs(roleKeys["write"], "PUT", inspectPath+"/environment", writePayload, 200)
			expected = configurationExpected(t, call, owner)
			installed = callAs(roleKeys["admin"], "POST", inspectPath+"/install", map[string]any{"expected": configurationWire(expected)}, 200)
			newArtifact = installed["current_artifact"].(string)
			_, err = policies.Review(ctx, ref, owner, workloadpolicy.Expected{Revision: expected.PolicyRevision, ApprovedArtifact: expected.ApprovedArtifact, CurrentArtifact: newArtifact})
			liveMust(t, err)
			expected = configurationExpected(t, call, owner)
			writePayload = map[string]any{"expected": configurationWire(expected), "current_artifact": newArtifact, "environment": map[string]string{"SCRATCH_CONFIG": "new"}}
			_, err = sqlConn.Exec(`UPDATE api_keys SET permissions='[]' WHERE id=?`, roleIDs["write"])
			liveMust(t, err)
			callAs(roleKeys["write"], "PUT", inspectPath+"/environment", writePayload, 403)
			assert(call("POST", "/fn/"+owner+"/demo", nil, 200), "IAM_DENIED", "new", "absent")
			t.Log("real HTTP/MCP inspection: READ-only, no ADMIN/WRITE implication; empty-owner WRITE and ADMIN-only install; revoked frozen READ/WRITE denied: PASS")
		})
	}
}

type workloadspawnResourceProof struct {
	memory      int64
	cpus        float64
	concurrency int
}

func configurationSource(lang string) (string, string) {
	if lang == "node" {
		return "handler.js", `const {kv}=require('orva'); exports.handler=async event=>{
let decision='ALLOWED';try{await kv.get(event.path.includes('barrier')?event.path.split('/').pop():'demo')}catch(e){decision=String(e.message).includes('IAM_DENIED')?'IAM_DENIED':'SDK_ERROR'}
return {statusCode:200,body:JSON.stringify({decision,config_old:process.env.SCRATCH_CONFIG==='old',config_new:process.env.SCRATCH_CONFIG==='new',secret_old:process.env.SCRATCH_SECRET==='old',secret_new:process.env.SCRATCH_SECRET==='new',secret_absent:!('SCRATCH_SECRET' in process.env)})}};`
	}
	return "handler.py", `import os,json
from orva import kv
def handler(event):
    decision="ALLOWED"
    try: kv.get(event["path"].split("/")[-1] if "barrier" in event["path"] else "demo")
    except Exception as e: decision="IAM_DENIED" if "IAM_DENIED" in str(e) else "SDK_ERROR"
    return {"statusCode":200,"body":json.dumps(dict(decision=decision,config_old=os.getenv("SCRATCH_CONFIG")=="old",config_new=os.getenv("SCRATCH_CONFIG")=="new",secret_old=os.getenv("SCRATCH_SECRET")=="old",secret_new=os.getenv("SCRATCH_SECRET")=="new",secret_absent="SCRATCH_SECRET" not in os.environ))}
`
}
