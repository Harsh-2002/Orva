package pool

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestNestedCgroupAncestorCapacity(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "system.slice")
	leaf := filepath.Join(parent, "orva.service")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "memory.max"), "max\n")
	write(filepath.Join(parent, "memory.max"), "1073741824\n")
	write(filepath.Join(parent, "memory.current"), "1006632960\n")
	write(filepath.Join(leaf, "memory.max"), "536870912\n")
	write(filepath.Join(leaf, "memory.current"), "104857600\n")
	write(filepath.Join(parent, "cpu.max"), "100000 100000\n")
	write(filepath.Join(leaf, "cpu.max"), "max 100000\n")
	write(filepath.Join(leaf, "cpuset.cpus.effective"), "0-1\n")
	dirs := cgroupDirs(root, "/system.slice/orva.service")
	if len(dirs) != 3 || dirs[0] != leaf || dirs[2] != root {
		t.Fatalf("cgroup ancestry = %v", dirs)
	}
	if got, ok := cgroupMemoryCapacity(dirs, 4<<30); !ok || got != 512<<20 {
		t.Fatalf("capacity = %d, constrained=%v", got, ok)
	}
	if got, ok := cgroupMemoryAvailable(dirs, 512<<20); !ok || got != 64<<20 {
		t.Fatalf("available = %d, constrained=%v", got, ok)
	}
	if got := cgroupCPUWorkers(dirs, 8); got != 8 {
		t.Fatalf("CPU worker slots = %d, want eight from ancestor quota", got)
	}
	write(filepath.Join(parent, "cpu.max"), "max 100000\n")
	if got := cgroupCPUWorkers(dirs, 8); got != 16 {
		t.Fatalf("CPU worker slots = %d, want 16 from leaf cpuset", got)
	}
}

func TestParseCPUSet(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int
		ok    bool
	}{
		{"0-3,6,8-9\n", 7, true},
		{"0-2,2-3", 4, true},
		{"", 0, false},
		{"3-1", 0, false},
		{"1-999999", 0, false},
	} {
		got, ok := parseCPUSet(tc.input)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseCPUSet(%q) = %d, %v; want %d, %v", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}
