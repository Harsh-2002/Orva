package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Keep database/sql's optional-interface dispatch intact; statements and rows
// are returned untouched. Fail explicitly if the pinned driver's surface changes.
type deleteProgressDriverConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.Execer
	driver.ExecerContext
	driver.Queryer
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

type deleteProgressConnector struct {
	base  driver.Driver
	dsn   string
	gate  *deleteProgressGate
	opens atomic.Int32
}

func (c *deleteProgressConnector) Driver() driver.Driver { return c.base }

func (c *deleteProgressConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var conn driver.Conn
	var err error
	if d, ok := c.base.(driver.DriverContext); ok {
		var connector driver.Connector
		connector, err = d.OpenConnector(c.dsn)
		if err == nil {
			conn, err = connector.Connect(ctx)
		}
	} else {
		conn, err = c.base.Open(c.dsn)
	}
	if err != nil {
		return nil, err
	}
	base, supported := conn.(deleteProgressDriverConn)
	_, namedChecker := conn.(driver.NamedValueChecker)
	if !supported || namedChecker {
		_ = conn.Close()
		return nil, fmt.Errorf("delete progress: unsupported driver interface surface %T (required=%v named checker=%v)", conn, supported, namedChecker)
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	c.opens.Add(1)
	return &deleteProgressConn{deleteProgressDriverConn: base, gate: c.gate}, nil
}

type deleteProgressGate struct {
	prefix   string
	held     chan struct{}
	release  chan struct{}
	once     sync.Once
	unblock  sync.Once
	failAt   int
	parents  int
	children int64
	injected bool
}

func (g *deleteProgressGate) open() { g.unblock.Do(func() { close(g.release) }) }

type deleteProgressConn struct {
	deleteProgressDriverConn
	gate *deleteProgressGate
}

var errDeleteProgressParent = errors.New("delete progress: injected parent failure")

func (c *deleteProgressConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	g := c.gate
	if strings.HasPrefix(query, g.prefix) {
		var err error
		g.once.Do(func() {
			close(g.held)
			select {
			case <-g.release:
			case <-ctx.Done():
				err = ctx.Err()
			}
		})
		if err != nil {
			return nil, err
		}
	}
	if strings.HasPrefix(query, "DELETE FROM executions WHERE id IN") {
		g.parents++
		if g.failAt == g.parents {
			if g.children != int64(len(executionChildTables)*purgeChunk) || len(args) != purgeChunk {
				return nil, fmt.Errorf("injection reached without complete child deletes: children=%d parents=%d", g.children, len(args))
			}
			g.injected = true
			return nil, errDeleteProgressParent
		}
		g.children = 0
	}
	result, err := c.deleteProgressDriverConn.ExecContext(ctx, query, args)
	if err == nil {
		for _, table := range executionChildTables {
			if strings.HasPrefix(query, "DELETE FROM "+table+" WHERE execution_id IN") {
				n, countErr := result.RowsAffected()
				if countErr != nil {
					return nil, countErr
				}
				g.children += n
				break
			}
		}
	}
	return result, err
}

type deleteProgressFixture struct {
	db        *Database
	gate      *deleteProgressGate
	connector *deleteProgressConnector
	deletion  context.Context
	contender context.Context
	cleanup   context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

const deleteProgressID = "progress-target"
const deleteProgressTotal = 2*purgeChunk + 37

func newDeleteProgressFixture(t *testing.T, phase string, failAt int) *deleteProgressFixture {
	t.Helper()
	db := newTestDB(t)
	nExec, nKV, nJobs := deleteProgressTotal, 0, 0
	prefix := "DELETE FROM execution_logs WHERE execution_id IN"
	if phase == "kv_store" {
		nExec, nKV = 0, deleteProgressTotal
		prefix = "DELETE FROM kv_store WHERE rowid IN"
	} else if phase == "jobs" {
		nExec, nJobs = 0, deleteProgressTotal
		prefix = "DELETE FROM jobs WHERE rowid IN"
	}
	seedFnData(t, db, deleteProgressID, nExec, nKV, nJobs, 0)
	db.asyncWG.Wait()
	db.writer.stop(5 * time.Second)
	select {
	case <-db.writer.done:
		db.writer = nil
	default:
		t.Fatal("async consumer did not stop; cannot replace its writer")
	}
	base := db.write.Driver()
	if err := db.write.Close(); err != nil {
		t.Fatal(err)
	}
	gate := &deleteProgressGate{
		prefix: prefix, held: make(chan struct{}), release: make(chan struct{}), failAt: failAt,
	}
	connector := &deleteProgressConnector{
		base: base, gate: gate,
		dsn: db.path + "?_pragma=foreign_keys(ON)&_pragma=busy_timeout(10000)" +
			"&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
			"&_pragma=cache_size(-64000)&_pragma=mmap_size(268435456)&_pragma=temp_store(MEMORY)",
	}
	db.write = sql.OpenDB(connector)
	db.write.SetMaxOpenConns(1)
	db.write.SetMaxIdleConns(1)
	deletion, cancelDelete := context.WithTimeout(context.Background(), 20*time.Second)
	contender, cancelContender := context.WithTimeout(context.Background(), 20*time.Second)
	cleanup, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
	f := &deleteProgressFixture{
		db: db, gate: gate, connector: connector, deletion: deletion,
		contender: contender, cleanup: cleanup, cancel: cancelDelete,
	}
	// Registered after newTestDB: join all users before its sole Database.Close.
	t.Cleanup(func() {
		gate.open()
		cancelDelete()
		cancelContender()
		f.wg.Wait()
		cancelCleanup()
	})
	for _, query := range []string{
		"PRAGMA wal_autocheckpoint = 10000",
		"PRAGMA journal_size_limit = 67108864",
	} {
		if _, err := db.write.ExecContext(cleanup, query); err != nil {
			t.Fatal(err)
		}
	}
	for pragma, want := range map[string]string{
		"foreign_keys": "1", "busy_timeout": "10000", "journal_mode": "wal", "synchronous": "1",
	} {
		var got string
		if err := db.write.QueryRowContext(cleanup, "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
			t.Fatalf("%s = %q, %v; want %q", pragma, got, err, want)
		}
	}
	return f
}

func (f *deleteProgressFixture) start(fn func() error) <-chan error {
	done := make(chan error, 1)
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		done <- fn()
	}()
	return done
}

func awaitDeleteProgress[T any](t *testing.T, ctx context.Context, ch <-chan T, label string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatalf("watchdog waiting for %s: %v", label, ctx.Err())
		var zero T
		return zero
	}
}

type deleteProgressObservation struct {
	left   int
	status string
}

func (o deleteProgressObservation) oracle() error {
	if o.left != deleteProgressTotal-purgeChunk || o.status != FunctionStatusDeleting {
		return fmt.Errorf("no inter-batch progress: remaining=%d status=%q", o.left, o.status)
	}
	return nil
}

func observeDeleteProgress(ctx context.Context, tx *sql.Tx, phase string) (deleteProgressObservation, error) {
	var o deleteProgressObservation
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+phase+" WHERE function_id = ?", deleteProgressID).Scan(&o.left); err != nil {
		return o, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT status FROM functions WHERE id = ?), '')", deleteProgressID).Scan(&o.status); err != nil {
		return o, err
	}
	for _, table := range executionChildTables {
		for _, query := range []string{
			"SELECT COUNT(*) FROM " + table + " c WHERE NOT EXISTS (SELECT 1 FROM executions e WHERE e.id=c.execution_id)",
			"SELECT COUNT(*) FROM executions e WHERE NOT EXISTS (SELECT 1 FROM " + table + " c WHERE c.execution_id=e.id)",
		} {
			var bad int
			if err := tx.QueryRowContext(ctx, query).Scan(&bad); err != nil {
				return o, err
			}
			if bad != 0 {
				return o, fmt.Errorf("%s: inconsistent execution children: %d", table, bad)
			}
		}
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO system_config(key,value) VALUES ('interbatch-progress',?)", fmt.Sprint(o.left))
	return o, err
}

func assertDeleteProgressPool(t *testing.T, f *deleteProgressFixture) {
	t.Helper()
	stats := f.db.write.Stats()
	if f.connector.opens.Load() != 1 || stats.OpenConnections != 1 || stats.InUse != 0 ||
		stats.MaxIdleClosed != 0 || stats.MaxIdleTimeClosed != 0 || stats.MaxLifetimeClosed != 0 {
		t.Fatalf("writer leaked/recycled: opens=%d stats=%+v", f.connector.opens.Load(), stats)
	}
}

func runDeleteProgress(t *testing.T, phase string, monolithic, cancelAfterCommit bool) {
	t.Helper()
	f := newDeleteProgressFixture(t, phase, 0)
	var observed deleteProgressObservation
	deleted := f.start(func() error {
		if monolithic {
			return monolithicDeleteProgress(f.deletion, f.db)
		}
		return f.db.DeleteFunction(f.deletion, deleteProgressID)
	})
	awaitDeleteProgress(t, f.cleanup, f.gate.held, "first delete barrier")
	before := f.db.write.Stats()
	if before.OpenConnections != 1 || before.InUse != 1 || before.WaitCount != 0 {
		t.Fatalf("expected sole occupied writer without waiters: %+v", before)
	}
	competed := f.start(func() error {
		conn, err := f.db.write.Conn(f.contender)
		if err != nil {
			return err
		}
		defer conn.Close()
		tx, err := conn.BeginTx(f.contender, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		observed, err = observeDeleteProgress(f.contender, tx, phase)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		// Cancel while still owning the connection, before another batch can start.
		if cancelAfterCommit {
			f.cancel()
		}
		return nil
	})
	// WaitCount registers the actual sole pool waiter, not just a runnable goroutine.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		queued := f.db.write.Stats()
		if queued.WaitCount != before.WaitCount {
			if queued.WaitCount-before.WaitCount != 1 || queued.OpenConnections != 1 || queued.InUse != 1 ||
				f.connector.opens.Load() != 1 {
				t.Fatalf("not exactly one waiter on the original occupied writer: %+v", queued)
			}
			break
		}
		select {
		case <-ticker.C:
		case <-f.cleanup.Done():
			t.Fatal("watchdog waiting for competitor queue registration")
		}
	}
	f.gate.open()
	if err := awaitDeleteProgress(t, f.cleanup, competed, "contender commit"); err != nil {
		t.Fatal(err)
	}
	err := awaitDeleteProgress(t, f.cleanup, deleted, "deletion")
	if cancelAfterCommit {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected deletion cancellation: %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	var marker string
	if err := f.db.read.QueryRowContext(f.cleanup, "SELECT value FROM system_config WHERE key='interbatch-progress'").Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if marker != fmt.Sprint(observed.left) {
		t.Fatalf("committed marker=%q observation=%+v", marker, observed)
	}
	if monolithic {
		if observed.left != 0 || observed.status != "" || observed.oracle() == nil {
			t.Fatalf("negative control did not reject monolithic committed state: %+v", observed)
		}
	} else if err := observed.oracle(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: committed remaining=%d status=%q marker=%s", phase, observed.left, observed.status, marker)
	assertDeleteProgressPool(t, f)
	assertExecutionsConsistent(t, f.db)
	if cancelAfterCommit {
		counts := fnRowCounts(t, f.db, deleteProgressID)
		if counts[phase] != deleteProgressTotal-purgeChunk {
			t.Fatalf("cancellation allowed another batch: %v", counts)
		}
		if status, exists := fnStatus(t, f.db, deleteProgressID); !exists || status != FunctionStatusDeleting {
			t.Fatalf("cancelled function lost deleting status: %q %v", status, exists)
		}
		if err := f.db.DeleteFunction(f.cleanup, deleteProgressID); err != nil {
			t.Fatal(err)
		}
	}
	for table, count := range fnRowCounts(t, f.db, deleteProgressID) {
		if count != 0 {
			t.Errorf("%s survived deletion: %d", table, count)
		}
	}
	assertDeleteProgressPool(t, f)
}

func TestDeleteFunctionCommittedWriterProgress(t *testing.T) {
	for _, phase := range []string{"executions", "kv_store", "jobs"} {
		t.Run(phase, func(t *testing.T) { runDeleteProgress(t, phase, false, false) })
	}
}

func TestDeleteFunctionProgressRejectsMonolithicControl(t *testing.T) {
	runDeleteProgress(t, "executions", true, false)
}

func TestDeleteFunctionProgressCancellationResumes(t *testing.T) {
	runDeleteProgress(t, "executions", false, true)
}

func TestDeleteFunctionProgressBatchRollbackResumes(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("batch_%d", failAt), func(t *testing.T) {
			f := newDeleteProgressFixture(t, "executions", failAt)
			f.gate.open()
			err := f.db.DeleteFunction(f.deletion, deleteProgressID)
			if !errors.Is(err, errDeleteProgressParent) || !f.gate.injected {
				t.Fatalf("expected failure after child deletes: %v", err)
			}
			want := deleteProgressTotal - (failAt-1)*purgeChunk
			for table, count := range fnRowCounts(t, f.db, deleteProgressID) {
				expected := 0
				switch table {
				case "functions", "deployments":
					expected = 1
				case "executions", "execution_logs", "execution_requests", "user_spans", "execution_log_entries":
					expected = want
				}
				if count != expected {
					t.Errorf("%s changed in failed batch: got %d want %d", table, count, expected)
				}
			}
			if status, exists := fnStatus(t, f.db, deleteProgressID); !exists || status != FunctionStatusDeleting {
				t.Fatalf("rollback lost deleting status: %q %v", status, exists)
			}
			assertExecutionsConsistent(t, f.db)
			assertDeleteProgressPool(t, f)
			if err := f.db.DeleteFunction(f.cleanup, deleteProgressID); err != nil {
				t.Fatal(err)
			}
			for table, count := range fnRowCounts(t, f.db, deleteProgressID) {
				if count != 0 {
					t.Errorf("%s survived resumed deletion: %d", table, count)
				}
			}
			assertDeleteProgressPool(t, f)
		})
	}
}

// Same unbounded transaction shape as the unchanged latency test's reference.
func monolithicDeleteProgress(ctx context.Context, db *Database) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		"DELETE FROM execution_logs WHERE execution_id IN (SELECT id FROM executions WHERE function_id=?)",
		"DELETE FROM execution_requests WHERE function_id=?",
		"DELETE FROM user_spans WHERE function_id=?",
		"DELETE FROM execution_log_entries WHERE function_id=?",
		"DELETE FROM functions WHERE id=?",
	} {
		if _, err := tx.ExecContext(ctx, query, deleteProgressID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
