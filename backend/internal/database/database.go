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

	writeDB, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=10000")
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	readDB, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=10000&mode=ro")
	if err != nil {
		writeDB.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	readDB.SetMaxOpenConns(runtime.NumCPU())
	readDB.SetMaxIdleConns(runtime.NumCPU())

	pragmas := []string{
		"PRAGMA cache_size = -64000",
		"PRAGMA mmap_size = 268435456",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA busy_timeout = 10000",
		// Checkpoint the WAL less aggressively so short bursts don't pause
		// writers to compact the file. Default is 1000 pages (~4MB); 10000
		// lets us amortize over more writes at ~40MB of WAL growth.
		"PRAGMA wal_autocheckpoint = 10000",
		// Cap WAL growth at 64MB to bound on-disk footprint under sustained
		// writes. The checkpoint will truncate back to this size.
		"PRAGMA journal_size_limit = 67108864",
	}
	for _, p := range pragmas {
		if _, err := writeDB.Exec(p); err != nil {
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}
	// Also apply busy_timeout to read connections so they wait for writers
	// instead of immediately returning SQLITE_BUSY.
	if _, err := readDB.Exec("PRAGMA busy_timeout = 10000"); err != nil {
		return nil, fmt.Errorf("pragma busy_timeout (read): %w", err)
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
