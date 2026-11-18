//go:build linux

package workloadspawn_test

import (
	"bufio"
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
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	orvamcp "github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func deploymentSchema(t *testing.T, path string, names ...string) {
	t.Helper()
	conn, err := sql.Open("sqlite", path)
	liveMust(t, err)
	defer conn.Close()
	for _, name := range names {
		src, err := parser.ParseFile(token.NewFileSet(), "../database/"+name, nil, 0)
		liveMust(t, err)
		found := false
		ast.Inspect(src, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				body, _ := strconv.Unquote(lit.Value)
				if strings.Contains(body, "CREATE TABLE IF NOT EXISTS") {
					_, err := conn.Exec(body)
					liveMust(t, err)
					found = true
				}
			}
			return true
		})
		if !found {
			t.Fatal("missing explicit fixture schema")
		}
	}
}

func deploymentDomain(t *testing.T) *workloadfence.Domain {
	t.Helper()
	raw, err := os.ReadFile("/proc/self/cgroup")
	liveMust(t, err)
	base := "/sys/fs/cgroup" + strings.TrimSpace(strings.TrimPrefix(string(raw), "0::"))
	if base == "/sys/fs/cgroup" || base != os.Getenv("ORVA_OUTPUT_TEST_BUDGET") {
		t.Fatal("not in owned disposable budget")
	}
	budget, err := workloadfence.CaptureBudget()
	liveMust(t, err)
	daemon, parent := filepath.Join(base, "daemon"), filepath.Join(base, "domains")
	liveMust(t, os.Mkdir(daemon, 0700))
	liveMust(t, os.Mkdir(parent, 0700))
	liveMust(t, os.WriteFile(filepath.Join(daemon, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600))
	remaining, err := os.ReadFile(filepath.Join(base, "cgroup.procs"))
	liveMust(t, err)
	if strings.TrimSpace(string(remaining)) != "" {
		t.Fatal("other process in owned budget")
	}
	for _, p := range []string{base, parent} {
		liveMust(t, os.WriteFile(filepath.Join(p, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600))
	}
	meta := t.TempDir()
	liveMust(t, os.Chmod(meta, 0700))
	root, err := workloadfence.CreateUnderBudget(budget, meta, parent)
	liveMust(t, err)
	jails, err := root.PrepareJails(t.Context(), "cpu", "memory", "pids")
	liveMust(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		proof, err := root.Fence(ctx)
		liveMust(t, err)
		liveMust(t, root.Check(ctx, proof))
		liveMust(t, root.Close())
		deploymentRemoveGroups(t, filepath.Dir(jails.Path()))
		liveMust(t, os.Remove(parent))
		cleanup := filepath.Join(base, "cleanup")
		liveMust(t, os.Mkdir(cleanup, 0700))
		liveMust(t, os.WriteFile(filepath.Join(cleanup, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600))
		deploymentRemoveGroups(t, daemon)
		liveMust(t, os.WriteFile(filepath.Join(base, "cgroup.subtree_control"), []byte("-cpu -memory -pids"), 0600))
		liveMust(t, os.WriteFile(filepath.Join(base, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600))
		liveMust(t, os.Remove(cleanup))
		liveMust(t, budget.Close())
	})
	return root
}

func deploymentRemoveGroups(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	liveMust(t, err)
	for _, e := range entries {
		if e.IsDir() {
			deploymentRemoveGroups(t, filepath.Join(path, e.Name()))
		}
	}
	liveMust(t, os.Remove(path))
}

func deploymentSource(lang, version string) (string, string) {
	if lang == "node" {
		return "handler.js", `const orva=require('orva');exports.handler=async()=>{try{let r=await orva.kv.get('demo');return {statusCode:200,body:JSON.stringify({version:'` + version + `',allowed:r==='fixture'})}}catch(e){return {statusCode:200,body:JSON.stringify({version:'` + version + `',allowed:false})}}}`
	}
	return "handler.py", "import json\nimport orva\ndef handler(event):\n    try:\n        r = orva.kv.get('demo')\n        allowed = r == 'fixture'\n    except Exception:\n        allowed = False\n    return {'statusCode': 200, 'body': json.dumps({'version': '" + version + "', 'allowed': allowed})}\n"
}

func TestQueuedDeploymentLive(t *testing.T) {
	if os.Getenv("ORVA_DEPLOYMENT_LIVE") != "disposable-vm" {
		t.Skip("explicit owned VM only")
	}
	root := deploymentDomain(t)
	dir := os.Getenv("ORVA_RUNTIME_PACKAGE_DIR")
	jail := filepath.Join(dir, "nsjail-linux-amd64")
	verified, err := verifiedRuntimePackage(jail, os.Getenv("ORVA_RUNTIME_NSJAIL_SHA256"))
	liveMust(t, err)
	liveMust(t, verified.Close())
	liveMust(t, os.Chmod(jail, 0755))
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "deployment.db")
			db, err := database.New(path)
			liveMust(t, err)
			defer db.Close()
			liveMust(t, db.Migrate())
			deploymentSchema(t, path, "workload_iam_schema.go", "workload_artifact_schema.go")
			reg := registry.New(db)
			owner, keyID := ids.New(), ids.New()
			bearer := "orva_fixture_only_deployment"
			hash := sha256.Sum256([]byte(bearer))
			liveMust(t, db.InsertAPIKey(&database.APIKey{ID: keyID, Name: "fixture", KeyHash: hex.EncodeToString(hash[:]), Prefix: "fixture", Permissions: `["read","write","invoke","admin"]`}))
			ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: keyID})
			ref, _ := auth.OperatorFromContext(ctx)
			entry, oldSource := deploymentSource(lang, "old")
			fn := &database.Function{ID: owner, Name: "deployment-" + lang, Runtime: lang, Entrypoint: entry, Status: "active",
				CodeHash: digest(oldSource),
				Version:  1, MemoryMB: 256, CPUs: 1, TimeoutMS: 10000, MaxConcurrency: 2, ConcurrencyPolicy: "queue",
				NetworkMode: "none", AuthMode: "none", SDK: true, EnvVars: map[string]string{}}
			liveMust(t, reg.Set(fn))
			record, err := db.NewIAMPolicy(ctx, owner, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
			liveMust(t, err)
			deploymentSchema(t, path, "workload_managed_schema.go")
			generation, err := db.NewWorkloadArtifact(ctx, owner, database.IAMActorReference{Kind: "system", ID: ids.New()})
			liveMust(t, err)
			liveMust(t, db.InitWorkloadDeploymentSchema(ctx))
			e, err := record.AdmissionEvaluator()
			liveMust(t, err)
			authority, err := workloadiam.NewAuthority(32, []*workloadiam.Evaluator{e})
			liveMust(t, err)
			limits := artifactfs.Limits{Generations: 8, Leases: 8, Staging: 1, Entries: 32768, PathBytes: 4096, LinkBytes: 4096, ManifestBytes: 64 << 20, Bytes: 1 << 30, FileBytes: 256 << 20}
			codeRoot := t.TempDir()
			liveMust(t, os.Chmod(codeRoot, 0700))
			code, err := artifactfs.New(codeRoot, limits)
			liveMust(t, err)
			t.Cleanup(func() { liveMust(t, code.Close()) })
			runtime := newQualificationStore(t)
			rid := sealQualificationRuntime(t, runtime, dir, lang, true)
			oldCode, _ := sealQualificationCode(t, code, owner, entry, oldSource, "unused.txt", "unused")
			codeGenerations := []string{oldCode}
			defer func() {
				for _, generation := range codeGenerations {
					reclaimQualified(t, code, generation)
				}
				reclaimQualified(t, runtime, rid)
			}()
			sm := secrets.New(db, t.TempDir())
			snapshot, err := db.GetWorkloadArtifactSnapshot(ctx, owner)
			liveMust(t, err)
			oldFactory, err := NewFactory(ctx, &secrets.PreparedArtifactSnapshot{Function: snapshot.Function, Artifact: *generation, Secrets: map[string]string{}},
				code, oldCode, runtime, rid, FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20})
			liveMust(t, err)
			var manager *pool.Manager
			host := sdkhost.New(db, sdkhost.WithIAM(authority), sdkhost.WithIAMCurrentArtifact(func(id, artifact string) bool { return manager.IsCurrentArtifact(id, artifact) }))
			manager = pool.NewManager(pool.ManagerConfig{ManagedAuthority: authority, DefaultMax: 2, DefaultMaxUses: 0, ReapInterval: time.Hour},
				pool.SandboxTemplate{NsjailBin: jail, RootfsDir: "/no-legacy-rootfs", DataDir: t.TempDir(), DefaultSeccomp: "default", SDKHandler: host.Handle},
				db, reg, sandbox.NewLimiter(4))
			defer func() {
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				liveMust(t, manager.Shutdown(c))
			}()
			liveMust(t, manager.InstallFactory(ctx, oldFactory))
			policy, err := workloadpolicy.NewManaged(db, reg, authority, func(id string) workloadpolicy.ArtifactState {
				a := manager.CurrentArtifact(id)
				return workloadpolicy.ArtifactState{OwnerID: id, Artifact: a, Known: a != ""}
			})
			liveMust(t, err)
			review := func(op string) {
				p, err := db.GetIAMPolicy(ctx, owner)
				liveMust(t, err)
				var artifact string
				liveMust(t, authority.ChangeHostState(func() { artifact = manager.CurrentArtifact(owner) }))
				m := database.IAMManagedMutation{Operation: op, Owners: []database.IAMOwnerExpectation{{OwnerID: owner, Revision: p.Revision, ApprovedArtifact: p.ApprovedArtifact, CurrentArtifact: artifact}}}
				if op == "inline" {
					m.PolicyJSON = []byte(`{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"demo"}},"context":"execution"},{"actions":["functions.invoke"],"resource":{"function":{"id":"` + owner + `"}},"context":"execution"}],"inbound":[{"caller_id":"` + owner + `","triggers":["f2f"]}]}`)
				}
				_, err = policy.MutateManaged(ctx, ref, m)
				liveMust(t, err)
			}
			review("inline")
			liveMust(t, db.KVPut(owner, "demo", []byte(`"fixture"`), nil))
			meta := t.TempDir()
			liveMust(t, os.Chmod(meta, 0700))
			output, err := builder.NewOutputOwner(builder.OutputConfig{CodeStore: code, ParentDomain: root, MetadataRoot: meta, Limits: limits,
				ReservedBytes: 1 << 16, MaxReservedBytes: 1 << 16, ArchiveBytes: 1 << 20, MaxBuilds: 1, BuildTimeout: 30 * time.Second, CleanupTimeout: 5 * time.Second})
			liveMust(t, err)
			defer func() { liveMust(t, output.Close()) }()
			b := builder.New(builder.WithManagedOutput(output), builder.WithRuntimeBinding(func(context.Context, builder.RuntimeRequest) (builder.RuntimeBinding, error) {
				return builder.RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: runtime, Generation: rid}}, nil
			}))
			b.NsjailBin, b.DataDir, b.RootfsDir = jail, t.TempDir(), "/no-legacy-rootfs"
			queue, err := builder.NewPublicationQueue(b, db, reg, authority, manager, sm, code, runtime)
			liveMust(t, err)
			config, err := workloadconfig.New(db, reg, authority, manager, sm, map[string]workloadconfig.Binding{
				owner: {Code: code, Runtime: runtime, CodeGeneration: oldCode, RuntimeGeneration: rid},
			})
			liveMust(t, err)
			inspectConfig := func() database.WorkloadConfigurationExpected {
				row, err := config.Inspect(ctx, ref, owner)
				liveMust(t, err)
				return row["expected"].(database.WorkloadConfigurationExpected)
			}
			queue.Start()
			defer queue.Shutdown(context.Background())
			dispatch := func(acq *pool.AcquireResult, version string, allowed bool) {
				exec := sandbox.WithExecution(ctx, sandbox.ExecInfo{ExecutionID: ids.New()})
				reply, err := acq.Worker.DispatchEx(exec, []byte(`{}`))
				liveMust(t, err)
				var body struct {
					Version string `json:"version"`
					Allowed bool   `json:"allowed"`
				}
				liveMust(t, json.Unmarshal([]byte(reply.Body), &body))
				if body.Version != version || body.Allowed != allowed {
					t.Fatalf("wrong private SDK result version=%s allowed=%v", body.Version, body.Allowed)
				}
			}
			deps := orvamcp.Deps{DB: db, Registry: reg, DeployIAM: queue, RequireDeployIAM: true}
			mux := http.NewServeMux()
			h := &handlers.FunctionHandler{DB: db, Registry: reg, DeployIAM: queue, RequireDeployIAM: true}
			mux.HandleFunc("POST /functions/{fn_id}/deploy-inline", func(w http.ResponseWriter, r *http.Request) {
				h.DeployInline(w, r.WithContext(auth.WithOperator(r.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: keyID})))
			})
			mux.Handle("/mcp", orvamcp.NewHandler(deps))
			server := httptest.NewServer(mux)
			defer server.Close()
			inspection, instrumented := any(queue).(interface {
				InspectionPendingForQA(string) (int, bool)
				ReleaseFailedPinForQA(string) bool
			})
			if !instrumented {
				t.Fatal("physical failure qualification requires orva_publication_qa")
			}
			conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
			liveMust(t, err)
			defer conn.Close()
			faultWorker, err := manager.Acquire(ctx, owner)
			liveMust(t, err)
			defer manager.Release(faultWorker, nil)
			dispatch(faultWorker, "old", true)
			state, ok := faultWorker.Worker.WorkloadState()
			if !ok {
				t.Fatal("missing actual old worker identity")
			}
			// A host-only waiting-edge witness verifies cancellation publication,
			// not a claim that a live nested function invocation was dispatched.
			call, err := authority.BeginCall(ctx, workloadiam.Context{Principal: workloadiam.Principal{
				FunctionID: owner, WorkerID: state.Identity.WorkerID, ArtifactRevision: oldFactory.ArtifactRevision()},
				Ready: true, Phase: workloadiam.Execution, Lease: workloadiam.Lease{
					ID: ids.New(), WorkerID: state.Identity.WorkerID, ExecutionID: ids.New(), Active: true}},
				workloadiam.Request{Operation: workloadiam.OpInvoke, FunctionID: owner, Resource: workloadiam.Resource{Function: &workloadiam.FunctionResource{ID: owner}}})
			liveMust(t, err)
			defer call.Finish()
			beforeCache, err := reg.Get(owner)
			liveMust(t, err)
			beforeState, err := db.GetWorkloadArtifactSnapshot(ctx, owner)
			liveMust(t, err)
			beforePolicy, err := db.GetIAMPolicy(ctx, owner)
			liveMust(t, err)
			_, err = conn.Exec(`CREATE TABLE publication_commit_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
				CREATE TRIGGER reject_publication_commit AFTER INSERT ON workload_iam_audit
				BEGIN INSERT INTO publication_commit_guard VALUES('missing-owner'); END`)
			liveMust(t, err)
			_, rejectedSource := deploymentSource(lang, "rejected")
			failed, err := queue.Inline(ctx, owner, builder.PublicationInline{Code: rejectedSource})
			liveMust(t, err)
			deadline := time.Now().Add(45 * time.Second)
			for {
				dep, err := db.GetDeployment(failed)
				liveMust(t, err)
				if dep.Status == "succeeded" {
					t.Fatal("failed COMMIT published success")
				}
				if dep.Status == "failed" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("failed COMMIT did not terminate")
				}
				time.Sleep(20 * time.Millisecond)
			}
			afterCache, err := reg.Get(owner)
			liveMust(t, err)
			afterState, err := db.GetWorkloadArtifactSnapshot(ctx, owner)
			liveMust(t, err)
			afterPolicy, err := db.GetIAMPolicy(ctx, owner)
			liveMust(t, err)
			var current string
			liveMust(t, authority.ChangeHostState(func() { current = manager.CurrentArtifact(owner) }))
			if current != oldFactory.ArtifactRevision() || !reflect.DeepEqual(beforeCache, afterCache) ||
				!reflect.DeepEqual(beforeState, afterState) || !reflect.DeepEqual(beforePolicy, afterPolicy) || call.Context().Err() != nil {
				t.Fatal("failed COMMIT changed cache, durable generation, current Factory or cancellation")
			}
			dispatch(faultWorker, "old", true)
			manager.Release(faultWorker, nil)
			var outputJSON string
			liveMust(t, conn.QueryRow(`SELECT output FROM workload_deployment_capture WHERE deployment_id=?`, failed).Scan(&outputJSON))
			var retained database.WorkloadBuildOutput
			liveMust(t, json.Unmarshal([]byte(outputJSON), &retained))
			codeGenerations = append(codeGenerations, retained.CodeGeneration)
			lease, err := code.Acquire(retained.CodeGeneration)
			liveMust(t, err)
			if lease.Identity().Digest != retained.CodeDigest {
				t.Fatal("retained sealed output association changed")
			}
			lease.Release()
			count, pinned := inspection.InspectionPendingForQA(failed)
			if count != 1 || !pinned {
				t.Fatal("failed sealed output was not retained and bounded")
			}
			if _, err := queue.Install(ctx, failed); err == nil {
				t.Fatal("failed output became installable authority")
			}
			if !inspection.ReleaseFailedPinForQA(failed) {
				t.Fatal("QA inspection pin cleanup failed")
			}
			lease, err = code.Acquire(retained.CodeGeneration)
			liveMust(t, err)
			lease.Release()
			_, err = conn.Exec(`DROP TRIGGER reject_publication_commit`)
			liveMust(t, err)
			t.Logf("%s failed-COMMIT/cache/Factory/cancel/retained-seal/no-install=PASS", lang)
			var supersededConfig database.WorkloadConfigurationExpected
			for _, transport := range []string{"rest", "mcp", "ai"} {
				old, err := manager.Acquire(ctx, owner)
				liveMust(t, err)
				defer manager.Release(old, nil)
				oldVersion := "old"
				switch transport {
				case "mcp":
					oldVersion = "rest"
				case "ai":
					oldVersion = "mcp"
				}
				dispatch(old, oldVersion, true)
				if transport == "mcp" {
					var artifact string
					liveMust(t, authority.ChangeHostState(func() { artifact = manager.CurrentArtifact(owner) }))
					_, err := config.Environment(ctx, ref, owner, inspectConfig(), artifact, map[string]string{"C1_FIXTURE": "pending"})
					liveMust(t, err)
					supersededConfig = inspectConfig()
				}
				_, source := deploymentSource(lang, transport)
				var deployment string
				if transport == "rest" {
					raw, _ := json.Marshal(map[string]any{"code": source})
					resp, err := http.Post(server.URL+"/functions/"+owner+"/deploy-inline", "application/json", bytes.NewReader(raw))
					liveMust(t, err)
					body, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					liveMust(t, err)
					if resp.StatusCode != 202 {
						t.Fatalf("REST status=%d body=%s", resp.StatusCode, body)
					}
					var out map[string]string
					liveMust(t, json.Unmarshal(body, &out))
					deployment = out["deployment_id"]
				} else {
					args, _ := json.Marshal(map[string]any{"function_id": owner, "code": source, "wait": false})
					if transport == "ai" {
						agent := orvamcp.BuildAgentRegistry(deps, map[string]bool{"read": true, "write": true, "admin": true})
						value, err := agent.Dispatch(ctx, "deploy_function_inline", args)
						liveMust(t, err)
						raw, _ := json.Marshal(value)
						var out map[string]any
						liveMust(t, json.Unmarshal(raw, &out))
						deployment, _ = out["deployment_id"].(string)
					} else {
						rpc := append([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},"name":"deploy_function_inline","arguments":`), args...)
						rpc = append(rpc, []byte(`}}`)...)
						req, err := http.NewRequest("POST", server.URL+"/mcp", bytes.NewReader(rpc))
						liveMust(t, err)
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("Accept", "application/json, text/event-stream")
						req.Header.Set("Authorization", "Bearer "+bearer)
						req.Header.Set("MCP-Protocol-Version", "2026-07-28")
						req.Header.Set("Mcp-Method", "tools/call")
						req.Header.Set("Mcp-Name", "deploy_function_inline")
						resp, err := http.DefaultClient.Do(req)
						liveMust(t, err)
						raw, err := io.ReadAll(resp.Body)
						resp.Body.Close()
						liveMust(t, err)
						if resp.StatusCode != 200 {
							t.Fatal("MCP tools/call did not return HTTP 200")
						}
						if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
							scanner := bufio.NewScanner(bytes.NewReader(raw))
							scanner.Buffer(make([]byte, 4096), 1<<20)
							var data []string
							messages := 0
							flush := func() {
								if len(data) != 0 {
									messages++
									raw = []byte(strings.Join(data, "\n"))
									data = nil
								}
							}
							for scanner.Scan() {
								line := scanner.Text()
								switch {
								case line == "":
									flush()
								case strings.HasPrefix(line, "data:"):
									data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
								case strings.HasPrefix(line, "event:"):
									if strings.TrimSpace(strings.TrimPrefix(line, "event:")) != "message" {
										t.Fatal("unexpected MCP SSE event")
									}
								case strings.HasPrefix(line, ":"), strings.HasPrefix(line, "id:"), strings.HasPrefix(line, "retry:"):
								default:
									t.Fatal("invalid MCP SSE field")
								}
							}
							flush()
							liveMust(t, scanner.Err())
							if messages != 1 {
								t.Fatal("expected one MCP JSON-RPC response")
							}
						} else if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
							t.Fatal("unexpected MCP response content type")
						}
						var reply struct {
							JSONRPC string          `json:"jsonrpc"`
							ID      int             `json:"id"`
							Error   json.RawMessage `json:"error"`
							Result  struct {
								IsError           bool `json:"isError"`
								StructuredContent struct {
									DeploymentID string `json:"deployment_id"`
								} `json:"structuredContent"`
							} `json:"result"`
						}
						liveMust(t, json.Unmarshal(raw, &reply))
						if reply.JSONRPC != "2.0" || reply.ID != 1 || len(reply.Error) != 0 || reply.Result.IsError {
							t.Fatal("MCP response was not a successful correlated tools/call")
						}
						deployment = reply.Result.StructuredContent.DeploymentID
						if deployment == "" {
							t.Fatalf("MCP missing capture status=%d body=%s", resp.StatusCode, raw)
						}
					}
				}
				deadline := time.Now().Add(45 * time.Second)
				for {
					dep, err := db.GetDeployment(deployment)
					liveMust(t, err)
					if dep.Status == "failed" {
						t.Fatalf("real build failed: %s", dep.ErrorMessage)
					}
					if dep.Status == "succeeded" {
						if dep.Phase != "review_required" {
							t.Fatal("not deny-pending-review")
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("build timeout")
					}
					time.Sleep(20 * time.Millisecond)
				}
				installed, err := db.PrepareWorkloadDeploymentInstallation(ctx, deployment)
				liveMust(t, err)
				codeGenerations = append(codeGenerations, installed.Output().CodeGeneration)
				// The exact old worker stays busy but loses SDK admission before new install.
				dispatch(old, oldVersion, false)
				manager.Release(old, nil)
				var current string
				liveMust(t, authority.ChangeHostState(func() { current = manager.CurrentArtifact(owner) }))
				if current != "" {
					t.Fatal("old Factory still current")
				}
				if _, err := config.Install(ctx, ref, owner, inspectConfig()); !errors.Is(err, workloadconfig.ErrUnavailable) {
					t.Fatal("configuration installed before queued deployment was installed")
				}
				if transport == "mcp" {
					if _, err := config.Install(ctx, ref, owner, supersededConfig); !errors.Is(err, database.ErrWorkloadArtifactConflict) {
						t.Fatal("queued successor did not invalidate old pending configuration")
					}
					liveMust(t, authority.ChangeHostState(func() {
						if manager.PendingConfigurationUnderGate(owner) != nil {
							t.Error("queued successor retained a superseded pending configuration")
						}
					}))
				}
				if transport == "rest" {
					select {
					case <-call.Context().Done():
					case <-time.After(2 * time.Second):
						t.Fatal("successful publication failed to cancel witness")
					}
					legacy := filepath.Join(b.DataDir, "functions", owner)
					liveMust(t, os.MkdirAll(legacy, 0700))
					if _, err := queue.Install(ctx, deployment); !errors.Is(err, builder.ErrPublicationUnavailable) {
						t.Fatal("mixed association unexpectedly installed")
					}
					inactive, err := reg.Get(owner)
					liveMust(t, err)
					if inactive.Status != "inactive" {
						t.Fatal("post-DENY filesystem fault activated function")
					}
					if _, err := manager.Acquire(ctx, owner); err == nil {
						t.Fatal("post-DENY fault admitted old or new worker")
					}
					liveMust(t, os.Remove(legacy))
				}
				newArtifact, err := queue.Install(ctx, deployment)
				liveMust(t, err)
				fresh, err := manager.Acquire(ctx, owner)
				liveMust(t, err)
				dispatch(fresh, transport, false)
				manager.Release(fresh, nil)
				review("review")
				fresh, err = manager.Acquire(ctx, owner)
				liveMust(t, err)
				dispatch(fresh, transport, true)
				manager.Release(fresh, nil)
				t.Logf("%s %s deployment=%s factory=%s sealed-build/capture/deny/install/review/SDK=PASS", lang, transport, deployment, newArtifact)
				if transport == "rest" {
					configure := func(artifact string, mutate func(database.WorkloadConfigurationExpected) (*database.WorkloadEnvironmentPublication, error)) string {
						expected := inspectConfig()
						out, err := mutate(expected)
						liveMust(t, err)
						if !out.Policy.ReviewRequired || out.Policy.ApprovedArtifact != expected.ApprovedArtifact || !out.Function.SDK {
							t.Fatal("configuration transferred approval or changed SDK setting")
						}
						if _, err := config.Install(ctx, ref, owner, expected); !errors.Is(err, database.ErrWorkloadArtifactConflict) {
							t.Fatal("stale configuration expectation did not conflict")
						}
						next, err := config.Install(ctx, ref, owner, inspectConfig())
						liveMust(t, err)
						if next == artifact {
							t.Fatal("configuration reused previous artifact")
						}
						liveMust(t, authority.ChangeHostState(func() {
							f := manager.CurrentFactory(owner)
							gotCode, gotRuntime := f.Identities()
							if f.ArtifactRevision() != next || gotCode.Generation != installed.Output().CodeGeneration ||
								gotCode.Digest != installed.Output().CodeDigest || gotRuntime.Generation != rid ||
								gotRuntime.Digest != installed.Output().RuntimeDigest {
								t.Error("configuration install lost queued deployment's concrete code/runtime")
							}
						}))
						acq, err := manager.Acquire(ctx, owner)
						liveMust(t, err)
						dispatch(acq, transport, false)
						manager.Release(acq, nil)
						review("review")
						acq, err = manager.Acquire(ctx, owner)
						liveMust(t, err)
						dispatch(acq, transport, true)
						manager.Release(acq, nil)
						return next
					}
					current := newArtifact
					env := map[string]string{"C1_FIXTURE": "new"}
					current = configure(current, func(expected database.WorkloadConfigurationExpected) (*database.WorkloadEnvironmentPublication, error) {
						return config.Environment(ctx, ref, owner, expected, current, env)
					})
					current = configure(current, func(expected database.WorkloadConfigurationExpected) (*database.WorkloadEnvironmentPublication, error) {
						settings := database.WorkloadExecutionSettings{MemoryMB: 192, CPUs: 1, TimeoutMS: 8000, MaxConcurrency: 2, ConcurrencyPolicy: "queue", NetworkMode: "none", SDK: true, AuthMode: "none"}
						return config.Settings(ctx, ref, owner, expected, current, settings, env)
					})
					current = configure(current, func(expected database.WorkloadConfigurationExpected) (*database.WorkloadEnvironmentPublication, error) {
						return config.Secret(ctx, ref, owner, expected, current, "C1_SECRET", "fixture-value", false)
					})
					configure(current, func(expected database.WorkloadConfigurationExpected) (*database.WorkloadEnvironmentPublication, error) {
						return config.Secret(ctx, ref, owner, expected, current, "C1_SECRET", "", true)
					})
					t.Logf("%s composition: original configuration service kept deployed code through env/settings/secret upsert/delete, exact install and review=PASS", lang)
				}
			}
		})
	}
}
