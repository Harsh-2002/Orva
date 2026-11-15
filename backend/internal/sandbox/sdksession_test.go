package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recState struct {
	mu      sync.Mutex
	revoked []string
	closed  int
}

func (r *recState) LeaseRevoked(l string) {
	r.mu.Lock()
	r.revoked = append(r.revoked, l)
	r.mu.Unlock()
}
func (r *recState) Closed() { r.mu.Lock(); r.closed++; r.mu.Unlock() }
func (r *recState) snapshot() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.revoked...), r.closed
}

func TestSDKSessionHooks(t *testing.T) {
	h := newWorkerHarness(t, true)
	h.w.sdkSess.Store(&h.srv.session) // as Spawn wires it
	rec := &recState{}
	h.srv.session.State(func() SDKSessionState { return rec })
	ctx, _ := testCtx(t)

	done := make(chan error, 1)
	go func() { _, _, err := h.w.Dispatch(ctx, []byte(`{}`)); done <- err }()
	_, lease := h.adapterFrames(t, 2)
	if got, _ := rec.snapshot(); len(got) != 0 {
		t.Fatalf("revoked while the dispatch is live: %v", got)
	}
	h.reply(t, `{"type":"response","statusCode":200}`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch end blocked")
	}
	if got, _ := rec.snapshot(); len(got) != 1 || got[0] != lease {
		t.Fatalf("LeaseRevoked = %v, want [%s]", got, lease)
	}

	// a superseding dispatch (beginLease swap) also reports the old lease
	st := newDispatchState(context.Background())
	h.w.disp.Store(st)
	h.w.beginLease(ctx)
	if got, _ := rec.snapshot(); len(got) != 2 || got[1] != st.info.Lease {
		t.Fatalf("superseded lease not reported: %v", got)
	}

	// the sandbox end going away ends serve(), which closes the session once
	_ = h.client.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, n := rec.snapshot(); n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Closed not called after the channel ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.srv.closeConn() // idempotent
	if _, n := rec.snapshot(); n != 1 {
		t.Fatalf("Closed called %d times, want 1", n)
	}
	// after close the session refuses new state
	if h.srv.session.State(func() SDKSessionState { return &recState{} }) != nil {
		t.Fatal("State after close must be nil")
	}
}
