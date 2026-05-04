package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// migrationMarkerKey is the system_config row that flags this
// migration as already applied. Once present, MigrateToUUIDv7 is a
// no-op — operators can restart the binary as many times as they like.
const migrationMarkerKey = "uuidv7_migration_done"

// idRewrite describes one parent table whose primary key must be
// rewritten to UUIDv7, plus every child column that references it.
type idRewrite struct {
	// Parent table whose `id` column gets rewritten.
	parentTable string
	// idColumn — usually "id" but oauth_clients has both `id` (PK) AND
	// `client_id` (wire-side OAuth identifier referenced by other tables).
	idColumn string
	// Child columns that hold a copy of the parent's old id and need
	// to be updated in lockstep. (childTable, childColumn) pairs.
	childRefs []childRef
}

type childRef struct {
	table  string
	column string
}

// rewrites lists, in dependency-safe order, every parent ID column to
// rewrite plus every FK pointing at it. Order isn't strictly required
// because FKs are off during the run, but keeping it logical helps
// when reading the logs.
var rewrites = []idRewrite{
	{
		parentTable: "functions", idColumn: "id",
		childRefs: []childRef{
			{"executions", "function_id"},
			{"pool_config", "function_id"},
			{"function_secrets", "function_id"},
			{"deployments", "function_id"},
			{"routes", "function_id"},
			{"cron_schedules", "function_id"},
			{"kv_store", "function_id"},
			{"jobs", "function_id"},
			{"fixtures", "function_id"},
			{"inbound_webhooks", "function_id"},
		},
	},
	{
		parentTable: "deployments", idColumn: "id",
		childRefs: []childRef{
			{"build_logs", "deployment_id"},
		},
	},
	{
		parentTable: "executions", idColumn: "id",
		childRefs: []childRef{
			{"execution_logs", "execution_id"},
			{"execution_requests", "execution_id"}, // soft FK
		},
	},
	{
		parentTable: "event_subscriptions", idColumn: "id",
		childRefs: []childRef{
			{"webhook_deliveries", "subscription_id"},
		},
	},
	// Tables with no children — just rewrite the PK.
	{parentTable: "cron_schedules", idColumn: "id"},
	{parentTable: "jobs", idColumn: "id"},
	{parentTable: "webhook_deliveries", idColumn: "id"},
	{parentTable: "fixtures", idColumn: "id"},
	{parentTable: "inbound_webhooks", idColumn: "id"},
	{parentTable: "api_keys", idColumn: "id"},
	{parentTable: "oauth_clients", idColumn: "id"},
	{parentTable: "oauth_access_tokens", idColumn: "id"},
	// Special: oauth_clients.client_id is the wire-side OAuth identifier
	// referenced by oauth_authorization_codes.client_id and
	// oauth_access_tokens.client_id. Rewrite separately AFTER
	// oauth_clients.id is done so we don't double-rewrite.
	{
		parentTable: "oauth_clients", idColumn: "client_id",
		childRefs: []childRef{
			{"oauth_authorization_codes", "client_id"},
			{"oauth_access_tokens", "client_id"},
		},
	},
}

// MigrateToUUIDv7 is the one-shot in-place rewrite of every prefix-typed
// storage ID to canonical UUIDv7. Idempotent — guarded by a marker row
// in system_config. Runs inside a single transaction with FKs disabled;
// any error rolls back and the operator can retry once the cause is
// fixed (or restore a backup).
//
// The function is called from Database.Migrate after the schema
// migrations finish. New installs skip the bulk because the SELECTs
// return zero rows; they still get the marker so the migration is
// permanently inert thereafter.
func (db *Database) MigrateToUUIDv7() error {
	if done, err := db.uuidMigrationDone(); err != nil {
		return fmt.Errorf("check migration marker: %w", err)
	} else if done {
		return nil
	}

	slog.Info("uuidv7 migration: starting")

	// FKs off so we can mutate parent PKs without cascade fireworks.
	// MUST happen outside the transaction — SQLite ignores PRAGMA
	// foreign_keys inside a tx.
	if _, err := db.write.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable FKs: %w", err)
	}
	defer func() { _, _ = db.write.Exec("PRAGMA foreign_keys = ON") }()

	tx, err := db.write.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after commit

	totalRewritten := int64(0)
	for _, rw := range rewrites {
		n, err := rewriteOne(tx, rw)
		if err != nil {
			return fmt.Errorf("rewrite %s.%s: %w", rw.parentTable, rw.idColumn, err)
		}
		if n > 0 {
			slog.Info("uuidv7 migration: rewrote table",
				"table", rw.parentTable, "column", rw.idColumn,
				"rows", n, "child_refs", len(rw.childRefs))
		}
		totalRewritten += n
	}

	// FK integrity check before we commit. If any reference is dangling
	// the migration has a logic bug and we should NOT commit.
	if err := checkFKIntegrity(tx); err != nil {
		return fmt.Errorf("integrity check failed: %w", err)
	}

	// Mark complete inside the same transaction so a power loss between
	// the rewrites and the marker leaves the DB in the unmigrated state
	// (which the next boot will retry).
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO system_config (key, value) VALUES (?, ?)`,
		migrationMarkerKey, "true",
	); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("uuidv7 migration: complete", "total_rows_rewritten", totalRewritten)
	return nil
}

// rewriteOne handles a single parent table:
//  1. SELECT every existing id whose value is NOT already a UUIDv7
//  2. Generate a fresh UUIDv7 per old id
//  3. UPDATE the parent row
//  4. UPDATE every child column referencing the old id
//
// Returns the number of parent rows rewritten.
func rewriteOne(tx *sql.Tx, rw idRewrite) (int64, error) {
	// Pull every old id. We skip rows whose id is already a valid UUID
	// — supports re-running the migration on a partially-migrated DB
	// (shouldn't happen given the marker, but cheap insurance).
	rows, err := tx.Query(fmt.Sprintf(`SELECT %s FROM %s`, rw.idColumn, rw.parentTable))
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}

	type pair struct{ old, new string }
	var pairs []pair
	for rows.Next() {
		var old sql.NullString
		if err := rows.Scan(&old); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan: %w", err)
		}
		if !old.Valid || old.String == "" {
			continue
		}
		if ids.IsUUID(old.String) {
			continue // already migrated
		}
		pairs = append(pairs, pair{old: old.String, new: ids.New()})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("rows: %w", err)
	}
	if len(pairs) == 0 {
		return 0, nil
	}

	// Update parent + each child column. One UPDATE per pair keeps
	// the SQL simple and avoids building an N-WHEN CASE expression.
	updateParent, err := tx.Prepare(fmt.Sprintf(
		`UPDATE %s SET %s = ? WHERE %s = ?`, rw.parentTable, rw.idColumn, rw.idColumn))
	if err != nil {
		return 0, fmt.Errorf("prepare parent update: %w", err)
	}
	defer updateParent.Close()

	childStmts := make([]*sql.Stmt, 0, len(rw.childRefs))
	for _, c := range rw.childRefs {
		s, err := tx.Prepare(fmt.Sprintf(
			`UPDATE %s SET %s = ? WHERE %s = ?`, c.table, c.column, c.column))
		if err != nil {
			return 0, fmt.Errorf("prepare child %s.%s: %w", c.table, c.column, err)
		}
		childStmts = append(childStmts, s)
		defer s.Close()
	}

	for _, p := range pairs {
		if _, err := updateParent.Exec(p.new, p.old); err != nil {
			return 0, fmt.Errorf("update parent %s: %w", p.old, err)
		}
		for i, c := range rw.childRefs {
			if _, err := childStmts[i].Exec(p.new, p.old); err != nil {
				return 0, fmt.Errorf("update child %s.%s for %s: %w",
					c.table, c.column, p.old, err)
			}
		}
	}

	return int64(len(pairs)), nil
}

// checkFKIntegrity runs SQLite's foreign_key_check pragma. Returns
// non-nil if any FK violation exists — that's an abort condition.
func checkFKIntegrity(tx *sql.Tx) error {
	rows, err := tx.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	var violations []string
	for rows.Next() {
		var table, parent sql.NullString
		var rowid sql.NullInt64
		var fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		violations = append(violations, fmt.Sprintf(
			"%s rowid=%d -> %s (fk #%d)",
			table.String, rowid.Int64, parent.String, fkid.Int64))
	}
	if len(violations) > 0 {
		return fmt.Errorf("foreign_key_check found %d dangling references: %v",
			len(violations), violations)
	}
	return nil
}

// uuidMigrationDone reports whether the marker row exists. Returns
// (false, nil) on a fresh DB with no system_config row — the migration
// will then run, mark itself, and never run again.
func (db *Database) uuidMigrationDone() (bool, error) {
	var v sql.NullString
	err := db.read.QueryRow(
		`SELECT value FROM system_config WHERE key = ?`, migrationMarkerKey,
	).Scan(&v)
	if err == nil {
		return v.String == "true", nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}
