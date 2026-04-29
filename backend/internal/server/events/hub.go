// Package events implements a single, in-process server-sent events broker.
// Producers (the metrics ticker, the async writer, the build queue) call
// Hub.Publish to fan out a typed event to every connected client. The HTTP
// handler in this package subscribes per-connection and writes events to
// the wire as text/event-stream frames.
//
// One unified stream:  GET /api/v1/events
//
// Why a single stream instead of one per concern:
//   - Browsers cap parallel connections per origin (~6 on HTTP/1.1). One
//     connection regardless of how many UI panels need live data leaves
//     headroom for the rest of the app's API calls.
//   - Subscribers filter on the client by `event:` field, which is cheap.
//   - One reconnect-storm to handle, not three.
//
// Backpressure is local to each subscriber: a 32-deep buffered channel.
// When full, the hub drops the event for that subscriber (it's behind)
// rather than blocking the producer. Slow clients reconnect on the next
// publish — far better than freezing the whole platform on a hung client.
package events

import (
	"sync"
)

// Event is the wire envelope. Type becomes the SSE `event:` field; Data is
// JSON-serialized into the `data:` field by the HTTP handler.
type Event struct {
	Type string
	Data any
}

// Event types. Keep these stable — the frontend filters on them by name.
const (
	TypeMetrics    = "metrics"    // periodic system snapshot, ~5s cadence
	TypeExecution  = "execution"  // new invocation row committed to DB
	TypeDeployment = "deployment" // build queue phase / status change
	TypeFunction   = "function"   // function created / updated / deleted
)

// Hub is the in-process broker. Zero value is not usable — call NewHub.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[*subscription]struct{}
}

type subscription struct {
	ch chan Event
}

// NewHub returns a ready-to-use Hub.
func NewHub() *Hub {
	return &Hub{subscribers: make(map[*subscription]struct{})}
}

// subscribe registers a new client. The returned channel is buffered (32);
// if the producer outpaces the consumer, the hub drops events for that
// subscriber on Publish (see comment there). Caller MUST call unsubscribe
// when done — typically with `defer`.
func (h *Hub) subscribe() *subscription {
	s := &subscription{ch: make(chan Event, 32)}
	h.mu.Lock()
	h.subscribers[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// unsubscribe removes a client and closes its channel. Safe to call twice.
func (h *Hub) unsubscribe(s *subscription) {
	h.mu.Lock()
	if _, ok := h.subscribers[s]; ok {
		delete(h.subscribers, s)
		close(s.ch)
	}
	h.mu.Unlock()
}

// Publish fans out an event to every current subscriber. Slow subscribers
// drop the event rather than blocking the producer. This is intentional:
// metrics/execution/deployment events are best-effort live data — if a
// client misses one, the next will arrive shortly. The browser's
// EventSource auto-reconnect rebuilds state on disconnect.
func (h *Hub) Publish(t string, data any) {
	ev := Event{Type: t, Data: data}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subscribers {
		select {
		case s.ch <- ev:
		default:
			// Channel full — slow client. Skip; they'll catch up on the next
			// event or reconnect.
		}
	}
}

// SubscriberCount is exposed for metrics / debugging. Cheap.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
