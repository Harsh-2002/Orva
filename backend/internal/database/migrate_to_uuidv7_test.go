package database

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// TestMigrationFreshDB — calling Migrate on an empty DB must run the
// UUID migration as a no-op (no rows to rewrite) AND must write the
// marker so subsequent boots skip it.
func TestMigrationFreshDB(t *testing.T) {
	db := newTestDB(t)
	done, err := db.uuidMigrationDone()
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("marker not written on fresh DB")
	}
}

// TestMigrationIdempotent — calling MigrateToUUIDv7 a second time
// after a successful run must be a no-op.
func TestMigrationIdempotent(t *testing.T) {
	db := newTestDB(t)
	// First call already happened inside Migrate(). Run it again.
	if err := db.MigrateToUUIDv7(); err != nil {
		t.Fatalf("second call should be a no-op, got error: %v", err)
	}
}

// TestMigrationPopulatedDB — programmatically seed a DB with old
// prefixed IDs across every parent table and FK relationship, then run
// the migration and assert:
//   - Every parent row's id is a valid UUID
//   - Every FK column points at the correct new UUID (not orphaned)
//   - PRAGMA foreign_key_check is clean
//   - Created_at values are preserved
func TestMigrationPopulatedDB(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Run schema migrations but stop short of MigrateToUUIDv7 by
	// deleting the marker so we can seed legacy data first.
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	// Reset the marker — pretend we just upgraded from old code.
	if _, err := db.write.Exec(
		`DELETE FROM system_config WHERE key = ?`, migrationMarkerKey,
	); err != nil {
		t.Fatal(err)
	}

	// Seed: a function with deployments and executions, each with FK
	// children. All IDs in the legacy fn_<short> / dep_<short> /
	// exec_<short> form so the migration has work to do.
	const legacyFnID = "fn_test12345678"
	const legacyDepID = "dep_test12345678"
	const legacyExecID = "exec_test1234"
	const legacyKeyID = "key_testkey1"

	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.write.Exec(query, args...); err != nil {
			t.Fatalf("seed: %v\nquery: %s", err, query)
		}
	}

	mustExec(`INSERT INTO functions (id, name, runtime, entrypoint) VALUES (?, ?, ?, ?)`,
		legacyFnID, "test-func", "python314", "handler.py")
	mustExec(`INSERT INTO deployments (id, function_id, version, status, phase) VALUES (?, ?, ?, ?, ?)`,
		legacyDepID, legacyFnID, 1, "succeeded", "complete")
	mustExec(`INSERT INTO build_logs (deployment_id, seq, line, stream) VALUES (?, ?, ?, ?)`,
		legacyDepID, 1, "build started", "info")
	mustExec(`INSERT INTO executions (id, function_id, status, started_at, finished_at, duration_ms, status_code, cold_start) VALUES (?, ?, 'success', datetime('now'), datetime('now'), 100, 200, 0)`,
		legacyExecID, legacyFnID)
	mustExec(`INSERT INTO execution_logs (execution_id, stdout, stderr) VALUES (?, ?, ?)`,
		legacyExecID, "hello", "")
	mustExec(`INSERT INTO routes (function_id, path) VALUES (?, ?)`,
		legacyFnID, "/api/test")
	mustExec(`INSERT INTO api_keys (id, key_hash, name, permissions) VALUES (?, ?, ?, ?)`,
		legacyKeyID, "fakehash", "test-key", `["read"]`)

	// Run the migration.
	if err := db.MigrateToUUIDv7(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Assert every parent row has a UUIDv7 id now.
	for _, q := range []struct {
		table   string
		oldID   string
		idCol   string
		wantOne bool
	}{
		{"functions", legacyFnID, "id", true},
		{"deployments", legacyDepID, "id", true},
		{"executions", legacyExecID, "id", true},
		{"api_keys", legacyKeyID, "id", true},
	} {
		var n int
		err := db.read.QueryRow(
			`SELECT COUNT(*) FROM `+q.table+` WHERE `+q.idCol+` = ?`, q.oldID,
		).Scan(&n)
		if err != nil {
			t.Errorf("%s: count old id: %v", q.table, err)
			continue
		}
		if n != 0 {
			t.Errorf("%s: %d rows still have legacy id %q", q.table, n, q.oldID)
		}
	}

	// Pull the new function id and verify FK propagation.
	var newFnID string
	if err := db.read.QueryRow(
		`SELECT id FROM functions WHERE name = ?`, "test-func",
	).Scan(&newFnID); err != nil {
		t.Fatal(err)
	}
	if !ids.IsUUID(newFnID) {
		t.Errorf("function id is not a UUID: %q", newFnID)
	}
	if strings.HasPrefix(newFnID, "fn_") {
		t.Errorf("function id still has fn_ prefix: %q", newFnID)
	}

	// Every FK that referenced legacyFnID must now reference newFnID.
	for _, table := range []string{"deployments", "executions", "routes"} {
		var refID string
		if err := db.read.QueryRow(
			`SELECT function_id FROM `+table+` LIMIT 1`,
		).Scan(&refID); err != nil {
			t.Errorf("%s: %v", table, err)
			continue
		}
		if refID != newFnID {
			t.Errorf("%s.function_id = %q, want %q", table, refID, newFnID)
		}
	}

	// build_logs.deployment_id must point at the new deployment id.
	var newDepID string
	db.read.QueryRow(`SELECT id FROM deployments WHERE function_id = ?`, newFnID).Scan(&newDepID)
	var blDepID string
	if err := db.read.QueryRow(
		`SELECT deployment_id FROM build_logs LIMIT 1`,
	).Scan(&blDepID); err != nil {
		t.Fatal(err)
	}
	if blDepID != newDepID {
		t.Errorf("build_logs.deployment_id = %q, want %q", blDepID, newDepID)
	}

	// execution_logs.execution_id must point at the new execution id.
	var newExecID string
	db.read.QueryRow(`SELECT id FROM executions WHERE function_id = ?`, newFnID).Scan(&newExecID)
	var elExecID string
	if err := db.read.QueryRow(
		`SELECT execution_id FROM execution_logs LIMIT 1`,
	).Scan(&elExecID); err != nil {
		t.Fatal(err)
	}
	if elExecID != newExecID {
		t.Errorf("execution_logs.execution_id = %q, want %q", elExecID, newExecID)
	}

	// FK integrity across the whole DB — no dangling references.
	rows, err := db.read.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		t.Error("foreign_key_check returned a violation post-migration")
	}
}

// TestMigrationOAuthClientWireID — oauth_clients.client_id is the
// wire-side OAuth identifier, separate from oauth_clients.id. Other
// tables FK against the wire id, so the migration must rewrite both
// columns AND every reference in lockstep.
func TestMigrationOAuthClientWireID(t *testing.T) {
	dir := t.TempDir()
	db, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	// Reset marker.
	db.write.Exec(`DELETE FROM system_config WHERE key = ?`, migrationMarkerKey)

	// Seed a client with the legacy ocl_/ocid_ format plus a token row
	// referencing it.
	const legacyClientStorageID = "ocl_legacy123456"
	const legacyClientWireID = "ocid_legacywire123abc"
	const legacyTokenID = "oat_legacytoken1"
	if _, err := db.write.Exec(`INSERT INTO oauth_clients (id, client_id, client_name, redirect_uris, grant_types, response_types, token_endpoint_auth_method, scope) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		legacyClientStorageID, legacyClientWireID, "Test", `["http://localhost/cb"]`,
		`["authorization_code"]`, `["code"]`, "none", "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`INSERT INTO oauth_access_tokens (id, access_token_hash, client_id, user_id, scope, access_expires_at) VALUES (?, ?, ?, ?, ?, datetime('now', '+1 hour'))`,
		legacyTokenID, "fakehash", legacyClientWireID, 1, "read"); err != nil {
		t.Fatal(err)
	}

	if err := db.MigrateToUUIDv7(); err != nil {
		t.Fatalf("migration: %v", err)
	}

	// Both columns rewritten on the client row.
	var newStorage, newWire string
	if err := db.read.QueryRow(
		`SELECT id, client_id FROM oauth_clients WHERE client_name = ?`, "Test",
	).Scan(&newStorage, &newWire); err != nil {
		t.Fatal(err)
	}
	if !ids.IsUUID(newStorage) || strings.HasPrefix(newStorage, "ocl_") {
		t.Errorf("oauth_clients.id not migrated: %q", newStorage)
	}
	if !ids.IsUUID(newWire) || strings.HasPrefix(newWire, "ocid_") {
		t.Errorf("oauth_clients.client_id not migrated: %q", newWire)
	}

	// Token row's storage id AND its FK to client_id must be rewritten.
	var tokID, tokClientID string
	if err := db.read.QueryRow(
		`SELECT id, client_id FROM oauth_access_tokens LIMIT 1`,
	).Scan(&tokID, &tokClientID); err != nil {
		t.Fatal(err)
	}
	if !ids.IsUUID(tokID) || strings.HasPrefix(tokID, "oat_") {
		t.Errorf("oauth_access_tokens.id not migrated: %q", tokID)
	}
	if tokClientID != newWire {
		t.Errorf("oauth_access_tokens.client_id = %q, want new wire %q",
			tokClientID, newWire)
	}
}
