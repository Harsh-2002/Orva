package sandbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrTooManyRequests is returned by TryAcquire when the host-wide
// concurrency cap is full and the wait would exceed the caller's budget.
// Distinct from a function-level timeout; this is a saturation signal
// the proxy maps to HTTP 429.
var ErrTooManyRequests = errors.New("host concurrency cap reached")

// Limiter caps the number of concurrent sandbox executions to prevent
// host resource exhaustion.
type Limiter struct {
	sem    chan struct{}
	active atomic.Int64
	total  atomic.Int64
	mu     sync.Mutex
}

// NewLimiter creates a limiter allowing at most maxConcurrent simultaneous
// sandbox processes.
func NewLimiter(maxConcurrent int) *Limiter {
	if maxConcurrent <= 0 {
		maxConcurrent = 100
	}
	return &Limiter{
		sem: make(chan struct{}, maxConcurrent),
	}
}

// Acquire blocks until a slot is available or ctx is cancelled.
func (l *Limiter) Acquire(ctx context.Context) error {
	select {
	case l.sem <- struct{}{}:
		l.active.Add(1)
		l.total.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire is a budget-bounded Acquire. If a slot is free immediately
// or within `maxWait`, it behaves like Acquire. If neither, it returns
// ErrTooManyRequests so the proxy can surface a fast 429 instead of
// hanging the request until ctx fires. ctx still wins if it fires first.
func (l *Limiter) TryAcquire(ctx context.Context, maxWait time.Duration) error {
	if maxWait <= 0 {
		return l.Acquire(ctx)
	}
	t := time.NewTimer(maxWait)
	defer t.Stop()
	select {
	case l.sem <- struct{}{}:
		l.active.Add(1)
		l.total.Add(1)
		return nil
	case <-t.C:
		return ErrTooManyRequests
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees one slot.
func (l *Limiter) Release() {
	l.active.Add(-1)
	<-l.sem
}

// Stats returns current active count and lifetime total.
func (l *Limiter) Stats() (active, total int64) {
	return l.active.Load(), l.total.Load()
}
