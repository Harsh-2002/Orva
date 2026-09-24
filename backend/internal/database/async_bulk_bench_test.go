package database

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkExecutionWriterInsert compares the same 200-row workload with
// individual Exec calls versus one grouped VALUES statement. It isolates the
// SQLite write path from pool, sandbox, HTTP and VM scheduling noise.
func BenchmarkExecutionWriterInsert(b *testing.B) {
	for _, tc := range []struct {
		name string
		bulk bool
	}{
		{"per_row", false},
		{"grouped", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			db, err := New(filepath.Join(b.TempDir(), "writer-bench.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			if err := db.Migrate(); err != nil {
				b.Fatal(err)
			}
			if _, err := db.write.Exec(`CREATE TABLE writer_bench (payload TEXT NOT NULL)`); err != nil {
				b.Fatal(err)
			}
			const statement = `INSERT INTO writer_bench (payload) VALUES (?)`
			batch := make([]writeJob, 200)
			for i := range batch {
				batch[i] = writeJob{sql: statement, args: []any{"x"}, bulkInsert: tc.bulk}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if retry := db.writer.commit(batch, writeCritical); len(retry) != 0 {
					b.Fatalf("writer unexpectedly requested retry: %d rows", len(retry))
				}
			}
		})
	}
}

// BenchmarkExecutionWriterFullSchema measures the real final-execution row
// shape, foreign key, and production indexes. The smaller benchmark above is
// useful for SQL construction overhead but cannot represent persistence cost.
func BenchmarkExecutionWriterFullSchema(b *testing.B) {
	for _, tc := range []struct {
		name string
		bulk bool
	}{
		{"per_row", false},
		{"grouped", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			db, err := New(filepath.Join(b.TempDir(), "writer-full-bench.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			if err := db.Migrate(); err != nil {
				b.Fatal(err)
			}
			if _, err := db.write.Exec(`INSERT INTO functions (id, name, runtime, entrypoint)
				VALUES ('bench-fn', 'bench-fn', 'node', 'handler.js')`); err != nil {
				b.Fatal(err)
			}
			const statement = `INSERT INTO executions (
				id, function_id, status, cold_start, container_id,
				duration_ms, status_code, error_message, response_size,
				started_at, finished_at, trace_id, span_id, parent_span_id,
				trigger, parent_function_id, is_outlier, baseline_p95_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?)`
			const rowsPerBatch = 200
			batch := make([]writeJob, rowsPerBatch)
			startedAt := time.Now().UTC()
			for i := range batch {
				batch[i] = writeJob{sql: statement, bulkInsert: tc.bulk, args: []any{
					"", "bench-fn", "success", 0, "worker-1", int64(3), 200,
					"", 12, startedAt, "trace", "span", nil, "http", nil, false, nil,
				}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := range b.N {
				b.StopTimer()
				for i := range batch {
					batch[i].args[0] = fmt.Sprintf("%d-%d", iteration, i)
				}
				b.StartTimer()
				if retry := db.writer.commit(batch, writeCritical); len(retry) != 0 {
					b.Fatalf("writer unexpectedly requested retry: %d rows", len(retry))
				}
			}
		})
	}
}
