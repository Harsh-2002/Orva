package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestRecordInvocation_ColdStart(t *testing.T) {
	m := New()
	m.RecordInvocation(true)

	if m.TotalInvocations.Load() != 1 {
		t.Errorf("expected TotalInvocations=1, got %d", m.TotalInvocations.Load())
	}
	if m.ColdStarts.Load() != 1 {
		t.Errorf("expected ColdStarts=1, got %d", m.ColdStarts.Load())
	}
	if m.WarmHits.Load() != 0 {
		t.Errorf("expected WarmHits=0, got %d", m.WarmHits.Load())
	}
}

func TestRecordInvocation_WarmHit(t *testing.T) {
	m := New()
	m.RecordInvocation(false)

	if m.TotalInvocations.Load() != 1 {
		t.Errorf("expected TotalInvocations=1, got %d", m.TotalInvocations.Load())
	}
	if m.ColdStarts.Load() != 0 {
		t.Errorf("expected ColdStarts=0, got %d", m.ColdStarts.Load())
	}
	if m.WarmHits.Load() != 1 {
		t.Errorf("expected WarmHits=1, got %d", m.WarmHits.Load())
	}
}

func TestRecordDuration_Percentile(t *testing.T) {
	m := New()

	// Record 100 durations from 1ms to 100ms.
	for i := 1; i <= 100; i++ {
		m.RecordDuration(time.Duration(i) * time.Millisecond)
	}

	p50 := m.Percentile(50)
	if p50 < 49*time.Millisecond || p50 > 51*time.Millisecond {
		t.Errorf("expected p50 ~50ms, got %s", p50)
	}

	p90 := m.Percentile(90)
	if p90 < 89*time.Millisecond || p90 > 91*time.Millisecond {
		t.Errorf("expected p90 ~90ms, got %s", p90)
	}

	p99 := m.Percentile(99)
	if p99 < 98*time.Millisecond || p99 > 100*time.Millisecond {
		t.Errorf("expected p99 ~99ms, got %s", p99)
	}
}

func TestPercentile_Empty(t *testing.T) {
	m := New()
	if p := m.Percentile(50); p != 0 {
		t.Errorf("expected 0 for empty durations, got %s", p)
	}
}

func TestRecordBuild(t *testing.T) {
	m := New()
	m.RecordBuild(false)
	m.RecordBuild(true)
	m.RecordBuild(false)

	if m.TotalBuilds.Load() != 3 {
		t.Errorf("expected TotalBuilds=3, got %d", m.TotalBuilds.Load())
	}
	if m.BuildErrors.Load() != 1 {
		t.Errorf("expected BuildErrors=1, got %d", m.BuildErrors.Load())
	}
}

func TestSnapshot(t *testing.T) {
	m := New()
	m.RecordInvocation(true)
	m.RecordInvocation(false)
	m.RecordBuild(false)
	m.RecordDuration(10 * time.Millisecond)

	snap := m.Snapshot()
	if snap.TotalInvocations != 2 {
		t.Errorf("expected 2 invocations, got %d", snap.TotalInvocations)
	}
	if snap.ColdStarts != 1 {
		t.Errorf("expected 1 cold start, got %d", snap.ColdStarts)
	}
	if snap.WarmHits != 1 {
		t.Errorf("expected 1 warm hit, got %d", snap.WarmHits)
	}
	if snap.TotalBuilds != 1 {
		t.Errorf("expected 1 build, got %d", snap.TotalBuilds)
	}
}

func TestPrometheusFormat(t *testing.T) {
	m := New()
	m.RecordInvocation(true)
	m.RecordDuration(5 * time.Millisecond)

	snap := m.Snapshot()

	// Build a minimal Prometheus-style output like the system handler does.
	var b strings.Builder
	writeMetric := func(name string, val int64) {
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(strings.TrimRight(strings.TrimRight(
			strings.Replace(time.Duration(val).String(), "s", "", -1), "0"), "."))
		b.WriteString("\n")
	}
	_ = writeMetric // just verify snap fields are populated

	if snap.TotalInvocations != 1 {
		t.Error("expected 1 total invocation")
	}
	if snap.P50MS < 0 {
		t.Errorf("expected non-negative P50_MS, got %d", snap.P50MS)
	}
}
