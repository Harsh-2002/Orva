package pool

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// hostMemTracker polls /proc/meminfo at 1 Hz and exposes the free-memory
// budget to the autoscaler. It also tracks per-function reservations so
// we can check "can this pool grow" without racing.
type hostMemTracker struct {
	// Static — filled at construction.
	totalBytes int64
	// Dynamic — refreshed by the poller goroutine.
	availBytes atomic.Int64 // MemAvailable from /proc/meminfo

	// Tracked reservations (bytes) — sum of (memory.max budget) across all
	// workers the scaler has promised to spawn. Updated by reserve/release.
	reserved atomic.Int64

	// reservationPct is the share of host RAM we let workers collectively
	// claim. 80% by default — leaves headroom for OS + Orva + SQLite.
	reservationPct float64

	stop chan struct{}
	once sync.Once
}

// newHostMemTracker reads total RAM and launches the poller. Safe to call
// even in containers — it reads /proc/meminfo which reflects the cgroup
// memory limit on modern kernels (when `memory.max` is set on the cgroup).
func newHostMemTracker(reservationPct float64) (*hostMemTracker, error) {
	t := &hostMemTracker{
		reservationPct: reservationPct,
		stop:           make(chan struct{}),
	}
	if err := t.refresh(); err != nil {
		return nil, err
	}
	total, _ := readMeminfo("MemTotal")
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
	t.once.Do(func() { close(t.stop) })
}

func (t *hostMemTracker) refresh() error {
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
	// Reserve reservationPct of total for workers; the rest stays for OS,
	// Orva's own heap, SQLite page cache.
	budget := int64(float64(total) * t.reservationPct)
	avail := t.availBytes.Load()
	// Don't let budget exceed what's physically available right now.
	if avail < budget {
		budget = avail
	}
	out := budget - t.reserved.Load()
	if out < 0 {
		out = 0
	}
	return out
}

// reserve tries to claim `bytes` of RAM. Returns true on success. The
// scaler calls this before spawning; if false, scale-up is denied this tick.
func (t *hostMemTracker) reserve(bytes int64) bool {
	if bytes <= 0 {
		return true
	}
	for {
		cur := t.reserved.Load()
		avail := t.availableForWorkers()
		if avail < bytes {
			return false
		}
		if t.reserved.CompareAndSwap(cur, cur+bytes) {
			return true
		}
	}
}

// release returns `bytes` to the budget when a worker dies.
func (t *hostMemTracker) release(bytes int64) {
	if bytes <= 0 {
		return
	}
	for {
		cur := t.reserved.Load()
		next := cur - bytes
		if next < 0 {
			next = 0
		}
		if t.reserved.CompareAndSwap(cur, next) {
			return
		}
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
