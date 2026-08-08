package events

import (
	"testing"
	"time"
)

// TestHubCloseUnblocksSubscribers is the M1 guard: Close() must close every
// live subscriber channel so the SSE handler loop (which selects on that
// channel) exits promptly instead of holding http.Server.Shutdown open until
// its 30s deadline.
func TestHubCloseUnblocksSubscribers(t *testing.T) {
	h := NewHub()
	sub := h.subscribe()

	done := make(chan struct{})
	go func() {
		// Mirrors the handler loop's read on sub.ch: unblocks when Close
		// closes the channel.
		for range sub.ch {
		}
		close(done)
	}()

	h.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not unblock within 2s after Hub.Close()")
	}

	// Idempotent + Publish is a no-op after Close (must not panic on a closed
	// channel).
	h.Close()
	h.Publish("execution", map[string]any{"x": 1})

	if n := h.SubscriberCount(); n != 0 {
		t.Errorf("SubscriberCount after Close = %d, want 0", n)
	}
}

// TestSubscribeAfterCloseIsClosed: a connection that races in after Close must
// get an already-closed channel so its handler loop exits at once.
func TestSubscribeAfterCloseIsClosed(t *testing.T) {
	h := NewHub()
	h.Close()

	sub := h.subscribe()
	select {
	case _, ok := <-sub.ch:
		if ok {
			t.Fatal("expected closed channel, got a value")
		}
	case <-time.After(time.Second):
		t.Fatal("subscribe() after Close did not return a closed channel")
	}
}
