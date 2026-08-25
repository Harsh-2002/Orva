package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func keysTestDB(t *testing.T) *database.Database {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "mcp-keys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

// The auth middleware memoises API keys by hash, so a revoked key is only
// actually revoked once something evicts that memo; the entry's TTL bounds the
// damage of forgetting but does not do the job. The REST
// handler has always done that. This tool did not, so a key revoked through
// the AI sidebar or an MCP client went on authenticating every /api/v1/*
// request for the life of the process.
//
// The asymmetry is what made it dangerous rather than merely wrong: /mcp
// resolves credentials straight from the database, so the key stopped working
// on exactly the surface the operator revoked it from, and kept working
// everywhere else. The tool's own description promises "Active sessions using
// that key fail their next request with 401".
func TestDeleteAPIKeyToolEvictsTheAuthCache(t *testing.T) {
	db := keysTestDB(t)

	const keyID, keyHash = "key_revokeme", "hash-of-the-revoked-key"
	if err := db.InsertAPIKey(&database.APIKey{
		ID: keyID, KeyHash: keyHash, Prefix: "orva_rev", Name: "to be revoked",
		Permissions: `["invoke","read","write","admin"]`,
	}); err != nil {
		t.Fatal(err)
	}

	var evicted []string
	reg := BuildAgentRegistry(Deps{
		DB:            db,
		InvalidateKey: func(h string) { evicted = append(evicted, h) },
	}, allPerms())

	tool := reg.Get("delete_api_key")
	if tool == nil {
		t.Fatal("delete_api_key is not registered for an admin principal")
	}
	if _, err := reg.Dispatch(context.Background(), "delete_api_key",
		json.RawMessage(`{"key_id":"`+keyID+`","confirm":true}`)); err != nil {
		t.Fatalf("delete_api_key: %v", err)
	}

	// The row is gone...
	if k, err := db.GetAPIKeyByID(keyID); err == nil && k != nil {
		t.Error("the key row survived the delete")
	}
	// ...and so is the cache entry that would have kept it working.
	if len(evicted) != 1 || evicted[0] != keyHash {
		t.Errorf("evicted = %v, want exactly [%q]: without this the revoked key keeps authenticating on /api/v1/* until the process restarts",
			evicted, keyHash)
	}
}

// A refused delete must not evict anything: the key is still valid, and
// dropping it from the cache would silently cost every holder a database
// round-trip on their next request for no reason.
func TestDeleteAPIKeyToolEvictsNothingWhenItRefuses(t *testing.T) {
	db := keysTestDB(t)
	const keyID, keyHash = "key_keepme", "hash-of-the-surviving-key"
	if err := db.InsertAPIKey(&database.APIKey{
		ID: keyID, KeyHash: keyHash, Prefix: "orva_keep", Name: "kept",
		Permissions: `["read"]`,
	}); err != nil {
		t.Fatal(err)
	}

	var evicted []string
	reg := BuildAgentRegistry(Deps{
		DB:            db,
		InvalidateKey: func(h string) { evicted = append(evicted, h) },
	}, allPerms())

	if _, err := reg.Dispatch(context.Background(), "delete_api_key",
		json.RawMessage(`{"key_id":"`+keyID+`"}`)); err == nil {
		t.Fatal("delete without confirm=true was accepted")
	}
	if len(evicted) != 0 {
		t.Errorf("evicted %v on a refused delete", evicted)
	}
	if k, err := db.GetAPIKeyByID(keyID); err != nil || k == nil {
		t.Error("a refused delete removed the row anyway")
	}
}

// Deps.InvalidateKey is optional, and the tool must not panic without it --
// several tests and the schema-only registration path build Deps bare.
func TestDeleteAPIKeyToolToleratesNoInvalidator(t *testing.T) {
	db := keysTestDB(t)
	if err := db.InsertAPIKey(&database.APIKey{
		ID: "key_nocb", KeyHash: "hash-nocb", Prefix: "orva_nocb", Name: "n",
		Permissions: `["read"]`,
	}); err != nil {
		t.Fatal(err)
	}
	reg := BuildAgentRegistry(Deps{DB: db}, allPerms())
	if _, err := reg.Dispatch(context.Background(), "delete_api_key",
		json.RawMessage(`{"key_id":"key_nocb","confirm":true}`)); err != nil {
		t.Fatalf("delete_api_key with a nil InvalidateKey: %v", err)
	}
}
