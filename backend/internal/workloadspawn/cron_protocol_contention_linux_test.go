//go:build linux

package workloadspawn_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
	"modernc.org/sqlite"
)

func TestCronProtocolFixtureWaitsForAsyncWriter(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	name := "cron_protocol_hold_" + strings.ReplaceAll(ids.New(), "-", "")
	liveMust(t, sqlite.RegisterScalarFunction(name, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		close(entered)
		select {
		case <-release:
			return "held", nil
		case <-t.Context().Done():
			return nil, t.Context().Err()
		}
	}))
	db, err := database.New(filepath.Join(t.TempDir(), "protocol.db"))
	liveMust(t, err)
	defer db.Close()
	defer unblock()
	liveMust(t, db.Migrate())
	db.AsyncExecActivity("INSERT INTO system_config(key,value) VALUES('cron-protocol-writer'," + name + "())")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual async writer did not reach held SQL statement")
	}

	bare, err := sql.Open("sqlite", db.Path())
	liveMust(t, err)
	defer bare.Close()
	_, err = bare.ExecContext(t.Context(), "INSERT INTO system_config(key,value) VALUES('bare-contender','blocked')")
	var busy *sqlite.Error
	if !errors.As(err, &busy) || busy.Code() != 5 {
		t.Fatal("bare fixture did not reproduce SQLITE_BUSY against the actual async writer")
	}
	t.Log("bare DSN reproduces SQLITE_BUSY while actual async writer holds SQLite's write lock")

	fixture := openCronProtocolSQL(t, db)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connections := make([]*sql.Conn, 0, 2)
	for range 2 {
		conn, err := fixture.Conn(ctx)
		liveMust(t, err)
		connections = append(connections, conn)
		defer conn.Close()
		var timeout int
		liveMust(t, conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout))
		if timeout != 10000 {
			t.Fatalf("fixture connection busy_timeout=%d, want canonical 10000", timeout)
		}
	}
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := connections[0].ExecContext(ctx, "INSERT INTO system_config(key,value) VALUES('fixture-contender','committed')")
		done <- err
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("contender returned before writer release: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-done:
		liveMust(t, err)
	case <-ctx.Done():
		t.Fatal("configured fixture failed to finish after writer release")
	}
	var value string
	liveMust(t, connections[1].QueryRowContext(ctx, "SELECT value FROM system_config WHERE key='fixture-contender'").Scan(&value))
	if value != "committed" {
		t.Fatal("contender did not persist its own row")
	}
	t.Log("both pooled connections have bounded 10000ms busy timeout; contending write committed after async-writer release")
}
