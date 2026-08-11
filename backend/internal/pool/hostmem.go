package pool

import (
	"bufio"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// workerSlotsPerCPU permits I/O-bound sandboxes to overlap while bounding
// aggregate runnable processes. The demand controller's 70% target provides
// the per-pool operating headroom inside this global ceiling.
const workerSlotsPerCPU = 8

// hostMemTracker polls the current cgroup v2 memory usage at 1 Hz when Orva
// is constrained, falling back to /proc/meminfo only on an unconstrained
// host. It also tracks per-worker reservations so concurrent coordinators
// cannot over-admit while a new process is still starting.
type hostMemTracker struct {
	// Static — filled at construction.
	totalBytes        int64
	cgroupConstrained bool
	cgroupCurrentPath string
	cpuWorkers        int
	// Dynamic — refreshed by the poller goroutine.
	availBytes atomic.Int64 // MemAvailable from /proc/meminfo

	// Tracked reservations (bytes) — sum of (memory.max budget) across all
	// workers the scaler has promised to spawn. Updated by reserve/release.
	reserved atomic.Int64
	// CPU reservations are thousandths of a CPU. cpuWorkers is the internal
	// count of one-CPU worker slots after applying the cgroup quota.
	reservedCPU atomic.Int64
	capacityMu  sync.Mutex

	// reservationPct is the share of host RAM we let workers collectively
	// claim. 80% by default — leaves headroom for OS + Orva + SQLite.
	reservationPct float64

	stop chan struct{}
	once sync.Once
}

// newHostMemTracker discovers the current cgroup v2 hard limit and launches
// the poller. /proc/meminfo is used only when memory.max is absent, unlimited,
// or no tighter than the physical host.
func newHostMemTracker(reservationPct float64) (*hostMemTracker, error) {
	t := &hostMemTracker{
		reservationPct: reservationPct,
		stop:           make(chan struct{}),
	}
	hostTotal, _ := readMeminfo("MemTotal")
	limit, constrained := readCgroupMemoryLimit(hostTotal)
	if constrained {
		t.totalBytes = limit
		t.cgroupConstrained = true
		t.cgroupCurrentPath = "/sys/fs/cgroup/memory.current"
	} else {
		t.totalBytes = hostTotal
	}
	t.cpuWorkers = effectiveCPUWorkers()
	if err := t.refresh(); err != nil {
		return nil, err
	}
	total := t.totalBytes
	if total <= 0 {
		return nil, errors.New("MemTotal is 0 — /proc/meminfo unavailable?")
	}
	t.totalBytes = total
	go t.run()
	return t, nil
}

func (t *hostMemTracker) run() {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-tk.C:
			_ = t.refresh()
		case <-t.stop:
			return
		}
	}
}

func (t *hostMemTracker) close() {
	t.once.Do(func() {
		if t.stop != nil {
			close(t.stop)
		}
	})
}

func (t *hostMemTracker) refresh() error {
	if t.cgroupConstrained {
		current, err := readIntFile(t.cgroupCurrentPath)
		if err != nil {
			return err
		}
		avail := t.totalBytes - current
		if avail < 0 {
			avail = 0
		}
		t.availBytes.Store(avail)
		return nil
	}
	avail, err := readMeminfo("MemAvailable")
	if err != nil {
		return err
	}
	t.availBytes.Store(avail)
	return nil
}

// availableForWorkers returns how many bytes the scaler may still claim
// across all pools combined, net of outstanding reservations.
func (t *hostMemTracker) availableForWorkers() int64 {
	total := t.totalBytes
	if total <= 0 {
		return 0
	}
	// Two independent gates protect the declared/observed reservation budget
	// and live physical headroom. Reservations are subtracted from both so a
	// worker that is still spawning cannot be admitted twice before its RSS is
	// visible in memory.current.
	budget := int64(float64(total) * t.reservationPct)
	reserved := t.reserved.Load()
	logical := budget - reserved
	physical := t.availBytes.Load()
	if t.cgroupConstrained {
		physical -= total - budget
	}
	physical -= reserved
	out := logical
	if physical < out {
		out = physical
	}
	if out < 0 {
		out = 0
	}
	return out
}

func (t *hostMemTracker) effectiveCPUWorkers() int {
	if t == nil || t.cpuWorkers < 1 {
		return 1
	}
	return t.cpuWorkers
}

func (t *hostMemTracker) availableCPUUnits() int64 {
	available := int64(t.effectiveCPUWorkers())*1000 - t.reservedCPU.Load()
	if available < 0 {
		return 0
	}
	return available
}

// reserve tries to claim `bytes` of RAM. Returns true on success. The
// scaler calls this before spawning; if false, scale-up is denied this tick.
func (t *hostMemTracker) reserve(bytes, cpuUnits int64) bool {
	t.capacityMu.Lock()
	defer t.capacityMu.Unlock()
	if bytes > 0 && t.availableForWorkers() < bytes {
		return false
	}
	if cpuUnits > 0 && t.availableCPUUnits() < cpuUnits {
		return false
	}
	if bytes > 0 {
		t.reserved.Add(bytes)
	}
	if cpuUnits > 0 {
		t.reservedCPU.Add(cpuUnits)
	}
	return true
}

// release returns a worker's exact memory and CPU reservation.
func (t *hostMemTracker) release(bytes, cpuUnits int64) {
	t.capacityMu.Lock()
	defer t.capacityMu.Unlock()
	if bytes > 0 {
		next := t.reserved.Load() - bytes
		if next < 0 {
			next = 0
		}
		t.reserved.Store(next)
	}
	if cpuUnits > 0 {
		next := t.reservedCPU.Load() - cpuUnits
		if next < 0 {
			next = 0
		}
		t.reservedCPU.Store(next)
	}
}

// Stats for metrics.
func (t *hostMemTracker) stats() (total, avail, reserved int64) {
	return t.totalBytes, t.availBytes.Load(), t.reserved.Load()
}

// readMeminfo parses a named line from /proc/meminfo, returning bytes.
func readMeminfo(key string) (int64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if !strings.HasPrefix(line, key+":") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, errors.New("malformed meminfo line: " + line)
		}
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		// /proc/meminfo reports in kB unless it's "(bytes)".
		return v * 1024, nil
	}
	return 0, errors.New(key + " not found in /proc/meminfo")
}

func readIntFile(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

func readCgroupMemoryLimit(hostTotal int64) (int64, bool) {
	b, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	if err != nil {
		return 0, false
	}
	return parseCgroupMemoryLimit(string(b), hostTotal)
}

func parseCgroupMemoryLimit(value string, hostTotal int64) (int64, bool) {
	raw := strings.TrimSpace(value)
	if raw == "max" {
		return 0, false
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 || (hostTotal > 0 && limit >= hostTotal) {
		return 0, false
	}
	return limit, true
}

func effectiveCPUWorkers() int {
	return parseCPUQuota(runtime.NumCPU(), readCPUQuota())
}

func readCPUQuota() string {
	if b, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		return string(b)
	}
	return "max 100000"
}

func parseCPUQuota(hostCPUs int, value string) int {
	cores := float64(hostCPUs)
	fields := strings.Fields(value)
	if len(fields) == 2 && fields[0] != "max" {
		quota, qerr := strconv.ParseFloat(fields[0], 64)
		period, perr := strconv.ParseFloat(fields[1], 64)
		if qerr == nil && perr == nil && quota > 0 && period > 0 {
			limited := quota / period
			if limited < cores {
				cores = limited
			}
		}
	}
	workers := int(cores * workerSlotsPerCPU)
	if workers < 1 {
		workers = 1
	}
	return workers
}
