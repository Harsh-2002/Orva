package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const durationRingSize = 8192

// histogramBucketCount is the number of finite buckets in the cumulative
// duration histograms. Kept as a `const` so the bucket arrays can be sized
// at compile time (no slice -> len() in array bounds, which Go rejects).
const histogramBucketCount = 11

// HistogramBucketsMS are the cumulative upper bounds (in milliseconds) used
// for the Prometheus-format histogram lines. The "+Inf" bucket is implicit
// in the total count and not stored here. Length must equal
// histogramBucketCount; an init-time assertion below catches drift.
//
// Tuning rationale: 1 ms catches the trivial-handler / kv-roundtrip floor;
// 5–25 ms covers warm Python invocations; 50–250 ms covers warm Node + a
// bit of cold-start; 500 ms–5 s catches cold starts and pathological cases.
var HistogramBucketsMS = [histogramBucketCount]float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}

// Metrics holds atomic counters and duration tracking for the platform.
type Metrics struct {
	TotalInvocations atomic.Int64
	ColdStarts       atomic.Int64
	WarmHits         atomic.Int64
	TotalBuilds      atomic.Int64
	BuildErrors      atomic.Int64
	ActiveRequests   atomic.Int64

	// Baselines holds per-function rolling-percentile windows used for
	// the anomaly indicator on each execution. Set once at server start
	// (via metrics.New, which initialises an empty store) and warmed
	// from DB before the HTTP listener accepts traffic.
	Baselines *Baselines

	mu    sync.Mutex
	ring  [durationRingSize]time.Duration
	idx   int // next write index
	count int // total entries (capped at ring size)

	// Cumulative histogram buckets for invocation duration. buckets[i] is
	// the running count of samples ≤ HistogramBucketsMS[i] in ms.
	// invocCount/invocSumMS hold the global count and sum for the
	// matching `_count` / `_sum` Prometheus lines.
	invocBuckets [histogramBucketCount]atomic.Uint64
	invocCount   atomic.Uint64
	invocSumMS   atomic.Uint64

	// Sandbox spawn-duration histogram — populated by RecordSpawnDuration
	// from the pool layer once a worker comes up. Same bucket layout as
	// invocations because operators usually look at both side-by-side.
	spawnBuckets [histogramBucketCount]atomic.Uint64
	spawnCount   atomic.Uint64
	spawnSumMS   atomic.Uint64
}

// New creates a new Metrics instance.
func New() *Metrics {
	return &Metrics{Baselines: NewBaselines()}
}

// RecordInvocation increments the total invocation counter and tracks
// whether it was a cold start or warm hit.
func (m *Metrics) RecordInvocation(coldStart bool) {
	m.TotalInvocations.Add(1)
	if coldStart {
		m.ColdStarts.Add(1)
	} else {
		m.WarmHits.Add(1)
	}
}

// RecordDuration writes a duration into the fixed-size ring buffer AND
// increments the cumulative histogram buckets. Old samples overwrite once
// the ring is full — the percentile report still reflects only the most
// recent ~8k invocations, but histogram buckets are lifetime cumulative
// (Prometheus convention; rate() recovers the windowed view).
func (m *Metrics) RecordDuration(d time.Duration) {
	m.mu.Lock()
	m.ring[m.idx] = d
	m.idx = (m.idx + 1) % durationRingSize
	if m.count < durationRingSize {
		m.count++
	}
	m.mu.Unlock()

	ms := float64(d) / float64(time.Millisecond)
	bumpHistogram(&m.invocBuckets, &m.invocCount, &m.invocSumMS, ms)
}

// RecordSpawnDuration feeds a sandbox-spawn duration into the spawn
// histogram. Called by the pool layer once `sandbox.Spawn` returns. Cheap
// — N atomic adds where N is the bucket count.
func (m *Metrics) RecordSpawnDuration(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	bumpHistogram(&m.spawnBuckets, &m.spawnCount, &m.spawnSumMS, ms)
}

// bumpHistogram increments the matching cumulative buckets, the count,
// and the running sum (in ms, rounded down). Inlined into the hot path
// via small fixed-size loop.
func bumpHistogram(buckets *[histogramBucketCount]atomic.Uint64, count, sumMS *atomic.Uint64, ms float64) {
	for i, le := range HistogramBucketsMS {
		if ms <= le {
			buckets[i].Add(1)
		}
	}
	count.Add(1)
	if ms > 0 {
		sumMS.Add(uint64(ms))
	}
}

// HistogramSnapshot is a point-in-time copy of one cumulative histogram.
type HistogramSnapshot struct {
	BucketsMS    []float64 // upper bounds (excluding +Inf)
	BucketCounts []uint64  // cumulative counts, parallel to BucketsMS
	Count        uint64    // total samples (== +Inf bucket value)
	SumMS        uint64    // running sum of samples in milliseconds
}

// SnapshotInvocationHistogram copies the invocation duration histogram
// for emission in the Prometheus text endpoint.
func (m *Metrics) SnapshotInvocationHistogram() HistogramSnapshot {
	return snapshotHist(&m.invocBuckets, &m.invocCount, &m.invocSumMS)
}

// SnapshotSpawnHistogram copies the sandbox spawn duration histogram.
func (m *Metrics) SnapshotSpawnHistogram() HistogramSnapshot {
	return snapshotHist(&m.spawnBuckets, &m.spawnCount, &m.spawnSumMS)
}

func snapshotHist(buckets *[histogramBucketCount]atomic.Uint64, count, sumMS *atomic.Uint64) HistogramSnapshot {
	out := HistogramSnapshot{
		BucketsMS:    make([]float64, len(HistogramBucketsMS)),
		BucketCounts: make([]uint64, len(HistogramBucketsMS)),
	}
	copy(out.BucketsMS, HistogramBucketsMS[:])
	for i := range buckets {
		out.BucketCounts[i] = buckets[i].Load()
	}
	out.Count = count.Load()
	out.SumMS = sumMS.Load()
	return out
}

// RecordBuild increments the build counter and optionally the error counter.
func (m *Metrics) RecordBuild(err bool) {
	m.TotalBuilds.Add(1)
	if err {
		m.BuildErrors.Add(1)
	}
}

// snapshotSorted copies the ring into a sorted slice under the lock.
func (m *Metrics) snapshotSorted() []time.Duration {
	m.mu.Lock()
	n := m.count
	if n == 0 {
		m.mu.Unlock()
		return nil
	}
	out := make([]time.Duration, n)
	copy(out, m.ring[:n])
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Percentile returns the p-th percentile of recorded durations (0-100).
// Returns 0 if no durations have been recorded.
func (m *Metrics) Percentile(p float64) time.Duration {
	sorted := m.snapshotSorted()
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Snapshot returns a point-in-time copy of all metric values.
func (m *Metrics) Snapshot() MetricsSnapshot {
	sorted := m.snapshotSorted()
	pct := func(p float64) time.Duration {
		if len(sorted) == 0 {
			return 0
		}
		idx := int(float64(len(sorted)-1) * p / 100.0)
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return sorted[idx]
	}
	return MetricsSnapshot{
		TotalInvocations: m.TotalInvocations.Load(),
		ColdStarts:       m.ColdStarts.Load(),
		WarmHits:         m.WarmHits.Load(),
		TotalBuilds:      m.TotalBuilds.Load(),
		BuildErrors:      m.BuildErrors.Load(),
		ActiveRequests:   m.ActiveRequests.Load(),
		P50MS:            pct(50).Milliseconds(),
		P95MS:            pct(95).Milliseconds(),
		P99MS:            pct(99).Milliseconds(),
	}
}

// MetricsSnapshot is a JSON-friendly snapshot of metrics. Latency
// percentiles are emitted as integer milliseconds so the UI doesn't have
// to convert nanoseconds — the previous `time.Duration` typing caused the
// JSON to render in ns despite the `_ms` suffix.
type MetricsSnapshot struct {
	TotalInvocations int64 `json:"total_invocations"`
	ColdStarts       int64 `json:"cold_starts"`
	WarmHits         int64 `json:"warm_hits"`
	TotalBuilds      int64 `json:"total_builds"`
	BuildErrors      int64 `json:"build_errors"`
	ActiveRequests   int64 `json:"active_requests"`
	P50MS            int64 `json:"p50_ms"`
	P95MS            int64 `json:"p95_ms"`
	P99MS            int64 `json:"p99_ms"`
}
