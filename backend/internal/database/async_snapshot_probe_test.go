package database

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/trace"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// snapshotOrderedTraceID isolates trace-index locality from schema, SQL, and
// execution-ID changes. The rightmost 80 bits remain cryptographically random.
func snapshotOrderedTraceID(t *testing.T, at time.Time) string {
	t.Helper()
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(at.UnixMilli())<<16)
	if _, err := rand.Read(raw[6:]); err != nil {
		t.Fatal(err)
	}
	return "tr_" + hex.EncodeToString(raw[:])
}

func TestSnapshotOrderedTraceIDFormat(t *testing.T) {
	before := snapshotOrderedTraceID(t, time.UnixMilli(1_800_000_000_000))
	after := snapshotOrderedTraceID(t, time.UnixMilli(1_800_000_000_001))
	if len(before) != 35 || !strings.HasPrefix(before, "tr_") || len(after) != 35 || before >= after {
		t.Fatalf("ordered trace IDs are not 32-hex time-prefixed values: %q %q", before, after)
	}
	if _, err := hex.DecodeString(before[3:]); err != nil {
		t.Fatal(err)
	}
	if before == snapshotOrderedTraceID(t, time.UnixMilli(1_800_000_000_000)) {
		t.Fatal("same-millisecond trace IDs collided")
	}
}

// snapshotProbePaths accepts only the two database copies created by the
// explicit-scratch Python harness. A typo must not benchmark-write or DROP an
// operator's actual /var/lib/orva/orva.db.
func snapshotProbePaths(baseline, candidate string) (string, string, error) {
	if baseline == "" || candidate == "" || !filepath.IsAbs(baseline) || !filepath.IsAbs(candidate) {
		return "", "", fmt.Errorf("both snapshot paths must be absolute")
	}
	var err error
	baseline, err = filepath.EvalSymlinks(baseline)
	if err != nil {
		return "", "", err
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", err
	}
	if filepath.Base(baseline) != "baseline.db" || filepath.Base(candidate) != "candidate.db" ||
		filepath.Dir(baseline) != filepath.Dir(candidate) ||
		!strings.HasPrefix(filepath.Base(filepath.Dir(baseline)), "orva-index-ab-") {
		return "", "", fmt.Errorf("paths must be baseline.db and candidate.db in one orva-index-ab-* directory")
	}
	return baseline, candidate, nil
}

func TestSnapshotProbePathSafety(t *testing.T) {
	dir := t.TempDir()
	scratch := filepath.Join(dir, "orva-index-ab-123")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	base, cand := filepath.Join(scratch, "baseline.db"), filepath.Join(scratch, "candidate.db")
	for _, path := range []string{base, cand} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := snapshotProbePaths(base, cand); err != nil {
		t.Fatalf("valid temporary copies rejected: %v", err)
	}
	for _, paths := range [][2]string{
		{"/var/lib/orva/orva.db", cand},
		{base, base},
		{base, "/var/lib/orva/orva.db"},
		{"relative.db", cand},
	} {
		if _, _, err := snapshotProbePaths(paths[0], paths[1]); err == nil {
			t.Errorf("unsafe snapshot paths accepted: %q, %q", paths[0], paths[1])
		}
	}
}

type snapshotProbeSample struct {
	WallMS         float64 `json:"wall_ms"`
	StatementMS    float64 `json:"statement_ms"`
	CommitMS       float64 `json:"commit_ms"`
	ConnectionMS   float64 `json:"connection_ms"`
	DiskReadBytes  uint64  `json:"disk_read_bytes"`
	DiskWriteBytes uint64  `json:"disk_write_bytes"`
}

func snapshotProbeProcessIO() (readBytes, writeBytes uint64) {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil { // non-Linux test builds do not expose this diagnostic
		return 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "read_bytes:":
			readBytes, _ = strconv.ParseUint(fields[1], 10, 64)
		case "write_bytes:":
			writeBytes, _ = strconv.ParseUint(fields[1], 10, 64)
		}
	}
	return readBytes, writeBytes
}

type snapshotProbeVariant struct {
	Samples      []snapshotProbeSample `json:"samples"`
	MedianWallMS float64               `json:"median_wall_ms"`
}

// TestExecutionWriterSnapshotProbe is an opt-in local diagnostic. Build it
// with `go test -c` and let sqlite_index_ab.py supply disposable copies;
// ordinary CI compiles it but never writes a real instance database.
func TestExecutionWriterSnapshotProbe(t *testing.T) {
	baselinePath := os.Getenv("ORVA_BENCH_BASELINE_DB")
	candidatePath := os.Getenv("ORVA_BENCH_CANDIDATE_DB")
	if baselinePath == "" && candidatePath == "" {
		t.Skip("same-snapshot writer probe requires explicit scratch copies")
	}
	if os.Getenv("ORVA_BENCH_SCRATCH") != "1" {
		t.Fatal("refusing snapshot writes without ORVA_BENCH_SCRATCH=1")
	}
	var err error
	baselinePath, candidatePath, err = snapshotProbePaths(baselinePath, candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	batches, err := strconv.Atoi(os.Getenv("ORVA_BENCH_BATCHES"))
	if err != nil || batches < 1 || batches > 100 {
		t.Fatal("ORVA_BENCH_BATCHES must be an integer from 1 to 100")
	}
	batchSize, err := strconv.Atoi(os.Getenv("ORVA_BENCH_BATCH_SIZE"))
	if err != nil || batchSize < 1 || batchSize > 200 {
		t.Fatal("ORVA_BENCH_BATCH_SIZE must be an integer from 1 to 200")
	}
	orderedTraceMode := os.Getenv("ORVA_BENCH_CANDIDATE_ORDERED_TRACE")
	if orderedTraceMode != "" && orderedTraceMode != "1" {
		t.Fatal("ORVA_BENCH_CANDIDATE_ORDERED_TRACE must be 1 when set")
	}
	paths := []string{baselinePath, candidatePath}
	var dbs [2]*Database
	var writers [2]*asyncWriter
	var sourceRows [2]int64
	for i, path := range paths {
		dbs[i], err = New(path)
		if err != nil {
			t.Fatal(err)
		}
		defer dbs[i].Close()
		// Do not call Migrate: it would recreate the deliberately omitted
		// candidate indexes and invalidate the comparison.
		if err := dbs[i].read.QueryRow("SELECT COUNT(*) FROM executions").Scan(&sourceRows[i]); err != nil {
			t.Fatal(err)
		}
		writers[i] = newAsyncWriter(dbs[i])
	}
	if cacheText := os.Getenv("ORVA_BENCH_CANDIDATE_CACHE_KIB"); cacheText != "" {
		cacheKiB, parseErr := strconv.Atoi(cacheText)
		if parseErr != nil || cacheKiB < 1024 || cacheKiB > 524288 {
			t.Fatal("ORVA_BENCH_CANDIDATE_CACHE_KIB must be 1024..524288")
		}
		if _, err := dbs[1].write.Exec(fmt.Sprintf("PRAGMA cache_size = -%d", cacheKiB)); err != nil {
			t.Fatal(err)
		}
	}
	if sourceRows[0] != sourceRows[1] {
		t.Fatalf("copies are not the same snapshot: %d versus %d rows", sourceRows[0], sourceRows[1])
	}
	var functionID string
	if err := dbs[0].read.QueryRow("SELECT id FROM functions LIMIT 1").Scan(&functionID); err != nil {
		t.Fatal(err)
	}

	variants := [2]snapshotProbeVariant{
		{Samples: make([]snapshotProbeSample, 0, batches)},
		{Samples: make([]snapshotProbeSample, 0, batches)},
	}
	for iteration := range batches {
		startedAt := time.Now().UTC()
		idsForBatch := make([]string, batchSize)
		spansForBatch := make([]string, batchSize)
		tracesForBatch := make([]string, batchSize)
		for row := range idsForBatch {
			idsForBatch[row] = ids.New()
			spansForBatch[row] = trace.NewSpanID()
			tracesForBatch[row] = trace.NewTraceID()
		}
		order := [2]int{0, 1}
		if iteration%2 == 1 {
			order = [2]int{1, 0}
		}
		for _, variant := range order {
			batch := make([]writeJob, batchSize)
			for row := range batch {
				traceID := tracesForBatch[row]
				if variant == 1 && orderedTraceMode == "1" {
					traceID = snapshotOrderedTraceID(t, time.Now())
				}
				args := []any{idsForBatch[row], functionID, "success", 0, "benchmark-worker",
					int64(10), 200, "", 12, startedAt, traceID,
					spansForBatch[row], nil, "http", nil, false, nil}
				batch[row] = writeJob{sql: finalExecutionInsertSQL, args: args,
					functionID: functionID, bulkInsert: true}
			}
			before := &writers[variant].timing[writeCritical]
			statementBefore, commitBefore := before.statementNS.Load(), before.commitNS.Load()
			connectionBefore := before.connectionNS.Load()
			readBefore, writeBefore := snapshotProbeProcessIO()
			start := time.Now()
			if retry := writers[variant].commit(batch, writeCritical); len(retry) != 0 {
				t.Fatalf("variant %d requested retry for %d jobs", variant, len(retry))
			}
			wall := time.Since(start)
			readAfter, writeAfter := snapshotProbeProcessIO()
			variants[variant].Samples = append(variants[variant].Samples, snapshotProbeSample{
				WallMS:         float64(wall.Nanoseconds()) / 1e6,
				StatementMS:    float64(before.statementNS.Load()-statementBefore) / 1e6,
				CommitMS:       float64(before.commitNS.Load()-commitBefore) / 1e6,
				ConnectionMS:   float64(before.connectionNS.Load()-connectionBefore) / 1e6,
				DiskReadBytes:  readAfter - readBefore,
				DiskWriteBytes: writeAfter - writeBefore,
			})
		}
	}
	for i := range variants {
		walls := make([]float64, len(variants[i].Samples))
		for j, sample := range variants[i].Samples {
			walls[j] = sample.WallMS
		}
		sort.Float64s(walls)
		variants[i].MedianWallMS = (walls[(len(walls)-1)/2] + walls[len(walls)/2]) / 2
		var afterRows int64
		if err := dbs[i].read.QueryRow("SELECT COUNT(*) FROM executions").Scan(&afterRows); err != nil {
			t.Fatal(err)
		}
		if want := sourceRows[i] + int64(batches*batchSize); afterRows != want {
			t.Fatalf("variant %d committed %d rows, want %d", i, afterRows, want)
		}
	}
	result := struct {
		SourceRows            int64                `json:"source_rows"`
		BatchSize             int                  `json:"batch_size"`
		Batches               int                  `json:"batches"`
		CandidateOrderedTrace bool                 `json:"candidate_ordered_trace"`
		Baseline              snapshotProbeVariant `json:"baseline"`
		Candidate             snapshotProbeVariant `json:"candidate"`
	}{sourceRows[0], batchSize, batches, orderedTraceMode == "1", variants[0], variants[1]}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SNAPSHOT_AB_JSON=%s\n", payload)
}
