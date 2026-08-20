package registry

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func isolationTestDB(t *testing.T) *database.Database {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedFn(t *testing.T, r *Registry, name string) *database.Function {
	t.Helper()
	fn := &database.Function{
		Name: name, Runtime: "node", Entrypoint: "handler.js",
		Status: "active", MemoryMB: 64, TimeoutMS: 30000,
		EnvVars: map[string]string{"A": "1"},
	}
	if err := r.Set(fn); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	return fn
}

// TestGetReturnsAnIsolatedCopy — mutating what Get returned must not change
// what the next Get returns. This is the property every other fix here
// depends on.
func TestGetReturnsAnIsolatedCopy(t *testing.T) {
	r := New(isolationTestDB(t))
	fn := seedFn(t, r, "isolated")

	got, err := r.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.Name = "mutated"
	got.MemoryMB = 4096
	got.EnvVars["A"] = "tampered"
	got.EnvVars["B"] = "added"

	again, err := r.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "isolated" {
		t.Errorf("Name leaked through the cache: %q", again.Name)
	}
	if again.MemoryMB != 64 {
		t.Errorf("MemoryMB leaked through the cache: %d", again.MemoryMB)
	}
	if again.EnvVars["A"] != "1" {
		t.Errorf("EnvVars value leaked through the cache: %q", again.EnvVars["A"])
	}
	if _, added := again.EnvVars["B"]; added {
		t.Error("EnvVars key added by a caller leaked into the cache")
	}
}

// TestFailedSetDoesNotPoisonTheCache is the concrete bug: renaming a
// function to a name already taken fails at the UNIQUE constraint, the
// handler returns 500 and the row is untouched — but the cache used to keep
// the rejected values for the rest of the process lifetime, and the next
// successful deploy then persisted them.
func TestFailedSetDoesNotPoisonTheCache(t *testing.T) {
	r := New(isolationTestDB(t))
	alpha := seedFn(t, r, "alpha")
	seedFn(t, r, "beta")

	// Read-modify-write the way a handler does.
	pending, err := r.Get(alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	pending.Name = "beta" // already taken
	pending.Version++

	if err := r.Set(pending); err == nil {
		t.Fatal("expected a UNIQUE constraint failure renaming onto an existing name")
	}

	after, err := r.Get(alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "alpha" {
		t.Errorf("cache reports the rejected name %q; the database still says alpha", after.Name)
	}
	if after.Version != alpha.Version {
		t.Errorf("cache reports a bumped version %d after a failed write, want %d",
			after.Version, alpha.Version)
	}
}

// TestSetDoesNotRetainTheCallersStruct — the build queue calls SetSilent and
// then keeps writing to the same struct for the rest of the build. The cache
// must not be aliased to it.
func TestSetDoesNotRetainTheCallersStruct(t *testing.T) {
	r := New(isolationTestDB(t))
	fn := seedFn(t, r, "building")

	// Caller keeps mutating after the write returns, as queue.go does.
	fn.Status = "scribbled"
	fn.EnvVars["A"] = "scribbled"

	got, err := r.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" {
		t.Errorf("post-Set caller mutation reached the cache: status=%q", got.Status)
	}
	if got.EnvVars["A"] != "1" {
		t.Errorf("post-Set caller mutation reached the cache: env=%q", got.EnvVars["A"])
	}
}

// TestFunctionCloneCoversEveryReferenceField guards the clone against a
// future field. Any new map, slice, pointer or channel on Function is a
// shallow-share unless Clone is taught about it, and every failure of that
// kind is silent.
func TestFunctionCloneCoversEveryReferenceField(t *testing.T) {
	// Fields Clone is known to handle. Adding a reference-typed field
	// without updating Clone AND this list is the failure being prevented.
	handled := map[string]bool{"EnvVars": true}

	typ := reflect.TypeOf(database.Function{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		switch f.Type.Kind() {
		case reflect.Map, reflect.Slice, reflect.Ptr, reflect.Chan:
			if !handled[f.Name] {
				t.Errorf("Function.%s is %s: it shares storage across a copy. "+
					"Teach (*Function).Clone about it, then add it to `handled` here.",
					f.Name, f.Type.Kind())
			}
		}
	}
}

// TestExistsPopulatesCacheWithoutAliasing — Exists is the replacement for
// call sites that discarded Get's value, so it must not reintroduce the
// shared pointer through a side door.
func TestExistsPopulatesCacheWithoutAliasing(t *testing.T) {
	db := isolationTestDB(t)
	r := New(db)
	fn := seedFn(t, r, "probe")

	// Fresh registry so the lookup misses the cache and loads from SQLite.
	r2 := New(db)
	if !r2.Exists(fn.ID) {
		t.Fatal("Exists should resolve a persisted function")
	}
	if r2.Exists("no-such-id") {
		t.Error("Exists should be false for an unknown id")
	}

	got, err := r2.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.EnvVars["A"] = "tampered"
	again, err := r2.Get(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.EnvVars["A"] != "1" {
		t.Error("cache entry populated by Exists is aliased to callers")
	}
}
