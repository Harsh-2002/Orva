package pool

import (
	"math"
	"sync"
	"syscall"
)

// A waiting HTTP request retains its body, a string copy, a JSON frame and a
// replay-capture copy. io.ReadAll can temporarily retain an older backing
// array too. Charge six copies before reading it, with a floor for the
// connection, headers and goroutine. This is daemon memory, separate from the
// hostMem worker reservations (which deliberately use a different 80% share).
const (
	requestBaseCharge = int64(64 << 10)
	requestBodyCopies = int64(6)
)

type requestBudget struct {
	mu         sync.Mutex
	limit      int64
	used       int64
	byFunction map[string]int64
	hostMem    *hostMemTracker
}

func newRequestBudget(hm *hostMemTracker) *requestBudget {
	if hm == nil || hm.totalBytes <= 0 {
		return nil
	}
	return &requestBudget{
		limit:      hm.totalBytes / 10, // half of the daemon's non-worker 20% share
		byFunction: make(map[string]int64),
		hostMem:    hm,
	}
}

// queueLimits scale with the actual memory and file-descriptor envelope, not
// with a fixed number of clients. One function may use three quarters of the
// pending budget; the rest stays available when another function arrives.
func (b *requestBudget) queueLimits() (global, perFunction int64) {
	if b == nil {
		return 1024, 256 // degraded resource discovery: retain safe old bounds
	}
	global = b.limit / requestBaseCharge
	var files syscall.Rlimit
	if syscall.Getrlimit(syscall.RLIMIT_NOFILE, &files) == nil && files.Cur > 0 {
		fdLimit := int64(files.Cur / 4) // reserve descriptors for workers and control plane
		if fdLimit < global {
			global = fdLimit
		}
	}
	if global < 1 {
		global = 1
	}
	perFunction = global - global/4
	if perFunction < 1 {
		perFunction = 1
	}
	return global, perFunction
}

func requestCharge(contentLength, maxBodyBytes int64) int64 {
	if maxBodyBytes <= 0 {
		maxBodyBytes = 6 << 20
	}
	if contentLength < 0 || contentLength > maxBodyBytes {
		contentLength = maxBodyBytes
	}
	if contentLength > (math.MaxInt64-requestBaseCharge)/requestBodyCopies {
		return math.MaxInt64
	}
	return requestBaseCharge + contentLength*requestBodyCopies
}

// reserve holds daemon heap capacity across body read, admission and dispatch.
// It is non-blocking so overload cannot itself create an unbounded waiter set.
func (b *requestBudget) reserve(fnID string, charge int64) (func(), bool) {
	if b == nil {
		return func() {}, true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if charge <= 0 || charge > b.limit-b.used {
		return nil, false
	}
	perFunctionLimit := b.limit - b.limit/4
	if charge > perFunctionLimit-b.byFunction[fnID] {
		return nil, false
	}
	// Observe current physical/cgroup pressure as well as the static daemon
	// allocation. Keep another 10% of total memory free for the OS, SQLite,
	// builders and management endpoints; never wait for an OOM to shed work.
	if b.hostMem.availBytes.Load()-b.used-charge < b.hostMem.totalBytes/10 {
		return nil, false
	}
	b.used += charge
	b.byFunction[fnID] += charge
	return func() {
		b.mu.Lock()
		b.used -= charge
		b.byFunction[fnID] -= charge
		if b.byFunction[fnID] == 0 {
			delete(b.byFunction, fnID)
		}
		b.mu.Unlock()
	}, true
}

// ReserveIngress rejects a request before reading its body when the daemon
// cannot safely hold it alongside queued and active requests. The caller must
// call the returned release exactly once, after the frame is no longer used.
func (m *Manager) ReserveIngress(fnID string, contentLength, maxBodyBytes int64) (func(), error) {
	if m.closing.Load() {
		return nil, ErrManagerClosed
	}
	release, ok := m.requestBudget.reserve(fnID, requestCharge(contentLength, maxBodyBytes))
	if !ok {
		return nil, ErrInvocationQueueFull
	}
	return release, nil
}
