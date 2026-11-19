package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedBuildCachePurge(t *testing.T) {
	f := newManagedOpsFixture(t)
	writer, writerID := f.key("cache-writer", `["write"]`)
	reader, _ := f.key("cache-reader", `["read"]`)
	dir := t.TempDir()
	const route = "DELETE /api/v1/functions/{fn_id}/build-cache"
	path := "/api/v1/functions/" + f.owner + "/build-cache"
	f.expect(http.MethodDelete, path, writer, "", 404)

	mux := http.NewServeMux()
	gateRoute(mux, f.db, nil, route, managedWrite, purgeManagedBuildCache(f.db, f.reg, f.runtime.pool, dir))
	f.router = mux
	cache, err := builder.BuildCacheDir(dir, f.owner)
	managedMust(t, err)
	uuidName := ids.New()
	sibling := &database.Function{ID: ids.New(), Name: uuidName, Runtime: "node", Status: "inactive"}
	managedMust(t, f.reg.Set(sibling))
	siblingCache, err := builder.BuildCacheDir(dir, sibling.ID)
	managedMust(t, err)
	fnDir, err := builder.FunctionDir(dir, f.owner)
	managedMust(t, err)
	seed := func(dir string) string {
		t.Helper()
		managedMust(t, os.MkdirAll(dir, 0700))
		file := filepath.Join(dir, "fixture")
		managedMust(t, os.WriteFile(file, []byte("intact"), 0600))
		return file
	}
	target := seed(filepath.Join(cache, "npm"))
	sentinel := seed(siblingCache)
	source := seed(filepath.Join(fnDir, "versions"))
	assertIntact := func(file string) {
		t.Helper()
		b, err := os.ReadFile(file)
		if err != nil || string(b) != "intact" {
			t.Fatalf("changed scratch file %s: %v", file, err)
		}
	}
	f.expect(http.MethodDelete, path, "", "", 401)
	f.expect(http.MethodDelete, path, reader, "", 403)
	f.expect(http.MethodDelete, "/api/v1/functions/"+uuidName+"/build-cache", writer, "", 404)
	assertIntact(target)
	assertIntact(sentinel)

	t.Run("revoked before entering owner-critical section", func(t *testing.T) {
		buildLock := f.runtime.pool.FunctionLock(f.owner)
		ownerLock := f.reg.MutationLock(f.owner)
		ownerLock.Lock()
		done := make(chan int, 1)
		go func() { done <- f.call(http.MethodDelete, path, writer, "").Code }()
		deadline := time.Now().Add(2 * time.Second)
		for buildLock.TryLock() {
			buildLock.Unlock()
			if time.Now().After(deadline) {
				ownerLock.Unlock()
				t.Fatal("purge did not take the shared build lock before the owner lock")
			}
			time.Sleep(time.Millisecond)
		}
		err := f.db.DeleteAPIKey(writerID)
		ownerLock.Unlock()
		managedMust(t, err)
		if status := <-done; status != 403 {
			t.Fatalf("stale writer status = %d; want 403", status)
		}
		assertIntact(target)
	})

	writer, _ = f.key("cache-writer-replacement", `["write"]`)
	rec := f.expect(http.MethodDelete, "/api/v1/functions/managed/build-cache", writer, "", 200)
	var result map[string]string
	managedMust(t, json.Unmarshal(rec.Body.Bytes(), &result))
	if result["status"] != "purged" || result["id"] != f.owner {
		t.Fatalf("unexpected purge result: %v", result)
	}
	if _, err := os.Stat(cache); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dependency cache remains: %v", err)
	}
	f.expect(http.MethodDelete, path, writer, "", 200)
	assertIntact(sentinel)
	assertIntact(source)
}
