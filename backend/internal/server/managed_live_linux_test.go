//go:build linux

package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/backup"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/backend/internal/testutil/iamruntime"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/backend/internal/workloadrecovery"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedServingLive(t *testing.T) {
	if os.Getenv("ORVA_MANAGED_SERVING_TEST") != "disposable-vm" {
		t.Skip("requires a delegated disposable VM and hash-pinned Node/Python assets")
	}
	dir := os.Getenv("ORVA_STARTUP_SEALED_RUNTIME_DIR")
	jail := iamruntime.Nsjail(t, dir)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	budget, parent := managedTestBudget(t)
	root := t.TempDir()
	mkdir := func(name string) string {
		path := filepath.Join(root, name)
		managedMust(t, os.Mkdir(path, 0700))
		return path
	}
	dataDir := mkdir("data")
	codeLimits := artifactfs.Limits{Generations: 16, Leases: 64, Staging: 4, Entries: 512,
		PathBytes: 4096, LinkBytes: 4096, ManifestBytes: 1 << 20, Bytes: 64 << 20, FileBytes: 8 << 20}
	ownerConfig := artifactfs.OwnerConfig{Budget: budget, Parent: parent, MetadataRoot: mkdir("owner"), CodeRoot: mkdir("code"),
		RuntimeRoot: mkdir("runtime"), CodeLimits: codeLimits, RuntimeLimits: iamruntime.Limits(), MaxEpochs: 8}
	owner, err := artifactfs.NewOwner(ctx, ownerConfig)
	managedMust(t, err)
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if owner != nil {
			managedMust(t, owner.Close(stop))
		}
	}()
	assets, err := owner.Bindings(ctx)
	managedMust(t, err)
	instanceID, err := owner.InstanceID(ctx)
	managedMust(t, err)
	layout, err := servingmode.Acquire(dataDir)
	managedMust(t, err)
	relocateErr := layout.RelocateToManaged(instanceID, ids.New())
	managedMust(t, layout.Close())
	managedMust(t, relocateErr)
	managedDir := filepath.Join(dataDir, servingmode.Managed)
	input := ManagedOptions{Artifacts: owner, DataDir: dataDir, NsjailBin: jail,
		RuntimeGenerations: make(map[sandbox.Language]string), Output: builder.OutputConfig{
			MetadataRoot: mkdir("build"), Limits: codeLimits, ReservedBytes: 1 << 20, MaxReservedBytes: 2 << 20,
			ArchiveBytes: 2 << 20, MaxBuilds: 2, BuildTimeout: 30 * time.Second, CleanupTimeout: 5 * time.Second}}
	for _, lang := range []sandbox.Language{sandbox.Node, sandbox.Python} {
		input.RuntimeGenerations[lang] = iamruntime.Seal(t, assets.Runtime, ownerConfig.RuntimeLimits, dir, string(lang)).Generation
	}
	browserNode := os.Getenv("ORVA_MANAGED_BROWSER_NODE")
	var providerURL string
	if browserNode != "" {
		providerURL, input.ChatGuard = managedChatProvider(t)
	}
	const fixtureKey = "orva_scratch_managed_serving"
	func() {
		db, err := database.New(filepath.Join(managedDir, "orva.db"))
		managedMust(t, err)
		defer func() { managedMust(t, db.Close()) }()
		managedMust(t, db.Migrate())
		key := sha256.Sum256([]byte(fixtureKey))
		managedMust(t, db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: "fixture", Prefix: "fixture",
			KeyHash: fmt.Sprintf("%x", key), Permissions: `["read","write","admin","invoke"]`}))
	}()
	service, err := StartManaged(ctx, input)
	managedMust(t, err)
	db := service.database
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if service != nil {
			managedMust(t, service.Close(stop))
		}
	}()
	listener := httptest.NewServer(service)
	defer func() { listener.Close() }()
	reopen := func(t *testing.T) {
		t.Helper()
		listener.Close()
		managedMust(t, service.Close(ctx))
		managedMust(t, owner.Close(ctx))
		owner, err = artifactfs.OpenOwnerRecovery(ctx, ownerConfig)
		managedMust(t, err)
		input.Artifacts = owner
		service, err = StartManaged(ctx, input)
		managedMust(t, err)
		db = service.database
		listener = httptest.NewServer(service)
	}
	call := func(t *testing.T, method, path string, body any, want int) []byte {
		t.Helper()
		raw, err := json.Marshal(body)
		managedMust(t, err)
		req, err := http.NewRequestWithContext(ctx, method, listener.URL+path, strings.NewReader(string(raw)))
		managedMust(t, err)
		req.Header.Set("X-Orva-API-Key", fixtureKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res, err := listener.Client().Do(req)
		managedMust(t, err)
		defer res.Body.Close()
		b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		managedMust(t, err)
		if res.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, res.StatusCode, want, b)
		}
		return b
	}
	fixtureRan := false
	t.Run("fixture-invoke", func(t *testing.T) {
		fixtureRan = true
		managedFixtureInvoke(t, db, call)
	})
	egressRan := false
	t.Run("egress-rotation", func(t *testing.T) {
		egressRan = true
		var fn struct{ ID string }
		managedMust(t, json.Unmarshal(call(t, http.MethodPost, "/api/v1/functions",
			map[string]string{"name": "managed-egress", "runtime": "node"}, 201), &fn))
		base := "/api/v1/functions/" + fn.ID
		config := base + "/configuration"
		var deployment struct {
			ID string `json:"deployment_id"`
		}
		managedMust(t, json.Unmarshal(call(t, http.MethodPost, base+"/deploy-inline", map[string]string{
			"filename": "handler.js",
			"code":     `exports.handler=()=>({statusCode:200,body:"egress fixture"})`,
		}, 202), &deployment))
		install := "/api/v1/deployments/" + deployment.ID + "/install"
		deadline := time.Now().Add(30 * time.Second)
		for {
			row, err := db.GetDeployment(deployment.ID)
			managedMust(t, err)
			if row.Status == "succeeded" {
				break
			}
			if row.Status == "failed" || time.Now().After(deadline) {
				t.Fatalf("egress fixture build: %s %s", row.Status, row.ErrorMessage)
			}
			time.Sleep(30 * time.Millisecond)
		}
		call(t, http.MethodPost, install, nil, 200)
		var current struct {
			Expected map[string]string `json:"expected"`
			Artifact string            `json:"current_artifact"`
		}
		managedMust(t, json.Unmarshal(call(t, http.MethodGet, config, nil, 200), &current))
		call(t, http.MethodPut, config+"/settings", map[string]any{
			"expected": current.Expected, "current_artifact": current.Artifact, "environment": map[string]string{},
			"settings": database.WorkloadExecutionSettings{TimeoutMS: 5000, MemoryMB: 128, CPUs: 0.5,
				NetworkMode: "egress", ConcurrencyPolicy: "queue", AuthMode: "none"},
		}, 200)
		call(t, http.MethodPost, install, nil, 200)
		invoke := func() {
			t.Helper()
			if got := string(call(t, http.MethodGet, "/fn/"+fn.ID, nil, 200)); got != "egress fixture" {
				t.Fatalf("egress fixture returned %q", got)
			}
		}
		invoke()
		manager := service.service.runtime.pool
		factory := manager.CurrentFactory(fn.ID)
		if factory == nil || factory.Resources().NetworkMode != "egress" {
			t.Fatal("no installed egress Factory")
		}
		acquired, err := manager.Acquire(ctx, fn.ID)
		managedMust(t, err)
		oldWorker := acquired.Worker
		manager.Release(acquired, nil)
		_, before, err := service.service.firewall.EgressPolicy()
		managedMust(t, err)
		call(t, http.MethodPost, "/api/v1/firewall/rules",
			map[string]string{"rule_type": "cidr", "value": "192.0.2.77/32", "label": "egress fixture"}, 201)
		_, after, err := service.service.firewall.EgressPolicy()
		managedMust(t, err)
		if before == after || oldWorker.AcceptsNewDispatch() || manager.CurrentFactory(fn.ID) != factory {
			t.Fatal("policy publication did not replace only the egress workers")
		}
		invoke()
		acquired, err = manager.Acquire(ctx, fn.ID)
		managedMust(t, err)
		replacement := acquired.Worker
		manager.Release(acquired, nil)
		if replacement == oldWorker {
			t.Fatal("rotated worker was acquired again")
		}
		reopen(t)
		invoke()
	})
	nodeRan := false
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			if lang == "node" {
				nodeRan = true
			}
			managedServeLifecycle(t, db, call, lang)
			t.Run("build-cache", func(t *testing.T) {
				fn, err := db.GetFunctionByName("managed-" + lang)
				managedMust(t, err)
				cache, err := builder.BuildCacheDir(managedDir, fn.ID)
				managedMust(t, err)
				managedMust(t, os.MkdirAll(cache, 0700))
				managedMust(t, os.WriteFile(filepath.Join(cache, "fixture"), []byte("cache"), 0600))
				call(t, http.MethodDelete, "/api/v1/functions/"+fn.ID+"/build-cache", nil, 200)
				if _, err := os.Stat(cache); !os.IsNotExist(err) {
					t.Fatalf("dependency cache remains: %v", err)
				}
				call(t, http.MethodGet, "/fn/"+fn.ID, nil, 200)
			})
		})
	}
	// The standalone journeys do not create the Node owner used below.
	if (egressRan || fixtureRan) && !nodeRan {
		return
	}
	node, err := db.GetFunctionByName("managed-node")
	managedMust(t, err)
	t.Run("deployment-compare", func(t *testing.T) {
		base := "/api/v1/functions/"
		var fn struct{ ID string }
		managedMust(t, json.Unmarshal(call(t, http.MethodPost, strings.TrimSuffix(base, "/"),
			map[string]string{"name": "compare-probe", "runtime": "node"}, 201), &fn))
		deploy := func(code string) string {
			t.Helper()
			var result struct {
				ID string `json:"deployment_id"`
			}
			managedMust(t, json.Unmarshal(call(t, http.MethodPost, base+fn.ID+"/deploy-inline",
				map[string]string{"filename": "handler.js", "code": code}, 202), &result))
			for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
				row, err := db.GetDeployment(result.ID)
				managedMust(t, err)
				switch row.Status {
				case "succeeded":
					return result.ID
				case "failed":
					t.Fatal("fixture deployment failed")
				}
				time.Sleep(25 * time.Millisecond)
			}
			t.Fatal("fixture deployment timed out")
			return ""
		}
		before := "exports.handler = () => ({statusCode: 200, body: 'before-value'});\n"
		after := "exports.handler = () => ({statusCode: 200, body: 'after-value'});\n"
		from := deploy(before)
		to := deploy(after)
		query := "/diff?from=" + from + "&to=" + to
		var comparison struct {
			Files []struct{ Path, Kind, Before, After string }
		}
		managedMust(t, json.Unmarshal(call(t, http.MethodGet, base+"compare-probe"+query, nil, 200), &comparison))
		if len(comparison.Files) != 1 || comparison.Files[0].Path != "handler.js" ||
			comparison.Files[0].Kind != "handler" || comparison.Files[0].Before != before || comparison.Files[0].After != after {
			t.Fatal("managed comparison did not read the exact authored pair")
		}
		diff := string(call(t, http.MethodGet, base+fn.ID+query+"&format=unified", nil, 200))
		if !strings.Contains(diff, "-"+before) || !strings.Contains(diff, "+"+after) {
			t.Fatal("managed unified diff does not match the authored pair")
		}
		call(t, http.MethodGet, base+node.ID+query, nil, 400)
		call(t, http.MethodGet, base+fn.ID+"/diff?from="+from+"&to="+from, nil, 400)
	})
	configuration := "/api/v1/functions/" + node.ID + "/configuration"
	lifecycle := func(operation string) {
		t.Helper()
		var inspection struct {
			Expected struct {
				Generation     string `json:"generation"`
				PolicyRevision string `json:"policy_revision"`
				AuthorityEpoch string `json:"authority_epoch"`
			} `json:"expected"`
		}
		managedMust(t, json.Unmarshal(call(t, http.MethodGet, configuration, nil, 200), &inspection))
		e := inspection.Expected
		call(t, http.MethodPost, "/api/v1/functions/"+node.ID+"/"+operation,
			map[string]any{"generation": e.Generation, "policy_revision": e.PolicyRevision,
				"authority_epoch": e.AuthorityEpoch, "confirm": true}, 200)
	}
	t.Run("pause-reopen-resume", func(t *testing.T) {
		lifecycle("pause")
		call(t, http.MethodGet, "/fn/"+node.ID, nil, 409)
		reopen(t)
		call(t, http.MethodGet, "/fn/"+node.ID, nil, 409)
		lifecycle("resume")
		call(t, http.MethodGet, "/fn/"+node.ID, nil, 409)
		call(t, http.MethodPost, "/api/v1/deployments/"+node.ActiveDeploymentID+"/install", nil, 200)
		if got := string(call(t, http.MethodGet, "/fn/"+node.ID, nil, 200)); got != "denied:403" {
			t.Fatalf("resume bypassed review: %q", got)
		}
	})
	for _, lang := range []string{"node", "python"} {
		t.Run("configuration-"+lang, func(t *testing.T) {
			fn, err := db.GetFunctionByName("managed-" + lang)
			managedMust(t, err)
			base := "/api/v1/functions/" + fn.ID
			type inspection struct {
				Expected map[string]string `json:"expected"`
				Current  string            `json:"current_artifact"`
				Review   bool              `json:"review_required"`
			}
			inspect := func() inspection {
				t.Helper()
				var out inspection
				managedMust(t, json.Unmarshal(call(t, http.MethodGet, base+"/configuration", nil, 200), &out))
				return out
			}
			invoke := func(want string) {
				t.Helper()
				if got := string(call(t, http.MethodGet, "/fn/"+fn.ID, nil, 200)); got != want {
					t.Fatalf("configuration invocation: got %q, want %q", got, want)
				}
			}
			initial := inspect()
			call(t, http.MethodPut, base+"/permissions", map[string]any{
				"expected": map[string]string{"revision": initial.Expected["policy_revision"], "current_artifact": initial.Current,
					"approved_artifact": initial.Expected["approved_artifact"]},
				"policy": json.RawMessage(`{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"test-key"}},"context":"execution"}],"inbound":[]}`),
			}, 200)
			initial = inspect()
			review := func(cur inspection) {
				t.Helper()
				call(t, http.MethodPost, base+"/permissions/review", map[string]any{"expected": map[string]string{
					"revision": cur.Expected["policy_revision"], "current_artifact": cur.Current,
					"approved_artifact": cur.Expected["approved_artifact"],
				}}, 200)
			}
			review(initial)
			initial = inspect()
			invoke("allowed")
			body := map[string]any{"expected": initial.Expected, "current_artifact": initial.Current,
				"environment": map[string]string{"ORVA_IAM_FIXTURE": "changed"}}
			_, err = db.WriteDB().ExecContext(ctx, `CREATE TABLE config_commit_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
				CREATE TRIGGER config_commit_failure AFTER INSERT ON workload_iam_audit
				BEGIN INSERT INTO config_commit_guard VALUES('missing'); END`)
			managedMust(t, err)
			call(t, http.MethodPut, base+"/configuration/environment", body, 503)
			_, err = db.WriteDB().ExecContext(ctx, `DROP TRIGGER config_commit_failure; DROP TABLE config_commit_guard`)
			managedMust(t, err)
			if after := inspect(); !maps.Equal(after.Expected, initial.Expected) || after.Current != initial.Current || after.Review {
				t.Fatal("failed COMMIT changed the observed configuration")
			}
			invoke("allowed")
			call(t, http.MethodPut, base+"/configuration/environment", body, 200)
			call(t, http.MethodGet, "/fn/"+fn.ID, nil, 409)
			pending := inspect()
			if pending.Current != "" || !pending.Review || pending.Expected["generation"] == initial.Expected["generation"] {
				t.Fatal("configuration mutation did not publish an inactive review-required generation")
			}
			if lang == "node" {
				reopen(t)
				call(t, http.MethodGet, "/fn/"+fn.ID, nil, 409)
				call(t, http.MethodPost, "/api/v1/deployments/"+fn.ActiveDeploymentID+"/install", nil, 200)
			} else {
				call(t, http.MethodPost, base+"/configuration/install", map[string]any{"expected": pending.Expected}, 200)
				reopen(t)
			}
			invoke("denied:403")
			installed := inspect()
			if installed.Current == "" || installed.Current == initial.Current || !installed.Review {
				t.Fatal("restart/install did not recover the new exact deny generation")
			}
			manager := service.service.runtime.pool
			acquired, err := manager.Acquire(ctx, fn.ID)
			managedMust(t, err)
			version, err := strconv.Atoi(installed.Expected["function_version"])
			managedMust(t, err)
			previous, err := strconv.Atoi(initial.Expected["function_version"])
			managedMust(t, err)
			rejectedStale := !manager.AcceptsManagedVersion(acquired, previous)
			acceptedCurrent := manager.AcceptsManagedVersion(acquired, version)
			manager.Release(acquired, nil)
			if !rejectedStale || !acceptedCurrent {
				t.Fatal("managed HTTP admission did not bind to the acquired function version")
			}
			review(installed)
			invoke("allowed:changed")
		})
	}
	t.Run("executions-replay", func(t *testing.T) {
		var original string
		deadline := time.Now().Add(10 * time.Second)
		for original == "" && time.Now().Before(deadline) {
			rows, err := db.ListExecutions(database.ListExecutionsParams{FunctionID: node.ID, Limit: 50})
			managedMust(t, err)
			for _, row := range rows.Executions {
				if _, err := db.GetExecutionRequest(row.ID); err == nil {
					original = row.ID
					break
				}
			}
			if original == "" {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if original == "" {
			t.Fatal("no managed invocation left a captured request")
		}
		replay := func(want int) *http.Response {
			t.Helper()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, listener.URL+"/api/v1/executions/"+original+"/replay", nil)
			managedMust(t, err)
			req.Header.Set("X-Orva-API-Key", fixtureKey)
			res, err := listener.Client().Do(req)
			managedMust(t, err)
			defer res.Body.Close()
			io.Copy(io.Discard, res.Body)
			if res.StatusCode != want {
				t.Fatalf("replay: got %d want %d", res.StatusCode, want)
			}
			return res
		}
		res := replay(200)
		if res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "sandbox ") {
			t.Fatalf("replay response lacks the function isolation guards: %v", res.Header)
		}
		replayed := res.Header.Get("X-Orva-Execution-ID")
		if replayed == "" || res.Header.Get("X-Orva-Replay-Of") != original {
			t.Fatal("replay did not report the new execution it created")
		}
		deadline = time.Now().Add(10 * time.Second)
		for {
			row, err := db.GetExecution(replayed)
			if err == nil {
				if row.Trigger != "replay" || row.ReplayOf == nil || *row.ReplayOf != original {
					t.Fatalf("replay row not linked to its original: %+v", row)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("replay execution row was not recorded")
			}
			time.Sleep(50 * time.Millisecond)
		}
		// A request admitted against a Factory whose version no longer matches the
		// registry is refused, never served by the stale Factory.
		reg := service.service.runtime.invoke.Registry
		current, err := reg.Get(node.ID)
		managedMust(t, err)
		stale := current.Clone()
		stale.Version++
		reg.CacheCommitted(stale)
		replay(http.StatusConflict)
		reg.CacheCommitted(current)
		replay(200)
	})
	t.Run("operator-surfaces", func(t *testing.T) {
		do := func(method, path, key string, body any, want int) (http.Header, []byte) {
			t.Helper()
			var reader io.Reader
			if body != nil {
				raw, err := json.Marshal(body)
				managedMust(t, err)
				reader = strings.NewReader(string(raw))
			}
			req, err := http.NewRequestWithContext(ctx, method, listener.URL+path, reader)
			managedMust(t, err)
			if key != "" {
				req.Header.Set("Authorization", "Bearer "+key)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			res, err := listener.Client().Do(req)
			managedMust(t, err)
			defer res.Body.Close()
			b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
			managedMust(t, err)
			if res.StatusCode != want {
				t.Fatalf("%s %s: got %d want %d: %s", method, path, res.StatusCode, want, b)
			}
			return res.Header, b
		}
		poll := func(what string, ok func() bool) {
			t.Helper()
			deadline := time.Now().Add(10 * time.Second)
			for !ok() {
				if time.Now().After(deadline) {
					t.Fatalf("timed out waiting for %s", what)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		sse := func(raw []byte) []byte {
			for _, line := range strings.Split(string(raw), "\n") {
				if payload, ok := strings.CutPrefix(line, "data: "); ok {
					return []byte(payload)
				}
			}
			return raw
		}

		// A real managed invocation, then everything an operator reads about it.
		head, _ := do(http.MethodGet, "/fn/"+node.ID, "", nil, 200)
		execID, traceID := head.Get("X-Orva-Execution-ID"), head.Get("X-Trace-Id")
		if execID == "" || traceID == "" {
			t.Fatal("managed invocation did not report its execution and trace ids")
		}
		poll("execution row", func() bool { _, err := db.GetExecution(execID); return err == nil })
		var execution struct {
			FunctionID string `json:"function_id"`
		}
		_, raw := do(http.MethodGet, "/api/v1/executions/"+execID, fixtureKey, nil, 200)
		managedMust(t, json.Unmarshal(raw, &execution))
		if execution.FunctionID != node.ID {
			t.Fatal("execution row names a different function")
		}
		// The fixture function writes nothing to stderr, and a run with no log has no
		// log row: the authorized route answers 404 NOT_FOUND, as it does on the legacy router.
		do(http.MethodGet, "/api/v1/executions/"+execID+"/logs", fixtureKey, nil, 404)
		poll("captured request", func() bool { _, err := db.GetExecutionRequest(execID); return err == nil })
		do(http.MethodGet, "/api/v1/executions/"+execID+"/request", fixtureKey, nil, 200)
		do(http.MethodGet, "/api/v1/traces/"+traceID, fixtureKey, nil, 200)
		poll("activity row for the invocation", func() bool {
			rows, _, _, err := db.ListActivity(database.ActivityFilter{Search: "/fn/" + node.ID})
			return err == nil && len(rows) > 0
		})
		poll("activity row for the operator", func() bool {
			rows, _, _, err := db.ListActivity(database.ActivityFilter{Search: "/api/v1/executions/" + execID})
			return err == nil && len(rows) > 0 && rows[0].ActorType == "api_key" && rows[0].Source == "api"
		})
		do(http.MethodGet, "/api/v1/activity", fixtureKey, nil, 200)
		do(http.MethodGet, "/api/v1/system/metrics.json", fixtureKey, nil, 200)
		_, metrics := do(http.MethodGet, "/metrics", "", nil, 200)
		if !strings.Contains(string(metrics), "orva_") {
			t.Fatal("managed /metrics exposed no Orva metrics")
		}
		do(http.MethodGet, "/api/v1/system/health", "", nil, 200)

		// Replay through the MCP tool, the same managed admission as REST replay.
		_, raw = do(http.MethodPost, "/mcp", fixtureKey, map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call",
			"params": map[string]any{"name": "replay_execution", "arguments": map[string]any{"execution_id": execID}}}, 200)
		var replayed struct {
			Result struct {
				IsError    bool `json:"isError"`
				Structured struct {
					ExecutionID string `json:"execution_id"`
					ReplayOf    string `json:"replay_of"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		managedMust(t, json.Unmarshal(sse(raw), &replayed))
		if replayed.Result.IsError || replayed.Result.Structured.ReplayOf != execID || replayed.Result.Structured.ExecutionID == "" {
			t.Fatalf("replay_execution did not replay through managed admission: %s", raw)
		}

		// API keys: a minted key works, and a revoked one stops at once.
		var minted struct{ ID, Key string }
		_, raw = do(http.MethodPost, "/api/v1/keys", fixtureKey, map[string]any{"name": "live-revoke", "permissions": []string{"read"}}, 201)
		managedMust(t, json.Unmarshal(raw, &minted))
		do(http.MethodGet, "/api/v1/executions", minted.Key, nil, 200)
		do(http.MethodGet, "/api/v1/keys", minted.Key, nil, 403)
		do(http.MethodDelete, "/api/v1/keys/"+minted.ID, fixtureKey, nil, 200)
		do(http.MethodGet, "/api/v1/executions", minted.Key, nil, 401)

		// A channel token reaches only its bundled function, through managed admission.
		var channel struct{ ID, Token string }
		_, raw = do(http.MethodPost, "/api/v1/channels", fixtureKey, map[string]any{"name": "live-agents", "function_ids": []string{node.ID}}, 201)
		managedMust(t, json.Unmarshal(raw, &channel))
		do(http.MethodGet, "/api/v1/channels", channel.Token, nil, 401)
		_, raw = do(http.MethodPost, "/mcp", channel.Token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200)
		if !strings.Contains(string(raw), `"name":"managed_node"`) || strings.Contains(string(raw), "create_iam_policy") {
			t.Fatalf("channel catalog is not the invoke-only bundle: %s", raw)
		}
		_, raw = do(http.MethodPost, "/mcp", channel.Token, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{"name": "managed_node", "arguments": map[string]any{"method": "GET"}}}, 200)
		var invoked struct {
			Result struct {
				IsError    bool `json:"isError"`
				Structured struct {
					StatusCode  int    `json:"status_code"`
					ExecutionID string `json:"execution_id"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		managedMust(t, json.Unmarshal(sse(raw), &invoked))
		if invoked.Result.IsError || invoked.Result.Structured.StatusCode != 200 || invoked.Result.Structured.ExecutionID == "" {
			t.Fatalf("channel invocation did not run through the managed pool: %s", raw)
		}
		poll("channel execution row", func() bool { _, err := db.GetExecution(invoked.Result.Structured.ExecutionID); return err == nil })
		do(http.MethodDelete, "/api/v1/channels/"+channel.ID, fixtureKey, nil, 200)
		do(http.MethodPost, "/mcp", channel.Token, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}, 401)
	})
	t.Run("external-mcp-catalog", func(t *testing.T) {
		body := call(t, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, 200)
		if !strings.Contains(string(body), `"name":"revoke_iam_policy"`) || strings.Contains(string(body), `"name":"update_function"`) {
			t.Fatal("HTTP MCP did not expose the restricted live managed catalog")
		}
	})
	if browserNode != "" {
		t.Run("browser", func(t *testing.T) {
			configureManagedChat(t, service.service.chat, providerURL)
			cmd := exec.CommandContext(ctx, browserNode, "/browser/managed-iam.mjs")
			cmd.Env = append(os.Environ(), "ORVA_MANAGED_BROWSER_ORIGIN="+listener.URL,
				"ORVA_MANAGED_BROWSER_OWNER="+node.ID, "ORVA_MANAGED_BROWSER_KEY="+fixtureKey)
			if libs := os.Getenv("ORVA_MANAGED_BROWSER_LIBRARY_PATH"); libs != "" {
				cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH="+libs)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("managed browser journey failed: %v\n%s", err, out)
			}
			t.Log(string(out))
		})
	}
	if cli := os.Getenv("ORVA_MANAGED_CLI"); cli != "" {
		t.Run("slim-cli-runtime-discovery", func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			env := append(os.Environ(), "ORVA_CONFIG="+configPath, "ORVA_ENDPOINT=", "ORVA_API_KEY=")
			login := func(key string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, cli, "login", "--endpoint", listener.URL, "--api-key", "-", "--test")
				cmd.Env = env
				cmd.Stdin = strings.NewReader(key)
				return cmd.CombinedOutput()
			}
			if out, err := login("orva_invalid_fixture"); err == nil || !bytes.Contains(out, []byte("credentials rejected")) {
				t.Fatalf("CLI did not reject invalid credentials: %v", err)
			}
			if _, err := os.Stat(configPath); !os.IsNotExist(err) {
				t.Fatalf("failed login unexpectedly touched config: %v", err)
			}
			if out, err := login(fixtureKey); err != nil || bytes.Contains(out, []byte("too old to verify")) {
				t.Fatalf("CLI did not verify valid credentials: %v", err)
			}
			cmd := exec.CommandContext(ctx, cli, "__complete", "deploy", "--runtime", "")
			cmd.Env = env
			out, err := cmd.Output()
			managedMust(t, err)
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) != 3 || lines[0] != "node" || lines[1] != "python" || !strings.HasPrefix(lines[2], ":") {
				t.Fatalf("expected node and python completion results")
			}
		})
		t.Run("slim-cli", func(t *testing.T) {
			cmd := exec.CommandContext(ctx, cli, "iam", "permissions", "get", node.ID, "--output", "json")
			cmd.Env = append(os.Environ(), "ORVA_ENDPOINT="+listener.URL, "ORVA_API_KEY="+fixtureKey)
			raw, err := cmd.Output()
			managedMust(t, err)
			var view workloadPolicySnapshot
			managedMust(t, json.Unmarshal(raw, &view))
			if view.Function.ID != node.ID || view.Host.CurrentArtifact == nil {
				t.Fatal("slim CLI did not observe the exact live function")
			}
			const name = "from-slim-cli"
			cmd = exec.CommandContext(ctx, cli, "iam", "policies", "create", "--request", "@-", "--output", "json")
			cmd.Env = append(os.Environ(), "ORVA_ENDPOINT="+listener.URL, "ORVA_API_KEY="+fixtureKey)
			cmd.Stdin = strings.NewReader(`{"name":"` + name + `","policy":{"schema_version":1,"allow":[],"inbound":[]}}`)
			raw, err = cmd.Output()
			managedMust(t, err)
			var created struct{ Policy database.IAMManagedPolicy }
			managedMust(t, json.Unmarshal(raw, &created))
			var detail struct{ Policy database.IAMManagedPolicy }
			managedMust(t, json.Unmarshal(call(t, http.MethodGet, "/api/v1/iam/policies/"+created.Policy.ID, nil, 200), &detail))
			if detail.Policy != created.Policy || detail.Policy.Name != name || detail.Policy.LatestVersion != 1 {
				t.Fatal("REST did not observe the policy committed through the slim CLI")
			}
		})
		t.Run("slim-cli-secrets", func(t *testing.T) {
			var fn struct{ ID string }
			managedMust(t, json.Unmarshal(call(t, http.MethodPost, "/api/v1/functions",
				map[string]string{"name": "cli-secret-probe", "runtime": "node"}, 201), &fn))
			var deployed struct {
				ID string `json:"deployment_id"`
			}
			managedMust(t, json.Unmarshal(call(t, http.MethodPost, "/api/v1/functions/"+fn.ID+"/deploy-inline", map[string]string{
				"filename": "handler.js",
				"code":     "exports.handler = () => ({statusCode: 200, body: process.env.CLI_SECRET || 'unset'});\n",
			}, 202), &deployed))
			for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(25 * time.Millisecond) {
				row, err := db.GetDeployment(deployed.ID)
				managedMust(t, err)
				if row.Status == "succeeded" {
					break
				}
				if row.Status == "failed" || time.Now().After(deadline) {
					t.Fatalf("fixture deployment %s", row.Status)
				}
			}
			call(t, http.MethodPost, "/api/v1/deployments/"+deployed.ID+"/install", nil, 200)
			if got := string(call(t, http.MethodGet, "/fn/"+fn.ID, nil, 200)); got != "unset" {
				t.Fatalf("fixture did not start without the secret: %q", got)
			}
			run := func(args ...string) string {
				t.Helper()
				cmd := exec.CommandContext(ctx, cli, append([]string{"secrets"}, args...)...)
				cmd.Env = append(os.Environ(), "ORVA_CONFIG="+filepath.Join(t.TempDir(), "config.yaml"),
					"ORVA_ENDPOINT="+listener.URL, "ORVA_API_KEY="+fixtureKey)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("orva secrets %s failed: %v\n%s", args[0], err, out)
				}
				if strings.Contains(string(out), "synthetic-cli-secret") {
					t.Fatalf("orva secrets %s printed the secret value", args[0])
				}
				return string(out)
			}
			run("set", fn.ID, "CLI_SECRET", "--value", "synthetic-cli-secret", "--install")
			if got := string(call(t, http.MethodGet, "/fn/"+fn.ID, nil, 200)); got != "synthetic-cli-secret" {
				t.Fatalf("sandboxed function did not receive the secret set through the CLI: %q", got)
			}
			if !strings.Contains(run("list", fn.ID), "CLI_SECRET") {
				t.Fatal("orva secrets list does not show the key")
			}
			run("--yes", "delete", fn.ID, "CLI_SECRET", "--install")
			if got := string(call(t, http.MethodGet, "/fn/"+fn.ID, nil, 200)); got != "unset" {
				t.Fatalf("secret still visible after CLI delete: %q", got)
			}
		})
	}
	const routePath = "/managed-acceptance/*"
	const routeInvokePath = "/managed-acceptance/probe"
	type routeState struct {
		Version   string                     `json:"version"`
		Expected  map[string]json.RawMessage `json:"expected"`
		Published bool                       `json:"route_published"`
	}
	inspectRoute := func(t *testing.T) routeState {
		t.Helper()
		var state routeState
		params := url.Values{"kind": {"route"}, "key": {routePath}, "function_id": {node.ID}}
		managedMust(t, json.Unmarshal(call(t, http.MethodGet, "/api/v1/exposures?"+params.Encode(), nil, 200), &state))
		return state
	}
	publishRoute := func(t *testing.T, state routeState, status int) {
		t.Helper()
		call(t, http.MethodPost, "/api/v1/routes", map[string]any{
			"path": routePath, "function_id": node.ID, "methods": "GET", "version": state.Version, "expected": state.Expected,
		}, status)
	}
	t.Run("custom-route", func(t *testing.T) {
		call(t, http.MethodGet, routeInvokePath, nil, 404)
		inspect := inspectRoute(t)
		publishRoute(t, inspect, 201)
		publishRoute(t, inspect, 409)
		call(t, http.MethodPost, routeInvokePath, nil, 405)
		if got := string(call(t, http.MethodGet, routeInvokePath, nil, 200)); got != "allowed:changed" {
			t.Fatalf("custom route did not use the same managed Factory and SDK policy: %q", got)
		}
		if current := inspectRoute(t); !current.Published || current.Version != "1" {
			t.Fatal("published route not observed through inspection")
		}
	})
	hookCollection := "/api/v1/functions/" + node.ID + "/inbound-webhooks"
	var hookID, hookSecret string
	type hookState struct {
		Version   string                     `json:"version"`
		Expected  map[string]json.RawMessage `json:"expected"`
		Published bool                       `json:"inbound_published"`
	}
	inspectHook := func(t *testing.T, id string) hookState {
		t.Helper()
		var state hookState
		params := url.Values{"kind": {"inbound"}, "function_id": {node.ID}}
		if id != "" {
			params.Set("key", id)
		}
		managedMust(t, json.Unmarshal(call(t, http.MethodGet, "/api/v1/exposures?"+params.Encode(), nil, 200), &state))
		return state
	}
	webhookPost := func(t *testing.T, id, body, secret string, want int) string {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, listener.URL+"/webhook/"+id, strings.NewReader(body))
		managedMust(t, err)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		req.Header.Set("X-Orva-Signature", hex.EncodeToString(mac.Sum(nil)))
		res, err := listener.Client().Do(req)
		managedMust(t, err)
		defer res.Body.Close()
		b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		managedMust(t, err)
		if res.StatusCode != want {
			t.Fatalf("POST /webhook/<id>: got %d want %d: %s", res.StatusCode, want, b)
		}
		return string(b)
	}
	t.Run("inbound-webhook", func(t *testing.T) {
		fresh := inspectHook(t, "")
		if fresh.Version != "0" || fresh.Published {
			t.Fatal("unexpected state for a webhook that does not exist")
		}
		var created struct {
			Webhook struct {
				ID string `json:"id"`
			} `json:"inbound_webhook"`
			Secret string `json:"secret"`
		}
		request := map[string]any{"name": "live hook", "version": fresh.Version, "expected": fresh.Expected}
		managedMust(t, json.Unmarshal(call(t, http.MethodPost, hookCollection, request, 201), &created))
		hookID, hookSecret = created.Webhook.ID, created.Secret
		if hookID == "" || len(hookSecret) != 64 {
			t.Fatal("create did not return the trigger and its one-time secret")
		}
		// Each create mints a fresh trigger ID; only a stale exposure counter conflicts.
		call(t, http.MethodPost, hookCollection, map[string]any{"name": "stale", "version": "1", "expected": fresh.Expected}, 409)
		for _, path := range []string{hookCollection, hookCollection + "/" + hookID} {
			if strings.Contains(string(call(t, http.MethodGet, path, nil, 200)), hookSecret) {
				t.Fatal("inspection returned the signature secret")
			}
		}
		// Rejected requests never reach the sealed worker.
		webhookPost(t, hookID, `{"event":"push"}`, strings.Repeat("0", 64), 401)
		if got := webhookPost(t, hookID, `{"event":"push"}`, hookSecret, 200); got != "allowed:changed" {
			t.Fatalf("signed webhook did not use the same managed Factory and SDK policy: %q", got)
		}
		if current := inspectHook(t, hookID); !current.Published || current.Version != "1" {
			t.Fatal("published webhook not observed through inspection")
		}
	})
	t.Run("inbound-webhook-final-admission", func(t *testing.T) {
		fresh := inspectHook(t, "")
		var created struct {
			Webhook struct {
				ID string `json:"id"`
			} `json:"inbound_webhook"`
			Secret string `json:"secret"`
		}
		managedMust(t, json.Unmarshal(call(t, http.MethodPost, hookCollection,
			map[string]any{"name": "final admission", "version": fresh.Version, "expected": fresh.Expected}, 201), &created))
		id, secret := created.Webhook.ID, created.Secret
		if got := webhookPost(t, id, `{"event":"ok"}`, secret, 200); got != "allowed:changed" {
			t.Fatalf("control request did not run: %q", got)
		}
		// An unpublishing commit lands after lookup and signature verification and
		// after the worker is acquired, but before dispatch: user code must not run.
		handlers.InboundFinalAdmissionTestHook = func(hookID string) {
			if hookID == id {
				_, err := db.WriteDB().Exec(`UPDATE inbound_webhooks SET active=0 WHERE id=?`, id)
				managedMust(t, err)
			}
		}
		t.Cleanup(func() { handlers.InboundFinalAdmissionTestHook = nil })
		if got := webhookPost(t, id, `{"event":"raced"}`, secret, 404); strings.Contains(got, "allowed") {
			t.Fatalf("raced webhook executed: %q", got)
		}
		handlers.InboundFinalAdmissionTestHook = nil
		// The released worker and execution slot are reusable; the mapping stays unpublished.
		webhookPost(t, id, `{"event":"after"}`, secret, 404)
		state := inspectHook(t, id)
		if state.Published || state.Version != "1" {
			t.Fatal("raw edit did not leave an unpublished mapping at its CAS version")
		}
		call(t, http.MethodDelete, hookCollection+"/"+id,
			map[string]any{"confirm": true, "version": state.Version, "expected": state.Expected}, 200)
		if got := webhookPost(t, hookID, `{"event":"still"}`, hookSecret, 200); got != "allowed:changed" {
			t.Fatalf("unrelated published webhook stopped serving: %q", got)
		}
	})
	if !t.Run("restore-archive", func(t *testing.T) {
		owners := make([]string, 0, 2)
		for _, lang := range []string{"node", "python"} {
			fn, err := db.GetFunctionByName("managed-" + lang)
			managedMust(t, err)
			owners = append(owners, fn.ID)
			if got := string(call(t, http.MethodGet, "/fn/"+fn.ID, nil, 200)); got != "allowed:changed" {
				t.Fatalf("%s archive control did not have approved access: %q", lang, got)
			}
		}
		snapshot := filepath.Join(t.TempDir(), "snapshot.db")
		managedMust(t, backup.SnapshotDB(db.ReadDB(), snapshot))
		var archive bytes.Buffer
		managedMust(t, backup.ArchiveTo(&archive, managedDir, snapshot))
		ownerID, err := owner.InstanceID(ctx)
		managedMust(t, err)
		listener.Close()
		managedMust(t, service.Close(ctx))
		managedMust(t, owner.Close(ctx))
		func() {
			restore, err := workloadrecovery.Open(dataDir, workloadrecovery.Host{
				Quiesce: func(context.Context) error { return nil }, AdmissionClosed: func() bool { return true },
			})
			managedMust(t, err)
			defer func() { managedMust(t, restore.Close()) }()
			operation, err := restore.RestoreBound(ctx, ownerID, &archive)
			managedMust(t, err)
			if operation == "" {
				t.Fatal("archive restore did not stage a durable operation")
			}
		}()
		owner, err = artifactfs.OpenOwnerRecovery(ctx, ownerConfig)
		managedMust(t, err)
		input.Artifacts = owner
		service, err = StartManaged(ctx, input)
		managedMust(t, err)
		db = service.database
		listener = httptest.NewServer(service)
		for _, id := range owners {
			var view struct {
				Host struct {
					Quarantined bool
					Current     *string `json:"current_artifact"`
				}
			}
			managedMust(t, json.Unmarshal(call(t, http.MethodGet, "/api/v1/functions/"+id+"/permissions", nil, 200), &view))
			if !view.Host.Quarantined || view.Host.Current == nil {
				t.Fatal("restore did not reconstruct an exact quarantine-only Factory")
			}
			if got := string(call(t, http.MethodGet, "/fn/"+id, nil, 200)); got != "denied:403" {
				t.Fatalf("restored sealed worker did not deny stored authority: %q", got)
			}
		}
		call(t, http.MethodGet, routeInvokePath, nil, 404)
		route := inspectRoute(t)
		if route.Published || route.Version != "1" {
			t.Fatal("restore did not preserve the route CAS counter while removing publication")
		}
		publishRoute(t, route, 201)
		if got := string(call(t, http.MethodGet, routeInvokePath, nil, 200)); got != "denied:403" {
			t.Fatalf("republished route changed quarantined workload authority: %q", got)
		}
		webhookPost(t, hookID, `{"event":"push"}`, hookSecret, 404)
		hook := inspectHook(t, hookID)
		if hook.Published || hook.Version != "1" {
			t.Fatal("restore did not preserve the webhook CAS counter while removing publication")
		}
		call(t, http.MethodPut, hookCollection+"/"+hookID, map[string]any{"version": hook.Version, "expected": hook.Expected}, 200)
		if got := webhookPost(t, hookID, `{"event":"push"}`, hookSecret, 200); got != "denied:403" {
			t.Fatalf("republished webhook changed quarantined workload authority: %q", got)
		}
	}) {
		t.FailNow()
	}
	t.Run("delete-history", func(t *testing.T) {
		fn, err := db.GetFunctionByName("managed-python")
		managedMust(t, err)
		call(t, http.MethodDelete, "/api/v1/functions/"+fn.Name, nil, 400)
		call(t, http.MethodDelete, "/api/v1/functions/"+fn.ID, nil, 200)
		call(t, http.MethodGet, "/fn/"+fn.ID, nil, 404)
		var history []database.HistoryEntry
		managedMust(t, json.Unmarshal(call(t, http.MethodPost, "/api/v1/work-history/list",
			map[string]any{"target_id": fn.ID, "limit": 20}, 200), &history))
		if len(history) != 1 || history[0].Kind != "delete" || history[0].Reserved {
			t.Fatal("managed deletion did not leave one completed inspection-only record")
		}
		if service.service.runtime.pool.CurrentArtifact(fn.ID) != "" {
			t.Fatal("managed deletion left a current Factory")
		}
	})
}

func managedServeLifecycle(t *testing.T, db *database.Database, call func(*testing.T, string, string, any, int) []byte, lang string) {
	t.Helper()
	decode := func(raw []byte, out any) { t.Helper(); managedMust(t, json.Unmarshal(raw, out)) }
	var fn struct{ ID string }
	decode(call(t, http.MethodPost, "/api/v1/functions", map[string]string{"name": "managed-" + lang, "runtime": lang}, 201), &fn)
	base := "/api/v1/functions/" + fn.ID
	filename, source := "handler.js", `const {kv}=require("orva")
exports.handler=async()=>{try{return {statusCode:200,body:(await kv.get("test-key"))+(process.env.ORVA_IAM_FIXTURE?":"+process.env.ORVA_IAM_FIXTURE:"")}}
catch(e){return {statusCode:200,body:"denied:"+e.status}}}`
	if lang == "python" {
		filename, source = "handler.py", `import os, orva
def handler(event):
    try:
        value = os.environ.get("ORVA_IAM_FIXTURE")
        return {"statusCode": 200, "body": orva.kv.get("test-key") + (":" + value if value else "")}
    except orva.OrvaError as err:
        return {"statusCode": 200, "body": "denied:" + str(err.status)}
`
	}
	var deployed struct {
		ID string `json:"deployment_id"`
	}
	decode(call(t, http.MethodPost, base+"/deploy-inline", map[string]string{"code": source, "filename": filename}, 202), &deployed)
	deadline := time.Now().Add(30 * time.Second)
	for {
		row, err := db.GetDeployment(deployed.ID)
		managedMust(t, err)
		if row.Status == "succeeded" {
			break
		}
		if row.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("managed build status=%s phase=%s error=%s", row.Status, row.Phase, row.ErrorMessage)
		}
		time.Sleep(30 * time.Millisecond)
	}
	call(t, http.MethodPost, "/api/v1/deployments/"+deployed.ID+"/install", nil, 200)
	var displayed struct{ Code, Dependencies string }
	decode(call(t, http.MethodGet, base+"/source", nil, 200), &displayed)
	if displayed.Code != source || displayed.Dependencies != "" {
		t.Fatal("Editor source does not match the authored sealed entrypoint")
	}
	permissions := base + "/permissions"
	getExpected := func() map[string]any {
		t.Helper()
		var info struct {
			Host struct {
				Revision string
				Current  *string `json:"current_artifact"`
				Approved *string `json:"approved_artifact"`
			}
		}
		decode(call(t, http.MethodGet, permissions, nil, 200), &info)
		if info.Host.Current == nil {
			t.Fatal("installed Factory not observed")
		}
		return map[string]any{"revision": info.Host.Revision, "current_artifact": info.Host.Current, "approved_artifact": info.Host.Approved}
	}
	checkInvoke := func(stage, want string) {
		t.Helper()
		if got := string(call(t, http.MethodGet, "/fn/"+fn.ID, nil, 200)); got != want {
			t.Fatalf("%s %s invocation: got %q want %q", lang, stage, got, want)
		}
	}
	managedMust(t, db.KVPut(fn.ID, "test-key", []byte(`"allowed"`), nil))
	checkInvoke("before attachment", "denied:403")
	policy := json.RawMessage(`{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"test-key"}},"context":"execution"}],"inbound":[]}`)
	var named struct {
		Policy struct{ ID string }
	}
	decode(call(t, http.MethodPost, "/api/v1/iam/policies", map[string]any{"name": "reader-" + lang, "policy": policy}, 200), &named)
	policyURL := "/api/v1/iam/policies/" + named.Policy.ID
	owners := func() []map[string]any {
		return []map[string]any{{"function_id": fn.ID, "expected": getExpected()}}
	}
	call(t, http.MethodPost, policyURL+"/attach", map[string]any{"membership_revision": "1", "version": "1", "owners": owners()}, 200)
	checkInvoke("before initial aggregate review", "denied:403")
	call(t, http.MethodPost, permissions+"/review", map[string]any{"expected": getExpected()}, 200)
	checkInvoke("after initial aggregate review", "allowed")
	mcpWrite := func(tool string, request map[string]any) {
		t.Helper()
		raw := call(t, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": map[string]any{"id": named.Policy.ID, "request": request}}}, 200)
		for _, line := range strings.Split(string(raw), "\n") {
			if payload, ok := strings.CutPrefix(line, "data: "); ok {
				raw = []byte(payload)
				break
			}
		}
		var response struct {
			Error  json.RawMessage
			Result struct {
				IsError    bool
				Structured json.RawMessage `json:"structuredContent"`
			}
		}
		decode(raw, &response)
		if len(response.Error) != 0 || response.Result.IsError || len(response.Result.Structured) == 0 {
			t.Fatalf("%s did not return a successful structured result", tool)
		}
	}
	mcpWrite("update_iam_policy", map[string]any{
		"definition_revision": "1",
		"policy":              json.RawMessage(`{"schema_version":1,"allow":[],"inbound":[]}`),
	})
	checkInvoke("after immutable version creation", "allowed")
	type policyDetail struct {
		Policy      database.IAMManagedPolicy
		Attachments []database.IAMAttachedOwner
		History     []struct{ Operation string }
	}
	inspect := func() policyDetail {
		t.Helper()
		var detail policyDetail
		decode(call(t, http.MethodGet, policyURL+"?history=true", nil, 200), &detail)
		return detail
	}
	detail := inspect()
	if detail.Policy.LatestVersion != 2 || len(detail.Attachments) != 1 || detail.Attachments[0].Version != 1 {
		t.Fatal("creating a new version silently changed the reviewed pin")
	}
	rollout := func(membership, version string, status int) {
		t.Helper()
		call(t, http.MethodPost, policyURL+"/attach", map[string]any{
			"membership_revision": membership, "version": version, "owners": owners(),
		}, status)
	}
	rollout("1", "2", 409)
	checkInvoke("after stale rollout refusal", "allowed")
	rollout("2", "2", 200)
	checkInvoke("after rollout to empty version", "denied:403")
	rollout("3", "1", 200)
	checkInvoke("after reviewed rollout on the same approved artifact", "allowed")
	mcpWrite("detach_iam_policy", map[string]any{"membership_revision": "4", "owners": owners(), "confirm": true})
	checkInvoke("after detach", "denied:403")
	rollout("5", "1", 200)
	checkInvoke("after reattachment on the same approved artifact", "allowed")
	call(t, http.MethodPost, policyURL+"/revoke", map[string]any{
		"definition_revision": "2", "membership_revision": "6", "owners": owners(), "confirm": true,
	}, 200)
	checkInvoke("after terminal revoke", "denied:403")
	detail = inspect()
	operations := map[string]bool{}
	for _, item := range detail.History {
		operations[item.Operation] = true
	}
	if !detail.Policy.Revoked || !operations["update"] || !operations["detach"] || !operations["revoke"] {
		t.Fatal("REST audit did not show the lifecycle performed through REST and MCP")
	}
}

func managedTestBudget(t *testing.T) (*workloadfence.Budget, string) {
	t.Helper()
	const base = "/sys/fs/cgroup/orva-startup-sealed"
	raw, err := os.ReadFile("/proc/self/cgroup")
	managedMust(t, err)
	if string(raw) != "0::/orva-startup-sealed\n" {
		t.Fatal("managed live test is not under its dedicated guest cgroup")
	}
	budget, err := workloadfence.CaptureBudget()
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, budget.Close()) })
	daemon, parent := filepath.Join(base, "daemon"), filepath.Join(base, "domains")
	managedMust(t, os.Mkdir(daemon, 0700))
	managedMust(t, os.Mkdir(parent, 0700))
	managedMust(t, os.WriteFile(filepath.Join(daemon, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0600))
	raw, err = os.ReadFile(filepath.Join(base, "cgroup.procs"))
	managedMust(t, err)
	if len(raw) != 0 {
		t.Fatal("refusing to delegate shared guest cgroup")
	}
	for _, path := range []string{base, parent} {
		managedMust(t, os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600))
	}
	return budget, parent
}
