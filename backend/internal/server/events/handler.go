package events

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Handler returns the http.HandlerFunc for GET /api/v1/events. It opens
// an SSE stream, subscribes the connection to the hub, and writes events
// until the client disconnects or ctx fires.
//
// Heartbeat: every 15 s a comment line (`:keepalive`) is written to keep
// reverse-proxy idle timers from closing the connection. Comments are
// invisible to EventSource consumers.
//
// Auth: this handler is mounted under the standard auth middleware; the
// browser's EventSource sends the session cookie automatically. API-key
// clients don't use SSE — they keep polling /api/v1/system/metrics.json.
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		// SSE response headers. X-Accel-Buffering disables nginx's response
		// buffering so events arrive in real time even behind a proxy.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		// Send a sentinel so the client knows the stream is open before
		// any real events arrive (helpful for tests / connection-state UI).
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		sub := h.subscribe()
		defer h.unsubscribe(sub)

		ctx := r.Context()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				// Heartbeat. SSE comments start with ":" and are ignored
				// by the browser, but they keep the TCP connection alive.
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()

			case ev, ok := <-sub.ch:
				if !ok {
					return // hub closed our channel (server shutting down)
				}
				body, err := json.Marshal(ev.Data)
				if err != nil {
					// Don't fail the stream on a marshal error — skip this
					// event and keep going.
					continue
				}
				// Standard SSE frame: `event: <type>\ndata: <json>\n\n`.
				if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, body); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
