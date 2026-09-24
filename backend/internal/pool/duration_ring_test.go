package pool

import (
	"sync"
	"testing"
	"time"
)

func TestDurationRingRetainsNewestWindow(t *testing.T) {
	var ring durationRing
	for i := range 1024 {
		ring.add(time.Duration(i)*time.Millisecond, 512)
	}
	if len(ring.samples) != 512 {
		t.Fatalf("sample count = %d, want 512", len(ring.samples))
	}
	seen := make(map[time.Duration]bool, 512)
	for _, sample := range ring.samples {
		if sample < 512*time.Millisecond || sample >= 1024*time.Millisecond || seen[sample] {
			t.Fatalf("ring retained stale or duplicate sample: %s", sample)
		}
		seen[sample] = true
	}
	p := &functionPool{serviceSamples: ring}
	if got := p.snapshotDemand(time.Now()).ServiceP95; got != 998*time.Millisecond {
		t.Fatalf("recent p95 = %s, want 998ms", got)
	}
	ring.add(-time.Second, 512)
	if ring.samples[0] != 0 {
		t.Fatalf("negative sample was not clamped: %s", ring.samples[0])
	}
}

func TestDurationRingRecordingDoesNotAllocateAfterWarmup(t *testing.T) {
	p := &functionPool{}
	for range 512 {
		p.recordLatency(time.Millisecond)
	}
	if allocs := testing.AllocsPerRun(1000, func() { p.recordLatency(time.Millisecond) }); allocs != 0 {
		t.Fatalf("full ring recording allocated %.2f times per invocation", allocs)
	}
}

func TestSnapshotDemandCanRunAlongsideRecorders(t *testing.T) {
	p := &functionPool{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for i := range 1000 {
				p.recordLatency(time.Duration(i) * time.Microsecond)
				p.recordQueueWait(time.Duration(i) * time.Microsecond)
				p.recordSpawn(time.Duration(i) * time.Microsecond)
			}
		})
	}
	for range 100 {
		_ = p.snapshotDemand(time.Now())
	}
	wg.Wait()
	if len(p.serviceSamples.samples) != 512 || len(p.queueWaitSamples.samples) != 512 || len(p.spawnSamples.samples) != 256 {
		t.Fatalf("sample windows not bounded: service=%d queue=%d spawn=%d",
			len(p.serviceSamples.samples), len(p.queueWaitSamples.samples), len(p.spawnSamples.samples))
	}
}

func BenchmarkRecordLatencyFullRing(b *testing.B) {
	p := &functionPool{}
	for range 512 {
		p.recordLatency(time.Millisecond)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.recordLatency(time.Millisecond)
	}
}
