package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
)

// newFnDiskHandler wires a FunctionHandler over a real DB + registry rooted at
// a throwaway data dir, with one function already on disk.
func newFnDiskHandler(t *testing.T, fnID string) (*FunctionHandler, string) {
	t.Helper()
	db := newTestDB(t)
	dataDir := t.TempDir()
	fn := &database.Function{
		ID: fnID, Name: "victim", Runtime: "node", Entrypoint: "handler.js", Status: "active",
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatalf("insert function: %v", err)
	}
	reg := registry.New(db)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return &FunctionHandler{Registry: reg, DB: db, DataDir: dataDir}, dataDir
}

func writeTree(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDeleteFunctionReclaimsDisk: deleting a function used to remove only the
// database row, leaving functions/<id>/ on disk forever — the version GC only
// walks functions that still exist, so the tree was never revisited.
func TestDeleteFunctionReclaimsDisk(t *testing.T) {
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f40"
	h, dataDir := newFnDiskHandler(t, fnID)
	writeTree(t, filepath.Join(dataDir, "functions", fnID, "versions", "abc"))
	writeTree(t, filepath.Join(dataDir, "build-cache", fnID, "npm"))
	// A neighbouring function must be untouched.
	writeTree(t, filepath.Join(dataDir, "functions", "other-fn", "versions", "abc"))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/functions/"+fnID, nil)
	req.SetPathValue("fn_id", fnID)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body %s", w.Code, w.Body.String())
	}
	for _, gone := range []string{
		filepath.Join(dataDir, "functions", fnID),
		filepath.Join(dataDir, "build-cache", fnID),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s must be reclaimed on delete, stat err = %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "functions", "other-fn", "versions", "abc", "blob")); err != nil {
		t.Errorf("another function's code must survive: %v", err)
	}
}

// TestDeleteFunctionWaitsForBuildLock pins the ordering that prevents an
// in-flight build from recreating a function after DELETE returned. The build
// queue and this handler receive the same mutex from pool.Manager.
func TestDeleteFunctionWaitsForBuildLock(t *testing.T) {
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f44"
	h, dataDir := newFnDiskHandler(t, fnID)
	writeTree(t, filepath.Join(dataDir, "functions", fnID, "versions", "abc"))
	writeTree(t, filepath.Join(dataDir, "build-cache", fnID, "npm"))

	var buildLock sync.Mutex
	buildLock.Lock() // simulate Queue.runJob holding the per-function lock
	lockRequested := make(chan struct{})
	h.FnLock = func(got string) *sync.Mutex {
		if got != fnID {
			t.Errorf("FnLock(%q), want %q", got, fnID)
		}
		close(lockRequested)
		return &buildLock
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/functions/"+fnID, nil)
		req.SetPathValue("fn_id", fnID)
		w := httptest.NewRecorder()
		h.Delete(w, req)
		done <- w
	}()
	<-lockRequested

	select {
	case <-done:
		t.Fatal("delete completed while the function build lock was held")
	default:
	}
	if _, err := h.Registry.Get(fnID); err != nil {
		t.Fatalf("function disappeared before the build lock was released: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "functions", fnID, "versions", "abc", "blob")); err != nil {
		t.Fatalf("function files disappeared before the build lock was released: %v", err)
	}

	// A build may persist its final state while it owns the lock. DELETE must
	// run afterward and be the final writer.
	fn, err := h.Registry.Get(fnID)
	if err != nil {
		t.Fatal(err)
	}
	fn.Status = "active"
	if err := h.Registry.SetSilent(fn); err != nil {
		t.Fatalf("simulated build final write: %v", err)
	}
	buildLock.Unlock()
	w := <-done
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body %s", w.Code, w.Body.String())
	}
	if _, err := h.Registry.Get(fnID); err == nil {
		t.Fatal("function was recreated after serialized delete")
	}
	for _, gone := range []string{
		filepath.Join(dataDir, "functions", fnID),
		filepath.Join(dataDir, "build-cache", fnID),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s must be reclaimed after delete, stat err = %v", gone, err)
		}
	}
}

// TestDeleteSucceedsWhenDiskCleanupCannot: the row is gone by the time the
// unlink runs, so a filesystem failure must not report a failed delete. The
// GC's orphan sweep retries.
func TestDeleteSucceedsWhenDiskCleanupCannot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test relies on")
	}
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f41"
	h, dataDir := newFnDiskHandler(t, fnID)
	// A data dir that is not writable makes RemoveAll fail on a populated tree.
	writeTree(t, filepath.Join(dataDir, "functions", fnID, "versions", "abc"))
	if err := os.Chmod(filepath.Join(dataDir, "functions", fnID), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dataDir, "functions", fnID), 0o755) })

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/functions/"+fnID, nil)
	req.SetPathValue("fn_id", fnID)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("a failed unlink must not fail the delete: status %d, body %s", w.Code, w.Body.String())
	}
	if _, err := h.Registry.Get(fnID); err == nil {
		t.Error("the function row must be gone regardless")
	}
}

// TestPurgeBuildCacheEndpoint: the explicit recovery path from a build that
// installed something malicious — the cache is the only place those bytes
// persist between deploys.
func TestPurgeBuildCacheEndpoint(t *testing.T) {
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f42"
	h, dataDir := newFnDiskHandler(t, fnID)
	cache := filepath.Join(dataDir, "build-cache", fnID)
	writeTree(t, filepath.Join(cache, "npm"))
	code := filepath.Join(dataDir, "functions", fnID, "versions", "abc")
	writeTree(t, code)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/functions/"+fnID+"/build-cache", nil)
	req.SetPathValue("fn_id", fnID)
	w := httptest.NewRecorder()
	h.PurgeBuildCache(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Errorf("cache must be gone, stat err = %v", err)
	}
	// Purging a cache must not touch the deployed code.
	if _, err := os.Stat(filepath.Join(code, "blob")); err != nil {
		t.Errorf("purging the cache must not touch the code tree: %v", err)
	}
	// The function still exists, so a second purge is a no-op success.
	w = httptest.NewRecorder()
	h.PurgeBuildCache(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("purging an already-empty cache = %d, want 200", w.Code)
	}
}

// TestPurgeBuildCacheUnknownFunctionIs404: the id is resolved through the
// registry before any path is built from it, which is what keeps a caller from
// naming a directory that is not a function's.
func TestPurgeBuildCacheUnknownFunctionIs404(t *testing.T) {
	h, dataDir := newFnDiskHandler(t, "019df200-7b00-7e00-9c00-aab1cd2e3f43")
	writeTree(t, filepath.Join(dataDir, "build-cache", "sensitive"))

	for _, raw := range []string{"../sensitive", "nope", "..", "build-cache"} {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/functions/x/build-cache", nil)
		req.SetPathValue("fn_id", raw)
		w := httptest.NewRecorder()
		h.PurgeBuildCache(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("fn_id=%q: status %d, want 404", raw, w.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "build-cache", "sensitive", "blob")); err != nil {
		t.Errorf("nothing outside a resolved function may be removed: %v", err)
	}
}
