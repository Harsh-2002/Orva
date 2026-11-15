package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
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
	// UUIDv7 canonical form: 36 chars including 4 dashes.
	if len(id) != 36 {
		t.Fatalf("expected length 36 UUIDv7, got %d (%s)", len(id), id)
	}
	if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("expected canonical UUID dash positions, got %s", id)
	}
}

func TestSetAndGetWarmCache(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	fn := &database.Function{
		Name:       "hello",
		Runtime:    "node",
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
		Runtime:    "node",
		Entrypoint: "handler.js",
		Status:     "active",
	}
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}

	// Update the function (switch runtime to verify the cache invalidates)
	fn.Runtime = "python"
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}

	got, err := reg.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "python" {
		t.Fatalf("expected python after update, got %s", got.Runtime)
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

	if err := reg.Delete(context.Background(), fn.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Should not be in cache or DB
	_, err := reg.Get(fn.ID)
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

// A delete cancelled before it starts leaves the cache matching the database.
func TestDeleteCancelledBeforeStartKeepsCacheCoherent(t *testing.T) {
	db := testDB(t)
	reg := New(db)
	fn := &database.Function{Name: "not-deleted", Runtime: "node", Entrypoint: "handler.js", Status: "active"}
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reg.Delete(ctx, fn.ID); err == nil {
		t.Fatal("a cancelled delete should report an error")
	}
	got, err := reg.Get(fn.ID)
	if err != nil || got.Status != "active" {
		t.Fatalf("cache after a delete that never started: %+v, %v; want status active", got, err)
	}
}

// A function whose delete has started cannot be revived by a late build,
// rollback or PUT, and a later Delete finishes it.
func TestSetRefusedWhileDeletingAndDeleteResumes(t *testing.T) {
	db := testDB(t)
	reg := New(db)
	fn := &database.Function{Name: "half-deleted", Runtime: "node", Entrypoint: "handler.js", Status: "active"}
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}
	deleting := fn.Clone()
	deleting.Status = database.FunctionStatusDeleting
	if err := db.UpdateFunction(deleting); err != nil { // the state a cancelled delete leaves
		t.Fatal(err)
	}

	revive := fn.Clone()
	revive.Status = "active"
	if err := reg.Set(revive); !errors.Is(err, database.ErrFunctionDeleting) {
		t.Fatalf("Set on a deleting function = %v, want ErrFunctionDeleting", err)
	}
	if err := reg.SetSilent(revive); !errors.Is(err, database.ErrFunctionDeleting) {
		t.Fatalf("SetSilent on a deleting function = %v, want ErrFunctionDeleting", err)
	}

	if err := reg.Delete(context.Background(), fn.ID); err != nil {
		t.Fatalf("resume delete: %v", err)
	}
	if _, err := reg.Get(fn.ID); err == nil {
		t.Fatal("function still present after the resumed delete")
	}
}

func TestList(t *testing.T) {
	db := testDB(t)
	reg := New(db)

	for i := 0; i < 3; i++ {
		fn := &database.Function{
			Name:       "list-fn-" + string(rune('A'+i)),
			Runtime:    "node",
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
		Runtime:    "node",
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
			Runtime:    "node",
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
