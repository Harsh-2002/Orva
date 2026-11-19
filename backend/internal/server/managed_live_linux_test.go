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
		db, err := database.New(filepath.Join(dataDir, "orva.db"))
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
	for _, lang := range []string{"node", "python"} {
		t.Run(lang, func(t *testing.T) {
			managedServeLifecycle(t, db, call, lang)
		})
	}
	node, err := db.GetFunctionByName("managed-node")
	managedMust(t, err)
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
		call(t, http.MethodPost, hookCollection, request, 409)
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
		managedMust(t, backup.ArchiveTo(&archive, dataDir, snapshot))
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
