package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	_ "modernc.org/sqlite"
	"time"
)

type Database struct {
	write *sql.DB
	read  *sql.DB
	path  string

	// writer batches async writes into small transactions to reduce fsync
	// pressure under sustained 500+ req/s. nil until Migrate() is called
	// (so tests that create a Database without migrating still work).
	writer *asyncWriter
	kv     kvMetrics

	// asyncWG tracks fire-and-forget goroutines (log inserts, last-used
	// updates) so Close() can wait for them to finish before tearing the
	// connections down. Without this the goroutines race with shutdown and
	// leave test temp dirs non-empty.
	asyncWG sync.WaitGroup
}

// Async runs the given function in a goroutine that Close() will wait for.
// Use this instead of a bare `go db.X()` for background DB writes.
func (db *Database) Async(fn func()) {
	db.asyncWG.Add(1)
	go func() {
		defer db.asyncWG.Done()
		fn()
	}()
}

func New(path string) (*Database, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	// Per-connection pragmas via DSN. modernc.org/sqlite recognizes only the
	// `_pragma=name(value)` form (other `_journal_mode=`/`_busy_timeout=`
	// names are silently dropped). Listing them here ensures every newly
	// opened pool connection runs them on connect — without this, only the
	// one connection that hosted the post-Open Exec call gets busy_timeout
	// and concurrent readers race writers into SQLITE_BUSY.
	// foreign_keys is per-CONNECTION, not per-database. It used to be set
	// once at the end of the schema string, so it lived only on whichever
	// pooled connection happened to serve that Exec -- if the driver ever
	// recycled it, the replacement had FKs off and DeleteFunction, which
	// relies entirely on ON DELETE CASCADE to clean up secrets, routes, kv
	// and crons, silently left them behind. It also made the async writer's
	// FK failures nondeterministic. PurgeOldExecutions already refuses to
	// trust it; nothing else should have to.
	const dsnPragmas = "_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(10000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=cache_size(-64000)" +
		"&_pragma=mmap_size(268435456)" +
		"&_pragma=temp_store(MEMORY)"

	writeDB, err := sql.Open("sqlite", path+"?"+dsnPragmas)
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	readDB, err := sql.Open("sqlite", path+"?"+dsnPragmas+"&mode=ro")
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readDB.SetMaxOpenConns(runtime.NumCPU())
	readDB.SetMaxIdleConns(runtime.NumCPU())

	// Database-level (file-scope) pragmas — set once on the writer. These
	// persist across connections so they don't belong in the per-conn DSN.
	dbScoped := []string{
		// Checkpoint the WAL less aggressively so short bursts don't pause
		// writers to compact the file. Default is 1000 pages (~4MB); 10000
		// lets us amortize over more writes at ~40MB of WAL growth.
		"PRAGMA wal_autocheckpoint = 10000",
		// Cap WAL growth at 64MB to bound on-disk footprint under sustained
		// writes. The checkpoint will truncate back to this size.
		"PRAGMA journal_size_limit = 67108864",
	}
	for _, p := range dbScoped {
		if _, err := writeDB.Exec(p); err != nil {
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}

	return &Database{write: writeDB, read: readDB, path: path}, nil
}

// Close shuts the database down in the only order that is safe:
// producers first, then the writer, then the connections.
//
// The reverse -- stopping the writer first -- was forced while the writer
// goroutine was itself registered in asyncWG, and it meant any producer that
// outlived the drain sent on a closed channel and panicked. A cron whose
// function overruns Scheduler.Stop's 5s grace does exactly that, turning a
// clean exit into a panic and a non-zero status that supervisors read as a
// crash loop.
func (db *Database) Close() error {
	// 1. Wait for fire-and-forget producers to finish enqueueing.
	db.asyncWG.Wait()
	// 2. Signal the writer and let it flush what is queued.
	if db.writer != nil {
		db.writer.stop(10 * time.Second)
	}
	// 3. Only now tear down the connections the writer was using.
	db.read.Close()
	return db.write.Close()
}

// Ping verifies the database is reachable and answering queries by running a
// trivial `SELECT 1` against the read pool. The health endpoint uses this as
// its hard liveness gate — a locked, corrupt, or unmounted orva.db surfaces
// here instead of being masked behind a hardcoded "ok". Returns nil when the
// query succeeds.
func (db *Database) Ping(ctx context.Context) error {
	var one int
	return db.read.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

func (db *Database) WriteDB() *sql.DB {
	return db.write
}

func (db *Database) ReadDB() *sql.DB {
	return db.read
}

func (db *Database) Path() string {
	return db.path
}
