//go:build linux

package workloadspawn_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	orvamcp "github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

type cronControlSurfaces struct {
	*asyncControls
	keyIDs map[string]string
	deps   orvamcp.Deps
}

func newCronControlSurfaces(t *testing.T, db *database.Database, reg *registry.Registry, service *workloadasync.Service, owner string) *cronControlSurfaces {
	t.Helper()
	c := &cronControlSurfaces{asyncControls: &asyncControls{keys: map[string]string{}}, keyIDs: map[string]string{},
		deps: orvamcp.Deps{DB: db, Registry: reg, CronEnabled: true, CronIAM: service}}
	for role, perms := range map[string]string{"admin": `["admin","read"]`, "admin-only": `["admin"]`, "reader": `["read"]`, "writer": `["write"]`} {
		raw := "orva_" + strings.ReplaceAll(ids.New(), "-", "") + strings.ReplaceAll(ids.New(), "-", "")
		hash := sha256.Sum256([]byte(raw))
		id := ids.New()
		c.keys[role] = raw
		c.keyIDs[role] = id
		liveMust(t, db.InsertAPIKey(&database.APIKey{ID: id, Name: "cron-" + role, KeyHash: hex.EncodeToString(hash[:]), Prefix: "fixture", Permissions: perms}))
	}
	h := &handlers.CronHandler{DB: db, Registry: reg, CronEnabled: true, CronIAM: service}
	rest := http.NewServeMux()
	rest.HandleFunc("GET /api/v1/cron", h.ListAll)
	rest.HandleFunc("GET /api/v1/functions/{fn_id}/cron", h.List)
	rest.HandleFunc("POST /api/v1/functions/{fn_id}/cron", h.Create)
	rest.HandleFunc("PUT /api/v1/functions/{fn_id}/cron/{id}", h.Update)
	rest.HandleFunc("DELETE /api/v1/functions/{fn_id}/cron/{id}", h.Delete)
	rest.HandleFunc("POST /api/v1/functions/{fn_id}/cron/{id}/resume", h.Resume)
	rest.HandleFunc("GET /api/v1/functions", func(w http.ResponseWriter, r *http.Request) {
		fn, err := db.GetFunction(owner)
		if err != nil {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"functions": []*database.Function{fn}})
	})
	mux := http.NewServeMux()
	// Scratch authentication resolves real disposable DB keys, never author identity.
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Orva-API-Key")
		hash := sha256.Sum256([]byte(raw))
		key, err := db.GetAPIKeyByHash(hex.EncodeToString(hash[:]))
		if err != nil || (key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now())) {
			w.WriteHeader(401)
			return
		}
		perms := auth.PermSet{}
		for _, p := range key.PermissionsList() {
			perms[p] = true
		}
		if r.Method == "GET" && !perms.Has("read") {
			w.WriteHeader(403)
			return
		}
		rest.ServeHTTP(w, r.WithContext(auth.WithOperator(r.Context(), &auth.Principal{Kind: "api_key", ID: key.ID, Perms: perms})))
	}))
	mux.Handle("/mcp", orvamcp.NewHandler(c.deps))
	if dir := os.Getenv("ORVA_CRON_UI_DIR"); dir != "" {
		files := http.FileServer(http.Dir(dir))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/other" {
				http.ServeFile(w, r, filepath.Join(dir, "cron-controls.html"))
				return
			}
			files.ServeHTTP(w, r)
		})
	}
	c.server = httptest.NewServer(mux)
	t.Cleanup(c.server.Close)
	return c
}
func cronExpected(c *database.AsyncCron) map[string]any {
	return map[string]any{"expected_config_revision": strconv.FormatUint(c.ConfigRevision, 10), "expected_authority_generation": strconv.FormatUint(c.AuthorityGeneration, 10)}
}
func (c *cronControlSurfaces) negativeChecks(t *testing.T, row *database.AsyncCron) {
	t.Helper()
	path := "/api/v1/functions/" + row.FunctionID + "/cron/" + row.ID + "/resume"
	for _, role := range []string{"reader", "writer"} {
		status, _ := c.request(t, role, "POST", path, cronExpected(row))
		if status != 403 {
			t.Fatal("non-admin HTTP resume", role, status)
		}
		c.cliResume(t, role, row, false)
		catalog := c.rpc(t, role, "tools/list", map[string]any{})
		for _, tool := range catalog.Result.Tools {
			switch tool.Name {
			case "create_cron_schedule", "update_cron_schedule", "resume_cron_schedule", "delete_cron_schedule":
				t.Fatal("non-admin mutation catalog", role, tool.Name)
			}
		}
		args := cronExpected(row)
		args["id"] = row.ID
		reply := c.rpc(t, role, "tools/call", map[string]any{"name": "resume_cron_schedule", "arguments": args})
		if len(reply.Error) == 0 && !reply.Result.IsError {
			t.Fatal("forced non-admin MCP resume succeeded")
		}
	}
	ai := orvamcp.BuildAgentRegistry(c.deps, map[string]bool{"admin": true})
	ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: "api_key", ID: c.keyIDs["admin-only"]})
	args := cronExpected(row)
	args["id"] = row.ID
	args["origin_kind"] = "operator"
	raw, _ := json.Marshal(args)
	if _, err := ai.Dispatch(ctx, "resume_cron_schedule", raw); err == nil {
		t.Fatal("AI author origin claim accepted")
	}
	delete(args, "origin_kind")
	raw, _ = json.Marshal(args)
	readonly := orvamcp.BuildAgentRegistry(c.deps, map[string]bool{"read": true})
	if _, err := readonly.Dispatch(ctx, "resume_cron_schedule", raw); err == nil {
		t.Fatal("read-only AI registry exposed resume")
	}
}
func (c *cronControlSurfaces) cliResume(t *testing.T, role string, row *database.AsyncCron, success bool) {
	t.Helper()
	bin := os.Getenv("ORVA_CRON_CLI")
	if bin == "" {
		t.Fatal("compiled CLI required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--endpoint", c.server.URL, "--api-key", c.keys[role], "-o", "json", "cron", "resume", row.ID, "--fn", row.FunctionID,
		"--config-revision", strconv.FormatUint(row.ConfigRevision, 10), "--authority-generation", strconv.FormatUint(row.AuthorityGeneration, 10))
	cmd.Env = []string{"HOME=" + t.TempDir(), "NO_COLOR=1", "PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if (err == nil) != success {
		t.Fatalf("compiled cron CLI role=%s success=%v wanted=%v", role, err == nil, success)
	}
	if success && !json.Valid(out) {
		t.Fatal("CLI stdout is not JSON")
	}
}
func (c *cronControlSurfaces) resume(t *testing.T, row *database.AsyncCron, surface string) {
	t.Helper()
	switch surface {
	case "cli":
		c.cliResume(t, "admin-only", row, true)
		c.cliResume(t, "admin-only", row, false) // The original CAS is now stale.
	case "mcp":
		args := cronExpected(row)
		args["id"] = row.ID
		reply := c.rpc(t, "admin-only", "tools/call", map[string]any{"name": "resume_cron_schedule", "arguments": args})
		if len(reply.Error) != 0 || reply.Result.IsError {
			t.Logf("MCP protocol error: %.800s", reply.Error)
			for _, part := range reply.Result.Content {
				t.Logf("MCP bounded error: %.500s", part.Text)
			}
			t.Fatal("external MCP admin-only resume failed")
		}
	case "ai":
		args := cronExpected(row)
		args["id"] = row.ID
		raw, _ := json.Marshal(args)
		ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: "api_key", ID: c.keyIDs["admin-only"]})
		_, err := orvamcp.BuildAgentRegistry(c.deps, map[string]bool{"admin": true}).Dispatch(ctx, "resume_cron_schedule", raw)
		liveMust(t, err)
	}
	current, err := c.deps.DB.GetAsyncCron(t.Context(), row.ID)
	liveMust(t, err)
	if current.OriginKind != row.OriginKind || current.CreatorID != row.CreatorID || current.OperatorID != row.OperatorID || current.CreationCallerRevision != row.CreationCallerRevision {
		t.Fatal("public resume adopted origin")
	}
	status, _ := c.request(t, "admin", "POST", "/api/v1/functions/"+row.FunctionID+"/cron/"+row.ID+"/resume", cronExpected(row))
	if status != 409 {
		t.Fatal("stale configuration did not return HTTP 409", status)
	}
}

func TestCronControlProtocol(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "protocol.db"))
	liveMust(t, err)
	defer db.Close()
	liveMust(t, db.Migrate())
	liveMust(t, db.InitCronSchemaForFixture(t.Context()))
	id := ids.New()
	liveMust(t, db.InsertFunction(&database.Function{ID: id, Name: "protocol", Runtime: "node", Entrypoint: "handler.js", Status: "active"}))
	e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: "artifact"}, workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}})
	liveMust(t, err)
	a, err := workloadiam.NewAuthority(4, []*workloadiam.Evaluator{e})
	liveMust(t, err)
	service := &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: func(string) string { return "artifact" }}
	controls := newCronControlSurfaces(t, db, registry.New(db), service, id)
	c := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: id, CronExpr: "@daily", Enabled: true}}
	liveMust(t, service.CreateCronOperator(t.Context(), database.OperatorCredentialReference{Kind: "api_key", ID: controls.keyIDs["admin-only"]}, c))
	liveMust(t, db.SuspendAsyncCron(t.Context(), c, "revoked"))
	c, err = db.GetAsyncCron(t.Context(), c.ID)
	liveMust(t, err)
	controls.resume(t, c, "mcp")
	for _, role := range []string{"admin-only", "reader"} {
		catalog := controls.rpc(t, role, "tools/list", map[string]any{})
		for _, tool := range catalog.Result.Tools {
			if !strings.HasSuffix(tool.Name, "_cron_schedule") && tool.Name != "list_cron_schedules" {
				continue
			}
			var output map[string]any
			liveMust(t, json.Unmarshal(tool.OutputSchema, &output))
			if tool.Name == "list_cron_schedules" {
				output = output["properties"].(map[string]any)["schedules"].(map[string]any)["items"].(map[string]any)
			}
			for _, key := range []string{"config_revision", "authority_generation", "creation_caller_revision", "creation_target_revision", "admission_caller_revision", "admission_target_revision"} {
				if output["properties"].(map[string]any)[key].(map[string]any)["type"] != "string" {
					t.Fatal("external output revision is not string", tool.Name, key)
				}
			}
			if tool.Name == "create_cron_schedule" || tool.Name == "list_cron_schedules" {
				continue
			}
			var input map[string]any
			liveMust(t, json.Unmarshal(tool.InputSchema, &input))
			ai := orvamcp.BuildAgentRegistry(controls.deps, map[string]bool{"admin": true})
			var aiInput map[string]any
			liveMust(t, json.Unmarshal(ai.Get(tool.Name).Schema, &aiInput))
			for _, key := range []string{"expected_config_revision", "expected_authority_generation"} {
				for _, schema := range []map[string]any{input, aiInput} {
					if schema["properties"].(map[string]any)[key].(map[string]any)["type"] != "string" {
						t.Fatal("input revision is not string", tool.Name, key)
					}
				}
			}
		}
	}
	fixtureSQL, err := sql.Open("sqlite", db.Path())
	liveMust(t, err)
	defer fixtureSQL.Close()
	for _, surface := range []string{"mcp", "ai"} {
		for _, revision := range []int64{9007199254740993, math.MaxInt64 - 1} {
			t.Run(surface+"-"+strconv.FormatInt(revision, 10), func(t *testing.T) {
				// Seed boundary counters only in this disposable, non-runtime protocol DB.
				_, err := fixtureSQL.ExecContext(t.Context(), `UPDATE cron_schedules SET authority_state='suspended',authority_reason='revoked',next_run_at=NULL,config_revision=?,authority_generation=?,creation_caller_revision=?,creation_target_revision=? WHERE id=?`,
					revision, revision, int64(math.MaxInt64), int64(math.MaxInt64), c.ID)
				liveMust(t, err)
				current, err := db.GetAsyncCron(t.Context(), c.ID)
				liveMust(t, err)
				args := cronExpected(current)
				args["id"] = current.ID
				var raw []byte
				if surface == "mcp" {
					reply := controls.rpc(t, "admin-only", "tools/call", map[string]any{"name": "resume_cron_schedule", "arguments": args})
					if len(reply.Error) != 0 || reply.Result.IsError {
						t.Fatal("large decimal MCP resume failed", string(reply.Error))
					}
					raw = []byte(reply.Result.Content[0].Text)
				} else {
					input, _ := json.Marshal(args)
					ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: "api_key", ID: controls.keyIDs["admin-only"]})
					value, err := orvamcp.BuildAgentRegistry(controls.deps, map[string]bool{"admin": true}).Dispatch(ctx, "resume_cron_schedule", input)
					liveMust(t, err)
					raw, err = json.Marshal(value)
					liveMust(t, err)
				}
				var wire map[string]any
				liveMust(t, json.Unmarshal(raw, &wire))
				want := strconv.FormatInt(revision+1, 10)
				for _, key := range []string{"config_revision", "authority_generation"} {
					if wire[key] != want {
						t.Fatal("wire revision rounded", surface, key, wire[key])
					}
				}
				for _, key := range []string{"creation_caller_revision", "creation_target_revision"} {
					if wire[key] != "9223372036854775807" {
						t.Fatal("creation revision rounded", surface, key)
					}
				}
				if revision == math.MaxInt64-1 {
					status, _ := controls.request(t, "admin-only", "PUT", "/api/v1/functions/"+id+"/cron/"+c.ID, map[string]any{"expected_config_revision": want, "expected_authority_generation": want, "enabled": false})
					if status != 409 {
						t.Fatal("signed-max mutation did not fail closed", status)
					}
					after, err := db.GetAsyncCron(t.Context(), c.ID)
					liveMust(t, err)
					if after.ConfigRevision != math.MaxInt64 || after.AuthorityGeneration != math.MaxInt64 || !after.Enabled {
						t.Fatal("signed-max mutation wrapped or changed configuration")
					}
				}
			})
		}
	}
	t.Log("PASS: external InputSchema/OutputSchema and AI registry use strings; real MCP/AI responses preserve >2^53 and signed-max counters; signed-max mutation denies without wrap")
}
func (c *cronControlSurfaces) browser(t *testing.T, row *database.AsyncCron, viewport string) {
	t.Helper()
	script := os.Getenv("ORVA_CRON_BROWSER_SCRIPT")
	if script == "" {
		t.Fatal("actual browser script required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	fn, err := c.deps.DB.GetFunction(row.FunctionID)
	liveMust(t, err)
	cmd := exec.CommandContext(ctx, "node", script)
	cmd.Env = append(os.Environ(), "CRON_URL="+c.server.URL, "CRON_ADMIN="+c.keys["admin"], "CRON_READER="+c.keys["reader"], "CRON_ID="+row.ID,
		"CRON_OWNER="+row.FunctionID, "CRON_VIEWPORT="+viewport, "CRON_RUNTIME="+fn.Runtime)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser %s failed: %s", viewport, out)
	}
	t.Logf("browser %s: %s", viewport, out)
}
