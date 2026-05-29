package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// seedDiffFunction creates a function via the API and lays two version
// trees + two succeeded deployment rows on disk. Returns the function ID,
// the two deployment IDs (fromID, toID), and their code hashes (fromHash,
// toHash). Source code for each version is deliberately different so the
// diff endpoint has something to chew on.
func seedDiffFunction(t *testing.T, tc *testContext) (fnID, fromID, toID, fromHash, toHash string) {
	t.Helper()

	// Create the function via the API.
	body := `{"name":"diff-demo","runtime":"node22","entrypoint":"handler.js"}`
	req := httptest.NewRequest("POST", "/api/v1/functions",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("create function: %d %s", w.Code, w.Body.String())
	}
	var fn database.Function
	if err := json.NewDecoder(w.Body).Decode(&fn); err != nil {
		t.Fatal(err)
	}
	fnID = fn.ID

	// Two version trees on disk under <dataDir>/functions/<fnID>/versions/<hash>/.
	fromHash = "a1b2c3d4e5f600000000000000000000000000000000000000000000000000ff"
	toHash = "ff0011223344556677889900aabbccddeeff00112233445566778899aabbccdd"

	dataDir := tc.srv.cfg.Data.Dir
	mk := func(hash, handler, pkg string) {
		dir := filepath.Join(dataDir, "functions", fnID, "versions", hash)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte(handler), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".orva-ready"), []byte(hash), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(fromHash,
		"module.exports.handler = async () => ({ statusCode: 200, body: 'v1' });\n",
		`{"name":"diff-demo","version":"1.0.0"}`+"\n")
	mk(toHash,
		"module.exports.handler = async () => ({ statusCode: 200, body: 'v3' });\n",
		`{"name":"diff-demo","version":"1.0.1","dependencies":{"axios":"^1.0.0"}}`+"\n")

	// Two succeeded deployment rows pointing at the hashes above.
	fromID = "dep_difffrom00000000000000000000000"
	toID = "dep_diffto00000000000000000000000000"
	for _, d := range []*database.Deployment{
		{ID: fromID, FunctionID: fnID, Version: 1, Status: "queued", Phase: "build", CodeHash: fromHash, Source: "deploy"},
		{ID: toID, FunctionID: fnID, Version: 2, Status: "queued", Phase: "build", CodeHash: toHash, Source: "deploy"},
	} {
		if err := tc.srv.db.InsertDeployment(d); err != nil {
			t.Fatal(err)
		}
		if err := tc.srv.db.FinishDeployment(d.ID, "succeeded", "", 100); err != nil {
			t.Fatal(err)
		}
	}
	return fnID, fromID, toID, fromHash, toHash
}

// TestDiff_HappyPath_JSON walks the JSON form end-to-end and asserts the
// before/after blobs come back unchanged for both the handler and the
// dependency manifest.
func TestDiff_HappyPath_JSON(t *testing.T) {
	tc := newTestServer(t)
	fnID, fromID, toID, _, _ := seedDiffFunction(t, tc)

	req := httptest.NewRequest("GET",
		"/api/v1/functions/"+fnID+"/diff?from="+fromID+"&to="+toID, nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		From  map[string]any `json:"from"`
		To    map[string]any `json:"to"`
		Files []struct {
			Path, Kind, Before, After string
			Added, Removed            bool
		} `json:"files"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if got := resp.From["deployment_id"]; got != fromID {
		t.Errorf("from.deployment_id = %v, want %v", got, fromID)
	}
	if got := resp.To["deployment_id"]; got != toID {
		t.Errorf("to.deployment_id = %v, want %v", got, toID)
	}
	if len(resp.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(resp.Files))
	}
	handler := resp.Files[0]
	if handler.Path != "handler.js" || handler.Kind != "handler" {
		t.Errorf("first file = %+v, want path=handler.js kind=handler", handler)
	}
	if !strings.Contains(handler.Before, "'v1'") || !strings.Contains(handler.After, "'v3'") {
		t.Errorf("handler diff didn't carry expected bodies: before=%q after=%q",
			handler.Before, handler.After)
	}
	manifest := resp.Files[1]
	if manifest.Path != "package.json" || manifest.Kind != "manifest" {
		t.Errorf("second file = %+v, want path=package.json kind=manifest", manifest)
	}
	if !strings.Contains(manifest.After, "axios") {
		t.Errorf("manifest after-side missing axios: %q", manifest.After)
	}
}

// TestDiff_HappyPath_Unified asserts the CLI-facing unified format
// produces git-style hunks for the handler change.
func TestDiff_HappyPath_Unified(t *testing.T) {
	tc := newTestServer(t)
	fnID, fromID, toID, _, _ := seedDiffFunction(t, tc)

	req := httptest.NewRequest("GET",
		"/api/v1/functions/"+fnID+"/diff?from="+fromID+"&to="+toID+"&format=unified", nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/x-diff") {
		t.Errorf("Content-Type = %q, want text/x-diff", ct)
	}
	body := w.Body.String()
	for _, frag := range []string{
		"--- a/handler.js", "+++ b/handler.js", "@@", "-module.exports.handler", "+module.exports.handler",
		"--- a/package.json", "+++ b/package.json",
	} {
		if !strings.Contains(body, frag) {
			t.Errorf("unified body missing %q\nbody=%s", frag, body)
		}
	}
}

// TestDiff_VersionGCD asserts that wiping one side's version tree from
// disk surfaces a 410 VERSION_GCD with the surviving hash in
// available_hashes.
func TestDiff_VersionGCD(t *testing.T) {
	tc := newTestServer(t)
	fnID, fromID, toID, fromHash, toHash := seedDiffFunction(t, tc)

	// Reap the "from" version tree.
	if err := os.RemoveAll(filepath.Join(tc.srv.cfg.Data.Dir, "functions", fnID,
		"versions", fromHash)); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET",
		"/api/v1/functions/"+fnID+"/diff?from="+fromID+"&to="+toID, nil)
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)

	if w.Code != 410 {
		t.Fatalf("expected 410, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error.Code != "VERSION_GCD" {
		t.Errorf("error.code = %q, want VERSION_GCD", resp.Error.Code)
	}
	avail, _ := resp.Error.Details["available_hashes"].([]any)
	found := false
	for _, h := range avail {
		if s, _ := h.(string); s == toHash {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in available_hashes, got %v", toHash, avail)
	}
}

// TestDiff_RejectsCrossFunction asserts a deployment that belongs to a
// different function is rejected with VALIDATION rather than silently
// crossing function boundaries.
func TestDiff_RejectsCrossFunction(t *testing.T) {
	tc := newTestServer(t)
	fnID, fromID, _, _, _ := seedDiffFunction(t, tc)

	// Insert a second function + a deployment on it.
	body := `{"name":"diff-other","runtime":"node22","entrypoint":"handler.js"}`
	req := httptest.NewRequest("POST", "/api/v1/functions",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tc.setAuth(req)
	w := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("create other fn: %d %s", w.Code, w.Body.String())
	}
	var other database.Function
	json.NewDecoder(w.Body).Decode(&other)

	stranger := "dep_strangerxx0000000000000000000000"
	if err := tc.srv.db.InsertDeployment(&database.Deployment{
		ID: stranger, FunctionID: other.ID, Version: 1, Status: "queued",
		Phase: "build", CodeHash: "deadbeef", Source: "deploy",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tc.srv.db.FinishDeployment(stranger, "succeeded", "", 50); err != nil {
		t.Fatal(err)
	}

	// Query under fnID with a `to` from the other function.
	req2 := httptest.NewRequest("GET",
		"/api/v1/functions/"+fnID+"/diff?from="+fromID+"&to="+stranger, nil)
	tc.setAuth(req2)
	w2 := httptest.NewRecorder()
	tc.srv.router.ServeHTTP(w2, req2)

	if w2.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w2.Code, w2.Body.String())
	}
}
