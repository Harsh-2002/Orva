package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

var (
	ErrManagedOwnerInvalid  = errors.New("managed database owner binding is invalid")
	ErrManagedOwnerLegacy   = errors.New("unbound database has existing workloads; explicit enrollment required")
	ErrManagedOwnerMismatch = errors.New("managed database belongs to a different artifact Owner")
)

const ownerBindingTable = `CREATE TABLE workload_owner_binding (
 id INTEGER PRIMARY KEY CHECK(id=1),
 version INTEGER NOT NULL CHECK(typeof(version)='integer' AND version=1),
 owner TEXT NOT NULL CHECK(typeof(owner)='text' AND length(owner)=36)
)`

func validManagedOwnerID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v.String() == id && v.Version() == 7 && v.Variant() == uuid.RFC4122
}

// InspectManagedOwnerBinding must run under exclusive startup ownership.
// A read-only SQLite connection can use WAL bookkeeping, but neither opens a
// writer nor requests journal conversion.
func InspectManagedOwnerBinding(ctx context.Context, path, owner string) error {
	return inspectManagedOwnerBinding(ctx, path, owner, false)
}

// RequireManagedOwnerBinding does not accept a new, unbound database.
func RequireManagedOwnerBinding(ctx context.Context, path, owner string) error {
	return inspectManagedOwnerBinding(ctx, path, owner, true)
}

func inspectManagedOwnerBinding(ctx context.Context, path, owner string, required bool) error {
	if ctx == nil || !filepath.IsAbs(path) || !validManagedOwnerID(owner) {
		return ErrManagedOwnerInvalid
	}
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrManagedOwnerInvalid
	}
	missing := errors.Is(err, os.ErrNotExist)
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		sidecar, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || missing || suffix == "-journal" || !sidecar.Mode().IsRegular() {
			return ErrManagedOwnerInvalid
		}
	}
	if missing {
		if required {
			return ErrManagedOwnerInvalid
		}
		return ctx.Err()
	}
	if !info.Mode().IsRegular() {
		return ErrManagedOwnerInvalid
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)"}
	conn, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return ErrManagedOwnerInvalid
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ErrManagedOwnerInvalid
	}
	defer tx.Rollback()
	bound, err := checkManagedOwnerBinding(ctx, tx, owner)
	if err != nil {
		return err
	}
	if required && !bound {
		return ErrManagedOwnerLegacy
	}
	return nil
}

func checkManagedOwnerBinding(ctx context.Context, tx *sql.Tx, owner string) (bool, error) {
	var typ, ddl string
	err := tx.QueryRowContext(ctx, `SELECT type,coalesce(sql,'') FROM main.sqlite_schema WHERE name='workload_owner_binding' COLLATE NOCASE`).
		Scan(&typ, &ddl)
	if errors.Is(err, sql.ErrNoRows) {
		var managed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main.sqlite_schema WHERE lower(name) GLOB 'workload_*')`).Scan(&managed); err != nil {
			return false, ErrManagedOwnerInvalid
		}
		if managed {
			return false, ErrManagedOwnerLegacy
		}
		for _, table := range []string{"functions", "jobs", "cron_schedules", "deployments"} {
			var kind string
			err := tx.QueryRowContext(ctx, `SELECT type FROM main.sqlite_schema WHERE name=? COLLATE NOCASE`, table).Scan(&kind)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil || kind != "table" {
				return false, ErrManagedOwnerInvalid
			}
			var hasRows bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main.`+table+`)`).Scan(&hasRows); err != nil {
				return false, ErrManagedOwnerInvalid
			}
			if hasRows {
				return false, ErrManagedOwnerLegacy
			}
		}
		return false, nil
	}
	if err != nil || typ != "table" || ddl != ownerBindingTable {
		return false, ErrManagedOwnerInvalid
	}
	var count, version int
	var id, storage string
	err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM main.workload_owner_binding), version, owner, typeof(owner)
		FROM main.workload_owner_binding WHERE id=1`).Scan(&count, &version, &id, &storage)
	if err != nil || count != 1 || version != 1 || storage != "text" || !validManagedOwnerID(id) {
		return false, ErrManagedOwnerInvalid
	}
	if owner != id {
		return false, ErrManagedOwnerMismatch
	}
	var functionsExist bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main.functions)`).Scan(&functionsExist); err != nil {
		return false, ErrManagedOwnerInvalid
	}
	if functionsExist {
		var missing bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM main.functions f WHERE NOT EXISTS(
				SELECT 1 FROM main.workload_iam_policies p WHERE p.function_id=f.id
			) OR NOT EXISTS(
				SELECT 1 FROM main.workload_artifact_generations a WHERE a.function_id=f.id
			))`).Scan(&missing); err != nil || missing {
			return false, ErrManagedOwnerLegacy
		}
	}
	return true, nil
}

// BindManagedOwner atomically rechecks provisioning under the SQLite write
// lock. It neither enrolls legacy workloads nor supplies an artifact fence.
func (db *Database) BindManagedOwner(ctx context.Context, owner string) error {
	if db == nil || ctx == nil || !validManagedOwnerID(owner) {
		return ErrManagedOwnerInvalid
	}
	db.lifecycleMu.Lock()
	defer db.lifecycleMu.Unlock()
	conn, err := db.write.Conn(ctx)
	if err != nil {
		return ErrManagedOwnerInvalid
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA synchronous=FULL`); err != nil {
		return ErrManagedOwnerInvalid
	}
	defer conn.ExecContext(context.Background(), `PRAGMA synchronous=NORMAL`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return ErrManagedOwnerInvalid
	}
	defer tx.Rollback()
	bound, err := checkManagedOwnerBinding(ctx, tx, owner)
	if err != nil || bound {
		return err
	}
	// In WAL a stale transaction cannot upgrade to a writer if another writer
	// changed the database after the read above; SQLite refuses the upgrade.
	if _, err := tx.ExecContext(ctx, ownerBindingTable); err != nil {
		return ErrManagedOwnerInvalid
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workload_owner_binding VALUES(1,1,?);
		CREATE TRIGGER workload_owner_binding_no_update BEFORE UPDATE ON workload_owner_binding
			BEGIN SELECT RAISE(ABORT,'managed owner binding is permanent'); END;
		CREATE TRIGGER workload_owner_binding_no_delete BEFORE DELETE ON workload_owner_binding
			BEGIN SELECT RAISE(ABORT,'managed owner binding is permanent'); END;`, owner); err != nil {
		return ErrManagedOwnerInvalid
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("bind managed owner: %w", ErrManagedOwnerInvalid)
	}
	return nil
}
