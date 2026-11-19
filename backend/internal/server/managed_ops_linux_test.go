//go:build linux

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// managedOpsFixture is a managed router over a real database and a real managed
// pool without installed Factories: every function dispatch is denied, so the
// operator surfaces can be exercised without a sandbox.
type managedOpsFixture struct {
	t       *testing.T
	db      *database.Database
	reg     *registry.Registry
	runtime *managedRuntime
	router  http.Handler
	owner   string
}

func newManagedOpsFixture(t *testing.T) *managedOpsFixture {
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
	router, err := managedRoutesWithAsync(db, reg, authority, runtime.pool, nil, nil, filepath.Dir(db.Path()), runtime.invoke, nil, nil, nil, runtime.routeDeps("/usr/local/bin/nsjail"))
	managedMust(t, err)
	return &managedOpsFixture{t: t, db: db, reg: reg, runtime: runtime, router: router, owner: owner}
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
