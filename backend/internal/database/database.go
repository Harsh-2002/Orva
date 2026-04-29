package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	_ "modernc.org/sqlite"
)

type Database struct {
	write *sql.DB
	read  *sql.DB
	path  string

	// writer batches async writes into small transactions to reduce fsync
	// pressure under sustained 500+ req/s. nil until Migrate() is called
	// (so tests that create a Database without migrating still work).
	writer *asyncWriter

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
	const dsnPragmas = "_pragma=busy_timeout(10000)" +
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

func (db *Database) Close() error {
	if db.writer != nil {
		db.writer.stop()
	}
	db.asyncWG.Wait()
	db.read.Close()
	return db.write.Close()
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
