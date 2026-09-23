package pool

import (
	"testing"
	"time"
)

func TestArrivalBucketsBoundMemoryAndExpireBothWindows(t *testing.T) {
	p := &functionPool{}
	base := time.Unix(1_000_000_000, 0)
	for i := 0; i < 100_000; i++ {
		p.recordArrival(base)
	}
	if len(p.arrivals) != 60 {
		t.Fatalf("arrival storage grew to %d buckets", len(p.arrivals))
	}
	current := p.snapshotDemand(base)
	if current.StableRate != 100_000/60.0 || current.BurstRate != 100_000/6.0 {
		t.Fatalf("initial rates = stable %.2f, burst %.2f", current.StableRate, current.BurstRate)
	}
	burstExpired := p.snapshotDemand(base.Add(6 * time.Second))
	if burstExpired.StableRate != current.StableRate || burstExpired.BurstRate != 0 {
		t.Fatalf("6s rates = stable %.2f, burst %.2f", burstExpired.StableRate, burstExpired.BurstRate)
	}
	stableExpired := p.snapshotDemand(base.Add(60 * time.Second))
	if stableExpired.StableRate != 0 || stableExpired.BurstRate != 0 {
		t.Fatalf("60s rates = stable %.2f, burst %.2f", stableExpired.StableRate, stableExpired.BurstRate)
	}
}

func TestArrivalBucketReuseDoesNotCarryStaleCount(t *testing.T) {
	p := &functionPool{}
	base := time.Unix(1_000_000_000, 0)
	p.recordArrival(base)
	p.recordArrival(base.Add(60 * time.Second))
	got := p.snapshotDemand(base.Add(60 * time.Second))
	if got.StableRate != 1.0/60.0 || got.BurstRate != 1.0/6.0 {
		t.Fatalf("reused bucket rates = stable %.3f, burst %.3f", got.StableRate, got.BurstRate)
	}
}
