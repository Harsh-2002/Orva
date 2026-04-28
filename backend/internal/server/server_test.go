package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/internal/config"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/registry"
)

// testContext holds a test server and a valid admin API key.
type testContext struct {
	srv    *Server
	apiKey string
}

func newTestServer(t *testing.T) *testContext {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Database.Path = filepath.Join(dir, "test.db")

	db, err := database.New(cfg.Database.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	// Insert a known admin key for testing before creating the server
	// so the bootstrap logic doesn't print to stdout.
	testKey := "orva_test_admin_key_for_testing_1234567890abcdef"
	hash := sha256.Sum256([]byte(testKey))
	keyHash := hex.EncodeToString(hash[:])
	testAPIKey := &database.APIKey{
		ID:          "key_testadmin01",
		KeyHash:     keyHash,
		Name:        "test-admin",
		Permissions: `["invoke","read","write","admin"]`,
	}
	if err := db.InsertAPIKey(testAPIKey); err != nil {
		t.Fatal(err)
	}

	srv := New(cfg, db)

	return &testContext{
		srv:    srv,
		apiKey: testKey,
	}
}

// setAuth sets the X-Orva-API-Key header on a request.
func (tc *testContext) setAuth(req *http.Request) {
	req.Header.Set("X-Orva-API-Key", tc.apiKey)
}

func TestHealthEndpoint(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/system/health", nil)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "healthy" {
		t.Errorf("expected healthy status, got %v", resp["status"])
	}
	if resp["version"] != "0.1.0" {
		t.Errorf("expected version 0.1.0, got %v", resp["version"])
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/system/health", nil)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	id := w.Header().Get("X-Request-ID")
	if id == "" {
		t.Error("expected X-Request-ID header")
	}
	if len(id) < 4 {
		t.Errorf("request ID too short: %s", id)
	}
}

func TestCustomRequestID(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/system/health", nil)
	req.Header.Set("X-Request-ID", "custom-id-123")
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") != "custom-id-123" {
		t.Errorf("expected custom request ID to be preserved")
	}
}

func Test404(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCreateAndGetFunction(t *testing.T) {
	tc := newTestServer(t)

	body := `{"name":"test-hello","runtime":"node22","entrypoint":"handler.js"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var fn database.Function
	if err := json.NewDecoder(w.Body).Decode(&fn); err != nil {
		t.Fatal(err)
	}
	if fn.ID == "" {
		t.Error("expected non-empty function ID")
	}
	if fn.Status != "created" {
		t.Errorf("expected status created, got %s", fn.Status)
	}

	// Get the function.
	req2 := httptest.NewRequest("GET", "/api/v1/functions/"+fn.ID, nil)
	tc.setAuth(req2)
	w2 := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("get: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var fn2 database.Function
	if err := json.NewDecoder(w2.Body).Decode(&fn2); err != nil {
		t.Fatal(err)
	}
	if fn2.Name != "test-hello" {
		t.Errorf("expected name test-hello, got %s", fn2.Name)
	}
}

func TestListFunctions(t *testing.T) {
	tc := newTestServer(t)

	body := `{"name":"list-test","runtime":"python313"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/api/v1/functions", nil)
	tc.setAuth(req2)
	w2 := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("list: expected 200, got %d", w2.Code)
	}

	var result database.ListFunctionsResult
	if err := json.NewDecoder(w2.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Total < 1 {
		t.Errorf("expected at least 1 function, got %d", result.Total)
	}
}

func TestDeleteFunction(t *testing.T) {
	tc := newTestServer(t)

	body := `{"name":"delete-me","runtime":"node22"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	var fn database.Function
	json.NewDecoder(w.Body).Decode(&fn)

	reqDel := httptest.NewRequest("DELETE", "/api/v1/functions/"+fn.ID, nil)
	tc.setAuth(reqDel)
	wDel := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(wDel, reqDel)

	if wDel.Code != 200 {
		t.Fatalf("delete: expected 200, got %d: %s", wDel.Code, wDel.Body.String())
	}

	// Verify it's gone.
	reqGet := httptest.NewRequest("GET", "/api/v1/functions/"+fn.ID, nil)
	tc.setAuth(reqGet)
	wGet := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(wGet, reqGet)

	if wGet.Code != 404 {
		t.Fatalf("expected 404 after delete, got %d", wGet.Code)
	}
}

func TestAuthMiddleware_MissingKey(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/functions", nil)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_InvalidKey(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/functions", nil)
	req.Header.Set("X-Orva-API-Key", "orva_invalid_key")
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_InsufficientPermissions(t *testing.T) {
	tc := newTestServer(t)

	// Create a read-only key.
	readOnlyKey := "orva_readonly_test_key_abcdef1234567890"
	hash := sha256.Sum256([]byte(readOnlyKey))
	keyHash := hex.EncodeToString(hash[:])
	apiKey := &database.APIKey{
		ID:          "key_readonly0001",
		KeyHash:     keyHash,
		Name:        "readonly-key",
		Permissions: `["read"]`,
	}
	if err := tc.srv.db.InsertAPIKey(apiKey); err != nil {
		t.Fatal(err)
	}

	// Try to write with a read-only key.
	body := `{"name":"test","runtime":"node22"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Orva-API-Key", readOnlyKey)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRuntimesEndpoint(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/runtimes", nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	runtimes, ok := resp["runtimes"].([]any)
	if !ok || len(runtimes) != 4 {
		t.Errorf("expected 4 runtimes (node22/node24/python313/python314), got %v", resp["runtimes"])
	}
}

func TestSystemMetricsEndpoint(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/system/metrics", nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("orva_invocations_total")) {
		t.Error("expected orva_invocations_total in metrics output")
	}
	if !bytes.Contains([]byte(body), []byte("orva_sandbox_active")) {
		t.Error("expected orva_sandbox_active in metrics output")
	}
}

func TestCORSHeaders(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("OPTIONS", "/api/v1/functions", nil)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}

	origin := w.Header().Get("Access-Control-Allow-Origin")
	if origin == "" {
		t.Error("expected Access-Control-Allow-Origin header")
	}
}

func TestKeyManagement(t *testing.T) {
	tc := newTestServer(t)

	// Create a new key.
	body := `{"name":"test-key","permissions":["invoke","read"]}`
	req := httptest.NewRequest("POST", "/api/v1/keys", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Fatalf("create key: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var createResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&createResp); err != nil {
		t.Fatal(err)
	}
	if _, ok := createResp["key"]; !ok {
		t.Error("expected key field in create response")
	}
	keyID, _ := createResp["id"].(string)

	// List keys.
	req2 := httptest.NewRequest("GET", "/api/v1/keys", nil)
	tc.setAuth(req2)
	w2 := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("list keys: expected 200, got %d", w2.Code)
	}

	// Delete the key.
	req3 := httptest.NewRequest("DELETE", "/api/v1/keys/"+keyID, nil)
	tc.setAuth(req3)
	w3 := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w3, req3)

	if w3.Code != 200 {
		t.Fatalf("delete key: expected 200, got %d: %s", w3.Code, w3.Body.String())
	}
}

func TestUpdateFunction_PartialUpdate(t *testing.T) {
	tc := newTestServer(t)

	// Create a function first.
	body := `{"name":"update-partial","runtime":"node22","entrypoint":"handler.js"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var fn database.Function
	json.NewDecoder(w.Body).Decode(&fn)

	// Update only the name.
	updateBody := `{"name":"updated-name"}`
	req2 := httptest.NewRequest("PUT", "/api/v1/functions/"+fn.ID, bytes.NewBufferString(updateBody))
	req2.Header.Set("Content-Type", "application/json")
	tc.setAuth(req2)
	w2 := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("update: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var updated database.Function
	json.NewDecoder(w2.Body).Decode(&updated)
	if updated.Name != "updated-name" {
		t.Errorf("expected name updated-name, got %s", updated.Name)
	}
	if updated.Runtime != "node22" {
		t.Errorf("expected runtime preserved as node22, got %s", updated.Runtime)
	}
	if updated.Version != fn.Version+1 {
		t.Errorf("expected version %d, got %d", fn.Version+1, updated.Version)
	}
}

func TestCreateFunction_DuplicateName(t *testing.T) {
	tc := newTestServer(t)

	body := `{"name":"dup-name","runtime":"node22"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("first create: expected 201, got %d", w.Code)
	}

	// Try again with same name.
	req2 := httptest.NewRequest("POST", "/api/v1/functions", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	tc.setAuth(req2)
	w2 := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w2, req2)

	if w2.Code != 409 {
		t.Errorf("expected 409 for duplicate name, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestCreateFunction_InvalidRuntime(t *testing.T) {
	tc := newTestServer(t)

	body := `{"name":"bad-runtime","runtime":"ruby33"}`
	req := httptest.NewRequest("POST", "/api/v1/functions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for invalid runtime, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListExecutions_Empty(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/executions", nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result database.ListExecutionsResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 executions, got %d", result.Total)
	}
}

func TestGetExecution_NotFound(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/executions/exec_nonexistent", nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetExecutionLogs_NotFound(t *testing.T) {
	tc := newTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/executions/exec_nonexistent/logs", nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// --- UI Serving Tests ---

func TestUIRedirect(t *testing.T) {
	tc := newTestServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	tc.srv.router.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/ui/" {
		t.Errorf("expected redirect to /ui/, got %q", loc)
	}
}

func TestUIIndex(t *testing.T) {
	tc := newTestServer(t)
	ts := httptest.NewServer(tc.srv.router)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/ui/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	if !bytes.Contains(buf.Bytes(), []byte("<")) {
		t.Error("expected HTML content in /ui/ response")
	}
}

func TestUIAndAPICoexist(t *testing.T) {
	tc := newTestServer(t)
	ts := httptest.NewServer(tc.srv.router)
	defer ts.Close()

	// UI request (follow redirects).
	uiResp, err := http.Get(ts.URL + "/ui/")
	if err != nil {
		t.Fatal(err)
	}
	defer uiResp.Body.Close()
	if uiResp.StatusCode != http.StatusOK {
		t.Errorf("UI: expected 200, got %d", uiResp.StatusCode)
	}

	// API request.
	apiReq, _ := http.NewRequest("GET", ts.URL+"/api/v1/system/health", nil)
	tc.setAuth(apiReq)
	apiResp, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		t.Fatal(err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusOK {
		t.Errorf("API health: expected 200, got %d", apiResp.StatusCode)
	}
}

// Ensure registry is used (compile-time check).
var _ = (*registry.Registry)(nil)
