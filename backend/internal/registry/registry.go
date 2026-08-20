package registry

import (
	"fmt"
	"sync"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// Registry provides a cache-first function registry backed by SQLite.
type Registry struct {
	db    *database.Database
	cache sync.Map // map[string]*database.Function

	// PublishEvent, if set, is fired on any mutation (Set / Delete) so
	// SSE clients (FunctionsList page, etc.) can refresh without polling.
	// Wired by server.New from events.Hub.Publish. Best-effort: a nil
	// publisher is a no-op for tests / setups without the event hub.
	PublishEvent func(eventType string, data any)
}

// New creates a new Registry with the given database backend.
func New(db *database.Database) *Registry {
	return &Registry{db: db}
}

// GenerateID returns a new function ID — a UUIDv7 in canonical
// 36-char dashed form. Time-sortable so newest functions appear at
// the right edge of the index without separate ORDER BY created_at.
func GenerateID() (string, error) {
	return ids.New(), nil
}

// Get retrieves a function by ID. It checks the in-memory cache first,
// falling back to SQLite on a miss.
//
// The returned value is always a COPY. Handing out the cached pointer let
// callers mutate the cache in place: an update handler that applied fields
// before persisting left the cache reporting the new values even when the
// write failed, and the invoke path read those same fields on every request
// while the update handler wrote them, unsynchronized. Copying on the way
// out makes the cached object immutable after publication by construction,
// which is what both problems actually needed.
func (r *Registry) Get(id string) (*database.Function, error) {
	// Cache hit
	if v, ok := r.cache.Load(id); ok {
		return v.(*database.Function).Clone(), nil
	}

	// Cache miss – read from database
	fn, err := r.db.GetFunction(id)
	if err != nil {
		return nil, err
	}

	// Populate cache with our own copy, hand the caller theirs.
	r.cache.Store(fn.ID, fn.Clone())
	return fn, nil
}

// Exists reports whether a function id resolves, without paying for a copy.
// Use this instead of discarding Get's result.
func (r *Registry) Exists(id string) bool {
	if _, ok := r.cache.Load(id); ok {
		return true
	}
	fn, err := r.db.GetFunction(id)
	if err != nil || fn == nil {
		return false
	}
	r.cache.Store(fn.ID, fn.Clone())
	return true
}

// Set writes a function to SQLite and updates the cache.
// If the function has no ID, one is generated. Publishes "created" on
// fresh inserts and "updated" on updates so subscribers (dashboard live
// counter, webhook fanout) can distinguish.
func (r *Registry) Set(fn *database.Function) error {
	return r.set(fn, true)
}

// SetSilent persists without publishing a Hub event. Used by code
// paths that flip internal state during a deploy lifecycle (build
// queue moving status: queued → building → active). Those changes
// already surface to subscribers via the deployment.* events; firing
// function.updated alongside would duplicate the signal and flood
// webhook receivers during a single deploy.
func (r *Registry) SetSilent(fn *database.Function) error {
	return r.set(fn, false)
}

func (r *Registry) set(fn *database.Function, publish bool) error {
	if fn.ID == "" {
		id, err := GenerateID()
		if err != nil {
			return err
		}
		fn.ID = id
	}

	// Try insert first; fall back to update only if the function already
	// exists by ID (i.e. an upsert). Other insert errors (e.g. UNIQUE
	// constraint on name) are propagated. The action we ultimately
	// publish depends on which path actually wrote the row.
	action := "created"
	if err := r.db.InsertFunction(fn); err != nil {
		if _, existsErr := r.db.GetFunction(fn.ID); existsErr != nil {
			// Function doesn't exist by ID — propagate the original error.
			return err
		}
		if err2 := r.db.UpdateFunction(fn); err2 != nil {
			return fmt.Errorf("set function: insert=%w, update=%v", err, err2)
		}
		action = "updated"
	}

	// Copy on the way IN as well. The build queue calls SetSilent and then
	// keeps mutating the same struct for the rest of a multi-minute build,
	// so storing the caller's pointer would leave the cache aliased to
	// something still being written. Publish the copy, not the original.
	cached := fn.Clone()
	r.cache.Delete(fn.ID)
	r.cache.Store(fn.ID, cached)
	if publish && r.PublishEvent != nil {
		// The SSE hub marshals this on another goroutine, arbitrarily later —
		// another reader that must not see the caller's live struct.
		r.PublishEvent("function", map[string]any{
			"action":   action,
			"function": cached,
		})
	}
	return nil
}

// Delete removes a function from both SQLite and the cache.
func (r *Registry) Delete(id string) error {
	if err := r.db.DeleteFunction(id); err != nil {
		return err
	}
	r.cache.Delete(id)
	if r.PublishEvent != nil {
		r.PublishEvent("function", map[string]any{
			"action": "deleted",
			"id":     id,
		})
	}
	return nil
}

// List returns functions from the database (always reads from SQLite for consistency).
func (r *Registry) List(params database.ListFunctionsParams) (*database.ListFunctionsResult, error) {
	return r.db.ListFunctions(params)
}

// LoadAll loads all functions from SQLite into the cache. Typically called at startup.
func (r *Registry) LoadAll() error {
	result, err := r.db.ListFunctions(database.ListFunctionsParams{Limit: 100000})
	if err != nil {
		return fmt.Errorf("load all functions: %w", err)
	}
	for _, fn := range result.Functions {
		r.cache.Store(fn.ID, fn.Clone())
	}
	return nil
}

// ListActive returns all functions with status "active". This is used by the
// pool replenisher to know which functions need warm pools.
func (r *Registry) ListActive() []*database.Function {
	result, err := r.db.ListFunctions(database.ListFunctionsParams{
		Status: "active",
		Limit:  100000,
	})
	if err != nil {
		return nil
	}
	return result.Functions
}
