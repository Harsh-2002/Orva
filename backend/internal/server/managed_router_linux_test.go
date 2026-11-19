//go:build linux

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadconfig"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedRouterPolicyAndDefaultDeny(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "managed.db"))
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, db.Close()) })
	managedMust(t, db.Migrate())
	owner := ids.New()
	managedMust(t, db.InsertFunction(&database.Function{ID: owner, Name: "managed", Runtime: "node", Status: "active", AuthMode: "none"}))
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
	manager := runtime.pool
	t.Cleanup(func() { managedMust(t, manager.Shutdown(context.Background())) })
	router, err := managedRoutes(db, reg, authority, manager, nil, nil, "", runtime.invoke)
	managedMust(t, err)
	other, err := workloadiam.NewAuthority(32, nil)
	managedMust(t, err)
	if got, err := managedRoutes(db, reg, other, manager, nil, nil, "", nil); err == nil || got != nil {
		t.Fatal("accepted a mismatched pool authority")
	}

	const key = "orva_managed_fixture"
	hash := sha256.Sum256([]byte(key))
	managedMust(t, db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: "fixture", Prefix: "fixture",
		KeyHash: hex.EncodeToString(hash[:]), Permissions: `["read","write","admin"]`}))
	base := "/api/v1/functions/" + owner
	call := func(method, path, bearer string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("X-Orva-API-Key", bearer)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	call(http.MethodGet, base+"/permissions", "", http.StatusUnauthorized)
	rec := call(http.MethodGet, base+"/permissions", key, http.StatusOK)
	var snapshot workloadPolicySnapshot
	managedMust(t, json.Unmarshal(rec.Body.Bytes(), &snapshot))
	if snapshot.Function.ID != owner || snapshot.Host.Revision != "1" || snapshot.Host.CurrentArtifact != nil {
		t.Fatalf("unexpected policy response: %s", rec.Body.String())
	}
	call(http.MethodPut, base, key, http.StatusNotFound)
	call(http.MethodPost, base+"/secrets", key, http.StatusNotFound)
	call(http.MethodPost, "/mcp", key, http.StatusNotFound)
	rec = call(http.MethodPost, "/fn/"+owner+"/nested/path", "", http.StatusServiceUnavailable)
	if rec.Header().Get("X-Orva-Execution-ID") == "" || !strings.Contains(rec.Body.String(), pool.ErrNoFunction.Error()) {
		t.Fatalf("direct invocation did not reach the managed pool's missing-Factory guard: %s", rec.Body)
	}
}

func TestManagedRouterReadsSecretNamesButNeverWritesThem(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "managed.db"))
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, db.Close()) })
	managedMust(t, db.Migrate())
	owner := ids.New()
	managedMust(t, db.InsertFunction(&database.Function{ID: owner, Name: "managed", Runtime: "node", Status: "active", AuthMode: "none"}))
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
	t.Cleanup(func() { managedMust(t, runtime.pool.Shutdown(context.Background())) })
	ops := runtime.routeDeps("/usr/local/bin/nsjail")
	recorded := map[string]managedTier{}
	ops.recordRoute = func(pattern string, tier managedTier) { recorded[pattern] = tier }
	// The editor's deploy and configuration routes need both services; the
	// read path below must live beside them, not behind a legacy fallback.
	router, err := managedRoutesWithAsync(db, reg, authority, runtime.pool, new(workloadconfig.Service), new(builder.PublicationQueue),
		t.TempDir(), runtime.invoke, nil, nil, nil, ops)
	managedMust(t, err)

	managedMust(t, db.UpsertSecret(owner, "API_TOKEN", "ciphertext-not-a-value"))
	managedMust(t, db.UpsertSecret(owner, "ANOTHER", "ciphertext-not-a-value"))
	const key = "orva_managed_secrets"
	hash := sha256.Sum256([]byte(key))
	managedMust(t, db.InsertAPIKey(&database.APIKey{ID: ids.New(), Name: "fixture", Prefix: "fixture",
		KeyHash: hex.EncodeToString(hash[:]), Permissions: `["read"]`}))
	call := func(method, path, bearer string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		if bearer != "" {
			req.Header.Set("X-Orva-API-Key", bearer)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	base := "/api/v1/functions/" + owner
	call(http.MethodGet, base+"/secrets", "", http.StatusUnauthorized)
	if got := recorded["GET /api/v1/functions/{fn_id}/secrets"]; got != managedRead {
		t.Fatalf("secret name read is gated at %q, want read", got)
	}
	var names struct {
		Secrets []string `json:"secrets"`
	}
	rec := call(http.MethodGet, base+"/secrets", key, http.StatusOK)
	managedMust(t, json.Unmarshal(rec.Body.Bytes(), &names))
	if len(names.Secrets) != 2 || names.Secrets[0] != "ANOTHER" || names.Secrets[1] != "API_TOKEN" {
		t.Fatalf("unexpected managed secret names: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "ciphertext-not-a-value") {
		t.Fatalf("the managed secret read leaked values: %s", rec.Body.String())
	}
	call(http.MethodGet, "/api/v1/functions/"+ids.New()+"/secrets", key, http.StatusNotFound)
	// Writes stay on the CAS-checked configuration route: POST on this path is
	// method-rejected (405) and the delete path has no managed route at all.
	call(http.MethodPost, base+"/secrets", key, http.StatusMethodNotAllowed)
	call(http.MethodDelete, base+"/secrets/API_TOKEN", key, http.StatusNotFound)
	var after struct {
		Secrets []string `json:"secrets"`
	}
	rec = call(http.MethodGet, base+"/secrets", key, http.StatusOK)
	managedMust(t, json.Unmarshal(rec.Body.Bytes(), &after))
	if len(after.Secrets) != 2 {
		t.Fatalf("a rejected write changed the secret set: %s", rec.Body.String())
	}
}

func TestManagedReconstructedAuthorityRequiresResume(t *testing.T) {
	meta := workloadiam.Metadata{OwnerID: ids.New(), Revision: 3, ApprovedArtifact: strings.Repeat("a", 64)}
	evaluator, err := workloadiam.CompileDeny(meta)
	managedMust(t, err)
	authority, err := newReconstructedAuthority([]*workloadiam.Evaluator{evaluator}, []workloadiam.Metadata{meta})
	managedMust(t, err)
	next := meta
	next.Revision++
	deny, err := workloadiam.CompileDeny(next)
	managedMust(t, err)
	persisted := false
	cancel, err := authority.ResumeOwnerDeferred(meta, deny, func() error {
		persisted = true
		return nil
	})
	managedMust(t, err)
	cancel()
	if !persisted {
		t.Fatal("the reconstructed pause did not allow a deny-only resume")
	}
	if a, err := newReconstructedAuthority([]*workloadiam.Evaluator{evaluator}, []workloadiam.Metadata{next}); err == nil || a != nil {
		t.Fatal("restored pause against a different policy revision")
	}
}

type workloadPolicySnapshot struct {
	Function struct {
		ID string `json:"id"`
	} `json:"function"`
	Host struct {
		Revision        string  `json:"revision"`
		CurrentArtifact *string `json:"current_artifact"`
	} `json:"host"`
}

func managedMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
