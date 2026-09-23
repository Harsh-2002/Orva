package database

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkTelemetryInsertShape compares one prepared step per row with a
// multi-row statement. It models the best-effort activity lane without
// changing its durability or indexes, so a batching change can be justified
// by evidence rather than by a larger queue hiding drops.
func BenchmarkTelemetryInsertShape(b *testing.B) {
	const rows = 50
	const tuple = "(?, ?, ?, ?)"
	const one = "INSERT INTO perf_activity (ts, path, status, request_id) VALUES " + tuple
	many := "INSERT INTO perf_activity (ts, path, status, request_id) VALUES " +
		strings.TrimSuffix(strings.Repeat(tuple+",", rows), ",")
	args := make([]any, 0, rows*4)
	for i := range rows {
		args = append(args, int64(i), "/fn/example", 200, fmt.Sprintf("req-%d", i))
	}
	for _, tc := range []struct {
		name string
		run  func(*Database, []any) error
	}{
		{"prepared-rows", func(db *Database, values []any) error {
			tx, err := db.write.Begin()
			if err != nil {
				return err
			}
			stmt, err := tx.Prepare(one)
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			for i := 0; i < len(values); i += 4 {
				if _, err := stmt.Exec(values[i : i+4]...); err != nil {
					_ = stmt.Close()
					_ = tx.Rollback()
					return err
				}
			}
			_ = stmt.Close()
			return tx.Commit()
		}},
		{"multi-row", func(db *Database, values []any) error {
			tx, err := db.write.Begin()
			if err != nil {
				return err
			}
			if _, err := tx.Exec(many, values...); err != nil {
				_ = tx.Rollback()
				return err
			}
			return tx.Commit()
		}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			db, err := New(b.TempDir() + "/bench.db")
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			if _, err := db.write.Exec(`CREATE TABLE perf_activity (id INTEGER PRIMARY KEY, ts INTEGER, path TEXT, status INTEGER, request_id TEXT); CREATE INDEX perf_activity_ts ON perf_activity(ts)`); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for range b.N {
				if err := tc.run(db, args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
