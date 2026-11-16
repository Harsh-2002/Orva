package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type dispatchCancelWriter struct {
	io.WriteCloser
	writes atomic.Int32
	closes atomic.Int32
}

func (w *dispatchCancelWriter) Write(p []byte) (int, error) {
	w.writes.Add(1)
	return w.WriteCloser.Write(p)
}

func (w *dispatchCancelWriter) Close() error {
	w.closes.Add(1)
	return w.WriteCloser.Close()
}

// Signal after sampling Err so cancellation can deterministically follow the
// entry check while the dispatch is still excluded by the worker mutex.
type dispatchCancelContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *dispatchCancelContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func assertDispatchCancellation(t *testing.T, w *Worker, writer *dispatchCancelWriter, transitions *atomic.Int32, original *dispatchState, res *DispatchResult, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || res == nil || res.w != w {
		t.Fatalf("dispatch = %+v, %v; want nonnil result and %v", res, err, want)
	}
	if w.IsDead() || !w.ready.Load() || w.Served.Load() != 0 || w.disp.Load() != original {
		t.Fatal("cancelled attempt changed healthy worker state or another lease")
	}
	if transitions.Load() != 0 || writer.writes.Load() != 0 || writer.closes.Load() != 0 {
		t.Fatalf("cancelled attempt: transitions=%d writes=%d closes=%d", transitions.Load(), writer.writes.Load(), writer.closes.Load())
	}
	if original != nil && original.ctx.Err() != nil {
		t.Fatal("cancelled attempt revoked another dispatch's context")
	}
	if got := res.Stderr(); len(got) != 0 {
		t.Fatalf("cancelled attempt included earlier stderr: %q", got)
	}
}

func assertDispatchReusable(t *testing.T, h *workerHarness, sdk bool) {
	t.Helper()
	ctx, _ := testCtx(t)
	done := make(chan error, 1)
	go func() {
		res, err := h.w.DispatchEx(ctx, []byte(`{}`))
		if err == nil && (res == nil || res.StatusCode != 200 || res.Body != "reused") {
			err = fmt.Errorf("unexpected reuse result: %+v", res)
		}
		done <- err
	}()
	n := 1
	if sdk {
		n++
	}
	types, lease := h.adapterFrames(t, n)
	if types[n-1] != "request" || (sdk && (types[0] != "lease" || lease == "")) {
		t.Fatalf("reuse protocol: %v, lease %q", types, lease)
	}
	h.reply(t, `{"type":"response","statusCode":200,"body":"reused"}`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("reuse dispatch did not finish")
	}
	if h.w.IsDead() || h.w.Served.Load() != 1 || h.w.disp.Load() != nil {
		t.Fatal("reuse did not complete normally")
	}
}

func TestDispatchAlreadyCancelled(t *testing.T) {
	for _, sdk := range []bool{false, true} {
		for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
			t.Run(fmt.Sprintf("sdk=%v/%v", sdk, want), func(t *testing.T) {
				h := newWorkerHarness(t, sdk)
				h.w.ready.Store(true)
				writer := &dispatchCancelWriter{WriteCloser: h.w.stdin}
				h.w.stdin = writer
				var transitions atomic.Int32
				h.w.workloadTransition = func(change func()) { transitions.Add(1); change() }
				_, _ = h.w.errBuf.Write([]byte("earlier dispatch"))
				var ctx context.Context
				var cancel context.CancelFunc
				if want == context.Canceled {
					ctx, cancel = context.WithCancel(context.Background())
					cancel()
				} else {
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				}
				defer cancel()
				res, err := h.w.DispatchEx(ctx, []byte(`{}`))
				assertDispatchCancellation(t, h.w, writer, &transitions, nil, res, err, want)
				if _, stderr, err := h.w.Dispatch(ctx, []byte(`{}`)); !errors.Is(err, want) || len(stderr) != 0 {
					t.Fatalf("Dispatch wrapper = %q, %v", stderr, err)
				}
				assertDispatchCancellation(t, h.w, writer, &transitions, nil, res, err, want)
				assertDispatchReusable(t, h, sdk)
			})
		}
	}
}

func TestDispatchCancelledWhileWaitingForMutex(t *testing.T) {
	h := newWorkerHarness(t, true)
	h.w.ready.Store(true)
	writer := &dispatchCancelWriter{WriteCloser: h.w.stdin}
	h.w.stdin = writer
	h.w.beginLease(context.Background())
	original := h.w.disp.Load()
	var transitions atomic.Int32
	h.w.workloadTransition = func(change func()) { transitions.Add(1); change() }
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &dispatchCancelContext{Context: base, checked: make(chan struct{})}
	type outcome struct {
		res *DispatchResult
		err error
	}
	done := make(chan outcome, 1)
	h.w.mu.Lock()
	go func() {
		res, err := h.w.DispatchEx(ctx, []byte(`{}`))
		done <- outcome{res, err}
	}()
	select {
	case <-ctx.checked:
	case <-time.After(5 * time.Second):
		h.w.mu.Unlock()
		t.Fatal("dispatch did not reach entry check")
	}
	cancel()
	if h.w.disp.Load() != original || original.ctx.Err() != nil || transitions.Load() != 0 {
		h.w.mu.Unlock()
		t.Fatal("queued cancellation touched the active lease before owning the mutex")
	}
	h.w.mu.Unlock()
	select {
	case got := <-done:
		assertDispatchCancellation(t, h.w, writer, &transitions, original, got.res, got.err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled dispatch did not return after mutex release")
	}
	h.w.revokeLease()
	assertDispatchReusable(t, h, true)
}

func TestDispatchCancelledDeadWorker(t *testing.T) {
	w := &Worker{errBuf: newRingBuffer(1024)}
	w.dead.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := w.DispatchEx(ctx, []byte(`{}`))
	if res == nil || !errors.Is(err, ErrWorkerExited) {
		t.Fatalf("dead worker = %+v, %v; want ErrWorkerExited", res, err)
	}
}
