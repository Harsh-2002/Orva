package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const durationRingSize = 8192

// Metrics holds atomic counters and duration tracking for the platform.
type Metrics struct {
	TotalInvocations atomic.Int64
	ColdStarts       atomic.Int64
	WarmHits         atomic.Int64
	TotalBuilds      atomic.Int64
	BuildErrors      atomic.Int64
	ActiveRequests   atomic.Int64

	mu    sync.Mutex
	ring  [durationRingSize]time.Duration
	idx   int  // next write index
	count int  // total entries (capped at ring size)
}

// New creates a new Metrics instance.
func New() *Metrics {
	return &Metrics{}
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

// RecordDuration writes a duration into the fixed-size ring buffer.
// Old samples overwrite once the ring is full — percentile reports
// reflect the most recent ~8k invocations.
func (m *Metrics) RecordDuration(d time.Duration) {
	m.mu.Lock()
	m.ring[m.idx] = d
	m.idx = (m.idx + 1) % durationRingSize
	if m.count < durationRingSize {
		m.count++
	}
	m.mu.Unlock()
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
