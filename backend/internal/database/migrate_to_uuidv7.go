package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Harsh-2002/Orva/internal/ids"
)

// migrationMarkerKey is the system_config row that flags this
// migration as already applied. Once present, MigrateToUUIDv7 is a
// no-op — operators can restart the binary as many times as they like.
const migrationMarkerKey = "uuidv7_migration_done"

// PendingFnRenamesKey holds the functions old->new id map between the
// database rewrite and the matching rename of <dataDir>/functions/<id>/.
// Its presence means the two disagree, which is why the build GC treats
// it as a hard stop on orphan sweeping.
const PendingFnRenamesKey = "uuidv7_pending_fn_renames"

// functionsTable is the one parent whose ids also name directories on
// disk, so its rewrite has a filesystem half.
const functionsTable = "functions"

// idPair is one old->new id mapping. Exported field names because the
// functions map is persisted as JSON in system_config.
type idPair struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// idRewrite describes one parent table whose primary key must be
// rewritten to UUIDv7.
//
// Child columns are NOT listed here. They are derived at run time from
// PRAGMA foreign_key_list so a table added later cannot be forgotten —
// which is exactly how channel_functions came to be missing, and how
// executionChildTables (executions.go) drifted before it. The hand-list
// below covers only what the schema cannot tell us: references carrying
// no declared FK.
type idRewrite struct {
	// Parent table whose `id` column gets rewritten.
	parentTable string
	// idColumn — usually "id" but oauth_clients has both `id` (PK) AND
	// `client_id` (wire-side OAuth identifier referenced by other tables).
	idColumn string
	// softRefs lists columns holding a copy of this parent's id with NO
	// declared FOREIGN KEY, so foreign_key_list cannot find them and
	// foreign_key_check cannot verify them. These are the dangerous ones:
	// a miss here is silent, not a boot failure.
	softRefs []childRef
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
		softRefs: []childRef{
			// Denormalised trace columns — no FK by design, so nothing
			// but this list keeps them pointing at a real function.
			{"executions", "parent_function_id"},
			{"jobs", "enqueued_by_function_id"},
		},
	},
	{
		parentTable: "deployments", idColumn: "id",
		softRefs: []childRef{
			{"deployments", "parent_deployment_id"}, // self-reference
		},
	},
	{
		parentTable: "executions", idColumn: "id",
		softRefs: []childRef{
			// FK dropped deliberately in v0.4 A3 (see dropExecutionRequestsFK)
			// because the proxy queues the row before the parent lands.
			{"execution_requests", "execution_id"},
			{"user_spans", "execution_id"},
			{"execution_log_entries", "execution_id"},
			{"executions", "replay_of"}, // self-reference
		},
	},
	{parentTable: "event_subscriptions", idColumn: "id"},
	// channels.id is ids.New() from birth in current code, so this is
	// normally a no-op — rewriteOne skips values that are already UUIDs.
	// It is here because the table was renamed from agent_connectors in the
	// v2026.05.04 dev cycle, and a database from that era is exactly the
	// kind that still has this migration pending. Costs nothing; closes an
	// unknown. channel_functions.channel_id is a declared FK, so it is
	// derived rather than listed.
	{parentTable: "channels", idColumn: "id"},
	{parentTable: "cron_schedules", idColumn: "id"},
	{parentTable: "jobs", idColumn: "id"},
	{parentTable: "webhook_deliveries", idColumn: "id"},
	{parentTable: "fixtures", idColumn: "id"},
	{parentTable: "inbound_webhooks", idColumn: "id"},
	{
		parentTable: "api_keys", idColumn: "id",
		softRefs: []childRef{
			// actor_id is polymorphic: an api_keys.id for api_key actors, an
			// 8-char session-token prefix for web actors, a caller string for
			// scoped tokens. Matching on the old VALUE is the discriminator —
			// a session prefix can never equal a prefix-typed key id — so no
			// actor_type predicate is needed here.
			{"activity_log", "actor_id"},
		},
	},
	{parentTable: "oauth_clients", idColumn: "id"},
	{parentTable: "oauth_access_tokens", idColumn: "id"},
	// Special: oauth_clients.client_id is the wire-side OAuth identifier
	// referenced by oauth_authorization_codes.client_id and
	// oauth_access_tokens.client_id. Rewrite separately AFTER
	// oauth_clients.id is done so we don't double-rewrite.
	//
	// Both children declare the column as a plain `client_id TEXT NOT NULL`
	// with no FOREIGN KEY clause, so foreign_key_list cannot see them and
	// they must be listed here.
	{
		parentTable: "oauth_clients", idColumn: "client_id",
		softRefs: []childRef{
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
	var fnPairs []idPair
	for _, rw := range rewrites {
		refs, err := resolveChildRefs(tx, rw)
		if err != nil {
			return fmt.Errorf("resolve child refs for %s.%s: %w",
				rw.parentTable, rw.idColumn, err)
		}
		pairs, err := rewriteOne(tx, rw, refs)
		if err != nil {
			return fmt.Errorf("rewrite %s.%s: %w", rw.parentTable, rw.idColumn, err)
		}
		if len(pairs) > 0 {
			slog.Info("uuidv7 migration: rewrote table",
				"table", rw.parentTable, "column", rw.idColumn,
				"rows", len(pairs), "child_refs", len(refs))
		}
		if rw.parentTable == functionsTable && rw.idColumn == "id" {
			fnPairs = pairs
		}
		totalRewritten += int64(len(pairs))
	}

	// FK integrity check before we commit. If any reference is dangling
	// the migration has a logic bug and we should NOT commit.
	if err := checkFKIntegrity(tx); err != nil {
		return fmt.Errorf("integrity check failed: %w", err)
	}

	// foreign_key_check cannot see undeclared references, so verify those
	// ourselves: no soft-ref column may still hold an id we just rewrote.
	if err := checkSoftRefsRewritten(tx); err != nil {
		return fmt.Errorf("soft reference check failed: %w", err)
	}

	// Persist the functions old->new map INSIDE the transaction. The code
	// for each function lives at <dataDir>/functions/<id>/, and nothing
	// else in the system renames it — every call site builds that path
	// from the database id. If the ids move and the directories do not,
	// each function first fails to spawn and then GC.sweepOrphanFunctionDirs
	// deletes the tree as an orphan, because the new ids are not the names
	// on disk. That is the operator's only copy of their source.
	//
	// Committing the map with the rewrite means the rename can always be
	// completed or resumed afterwards, and reconcileFunctionDirs is
	// idempotent, so a crash between commit and rename is repaired on the
	// next boot instead of losing code.
	if err := writePendingFnRenames(tx, fnPairs); err != nil {
		return fmt.Errorf("record pending renames: %w", err)
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

// ReconcileFunctionDirs renames <dataDir>/functions/<oldID> to <newID> for
// every pending pair recorded by MigrateToUUIDv7, then clears the record.
//
// Safe and cheap to call on every boot: with no pending record it returns
// immediately. Idempotent per directory — a pair whose target already
// exists, or whose source is gone, is treated as done. The record is only
// cleared once every pair has been accounted for, so an interrupted run
// resumes on the next boot rather than stranding code under a name nothing
// will ever look up.
func (db *Database) ReconcileFunctionDirs(dataDir string) error {
	pairs, err := db.pendingFnRenames()
	if err != nil {
		return fmt.Errorf("read pending renames: %w", err)
	}
	if len(pairs) == 0 {
		return nil
	}
	if dataDir == "" {
		return errors.New("pending function dir renames but no data dir given")
	}

	root := filepath.Join(dataDir, "functions")
	renamed, skipped := 0, 0
	for _, p := range pairs {
		oldPath := filepath.Join(root, p.Old)
		newPath := filepath.Join(root, p.New)

		if _, err := os.Stat(newPath); err == nil {
			skipped++ // already renamed on an earlier pass
			continue
		}
		if _, err := os.Stat(oldPath); errors.Is(err, os.ErrNotExist) {
			skipped++ // function never had code on disk
			continue
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			// Leave the record in place so the next boot retries. Do NOT
			// clear it — that would hand the remaining trees to the GC.
			return fmt.Errorf("rename %s -> %s: %w", oldPath, newPath, err)
		}
		renamed++
	}

	if err := db.clearPendingFnRenames(); err != nil {
		return fmt.Errorf("clear pending renames: %w", err)
	}
	slog.Info("uuidv7 migration: reconciled function directories",
		"renamed", renamed, "already_done_or_absent", skipped)
	return nil
}

// HasPendingFunctionDirRenames reports whether a UUIDv7 rename is recorded
// but not yet reconciled. The build GC consults this before sweeping
// orphans: while it is true, the ids in the database do not match the
// names on disk and every directory looks like garbage.
func (db *Database) HasPendingFunctionDirRenames() bool {
	pairs, err := db.pendingFnRenames()
	if err != nil {
		return true // fail closed: unreadable state must not license deletion
	}
	return len(pairs) > 0
}

func writePendingFnRenames(tx *sql.Tx, pairs []idPair) error {
	if len(pairs) == 0 {
		return nil
	}
	blob, err := json.Marshal(pairs)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		`INSERT OR REPLACE INTO system_config (key, value) VALUES (?, ?)`,
		PendingFnRenamesKey, string(blob),
	)
	return err
}

func (db *Database) pendingFnRenames() ([]idPair, error) {
	var v sql.NullString
	err := db.read.QueryRow(
		`SELECT value FROM system_config WHERE key = ?`, PendingFnRenamesKey,
	).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !v.Valid || v.String == "" {
		return nil, nil
	}
	var pairs []idPair
	if err := json.Unmarshal([]byte(v.String), &pairs); err != nil {
		return nil, err
	}
	return pairs, nil
}

func (db *Database) clearPendingFnRenames() error {
	_, err := db.write.Exec(
		`DELETE FROM system_config WHERE key = ?`, PendingFnRenamesKey)
	return err
}

// resolveChildRefs returns every column that must move with this parent id:
// the declared foreign keys, read from the schema, plus the hand-listed
// soft references the schema cannot express.
func resolveChildRefs(tx *sql.Tx, rw idRewrite) ([]childRef, error) {
	declared, err := declaredChildRefs(tx, rw.parentTable, rw.idColumn)
	if err != nil {
		return nil, err
	}
	seen := make(map[childRef]struct{}, len(declared)+len(rw.softRefs))
	out := make([]childRef, 0, len(declared)+len(rw.softRefs))
	for _, c := range append(declared, rw.softRefs...) {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out, nil
}

// declaredChildRefs walks every table in the schema and asks SQLite which
// of its foreign keys point at parentTable.idColumn.
//
// PRAGMA foreign_key_list reads sqlite_master, so it reports what the
// schema DECLARES regardless of whether enforcement is currently on. That
// is the same declaration-vs-enforcement distinction foreign_key_check
// already relies on, and it is why running with foreign_keys=OFF here is
// fine.
func declaredChildRefs(tx *sql.Tx, parentTable, idColumn string) ([]childRef, error) {
	tables, err := userTables(tx)
	if err != nil {
		return nil, err
	}
	var out []childRef
	for _, t := range tables {
		// Table names come from sqlite_master, not from user input.
		rows, err := tx.Query(fmt.Sprintf("PRAGMA foreign_key_list(%q)", t))
		if err != nil {
			return nil, fmt.Errorf("foreign_key_list(%s): %w", t, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return nil, err
			}
			// Columns: id, seq, table, from, to, on_update, on_delete, match.
			parent := asString(vals[2])
			from := asString(vals[3])
			to := asString(vals[4])
			if !strings.EqualFold(parent, parentTable) {
				continue
			}
			// A NULL "to" means the FK targets the parent's primary key.
			if to == "" {
				to = "id"
			}
			if !strings.EqualFold(to, idColumn) {
				continue
			}
			out = append(out, childRef{table: t, column: from})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func userTables(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return ""
	}
}

// checkSoftRefsRewritten is the counterpart to foreign_key_check for
// references the schema does not declare. After a successful rewrite no
// soft-ref column may still hold a non-UUID value that names a row in its
// parent table, because such a value can only be a legacy id we missed.
func checkSoftRefsRewritten(tx *sql.Tx) error {
	var problems []string
	for _, rw := range rewrites {
		for _, c := range rw.softRefs {
			var n int
			q := fmt.Sprintf(
				`SELECT COUNT(*) FROM %q WHERE %q IS NOT NULL AND %q != '' AND %q NOT LIKE '%%-%%-%%'`,
				c.table, c.column, c.column, c.column)
			if err := tx.QueryRow(q).Scan(&n); err != nil {
				// A soft-ref table that does not exist yet is not an error:
				// the schema is additive and older DBs predate some of them.
				continue
			}
			if n > 0 {
				problems = append(problems,
					fmt.Sprintf("%s.%s has %d non-UUID values", c.table, c.column, n))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d soft reference column(s) still hold legacy ids: %v",
			len(problems), problems)
	}
	return nil
}

// rewriteOne handles a single parent table:
//  1. SELECT every existing id whose value is NOT already a UUIDv7
//  2. Generate a fresh UUIDv7 per old id
//  3. UPDATE the parent row
//  4. UPDATE every child column referencing the old id
//
// Returns the old->new pairs it applied, so the caller can drive the
// on-disk rename for functions.
func rewriteOne(tx *sql.Tx, rw idRewrite, childRefs []childRef) ([]idPair, error) {
	// Pull every old id. We skip rows whose id is already a valid UUID
	// — supports re-running the migration on a partially-migrated DB
	// (shouldn't happen given the marker, but cheap insurance).
	rows, err := tx.Query(fmt.Sprintf(`SELECT %s FROM %s`, rw.idColumn, rw.parentTable))
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}

	var pairs []idPair
	for rows.Next() {
		var old sql.NullString
		if err := rows.Scan(&old); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan: %w", err)
		}
		if !old.Valid || old.String == "" {
			continue
		}
		if ids.IsUUID(old.String) {
			continue // already migrated
		}
		pairs = append(pairs, idPair{Old: old.String, New: ids.New()})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	if len(pairs) == 0 {
		return nil, nil
	}

	// Update parent + each child column. One UPDATE per pair keeps
	// the SQL simple and avoids building an N-WHEN CASE expression.
	updateParent, err := tx.Prepare(fmt.Sprintf(
		`UPDATE %s SET %s = ? WHERE %s = ?`, rw.parentTable, rw.idColumn, rw.idColumn))
	if err != nil {
		return nil, fmt.Errorf("prepare parent update: %w", err)
	}
	defer updateParent.Close()

	childStmts := make([]*sql.Stmt, 0, len(childRefs))
	for _, c := range childRefs {
		s, err := tx.Prepare(fmt.Sprintf(
			`UPDATE %q SET %q = ? WHERE %q = ?`, c.table, c.column, c.column))
		if err != nil {
			return nil, fmt.Errorf("prepare child %s.%s: %w", c.table, c.column, err)
		}
		childStmts = append(childStmts, s)
		defer s.Close()
	}

	for _, p := range pairs {
		if _, err := updateParent.Exec(p.New, p.Old); err != nil {
			return nil, fmt.Errorf("update parent %s: %w", p.Old, err)
		}
		for i, c := range childRefs {
			if _, err := childStmts[i].Exec(p.New, p.Old); err != nil {
				return nil, fmt.Errorf("update child %s.%s for %s: %w",
					c.table, c.column, p.Old, err)
			}
		}
	}

	return pairs, nil
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
