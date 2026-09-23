package database

import (
	"path/filepath"
	"testing"
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
