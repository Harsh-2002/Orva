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
	router, err := managedRoutesWithAsync(db, reg, authority, runtime.pool, nil, nil, "", runtime.invoke, nil, nil, nil, runtime.routeDeps("/usr/local/bin/nsjail"))
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
