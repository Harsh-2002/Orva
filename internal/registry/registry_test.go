package registry

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Harsh-2002/Orva/internal/database"
)

// helper: create a temp database for testing
func testDB(t *testing.T) *database.Database {
	t.Helper()
	dir := t.TempDir()
	db, err := database.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestGenerateID(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "fn_") {
		t.Fatalf("expected prefix fn_, got %s", id)
	}
	if len(id) != 3+12 { // fn_ + 12 chars
		t.Fatalf("expected length 15, got %d (%s)", len(id), id)
	}
}

func TestSetAndGetWarmCache(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	fn := &database.Function{
		Name:       "hello",
		Runtime:    "node22",
		Entrypoint: "handler.js",
		Status:     "active",
	}

	if err := reg.Set(fn); err != nil {
		t.Fatalf("set: %v", err)
	}
	if fn.ID == "" {
		t.Fatal("expected ID to be generated")
	}

	// Second Get should be a warm cache hit (already stored by Set)
	got, err := reg.Get(fn.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "hello" {
		t.Fatalf("expected name hello, got %s", got.Name)
	}
}

func TestGetColdCacheHit(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	fn := &database.Function{
		Name:       "cold-test",
		Runtime:    "python311",
		Entrypoint: "main.py",
		Status:     "active",
	}
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}

	// Create a fresh registry (empty cache) pointing at same DB
	reg2 := New(db)

	got, err := reg2.Get(fn.ID)
	if err != nil {
		t.Fatalf("cold get: %v", err)
	}
	if got.Name != "cold-test" {
		t.Fatalf("expected cold-test, got %s", got.Name)
	}

	// Subsequent call should now be warm
	got2, err := reg2.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Name != "cold-test" {
		t.Fatalf("expected cold-test on warm hit, got %s", got2.Name)
	}
}

func TestCacheInvalidationOnSet(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	fn := &database.Function{
		Name:       "invalidate-test",
		Runtime:    "node22",
		Entrypoint: "handler.js",
		Status:     "active",
	}
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}

	// Update the function
	fn.Runtime = "node22"
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}

	got, err := reg.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "node22" {
		t.Fatalf("expected node22 after update, got %s", got.Runtime)
	}
}

func TestDelete(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	fn := &database.Function{
		Name:       "delete-me",
		Runtime:    "go",
		Entrypoint: "main.go",
		Status:     "active",
	}
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}

	if err := reg.Delete(fn.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Should not be in cache or DB
	_, err := reg.Get(fn.ID)
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

func TestList(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	for i := 0; i < 3; i++ {
		fn := &database.Function{
			Name:       "list-fn-" + string(rune('A'+i)),
			Runtime:    "node22",
			Entrypoint: "handler.js",
			Status:     "active",
		}
		if err := reg.Set(fn); err != nil {
			t.Fatal(err)
		}
	}

	result, err := reg.List(database.ListFunctionsParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 3 {
		t.Fatalf("expected 3 total, got %d", result.Total)
	}
}

func TestLoadAll(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	fn := &database.Function{
		Name:       "loadall-test",
		Runtime:    "node22",
		Entrypoint: "handler.js",
		Status:     "active",
	}
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}

	// New registry, empty cache
	reg2 := New(db)
	if err := reg2.LoadAll(); err != nil {
		t.Fatal(err)
	}

	// Should be in cache now (no DB read needed)
	got, err := reg2.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "loadall-test" {
		t.Fatalf("expected loadall-test, got %s", got.Name)
	}
}

func TestConcurrentAccess(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	// Insert some functions
	ids := make([]string, 10)
	for i := 0; i < 10; i++ {
		fn := &database.Function{
			Name:       "conc-" + string(rune('A'+i)),
			Runtime:    "node22",
			Entrypoint: "handler.js",
			Status:     "active",
		}
		if err := reg.Set(fn); err != nil {
			t.Fatal(err)
		}
		ids[i] = fn.ID
	}

	// Concurrent reads
	var wg sync.WaitGroup
	errs := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := ids[idx%len(ids)]
			_, err := reg.Get(id)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent get error: %v", err)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
