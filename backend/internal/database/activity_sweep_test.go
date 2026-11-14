package database

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func newSweepDB(t *testing.T) *Database {
	t.Helper()
	db, err := New(filepath.Join(t.TempDir(), "orva.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedActivity inserts rows in one transaction. ts(i) lets a test create ties.
func seedActivity(t *testing.T, db *Database, n int, ts func(i int) int64) {
	t.Helper()
	tx, err := db.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO activity_log (ts, source, method, path, status) VALUES (?, 'api', 'GET', '/x', 200)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := stmt.Exec(ts(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func activityIDs(t *testing.T, db *Database) []int64 {
	t.Helper()
	rows, err := db.read.Query(`SELECT id FROM activity_log ORDER BY ts DESC, id DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// The cap must keep exactly the newest maxRows rows by (ts DESC, id DESC), as
// the single-statement version did, including when rows tie on ts at the cut.
func TestSweepActivityKeepsTheNewestRowsAcrossChunksAndTies(t *testing.T) {
	db := newSweepDB(t)
	const total, keep = 3500, 1200 // several chunks, and a tie group straddling the cut
	now := time.Now().UnixMilli()
	seedActivity(t, db, total, func(i int) int64 { return now + int64(i/7) }) // 7 rows share each ts
	before := activityIDs(t, db)
	if len(before) != total {
		t.Fatalf("seeded %d rows, want %d", len(before), total)
	}
	if err := db.SetSystemConfig("activity_retention_max_rows", "1200"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSystemConfig("activity_retention_days", "0"); err != nil {
		t.Fatal(err)
	}

	deleted, err := db.SweepActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != total-keep {
		t.Fatalf("deleted = %d, want %d", deleted, total-keep)
	}
	got := activityIDs(t, db)
	want := before[:keep] // newest first
	if len(got) != len(want) {
		t.Fatalf("kept %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kept set differs at %d: got %d want %d", i, got[i], want[i])
		}
	}
	if again, err := db.SweepActivity(context.Background()); err != nil || again != 0 {
		t.Fatalf("second sweep deleted %d (err %v), want 0", again, err)
	}
}

func TestSweepActivityRetentionWindowDeletesOnlyOldRows(t *testing.T) {
	db := newSweepDB(t)
	now := time.Now()
	old := now.Add(-10 * 24 * time.Hour).UnixMilli()
	seedActivity(t, db, 2500, func(int) int64 { return old })
	seedActivity(t, db, 40, func(i int) int64 { return now.UnixMilli() + int64(i) })
	if err := db.SetSystemConfig("activity_retention_max_rows", "0"); err != nil {
		t.Fatal(err)
	}
	deleted, err := db.SweepActivity(context.Background()) // default window is 7 days
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2500 || len(activityIDs(t, db)) != 40 {
		t.Fatalf("deleted %d, left %d; want 2500 and 40", deleted, len(activityIDs(t, db)))
	}
}

// The reason for chunking: a sweep must not keep the writer's only connection
// for long. Probe the connection while a large sweep runs.
func TestSweepActivityDoesNotMonopoliseTheWriter(t *testing.T) {
	db := newSweepDB(t)
	now := time.Now().UnixMilli()
	seedActivity(t, db, 15000, func(i int) int64 { return now + int64(i) })
	if err := db.SetSystemConfig("activity_retention_max_rows", "100"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSystemConfig("activity_retention_days", "0"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	var deleted int64
	go func() {
		var err error
		deleted, err = db.SweepActivity(context.Background())
		done <- err
	}()
	var waits []time.Duration
probe:
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			break probe
		default:
			start := time.Now()
			if err := db.SetSystemConfig("probe", "x"); err != nil { // needs the writer connection
				t.Fatal(err)
			}
			waits = append(waits, time.Since(start))
			time.Sleep(2 * time.Millisecond)
		}
	}
	if deleted != 15000-100 {
		t.Fatalf("deleted = %d, want %d", deleted, 15000-100)
	}
	if len(waits) < 5 {
		t.Fatalf("only %d probes landed during the sweep; it did not yield the writer", len(waits))
	}
	sort.Slice(waits, func(i, j int) bool { return waits[i] < waits[j] })
	// The old single statement admitted 1-2 probes and held one for the whole
	// delete. Bounds are loose enough for -race on a slow runner.
	if median := waits[len(waits)/2]; median > 250*time.Millisecond {
		t.Fatalf("median write wait %s during the sweep", median)
	}
	if worst := waits[len(waits)-1]; worst > time.Second {
		t.Fatalf("a write waited %s for the connection during the sweep; chunks must keep holds short", worst)
	}
}

// Both retention rules apply in one sweep, and a cancelled context stops it.
func TestSweepActivityAppliesWindowAndCapTogetherAndHonoursCancel(t *testing.T) {
	db := newSweepDB(t)
	now := time.Now()
	seedActivity(t, db, 1500, func(int) int64 { return now.Add(-9 * 24 * time.Hour).UnixMilli() }) // past the window
	seedActivity(t, db, 3000, func(i int) int64 { return now.UnixMilli() + int64(i) })             // recent, over the cap
	if err := db.SetSystemConfig("activity_retention_max_rows", "1000"); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.SweepActivity(cancelled); err == nil {
		t.Fatal("a cancelled context must stop the sweep")
	}
	if got := len(activityIDs(t, db)); got != 4500 {
		t.Fatalf("a cancelled sweep left %d rows, want 4500 untouched", got)
	}
	deleted, err := db.SweepActivity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3500 || len(activityIDs(t, db)) != 1000 {
		t.Fatalf("deleted %d, left %d; want 3500 and 1000", deleted, len(activityIDs(t, db)))
	}
}

// The chunk query must stay an index range search; a scan from the newest end
// would make every chunk cost the whole table.
func TestSweepActivityChunkQueryUsesTheTimestampIndex(t *testing.T) {
	db := newSweepDB(t)
	rows, err := db.read.Query(`EXPLAIN QUERY PLAN SELECT id FROM activity_log WHERE ts < ? OR (ts = ? AND id <= ?) LIMIT 1000`, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, "idx_activity_ts") || strings.Contains(joined, "SCAN") {
		t.Fatalf("chunk query plan is not an index range search: %s", joined)
	}
}
