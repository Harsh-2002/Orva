package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
)

// A "/*" custom route prefix-matches every path, including "/fn/{id}". The
// invoke handler used to consult the route table before extracting the id
// from the path, so a single such route redirected the whole instance's /fn/
// traffic into the route's function — bypassing the target function's
// auth_mode, because auth is evaluated against the *resolved* id.
//
// The request below names a function that does not exist, while the wildcard
// route points at one that does. A 404 proves the id came from the path: had
// the route won, the lookup would have succeeded and the handler would have
// continued past the registry.
func TestInvokeIgnoresWildcardRouteForFnPaths(t *testing.T) {
	db := newTestDB(t)
	reg := registry.New(db)

	const attackerFn = "019df200-7b00-7e00-9c00-aab1cd2e3f40"
	if err := db.InsertFunction(&database.Function{
		ID: attackerFn, Name: "attacker", Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 64, CPUs: 0.5, EnvVars: map[string]string{},
		NetworkMode: "none", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	// The hijack: one wildcard route owned by the attacker's function.
	if err := db.UpsertRoute("/*", attackerFn, "*"); err != nil {
		t.Fatal(err)
	}

	h := &InvokeHandler{Registry: reg, DB: db}

	const unknownFn = "019df200-7b00-7e00-9c00-ffffffffffff"
	req := httptest.NewRequest(http.MethodGet, "/fn/"+unknownFn, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 — the wildcard route hijacked a /fn/ invocation", w.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err == nil && body.Error.Code == "" {
		t.Logf("response body: %s", w.Body.String())
	}
}

// The wildcard route must still work for the paths it is meant to serve, so
// the fix above cannot be "ignore custom routes entirely".
func TestWildcardRouteStillMatchesNonFnPaths(t *testing.T) {
	db := newTestDB(t)

	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f40"
	if err := db.InsertFunction(&database.Function{
		ID: fnID, Name: "catchall", Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 64, CPUs: 0.5, EnvVars: map[string]string{},
		NetworkMode: "none", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertRoute("/*", fnID, "*"); err != nil {
		t.Fatal(err)
	}

	route, matched, err := db.MatchRoute("/anything/else")
	if err != nil {
		t.Fatal(err)
	}
	if route == nil || route.FunctionID != fnID {
		t.Fatalf("MatchRoute(/anything/else) = %v, want the wildcard route", route)
	}
	if matched != "/" {
		t.Errorf("matched prefix = %q, want %q", matched, "/")
	}
}
