//go:build linux

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// managedOpsFixture is a managed router over a real database and a real managed
// pool without installed Factories: every function dispatch is denied, so the
// operator surfaces can be exercised without a sandbox.
type managedOpsFixture struct {
	t         *testing.T
	db        *database.Database
	reg       *registry.Registry
	runtime   *managedRuntime
	router    http.Handler
	owner     string
	authority *workloadiam.Authority
}

func newManagedOpsFixture(t *testing.T, tune ...func(*managedRouteDeps)) *managedOpsFixture {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "managed.db"))
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, db.Close()) })
	managedMust(t, db.Migrate())
	owner := ids.New()
	managedMust(t, db.InsertFunction(&database.Function{ID: owner, Name: "managed", Runtime: "node", Status: "active", AuthMode: "none", Version: 1, TimeoutMS: 5000}))
	_, err = db.ApplyRecoveryForFixture(t.Context(), "")
	managedMust(t, err)
	record, err := db.GetIAMPolicy(t.Context(), owner)
	managedMust(t, err)
	evaluator, err := record.AdmissionEvaluator()
	managedMust(t, err)
	authority, err := workloadiam.NewAuthority(32, []*workloadiam.Evaluator{evaluator})
	managedMust(t, err)
	reg := registry.New(db)
	runtime := newManagedRuntime(db, reg, authority, nil, pool.SandboxTemplate{DataDir: t.TempDir()}, sandbox.NewLimiter(1))
	t.Cleanup(func() {
		managedMust(t, runtime.pool.Shutdown(context.Background()))
		runtime.hub.Close()
	})
	ops := runtime.routeDeps("/usr/local/bin/nsjail")
	for _, fn := range tune {
		fn(ops)
	}
	router, err := managedRoutesWithAsync(db, reg, authority, runtime.pool, nil, nil, filepath.Dir(db.Path()), runtime.invoke, nil, nil, nil, ops)
	managedMust(t, err)
	return &managedOpsFixture{t: t, db: db, reg: reg, runtime: runtime, router: router, owner: owner, authority: authority}
}

// key mints an API key with the given JSON permission list and returns its bearer.
func (f *managedOpsFixture) key(name, permissions string) (bearer, id string) {
	f.t.Helper()
	bearer = "orva_" + strings.ReplaceAll(ids.New(), "-", "")
	hash := sha256.Sum256([]byte(bearer))
	id = ids.New()
	managedMust(f.t, f.db.InsertAPIKey(&database.APIKey{ID: id, Name: name, Prefix: name,
		KeyHash: hex.EncodeToString(hash[:]), Permissions: permissions}))
	return bearer, id
}

func (f *managedOpsFixture) call(method, path, bearer, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("X-Orva-API-Key", bearer)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *managedOpsFixture) expect(method, path, bearer, body string, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := f.call(method, path, bearer, body)
	if rec.Code != want {
		f.t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	return rec
}

func TestManagedRouterWritesActivityForAuthenticatedRequests(t *testing.T) {
	f := newManagedOpsFixture(t)
	bearer, keyID := f.key("activity-key", `["read","write","admin"]`)
	f.expect(http.MethodGet, "/api/v1/functions/"+f.owner+"/permissions", bearer, "", http.StatusOK)
	f.expect(http.MethodGet, "/api/v1/functions/"+f.owner+"/permissions", "", "", http.StatusUnauthorized)

	var got []database.ActivityRow
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, _, _, err := f.db.ListActivity(database.ActivityFilter{ActorID: keyID})
		managedMust(t, err)
		if got = rows; len(got) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) != 1 {
		t.Fatalf("expected one activity row attributed to the key, got %+v", got)
	}
	row := got[0]
	if row.Source != "api" || row.ActorType != "api_key" || row.ActorLabel != "activity-key" ||
		row.Method != http.MethodGet || row.Status != http.StatusOK || row.RequestID == "" {
		t.Fatalf("unexpected activity row: %+v", row)
	}
	// The unauthenticated attempt is recorded too, as an anonymous 401.
	deadline = time.Now().Add(5 * time.Second)
	var denied bool
	for !denied && time.Now().Before(deadline) {
		rows, _, _, err := f.db.ListActivity(database.ActivityFilter{StatusMin: 401, StatusMax: 401})
		managedMust(t, err)
		denied = len(rows) > 0 && rows[0].ActorType == "anon"
		time.Sleep(20 * time.Millisecond)
	}
	if !denied {
		t.Fatal("an unauthenticated managed request left no anonymous activity row")
	}
}

func (f *managedOpsFixture) seedExecution(withCapture bool) string {
	f.t.Helper()
	id := ids.New()
	managedMust(f.t, f.db.InsertExecutionFinal(&database.Execution{ID: id, FunctionID: f.owner, Status: "success"}, 3, 200, "", 2))
	managedMust(f.t, f.db.InsertExecutionLog(&database.ExecutionLog{ExecutionID: id, FunctionID: f.owner, Stderr: "boom\n"}))
	if withCapture {
		f.db.AsyncInsertExecutionRequest(&database.ExecutionRequest{ExecutionID: id, FunctionID: f.owner, Method: "POST",
			Path: "/", HeadersJSON: "{}", Body: []byte(`{"a":1}`), CapturedAt: time.Now().UnixMilli()})
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := f.db.GetExecutionRequest(id); err == nil {
				return id
			}
			time.Sleep(20 * time.Millisecond)
		}
		f.t.Fatal("captured request was not persisted")
	}
	return id
}

func TestManagedExecutionsAuthorityAndDeletion(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("reader", `["read"]`)
	writer, _ := f.key("writer", `["write"]`)
	admin, _ := f.key("admin", `["read","write","admin"]`)
	revoked, revokedID := f.key("revoked", `["read","write","admin"]`)
	id := f.seedExecution(true)
	base := "/api/v1/executions"

	f.expect(http.MethodGet, base, "", "", http.StatusUnauthorized)
	for _, path := range []string{base, base + "/" + id, base + "/" + id + "/logs", base + "/" + id + "/request"} {
		f.expect(http.MethodGet, path, reader, "", http.StatusOK)
		f.expect(http.MethodGet, path, writer, "", http.StatusForbidden)
	}
	if body := f.expect(http.MethodGet, base+"/"+id+"/logs", reader, "", http.StatusOK).Body.String(); !strings.Contains(body, "boom") {
		t.Fatalf("logs missing stderr: %s", body)
	}
	// Mutations need admin, not merely read.
	f.expect(http.MethodDelete, base+"/"+id, reader, "", http.StatusForbidden)
	f.expect(http.MethodPost, base+"/bulk-delete", reader, `{"ids":["`+id+`"]}`, http.StatusForbidden)
	f.expect(http.MethodPost, base+"/"+id+"/replay", reader, "", http.StatusForbidden)

	managedMust(t, f.db.DeleteAPIKey(revokedID))
	f.expect(http.MethodGet, base, revoked, "", http.StatusUnauthorized)
	f.expect(http.MethodDelete, base+"/"+id, revoked, "", http.StatusUnauthorized)

	f.expect(http.MethodDelete, base+"/"+id, admin, "", http.StatusOK)
	f.expect(http.MethodGet, base+"/"+id, admin, "", http.StatusNotFound)
	f.expect(http.MethodDelete, base+"/"+id, admin, "", http.StatusNotFound)
	a, b := f.seedExecution(false), f.seedExecution(false)
	rec := f.expect(http.MethodPost, base+"/bulk-delete", admin, `{"ids":["`+a+`","`+b+`","missing"]}`, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"deleted":2`) || !strings.Contains(rec.Body.String(), `"not_found":1`) {
		t.Fatalf("unexpected bulk delete result: %s", rec.Body.String())
	}
}

func TestManagedReplayRefusesWithoutManagedAdmission(t *testing.T) {
	f := newManagedOpsFixture(t)
	admin, _ := f.key("admin", `["read","write","admin"]`)
	id := f.seedExecution(true)
	replays := func() int {
		rows, err := f.db.ListExecutions(database.ListExecutionsParams{FunctionID: f.owner, Limit: 100})
		managedMust(t, err)
		n := 0
		for _, e := range rows.Executions {
			if e.ReplayOf != nil {
				n++
			}
		}
		return n
	}
	// The fixture's manager has no installed Factory, so managed admission
	// denies; replay must surface that rather than reach any legacy path.
	rec := f.expect(http.MethodPost, "/api/v1/executions/"+id+"/replay", admin, "", http.StatusServiceUnavailable)
	if !strings.Contains(rec.Body.String(), pool.ErrNoFunction.Error()) {
		t.Fatalf("replay did not reach the managed missing-Factory guard: %s", rec.Body.String())
	}
	f.expect(http.MethodPost, "/api/v1/executions/"+ids.New()+"/replay", admin, "", http.StatusNotFound)
	f.expect(http.MethodPost, "/api/v1/executions/"+f.seedExecution(false)+"/replay", admin, "", http.StatusNotFound)
	time.Sleep(200 * time.Millisecond)
	if replays() != 0 {
		t.Fatal("a refused replay left an execution row")
	}
}

func TestManagedObservabilitySurfaces(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("reader", `["read"]`)
	writer, _ := f.key("writer", `["write"]`)
	admin, _ := f.key("admin", `["read","write","admin"]`)
	revoked, revokedID := f.key("revoked", `["read"]`)

	trace := "tr_" + strings.ReplaceAll(ids.New(), "-", "")
	exec := &database.Execution{ID: ids.New(), FunctionID: f.owner, Status: "success", TraceID: trace,
		SpanID: "sp_" + strings.ReplaceAll(ids.New(), "-", "")[:16], Trigger: "http", StartedAt: time.Now()}
	managedMust(t, f.db.AsyncInsertExecutionFinal(exec, 4, 200, "", 2))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := f.db.GetExecution(exec.ID); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("execution row was not persisted")
		}
		time.Sleep(20 * time.Millisecond)
	}

	reads := []string{"/api/v1/traces", "/api/v1/traces/" + trace, "/api/v1/functions/" + f.owner + "/baseline",
		"/api/v1/activity", "/api/v1/system/metrics", "/api/v1/system/metrics.json"}
	for _, path := range reads {
		f.expect(http.MethodGet, path, "", "", http.StatusUnauthorized)
		f.expect(http.MethodGet, path, writer, "", http.StatusForbidden)
		f.expect(http.MethodGet, path, reader, "", http.StatusOK)
	}
	if body := f.expect(http.MethodGet, "/api/v1/traces", reader, "", http.StatusOK).Body.String(); !strings.Contains(body, trace) {
		t.Fatalf("trace list does not show the managed execution: %s", body)
	}
	// Probes and scrapers need no credential, matching the legacy router.
	f.expect(http.MethodGet, "/api/v1/system/health", "", "", http.StatusOK)
	f.expect(http.MethodGet, "/metrics", "", "", http.StatusOK)
	// Storage and vacuum are admin-only.
	for _, op := range [][2]string{{http.MethodGet, "/api/v1/system/storage"}, {http.MethodPost, "/api/v1/system/vacuum"}} {
		f.expect(op[0], op[1], reader, "", http.StatusForbidden)
		f.expect(op[0], op[1], "", "", http.StatusUnauthorized)
		f.expect(op[0], op[1], admin, "", http.StatusOK)
	}
	managedMust(t, f.db.DeleteAPIKey(revokedID))
	for _, path := range reads {
		f.expect(http.MethodGet, path, revoked, "", http.StatusUnauthorized)
	}
}

func TestManagedEventStreamRequiresOperatorAndDeliversEvents(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("reader", `["read"]`)
	writer, _ := f.key("writer", `["write"]`)
	f.expect(http.MethodGet, "/api/v1/events", "", "", http.StatusUnauthorized)
	f.expect(http.MethodGet, "/api/v1/events", writer, "", http.StatusForbidden)

	server := httptest.NewServer(f.router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/events", nil)
	managedMust(t, err)
	req.Header.Set("X-Orva-API-Key", reader)
	res, err := server.Client().Do(req)
	managedMust(t, err)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("event stream: %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		chunk := make([]byte, 1024)
		for {
			n, err := res.Body.Read(chunk)
			buf = append(buf, chunk[:n]...)
			if strings.Contains(string(buf), "managed-fixture-event") {
				got <- string(buf)
				return
			}
			if err != nil {
				got <- string(buf)
				return
			}
		}
	}()
	// Publish until the subscriber (registered after the response head) sees it.
	for {
		f.runtime.hub.Publish("execution", map[string]string{"id": "managed-fixture-event"})
		select {
		case body := <-got:
			if !strings.Contains(body, "event: execution") || !strings.Contains(body, "managed-fixture-event") {
				t.Fatalf("event not delivered: %s", body)
			}
			return
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("event stream delivered nothing")
		}
	}
}

// Every managed operator route requires exactly the permission the legacy
// router's requiredPermission assigns it, no more and no less.
func TestManagedRoutesRequireTheLegacyTier(t *testing.T) {
	f := newManagedOpsFixture(t)
	keys := map[managedTier]string{}
	for _, tier := range []managedTier{managedRead, managedWrite, managedAdmin} {
		keys[tier], _ = f.key(string(tier)+"-only", `["`+string(tier)+`"]`)
	}
	routes := []struct {
		method, path string
		tier         managedTier
	}{
		{http.MethodGet, "/api/v1/executions", managedRead},
		{http.MethodGet, "/api/v1/traces", managedRead},
		{http.MethodGet, "/api/v1/activity", managedRead},
		{http.MethodGet, "/api/v1/system/metrics.json", managedRead},
		{http.MethodDelete, "/api/v1/executions/" + ids.New(), managedWrite},
		{http.MethodPost, "/api/v1/executions/bulk-delete", managedWrite},
		{http.MethodPost, "/api/v1/executions/" + ids.New() + "/replay", managedWrite},
		{http.MethodGet, "/api/v1/keys", managedAdmin},
		{http.MethodPost, "/api/v1/keys", managedAdmin},
		{http.MethodGet, "/api/v1/channels", managedAdmin},
		{http.MethodDelete, "/api/v1/channels/" + ids.New(), managedAdmin},
		{http.MethodGet, "/api/v1/system/storage", managedAdmin},
		{http.MethodPost, "/api/v1/system/vacuum", managedAdmin},
	}
	for _, route := range routes {
		if got := requiredPermission(route.method, route.path); got != string(route.tier) {
			t.Fatalf("%s %s: legacy tier is %q, test table says %q", route.method, route.path, got, route.tier)
		}
		for tier, bearer := range keys {
			rec := f.call(route.method, route.path, bearer, `{}`)
			denied := rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized
			if tier == route.tier && denied {
				t.Fatalf("%s %s refused a key holding exactly %q: %d", route.method, route.path, tier, rec.Code)
			}
			if tier != route.tier && rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s admitted a %q-only key: %d", route.method, route.path, tier, rec.Code)
			}
		}
	}
}

func TestManagedKeysEvictThroughTheSharedClosure(t *testing.T) {
	var mu sync.Mutex
	var evicted []string
	f := newManagedOpsFixture(t, func(ops *managedRouteDeps) {
		ops.InvalidateKey = func(hash string) { mu.Lock(); evicted = append(evicted, hash); mu.Unlock() }
	})
	admin, _ := f.key("admin", `["admin"]`)
	reader, _ := f.key("reader", `["read"]`)
	f.expect(http.MethodGet, "/api/v1/keys", reader, "", http.StatusForbidden)
	f.expect(http.MethodGet, "/api/v1/keys", "", "", http.StatusUnauthorized)

	rec := f.expect(http.MethodPost, "/api/v1/keys", admin, `{"name":"minted","permissions":["read"]}`, http.StatusCreated)
	var minted struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	managedMust(t, json.Unmarshal(rec.Body.Bytes(), &minted))
	if minted.ID == "" || !strings.HasPrefix(minted.Key, "orva_") {
		t.Fatalf("unexpected mint response: %s", rec.Body.String())
	}
	f.expect(http.MethodGet, "/api/v1/executions", minted.Key, "", http.StatusOK)
	if body := f.expect(http.MethodGet, "/api/v1/keys", admin, "", http.StatusOK).Body.String(); strings.Contains(body, minted.Key) {
		t.Fatal("key listing exposed a plaintext key")
	}

	f.expect(http.MethodDelete, "/api/v1/keys/"+minted.ID, admin, "", http.StatusOK)
	f.expect(http.MethodGet, "/api/v1/executions", minted.Key, "", http.StatusUnauthorized)
	sum := sha256.Sum256([]byte(minted.Key))
	mu.Lock()
	defer mu.Unlock()
	if len(evicted) != 1 || evicted[0] != hex.EncodeToString(sum[:]) {
		t.Fatalf("deleting a key did not call the shared invalidation closure with its hash: %v", evicted)
	}
}

func TestManagedSessionsListAndRevoke(t *testing.T) {
	var mu sync.Mutex
	var forgotten []string
	f := newManagedOpsFixture(t, func(ops *managedRouteDeps) {
		ops.InvalidateSession = func(token string) { mu.Lock(); forgotten = append(forgotten, token); mu.Unlock() }
	})
	// The reviewed session-identity shape that managed authentication requires.
	_, err := f.db.WriteDB().Exec(`ALTER TABLE sessions ADD COLUMN id TEXT; CREATE UNIQUE INDEX idx_sessions_identity ON sessions(id)`)
	managedMust(t, err)
	user, err := f.db.CreateUser("session-user", "test-only-password")
	managedMust(t, err)
	current, err := f.db.CreateSession(user.ID, time.Hour)
	managedMust(t, err)
	other, err := f.db.CreateSession(user.ID, time.Hour)
	managedMust(t, err)
	do := func(method, path, token string, origin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
		if origin {
			req.Header.Set("Origin", "http://"+req.Host)
		}
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		return rec
	}
	if rec := do(http.MethodGet, "/api/v1/auth/sessions", current.Token, false); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), other.Token[:8]) || !strings.Contains(rec.Body.String(), `"current":true`) {
		t.Fatalf("session list: %d %s", rec.Code, rec.Body.String())
	}
	f.expect(http.MethodGet, "/api/v1/auth/sessions", "", "", http.StatusUnauthorized)
	prefix := other.Token[:16]
	// A cross-site mutation is refused before the handler runs.
	if rec := do(http.MethodDelete, "/api/v1/auth/sessions/"+prefix, current.Token, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session revoke without same-origin proof: %d", rec.Code)
	}
	if rec := do(http.MethodDelete, "/api/v1/auth/sessions/"+current.Token[:16], current.Token, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("a session revoked itself: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodDelete, "/api/v1/auth/sessions/"+prefix, current.Token, true); rec.Code != http.StatusOK {
		t.Fatalf("session revoke: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/auth/sessions", other.Token, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session still authenticates: %d", rec.Code)
	}
	// Connected applications are session-only management, like the legacy router.
	f.expect(http.MethodGet, "/api/v1/oauth/connected-apps", "", "", http.StatusUnauthorized)
	if rec := do(http.MethodGet, "/api/v1/oauth/connected-apps", current.Token, false); rec.Code != http.StatusOK {
		t.Fatalf("connected apps: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodDelete, "/api/v1/oauth/clients/"+ids.New(), current.Token, true); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("application removal refused a session: %d %s", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(forgotten) != 1 || forgotten[0] != other.Token {
		t.Fatalf("revocation did not call the session invalidation hook: %v", forgotten)
	}
}

func TestManagedChannelsAndTheirManagedCatalog(t *testing.T) {
	f := newManagedOpsFixture(t)
	admin, _ := f.key("admin", `["admin"]`)
	rec := f.expect(http.MethodPost, "/api/v1/channels", admin, `{"name":"agents","function_ids":["`+f.owner+`"]}`, http.StatusCreated)
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	managedMust(t, json.Unmarshal(rec.Body.Bytes(), &created))
	if !strings.HasPrefix(created.Token, "orva_chn_") {
		t.Fatalf("channel creation returned no token: %s", rec.Body.String())
	}
	f.expect(http.MethodGet, "/api/v1/channels", admin, "", http.StatusOK)
	// A channel token carries no management authority on the REST surface.
	f.expect(http.MethodGet, "/api/v1/channels", created.Token, "", http.StatusUnauthorized)

	mcpCall := func(deps mcp.Deps, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rec := httptest.NewRecorder()
		mcp.NewHandler(deps).ServeHTTP(rec, req)
		return rec
	}
	deps := mcp.Deps{ManagedServer: true, DB: f.db, Registry: f.reg, DataDir: t.TempDir(),
		Proxy: f.runtime.invoke.Proxy, Metrics: f.runtime.metrics}
	list := mcpCall(deps, created.Token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"name":"managed"`) ||
		strings.Contains(list.Body.String(), "create_iam_policy") {
		t.Fatalf("channel catalog is not the invoke-only bundle: %d %s", list.Code, list.Body.String())
	}
	// The call reaches the managed pool, which has no Factory here: denied, no legacy fallback.
	call := mcpCall(deps, created.Token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"managed","arguments":{"method":"GET"}}}`)
	if call.Code != http.StatusOK || !strings.Contains(call.Body.String(), `"isError":true`) ||
		!strings.Contains(call.Body.String(), pool.ErrNoFunction.Error()) {
		t.Fatalf("channel invocation was not refused by managed admission: %d %s", call.Code, call.Body.String())
	}
	// Without a managed proxy the channel catalog stays off.
	bare := deps
	bare.Proxy = nil
	if rec := mcpCall(bare, created.Token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("channel token accepted without a managed proxy: %d", rec.Code)
	}

	// Rotation and deletion take effect immediately on /mcp.
	f.expect(http.MethodDelete, "/api/v1/channels/"+created.ID, admin, "", http.StatusOK)
	if rec := mcpCall(deps, created.Token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted channel token still authenticates: %d", rec.Code)
	}
}

// An open /api/v1/events stream never idles, so draining the listener must be
// preceded by StopEventStreams or one dashboard tab consumes the whole budget.
func TestManagedShutdownIsNotHeldOpenByAnEventStream(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("reader", `["read"]`)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	managedMust(t, err)
	srv := &http.Server{Handler: f.router}
	go srv.Serve(listener)
	t.Cleanup(func() { srv.Close() })

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String()+"/api/v1/events", nil)
	managedMust(t, err)
	req.Header.Set("X-Orva-API-Key", reader)
	res, err := http.DefaultClient.Do(req)
	managedMust(t, err)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("event stream: %d", res.StatusCode)
	}
	instance := &ManagedInstance{service: &managedServer{runtime: f.runtime}}

	// Without ending the stream the drain does not finish.
	probe, cancelProbe := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancelProbe()
	if err := srv.Shutdown(probe); err == nil {
		t.Fatal("drain finished while an event stream was open; the test proves nothing")
	}

	instance.StopEventStreams()
	instance.StopEventStreams() // idempotent
	drained := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		drained <- srv.Shutdown(ctx)
	}()
	select {
	case err := <-drained:
		managedMust(t, err)
	case <-time.After(6 * time.Second):
		t.Fatal("drain stayed blocked after the event streams were stopped")
	}
	if _, err := io.Copy(io.Discard, res.Body); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
}

// Every tier-gated managed route is compared with the legacy requiredPermission
// for the same method and path. A route may be stricter than legacy only when
// it has no legacy counterpart and says so here.
func TestEveryManagedRouteMatchesTheLegacyTier(t *testing.T) {
	f := newManagedOpsFixture(t)
	recorded := map[string]managedTier{}
	ops := f.runtime.routeDeps("/usr/local/bin/nsjail")
	ops.recordRoute = func(pattern string, tier managedTier) { recorded[pattern] = tier }
	chat := func(mux *http.ServeMux, _ mcp.Deps) { registerManagedChat(mux, &AIHandler{DB: f.db}) }
	_, err := managedRoutesWithAsync(f.db, f.reg, f.authority, f.runtime.pool, new(workloadconfig.Service), new(builder.PublicationQueue),
		t.TempDir(), f.runtime.invoke, f.runtime.async, chat, func(http.ResponseWriter, *http.Request) {}, ops)
	managedMust(t, err)
	if len(recorded) < 50 {
		t.Fatalf("only %d gated routes were recorded; the publication routes are not covered", len(recorded))
	}
	// Routes with no legacy counterpart may demand more than a method's default.
	stricter := map[string]string{
		"POST /api/v1/deployments/{id}/install": "installing a Factory is a managed-only admin act",
	}
	rank := map[managedTier]int{managedRead: 1, managedWrite: 2, managedAdmin: 3}
	for pattern, tier := range recorded {
		method, route, _ := strings.Cut(pattern, " ")
		path := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(route, "x1")
		legacy := managedTier(requiredPermission(method, path))
		switch {
		case tier == legacy:
		case stricter[pattern] != "" && rank[tier] > rank[legacy]:
		default:
			t.Errorf("%s is gated at %q but the legacy router requires %q", pattern, tier, legacy)
		}
	}
	for _, want := range []string{"POST /api/v1/functions/{fn_id}/deploy", "POST /api/v1/functions/{fn_id}/deploy-inline",
		"POST /api/v1/functions/{fn_id}/rollback", "POST /api/v1/functions", "DELETE /api/v1/functions/{fn_id}"} {
		if recorded[want] != managedWrite {
			t.Errorf("%s is gated at %q, want write", want, recorded[want])
		}
	}
}

// A GET pattern also serves HEAD; the legacy router requires write for it.
func TestManagedHeadRequiresTheLegacyWriteTier(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("reader", `["read"]`)
	writer, _ := f.key("writer", `["write"]`)
	admin, _ := f.key("admin", `["admin"]`)
	for _, path := range []string{"/api/v1/executions", "/api/v1/traces", "/api/v1/activity", "/api/v1/system/metrics.json"} {
		if got := requiredPermission(http.MethodHead, path); got != "write" {
			t.Fatalf("legacy tier for HEAD %s is %q", path, got)
		}
		f.expect(http.MethodHead, path, reader, "", http.StatusForbidden)
		f.expect(http.MethodHead, path, admin, "", http.StatusForbidden)
		if rec := f.call(http.MethodHead, path, writer, ""); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
			t.Fatalf("HEAD %s refused a write key: %d", path, rec.Code)
		}
	}
	// Admin routes stay admin for every method.
	f.expect(http.MethodHead, "/api/v1/system/storage", writer, "", http.StatusForbidden)
	if rec := f.call(http.MethodHead, "/api/v1/system/storage", admin, ""); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("HEAD storage refused an admin key: %d", rec.Code)
	}
}

// Sessions and connected applications authorise by cookie in their handlers, so
// the gate must authenticate and attribute that same cookie, never a bearer.
func TestManagedSessionOnlyRoutesUseTheCookieAlone(t *testing.T) {
	f := newManagedOpsFixture(t)
	_, err := f.db.WriteDB().Exec(`ALTER TABLE sessions ADD COLUMN id TEXT; CREATE UNIQUE INDEX idx_sessions_identity ON sessions(id)`)
	managedMust(t, err)
	user, err := f.db.CreateUser("cookie-user", "test-only-password")
	managedMust(t, err)
	current, err := f.db.CreateSession(user.ID, time.Hour)
	managedMust(t, err)
	other, err := f.db.CreateSession(user.ID, time.Hour)
	managedMust(t, err)
	admin, keyID := f.key("admin", `["read","write","admin"]`)

	type request struct{ method, path string }
	routes := []request{
		{http.MethodGet, "/api/v1/auth/sessions"},
		{http.MethodDelete, "/api/v1/auth/sessions/" + other.Token[:16]},
		{http.MethodGet, "/api/v1/oauth/connected-apps"},
		{http.MethodDelete, "/api/v1/oauth/connected-apps/" + ids.New()},
		{http.MethodDelete, "/api/v1/oauth/clients/" + ids.New()},
	}
	do := func(r request, bearer, cookie, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(r.method, r.path, nil)
		if bearer != "" {
			req.Header.Set("X-Orva-API-Key", bearer)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "session_token", Value: cookie})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		return rec
	}
	for _, r := range routes {
		// A key, however powerful, is not a session.
		if rec := do(r, admin, "", "http://"+httptest.NewRequest("GET", "/", nil).Host); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s accepted a bearer without a cookie: %d", r.method, r.path, rec.Code)
		}
		// A key cannot rescue an invalid cookie either.
		if rec := do(r, admin, "not-a-session", "http://example.com"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s accepted a bearer alongside an invalid cookie: %d", r.method, r.path, rec.Code)
		}
		if r.method != http.MethodGet {
			// The cookie that authorises a mutation must carry same-origin proof.
			for _, bearer := range []string{"", admin} {
				if rec := do(r, bearer, current.Token, "https://evil.example"); rec.Code != http.StatusUnauthorized {
					t.Fatalf("%s %s accepted a cross-origin cookie mutation (bearer=%v): %d", r.method, r.path, bearer != "", rec.Code)
				}
				if rec := do(r, bearer, current.Token, ""); rec.Code != http.StatusUnauthorized {
					t.Fatalf("%s %s accepted a cookie mutation with no origin: %d", r.method, r.path, rec.Code)
				}
			}
		}
	}
	// Cookie only, and cookie plus a bearer, both act as the cookie's user.
	for _, bearer := range []string{"", admin} {
		if rec := do(routes[0], bearer, current.Token, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"current":true`) {
			t.Fatalf("session list with bearer=%v: %d %s", bearer != "", rec.Code, rec.Body.String())
		}
		if rec := do(routes[2], bearer, current.Token, ""); rec.Code != http.StatusOK {
			t.Fatalf("connected apps with bearer=%v: %d", bearer != "", rec.Code)
		}
	}
	// Attribution follows the cookie, not the bearer that rode along.
	deadline := time.Now().Add(5 * time.Second)
	var rows []database.ActivityRow
	for time.Now().Before(deadline) {
		rows, _, _, err = f.db.ListActivity(database.ActivityFilter{Search: "/api/v1/oauth/connected-apps", StatusMax: 399})
		managedMust(t, err)
		if len(rows) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(rows) < 2 {
		t.Fatalf("expected two successful connected-apps rows, got %d", len(rows))
	}
	for _, row := range rows {
		if row.ActorType != "session" || row.Source != "web" || row.ActorID == keyID || row.ActorLabel != "cookie-user" {
			t.Fatalf("activity attributed to the wrong credential: %+v", row)
		}
	}
}
