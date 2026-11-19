package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadspawn"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedPoolConfigREST(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("pool-reader", `["read"]`)
	writer, _ := f.key("pool-writer", `["write"]`)
	admin, adminID := f.key("pool-admin", `["admin"]`)
	const path = "/api/v1/pool/config"
	get := path + "?function_id=" + f.owner
	body := func(fields string) string {
		return `{"function_id":"` + f.owner + `",` + fields + `}`
	}
	f.expect(http.MethodGet, get, "", "", 401)
	f.expect(http.MethodGet, get, admin, "", 403)
	f.expect(http.MethodHead, get, reader, "", 403)
	f.expect(http.MethodHead, get, writer, "", 200)
	f.expect(http.MethodGet, path+"?function_id="+ids.New(), reader, "", 404)
	f.expect(http.MethodGet, get, reader, "", 200)
	put := body(`"scale_to_zero":true`)
	for _, method := range []string{http.MethodPut, http.MethodPost} {
		f.expect(method, path, reader, put, 403)
		f.expect(method, path, writer, put, 403)
		f.expect(method, path, admin, put, 200)
	}
	if f.runtime.pool.CurrentFactory(f.owner) != nil {
		t.Fatal("saved tuning installed a Factory")
	}

	limits := artifactfs.Limits{Generations: 2, Leases: 8, Staging: 1, Entries: 8,
		PathBytes: 256, LinkBytes: 256, Bytes: 8192, FileBytes: 1024, ManifestBytes: 8192}
	root := t.TempDir()
	managedMust(t, os.Chmod(root, 0700))
	store, err := artifactfs.New(root, limits)
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, store.Close()) })
	code := sealFirewallFixture(t, store, limits, artifactfs.KindCode, f.owner, "handler.js")
	runtime := sealFirewallFixture(t, store, limits, artifactfs.KindRuntime, "node", "runtime")
	fn, err := f.reg.Get(f.owner)
	managedMust(t, err)
	fn.Status, fn.Entrypoint, fn.CodeHash = "active", "handler.js", strings.Repeat("a", 64)
	fn.MemoryMB, fn.CPUs, fn.NetworkMode, fn.ConcurrencyPolicy = 64, 1, "none", "queue"
	factory, err := workloadspawn.NewFactory(t.Context(), &secrets.PreparedArtifactSnapshot{
		Function: fn,
		Artifact: database.WorkloadArtifactRecord{OwnerID: f.owner, Generation: ids.New(), Revision: 1, Epoch: 1},
	}, store, code, store, runtime, workloadspawn.FactoryLimits{
		MaxPreparedPlans: 1, MaxPreparedBytes: 1 << 20, MaxSnapshotBytes: 1 << 20,
	})
	managedMust(t, err)
	t.Cleanup(factory.Close)
	managedMust(t, f.runtime.pool.InstallFactory(t.Context(), factory))

	results := make(chan int, 2)
	for _, field := range []string{`"idle_ttl_seconds":47`, `"max_warm":3`} {
		go func() { results <- f.call(http.MethodPut, path, admin, body(field)).Code }()
	}
	for range 2 {
		if status := <-results; status != 200 {
			t.Errorf("concurrent partial write status = %d, want 200", status)
		}
	}
	var cfg database.PoolConfig
	rec := f.expect(http.MethodGet, get, reader, "", 200)
	managedMust(t, json.Unmarshal(rec.Body.Bytes(), &cfg))
	if cfg.FunctionID != f.owner || cfg.IdleTTLS != 47 || cfg.MaxWarm != 3 || !cfg.ScaleToZero || cfg.MinWarm != 0 {
		t.Fatalf("partial tuning did not preserve committed fields: %+v", cfg)
	}
	if f.runtime.pool.CurrentFactory(f.owner) != factory || factory.Retired() {
		t.Fatal("tuning write replaced the installed Factory")
	}

	var cleanup func()
	managedMust(t, f.authority.ChangeHostState(func() { cleanup = f.runtime.pool.InvalidateOwnerFactoryUnderGate(f.owner) }))
	cleanup()
	f.expect(http.MethodPost, path, admin, body(`"idle_ttl_seconds":82`), 200)
	if f.runtime.pool.CurrentFactory(f.owner) != nil || !factory.Retired() {
		t.Fatal("tuning write reactivated an inactive Factory")
	}
	managedMust(t, f.db.DeleteAPIKey(adminID))
	f.expect(http.MethodPut, path, admin, put, 401)
}
