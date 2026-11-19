//go:build linux

package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadtrigger"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedCustomRouteHTTP(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.New(filepath.Join(dataDir, "orva.db"))
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, db.Close()) })
	managedMust(t, db.Migrate())
	public, protected := ids.New(), ids.New()
	for id, authMode := range map[string]string{public: "none", protected: "platform_key"} {
		managedMust(t, db.InsertFunction(&database.Function{ID: id, Name: id, Runtime: "node", Status: "active", AuthMode: authMode}))
	}
	_, err = db.ApplyRecoveryForFixture(t.Context(), "")
	managedMust(t, err)
	managedMust(t, db.InitManagedServingSchemas(t.Context()))
	var evaluators []*workloadiam.Evaluator
	for _, id := range []string{public, protected} {
		record, err := db.GetIAMPolicy(t.Context(), id)
		managedMust(t, err)
		e, err := record.AdmissionEvaluator()
		managedMust(t, err)
		evaluators = append(evaluators, e)
	}
	a, err := workloadiam.NewAuthority(16, evaluators)
	managedMust(t, err)
	reg := registry.New(db)
	runtime := newManagedRuntime(db, reg, a, nil, pool.SandboxTemplate{DataDir: dataDir}, sandbox.NewLimiter(1))
	t.Cleanup(func() { managedMust(t, runtime.pool.Shutdown(context.Background())) })
	service, err := workloadtrigger.New(db, reg, a)
	managedMust(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /web/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/fn/{id}", runtime.invoke.ServeManagedHTTP)
	router := bodySizeMiddleware(managedMaxBodyBytes, mountManagedCustomRoutes(mux, service, managedOperator(db), runtime.invoke))

	key := func(name, permissions string) (string, string) {
		id, token := ids.New(), "orva_route_fixture_"+name
		hash := sha256.Sum256([]byte(token))
		managedMust(t, db.InsertAPIKey(&database.APIKey{ID: id, Name: name, Prefix: name, KeyHash: fmt.Sprintf("%x", hash), Permissions: permissions}))
		return id, token
	}
	_, read := key("read", `["read"]`)
	writerID, write := key("write", `["write"]`)
	call := func(method, path, token, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-Orva-API-Key", token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: got %d; want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	expect := func(owner string) string {
		return fmt.Sprintf(`{"%s":{"policy_revision":"1","authority_epoch":"1"}}`, owner)
	}
	bind := func(path, owner, methods, version string) string {
		return fmt.Sprintf(`{"path":%q,"function_id":%q,"methods":%q,"version":%q,"expected":%s}`, path, owner, methods, version, expect(owner))
	}
	inspect := func(path, owner string) string {
		return "/api/v1/exposures?kind=route&key=" + url.QueryEscape(path) + "&function_id=" + owner
	}
	const routes = "/api/v1/routes"
	const exact = "/public/exact"
	managedMust(t, db.UpsertRoute("/legacy", public, "*"))
	call("GET", "/legacy", "", "", 404)
	call("GET", inspect(exact, protected), write, "", 403)
	call("GET", inspect(exact, protected)+"&key=/other", read, "", 400)
	call("GET", "/api/v1/exposures?kind=inbound&key=/unused", read, "", 400)
	call("POST", routes, read, bind(exact, protected, "POST", "0"), 403)
	call("POST", routes, write, `{"path":"/one","path":"/two"}`, 400)
	call("POST", routes, write, bind("/api", public, "*", "0"), 400)
	call("POST", routes, write, bind("/public/*", public, "*", "0"), 201)
	call("POST", routes, write, bind(exact, protected, "post", "0"), 201)
	w := call("GET", inspect(exact, protected), read, "", 200)
	if !strings.Contains(w.Body.String(), `"version":"1"`) || !strings.Contains(w.Body.String(), `"route_published":true`) {
		t.Fatalf("unexpected inspection: %s", w.Body)
	}
	call("POST", routes, write, bind(exact, protected, "POST", "0"), 409)
	call("GET", exact, "", "", 405)
	call("HEAD", exact, "", "", 405)
	call("POST", exact, "", "", 401)
	w = call("GET", "/public/miss", "", "", 503)
	if w.Header().Get("X-Orva-Execution-ID") == "" || !strings.Contains(w.Body.String(), pool.ErrNoFunction.Error()) {
		t.Fatalf("custom route bypassed the managed pool: %s", w.Body)
	}
	w = call("DELETE", routes+"?path="+exact, write, `{"confirm":false,"version":"1","expected":`+expect(protected)+`}`, 400)
	call("DELETE", routes+"?path="+exact, write, `{"confirm":true,"version":"1","expected":`+expect(protected)+`}`, 200)
	w = call("GET", inspect(exact, protected), read, "", 200)
	if !strings.Contains(w.Body.String(), `"version":"2"`) || !strings.Contains(w.Body.String(), `"route_published":false`) {
		t.Fatalf("delete did not leave inactive CAS tombstone: %s", w.Body)
	}
	call("POST", routes, write, bind("/*", public, "*", "0"), 201)
	for _, path := range []string{"/api", "/api/v1/never", "/auth", "/_orva/no", "/fn", "/mcp", "/mcp/", "/webhook/x",
		"/metrics", "/register", "/oauth/token", "/.well-known/x", "/%61pi", "/api%2fv1/never", "/some%2falias"} {
		call("POST", path, "", "", 404)
	}
	call("POST", "/web/index.html", "", "", 405)
	call("GET", "/web/index.html", "", "", 204)
	call("POST", "/fn/"+protected, "", "", 401)
	call("GET", routes, read, "", 200)
	managedMust(t, db.DeleteAPIKey(writerID))
	call("POST", routes, write, bind("/revoked", public, "*", "0"), 403)
}
