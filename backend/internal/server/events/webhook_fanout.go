package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	gonanoid "github.com/matoous/go-nanoid/v2"
)

// WebhookFanout subscribes to the in-process Hub and translates each
// fan-out event into webhook_deliveries rows for the operator's
// configured subscriptions. The actual HTTP POST happens later via the
// scheduler's delivery loop — this listener's only job is to write
// rows so a slow upstream URL never holds up internal event flow.
//
// One listener per server process. Started by server.New() right after
// the Hub is constructed; cancelled by the parent context on shutdown.
type WebhookFanout struct {
	db  *database.Database
	hub *Hub
}

// NewWebhookFanout returns a ready listener. Start to begin consuming.
func NewWebhookFanout(db *database.Database, hub *Hub) *WebhookFanout {
	return &WebhookFanout{db: db, hub: hub}
}

// Start kicks off the consumer goroutine. Returns immediately.
// ctx cancellation drains the subscriber and exits.
func (w *WebhookFanout) Start(ctx context.Context) {
	if w == nil || w.hub == nil || w.db == nil {
		return
	}
	sub := w.hub.subscribe()
	go func() {
		defer w.hub.unsubscribe(sub)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub.ch:
				if !ok {
					return
				}
				w.handle(ev)
			}
		}
	}()
	slog.Info("webhook fanout started")
}

// handle translates one Hub event into 0..N webhook_deliveries rows.
// Hub events that don't map to any qualified system event name are
// dropped silently — those are dashboard-only signals.
func (w *WebhookFanout) handle(ev Event) {
	name, payload := qualifyEvent(ev)
	if name == "" {
		return
	}
	subs, err := w.db.MatchingSubscriptions(name)
	if err != nil {
		slog.Warn("webhook fanout: matching subscriptions lookup failed",
			"event", name, "err", err)
		return
	}
	if len(subs) == 0 {
		return
	}

	// One envelope per delivery — keeps every webhook_deliveries row
	// independently replayable. fired_at is the moment the platform
	// observed the event, not when the delivery is attempted.
	envelopeID, _ := gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 14)
	envelope := map[string]any{
		"id":       "evt_" + envelopeID,
		"type":     name,
		"fired_at": time.Now().UTC().Format(time.RFC3339Nano),
		"data":     payload,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		slog.Warn("webhook fanout: envelope marshal failed", "err", err)
		return
	}

	for _, s := range subs {
		d := &database.WebhookDelivery{
			SubscriptionID: s.ID,
			EventName:      name,
			Payload:        body,
			Status:         "pending",
		}
		if err := w.db.InsertDelivery(d); err != nil {
			slog.Warn("webhook fanout: insert delivery failed",
				"sub", s.ID, "event", name, "err", err)
		}
	}
}

// qualifyEvent maps a Hub event Type + Data fields into a fully
// qualified webhook event name (e.g. "deployment" + status="failed"
// → "deployment.failed"). Returns "" for events we don't expose as
// webhooks. The payload returned is the original Hub data, untouched.
func qualifyEvent(ev Event) (string, any) {
	data, ok := ev.Data.(map[string]any)
	if !ok {
		return "", nil
	}
	switch ev.Type {
	case "deployment":
		switch data["status"] {
		case "succeeded":
			return "deployment.succeeded", data
		case "failed":
			return "deployment.failed", data
		}
	case "function":
		// registry publishes "created" on first insert, "updated" on
		// edits via PUT /functions/{id}, and "deleted" on removal.
		// Status flips during build use SetSilent and never reach
		// here — those are covered by deployment.* events.
		switch data["action"] {
		case "created":
			return "function.created", data
		case "updated":
			return "function.updated", data
		case "deleted":
			return "function.deleted", data
		}
	case "execution":
		// Only error-class executions; success-class fires every
		// invocation and would flood the queue.
		if data["status"] == "error" {
			return "execution.error", data
		}
	case "cron":
		if data["status"] == "failed" {
			return "cron.failed", data
		}
	case "job":
		switch data["status"] {
		case "succeeded":
			return "job.succeeded", data
		case "failed":
			return "job.failed", data
		}
	}
	return "", nil
}
