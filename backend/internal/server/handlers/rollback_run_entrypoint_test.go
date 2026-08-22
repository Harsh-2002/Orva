package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
)

// Rolling back to a TypeScript version deployed before run_entrypoint existed
// used to leave the function pointing at its .ts source. The snapshot on such a
// row carries no run_entrypoint, rollback applied that absence verbatim, and
// Node cannot execute TypeScript — so every invocation of the rolled-back
// version died with WORKER_CRASHED. Reproduced in the dashboard: promoting an
// older version of a TS function turned a working function into a 502.
//
// The file a version runs is now derived from what that version has on disk,
// which is right for legacy snapshots and for new ones alike.
func TestRollbackDerivesRunEntrypointFromDiskNotFromALegacySnapshot(t *testing.T) {
	const fnID = "01a02abf-351c-76ae-937a-90016387aaf1"
	const hash = "2c26794c4e9ae4e83167fd19b327ba882b22c1c7e0d2dc230ff6bb86b4e1ed55"

	db := newTestDB(t)
	dataDir := t.TempDir()

	// The function is live on some other version and currently knows it runs
	// compiled output.
	fn := &database.Function{
		ID: fnID, Name: "ts-fn", Runtime: "node", Status: "active",
		Entrypoint: "handler.ts", RunEntrypoint: "dist/handler.js",
		CodeHash:  "0000000000000000000000000000000000000000000000000000000000000000",
		TimeoutMS: 30000, MemoryMB: 128, CPUs: 0.5, EnvVars: map[string]string{},
		NetworkMode: "none", ConcurrencyPolicy: "queue", AuthMode: "none", Version: 2,
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatalf("insert function: %v", err)
	}

	// The rollback target: a real compiled version on disk, recorded by a
	// deployment row whose snapshot predates run_entrypoint — so it carries
	// every other setting and says nothing about the compiled path.
	legacy := database.SnapshotFromFunction(fn)
	legacy.RunEntrypoint = ""
	dep := &database.Deployment{
		ID: "01a02ac3-bcaf-7b4b-9340-a160335561ea", FunctionID: fnID, Version: 1,
		Status: "succeeded", Phase: "done", CodeHash: hash, Snapshot: legacy,
	}
	if err := db.InsertDeployment(dep); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}

	version := filepath.Join(dataDir, "functions", fnID, "versions", hash)
	if err := os.MkdirAll(filepath.Join(version, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		".orva-ready":     "",
		"handler.ts":      "export async function handler() {}",
		"tsconfig.json":   `{"compilerOptions":{"outDir":"dist"}}`,
		"dist/handler.js": "exports.handler = async () => {}",
	} {
		if err := os.WriteFile(filepath.Join(version, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	reg := registry.New(db)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("load registry: %v", err)
	}
	h := &FunctionHandler{Registry: reg, DB: db, DataDir: dataDir}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/functions/"+fnID+"/rollback",
		strings.NewReader(`{"deployment_id":"`+dep.ID+`"}`))
	req.SetPathValue("fn_id", fnID)
	rec := httptest.NewRecorder()
	h.Rollback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("rollback returned %d, want 200: %s", rec.Code, rec.Body.String())
	}

	got, err := db.GetFunction(fnID)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	if got.RunEntrypoint != "dist/handler.js" {
		t.Errorf("run_entrypoint = %q, want dist/handler.js: the promoted version has compiled output on disk, so the sandbox must be pointed at it — %q would hand Node a TypeScript file",
			got.RunEntrypoint, got.Entrypoint)
	}
	if got.Entrypoint != "handler.ts" {
		t.Errorf("entrypoint = %q, want handler.ts: the authored file is not a build product", got.Entrypoint)
	}
}
