package pool

import "testing"

func TestCgroupMemoryCapacityKeepsHeadroomAndPendingReservations(t *testing.T) {
	hm := &hostMemTracker{totalBytes: 1000, reservationPct: 0.8, cgroupConstrained: true}
	hm.availBytes.Store(700) // memory.current=300; 200 bytes remain reserved as host headroom
	hm.reserved.Store(100)
	if got := hm.availableForWorkers(); got != 400 {
		t.Fatalf("available=%d, want physical 500 minus pending reservations 100", got)
	}
	if !hm.reserve(400, 1000) {
		t.Fatal("exact remaining cgroup worker capacity was rejected")
	}
	if hm.reserve(1, 1) {
		t.Fatal("reservation exceeded effective cgroup capacity")
	}
}

func TestUnconstrainedMemoryFallsBackToMemAvailable(t *testing.T) {
	hm := &hostMemTracker{totalBytes: 1000, reservationPct: 0.8}
	hm.availBytes.Store(600)
	hm.reserved.Store(100)
	if got := hm.availableForWorkers(); got != 500 {
		t.Fatalf("available=%d, want MemAvailable 600 minus reservations 100", got)
	}
}

func TestCgroupLimitAndCPUQuotaParsing(t *testing.T) {
	if got, ok := parseCgroupMemoryLimit("536870912\n", 2<<30); !ok || got != 512<<20 {
		t.Fatalf("memory limit=%d/%v", got, ok)
	}
	if _, ok := parseCgroupMemoryLimit("max\n", 2<<30); ok {
		t.Fatal("unlimited memory reported as constrained")
	}
	if got := parseCPUQuota(8, "200000 100000\n"); got != 16 {
		t.Fatalf("two-core quota worker slots=%d, want 16", got)
	}
	if got := parseCPUQuota(4, "max 100000\n"); got != 32 {
		t.Fatalf("unlimited quota worker slots=%d, want 32", got)
	}
}
