package builder

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func newGCTestDB(t *testing.T) *database.Database {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "gc.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertFn(t *testing.T, db *database.Database, id string) {
	t.Helper()
	err := db.InsertFunction(&database.Function{
		ID: id, Name: id, Runtime: "node", Entrypoint: "handler.js", Status: "active",
	})
	if err != nil {
		t.Fatalf("insert function %s: %v", id, err)
	}
}

// agedCache creates a per-function cache of roughly `bytes` and back-dates it
// so it looks like it was last used `age` ago.
func agedCache(t *testing.T, data, fnID string, bytes int, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(data, "build-cache", fnID)
	mkTree(t, filepath.Join(dir, "npm"), bytes)
	when := time.Now().Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestGCSweepsCachesByAge: an unused cache is dropped after
// build_cache_max_age_days. Without an age bound a cache is an unbounded leak,
// which is strictly worse than having no cache at all.
func TestGCSweepsCachesByAge(t *testing.T) {
	data := t.TempDir()
	db := newGCTestDB(t)
	insertFn(t, db, "fresh")
	insertFn(t, db, "stale")

	freshDir := agedCache(t, data, "fresh", 512, time.Hour)
	staleDir := agedCache(t, data, "stale", 512, 30*24*time.Hour)

	gc := NewGC(data, db)
	gc.tick(t.Context())

	if !exists(freshDir) {
		t.Error("a cache used an hour ago must survive the 14-day age sweep")
	}
	if exists(staleDir) {
		t.Error("a cache unused for 30 days must be swept")
	}

	t.Run("age sweep disabled by 0", func(t *testing.T) {
		if err := db.SetSystemConfig(BuildCacheMaxAgeKey, "0"); err != nil {
			t.Fatal(err)
		}
		dir := agedCache(t, data, "stale", 512, 300*24*time.Hour)
		NewGC(data, db).tick(t.Context())
		if !exists(dir) {
			t.Error("max_age_days=0 must disable the age sweep entirely")
		}
	})
}

// TestGCEvictsWholeCachesUnderSizeCap: over build_cache_max_mb, the GC evicts
// least-recently-used WHOLE per-function directories. Never individual files —
// npm's cacache and pip's http cache both keep an index that would then point
// at content that no longer exists.
func TestGCEvictsWholeCachesUnderSizeCap(t *testing.T) {
	data := t.TempDir()
	db := newGCTestDB(t)
	for _, id := range []string{"oldest", "middle", "newest"} {
		insertFn(t, db, id)
	}
	// 1 MB each, 3 MB total, cap at 2 MB → the oldest must go, and only it.
	oldest := agedCache(t, data, "oldest", 1<<20, 72*time.Hour)
	middle := agedCache(t, data, "middle", 1<<20, 48*time.Hour)
	newest := agedCache(t, data, "newest", 1<<20, 24*time.Hour)
	if err := db.SetSystemConfig(BuildCacheMaxMBKey, "2"); err != nil {
		t.Fatal(err)
	}

	NewGC(data, db).tick(t.Context())

	if exists(oldest) {
		t.Error("least-recently-used cache must be evicted first")
	}
	if !exists(middle) || !exists(newest) {
		t.Error("eviction must stop as soon as the total is under the cap")
	}
	// What survives is a COMPLETE cache, not a half-emptied one.
	if _, err := os.Stat(filepath.Join(newest, "npm", "blob")); err != nil {
		t.Errorf("surviving caches must be untouched internally: %v", err)
	}
}

// TestGCDropsCachesOfDeletedFunctions: delete purges inline, but a crash
// between the DB delete and the unlink (or a row removed while orvad was down)
// would otherwise strand the cache forever.
func TestGCDropsCachesOfDeletedFunctions(t *testing.T) {
	data := t.TempDir()
	db := newGCTestDB(t)
	insertFn(t, db, "live")

	liveDir := agedCache(t, data, "live", 256, time.Hour)
	goneDir := agedCache(t, data, "gone", 256, time.Hour)

	NewGC(data, db).tick(t.Context())

	if !exists(liveDir) {
		t.Error("a live function's cache must survive")
	}
	if exists(goneDir) {
		t.Error("a cache with no matching function row must be dropped")
	}
}

// TestGCSweepsOrphanFunctionDirs: deleting a function used to leave
// functions/<id>/ on disk forever, because this GC only ever walks functions
// that still exist in the database. This reclaims what already leaked.
func TestGCSweepsOrphanFunctionDirs(t *testing.T) {
	data := t.TempDir()
	db := newGCTestDB(t)
	insertFn(t, db, "live")

	live := filepath.Join(data, "functions", "live", "versions", "aaa")
	orphan := filepath.Join(data, "functions", "orphan", "versions", "bbb")
	mkTree(t, live, 128)
	mkTree(t, orphan, 128)
	// Back-date the orphan past the in-flight grace window.
	old := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{
		filepath.Join(data, "functions", "orphan", "versions"),
		filepath.Join(data, "functions", "orphan"),
	} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	NewGC(data, db).tick(t.Context())

	if !exists(filepath.Join(data, "functions", "live")) {
		t.Fatal("a live function's code tree must never be removed")
	}
	if exists(filepath.Join(data, "functions", "orphan")) {
		t.Error("a function dir with no matching row must be reclaimed")
	}
}

// TestGCSparesFreshlyWrittenOrphanDirs is the race guard: a function created
// between the DB listing and the directory scan looks like an orphan. A recent
// write means "a build may be creating this right now", so leave it.
func TestGCSparesFreshlyWrittenOrphanDirs(t *testing.T) {
	data := t.TempDir()
	db := newGCTestDB(t)
	mkTree(t, filepath.Join(data, "functions", "just-created", "versions", "aaa"), 64)

	NewGC(data, db).tick(t.Context())

	if !exists(filepath.Join(data, "functions", "just-created")) {
		t.Fatal("a directory written seconds ago must not be treated as an orphan")
	}
}

// TestGCRespectsTheFunctionLock: a build holds the per-function lock for its
// whole duration, so nothing it might be writing is removed underneath it —
// and the GC must not block waiting for it either.
func TestGCRespectsTheFunctionLock(t *testing.T) {
	data := t.TempDir()
	db := newGCTestDB(t)

	cache := agedCache(t, data, "building", 256, 90*24*time.Hour)
	fnDir := filepath.Join(data, "functions", "building")
	mkTree(t, filepath.Join(fnDir, "versions", "aaa"), 64)
	old := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{filepath.Join(fnDir, "versions"), fnDir} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	var held sync.Mutex
	held.Lock() // stand in for an in-flight build
	gc := NewGC(data, db)
	gc.FnLock = func(string) *sync.Mutex { return &held }

	done := make(chan struct{})
	go func() { gc.tick(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("gc blocked on a held function lock; it must only try the lock")
	}

	if !exists(cache) {
		t.Error("a locked function's cache must not be evicted mid-build")
	}
	if !exists(fnDir) {
		t.Error("a locked function's code tree must not be removed mid-build")
	}
}

// TestGCSkipsSweepsOnTruncatedListing: the function listing is the authority
// for "which functions still exist". A truncated page would make every
// function past the limit look deleted — and take its code with it.
func TestGCSkipsSweepsOnTruncatedListing(t *testing.T) {
	data := t.TempDir()
	db := newGCTestDB(t)
	insertFn(t, db, "a")
	insertFn(t, db, "b")

	cache := agedCache(t, data, "not-listed", 256, 90*24*time.Hour)
	fnDir := filepath.Join(data, "functions", "not-listed")
	mkTree(t, filepath.Join(fnDir, "versions", "aaa"), 64)
	old := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{filepath.Join(fnDir, "versions"), fnDir} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	gc := NewGC(data, db)
	gc.listLimit = 1 // force a partial page
	gc.tick(t.Context())

	if !exists(cache) || !exists(fnDir) {
		t.Fatal("a truncated function listing must disable both orphan sweeps")
	}
}

// TestGCCacheSizeMemoTracksMtime: an untouched cache is not re-walked, but one
// stamped by a new build is. The memo is keyed on the mtime the build stamps.
func TestGCCacheSizeMemoTracksMtime(t *testing.T) {
	data := t.TempDir()
	dir := agedCache(t, data, "fn", 1024, 48*time.Hour)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	gc := NewGC(data, newGCTestDB(t))

	first := gc.cachedSize("fn", dir, info.ModTime())
	if first < 1024 {
		t.Fatalf("first measurement = %d, want >= 1024", first)
	}
	// Grow the tree WITHOUT changing the recorded mtime: the memo must hold,
	// which is the whole point (no walk per tick for an idle cache).
	mkTree(t, filepath.Join(dir, "npm", "more"), 4096)
	if got := gc.cachedSize("fn", dir, info.ModTime()); got != first {
		t.Errorf("memoized size = %d, want the cached %d", got, first)
	}
	// A new build stamps a new mtime, which must invalidate the memo.
	if got := gc.cachedSize("fn", dir, info.ModTime().Add(time.Second)); got <= first {
		t.Errorf("a new mtime must force a re-measure: got %d, previous %d", got, first)
	}
}
