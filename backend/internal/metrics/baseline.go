package metrics

import (
	"sort"
	"sync"
	"time"
)

// baselineSamples is the rolling window of per-function durations used to
// compute P95 / P99 baselines. 100 samples is the smallest size that gives
// a stable percentile while keeping memory tiny: 100 × 8 bytes = 800 B per
// function. 1000 functions = 800 KB total. We could grow it, but the
// statistical noise at small N matters less than reactivity — a function
// whose latency profile shifts should ride the new baseline within a few
// minutes of traffic, not hours.
const baselineSamples = 100

// baselineMinForOutlier is the minimum sample count required before we
// flag any execution as an outlier. Below this, the P95 is too noisy to
// trust — a single slow run early in a function's life shouldn't fire
// alarms.
const baselineMinForOutlier = 20

// outlierMultiplier is the threshold over the rolling P95 above which we
// flag an execution as anomalous. 2× is conservative: P95 already excludes
// the slowest 5%, so 2× P95 is roughly the P99-ish region for a normal
// distribution, the natural region where "this is unusual" lives.
const outlierMultiplier = 2.0

// ringBuf is a fixed-size FIFO of float64 samples (durations in ms).
// Concurrency-safe via the embedded mutex; we never expose internal
// state outside the package.
type ringBuf struct {
	mu      sync.Mutex
	samples [baselineSamples]float64
	count   int   // number of valid samples (≤ baselineSamples)
	idx     int   // next write position
	total   int64 // lifetime number of records (for diagnostics; never wraps before exa-counts)

	// Cached P95. Recomputed lazily on read; invalidated on every record.
	cachedP95     float64
	cachedP95Ok   bool
	cachedP99     float64
	cachedMean    float64
	cachedSampleN int
}

func (r *ringBuf) record(durationMS float64) {
	r.mu.Lock()
	r.samples[r.idx] = durationMS
	r.idx = (r.idx + 1) % baselineSamples
	if r.count < baselineSamples {
		r.count++
	}
	r.total++
	r.cachedP95Ok = false
	r.mu.Unlock()
}

func (r *ringBuf) snapshot() (p95, p99, mean float64, sampleN int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cachedP95Ok {
		return r.cachedP95, r.cachedP99, r.cachedMean, r.cachedSampleN
	}
	if r.count == 0 {
		return 0, 0, 0, 0
	}
	cp := make([]float64, r.count)
	copy(cp, r.samples[:r.count])
	sort.Float64s(cp)

	p95 = cp[percentileIndex(r.count, 0.95)]
	p99 = cp[percentileIndex(r.count, 0.99)]

	var sum float64
	for _, v := range cp {
		sum += v
	}
	mean = sum / float64(r.count)

	r.cachedP95, r.cachedP99, r.cachedMean = p95, p99, mean
	r.cachedSampleN = r.count
	r.cachedP95Ok = true
	return p95, p99, mean, r.count
}

func percentileIndex(n int, p float64) int {
	idx := int(float64(n-1) * p)
	if idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

// Baselines tracks per-function rolling-percentile baselines for the
// outlier detector. Use NewBaselines once at server start (after DB
// migration) and stash the singleton on the metrics or server struct.
//
// The store is sync.Map-backed so per-function records never contend with
// reads of unrelated functions. Concurrent records into the same function
// serialize on the per-buffer mutex — that's a 100-element memcpy + sort
// at most, well under a microsecond on any modern CPU.
type Baselines struct {
	bufs sync.Map // map[string]*ringBuf
}

// NewBaselines returns an empty store. Callers wishing to warm it from
// historical data should follow up with WarmFromDB at startup.
func NewBaselines() *Baselines {
	return &Baselines{}
}

func (b *Baselines) bufFor(fnID string) *ringBuf {
	if v, ok := b.bufs.Load(fnID); ok {
		return v.(*ringBuf)
	}
	rb := &ringBuf{}
	actual, _ := b.bufs.LoadOrStore(fnID, rb)
	return actual.(*ringBuf)
}

// Record registers a successful (non-cold-start, non-error) execution's
// duration against this function's baseline. Cold starts are noisy and
// would skew the baseline upward, so callers should skip them. Errors
// are skipped for the same reason — a failing function rejecting traffic
// in 1ms shouldn't drag the P95 down.
func (b *Baselines) Record(fnID string, durationMS int64) {
	if fnID == "" {
		return
	}
	b.bufFor(fnID).record(float64(durationMS))
}

// Snapshot returns the current rolling percentiles for the function.
// sampleN < baselineSamples means the buffer hasn't filled yet.
func (b *Baselines) Snapshot(fnID string) (p95, p99, mean float64, sampleN int) {
	if v, ok := b.bufs.Load(fnID); ok {
		return v.(*ringBuf).snapshot()
	}
	return 0, 0, 0, 0
}

// Classify decides whether an execution is an outlier given its duration.
// Returns (isOutlier, baselineP95) where baselineP95 is the P95 against
// which the comparison was made (0 when the baseline isn't ready yet).
//
// Classification rule (kept deliberately simple — see plan):
//   - sampleN < baselineMinForOutlier → never an outlier (warming up)
//   - duration > P95 × outlierMultiplier → outlier
//   - else → not an outlier
//
// Cold starts and errors are excluded by the caller, not here, so this
// helper stays a pure function of (fn, duration).
func (b *Baselines) Classify(fnID string, durationMS int64) (isOutlier bool, baselineP95MS int64) {
	p95, _, _, n := b.Snapshot(fnID)
	if n < baselineMinForOutlier {
		return false, int64(p95)
	}
	threshold := p95 * outlierMultiplier
	return float64(durationMS) > threshold, int64(p95)
}

// outlierUpdater is the narrow database surface FinalizeExecution needs
// to back-write the outlier flag. The metrics package depends only on
// this interface — no circular imports with database.
type outlierUpdater interface {
	UpdateOutlier(execID string, isOutlier bool, baselineP95MS int64)
}

// FinalizeExecution is the canonical post-finalize hook. It feeds the
// rolling baseline (only on warm successes), classifies the duration
// against the current P95, and back-writes is_outlier + baseline_p95_ms
// onto the execution row asynchronously.
//
// Callers invoke this immediately after AsyncInsertExecutionFinal so the
// outlier flag follows the row on the same next batch flush. status
// values "success" / "error" match what the execution row stores.
func (b *Baselines) FinalizeExecution(db outlierUpdater, execID, fnID, status string, coldStart bool, durationMS int64) {
	if b == nil || db == nil || execID == "" || fnID == "" {
		return
	}
	// Only feed warm successes into the baseline. Cold starts and errors
	// would skew the P95 enough to drown out genuine signal.
	if status == "success" && !coldStart {
		b.Record(fnID, durationMS)
	}
	// Classify against the (now-updated) baseline. We always back-write
	// the baseline_p95_ms (even when not flagged) so the UI can render
	// "took 80ms · baseline 60ms" without re-querying.
	isOutlier, p95 := b.Classify(fnID, durationMS)
	if p95 > 0 || isOutlier {
		db.UpdateOutlier(execID, isOutlier, p95)
	}
}

// BaselineSummary is the read-model returned by GET /api/v1/functions/{id}/baseline.
type BaselineSummary struct {
	FunctionID    string  `json:"function_id"`
	P95MS         int64   `json:"p95_ms"`
	P99MS         int64   `json:"p99_ms"`
	MeanMS        int64   `json:"mean_ms"`
	SampleCount   int     `json:"sample_count"`
	WindowSize    int     `json:"window_size"`
	LastUpdatedAt int64   `json:"last_updated_at"` // unix millis; zero when buffer is empty
}

// WarmEntry is a single (function_id, duration_ms) pair used to seed the
// rolling buffers at startup. Caller fills these from a DB query — see
// database.ListBaselineSeed.
type WarmEntry struct {
	FunctionID string
	DurationMS int64
}

// Warm populates the per-function ring buffers from historical
// executions so outlier detection works on the first invocation after
// restart, rather than waiting for the buffer to fill from live traffic.
//
// Order matters: entries should be passed oldest → newest so the FIFO
// ring ends up with the most recent samples (the caller pulls in
// started_at DESC and reverses, OR passes them in DESC; either yields
// the same final state because the ring is FIFO and we'll just overwrite
// older positions with newer ones).
func (b *Baselines) Warm(entries []WarmEntry) {
	for _, e := range entries {
		if e.FunctionID == "" {
			continue
		}
		b.bufFor(e.FunctionID).record(float64(e.DurationMS))
	}
}

// Summary returns the read-model for the given function. Useful both for
// the API endpoint and for embedding inline in execution responses.
func (b *Baselines) Summary(fnID string) BaselineSummary {
	p95, p99, mean, n := b.Snapshot(fnID)
	out := BaselineSummary{
		FunctionID:  fnID,
		P95MS:       int64(p95),
		P99MS:       int64(p99),
		MeanMS:      int64(mean),
		SampleCount: n,
		WindowSize:  baselineSamples,
	}
	if n > 0 {
		out.LastUpdatedAt = time.Now().UnixMilli()
	}
	return out
}
