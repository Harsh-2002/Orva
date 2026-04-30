package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server/handlers/respond"
)

// WebhooksHandler exposes operator-managed webhook subscription CRUD
// plus delivery introspection. The actual HTTP delivery to subscribers
// is done by the scheduler's webhookLoop in internal/scheduler.
type WebhooksHandler struct {
	DB *database.Database
}

// createRequest is the POST /webhooks shape. Secret is generated
// server-side; clients can't supply their own.
type createRequest struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled,omitempty"`
}

type updateRequest struct {
	Name    *string  `json:"name,omitempty"`
	URL     *string  `json:"url,omitempty"`
	Events  []string `json:"events,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

// List handles GET /api/v1/webhooks. The plaintext secret is never
// returned — only the preview (first 8 chars) for at-a-glance recall.
func (h *WebhooksHandler) List(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	subs, err := h.DB.ListEventSubscriptions()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "list webhooks failed: "+err.Error(), reqID)
		return
	}
	if subs == nil {
		subs = []*database.EventSubscription{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
}

// Create handles POST /api/v1/webhooks. Returns the freshly minted
// secret plaintext ONCE in the response body — operators must capture
// it now, since GET will only show the preview after.
func (h *WebhooksHandler) Create(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", "name is required", reqID)
		return
	}
	if err := validateWebhookURL(req.URL); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}
	events := normalizeEvents(req.Events)
	if err := validateEventNames(events); err != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	secret := database.NewWebhookSecret()
	sub := &database.EventSubscription{
		Name:    req.Name,
		URL:     req.URL,
		Secret:  secret,
		Events:  events,
		Enabled: enabled,
	}
	if err := h.DB.InsertEventSubscription(sub); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "create webhook failed: "+err.Error(), reqID)
		return
	}
	// Echo the plaintext secret one time in a top-level field so the
	// dashboard can render the copy-once UX. The embedded `subscription`
	// object also carries the redacted preview for ongoing display.
	sub.SecretPreview = secret[:8] + "…"
	respond.JSON(w, http.StatusCreated, map[string]any{
		"subscription": sub,
		"secret":       secret,
	})
}

// Get handles GET /api/v1/webhooks/{id}. Plaintext secret never returned.
func (h *WebhooksHandler) Get(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	sub, err := h.DB.GetEventSubscription(id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook not found", reqID)
		return
	}
	respond.JSON(w, http.StatusOK, sub)
}

// Update handles PUT /api/v1/webhooks/{id}. Any subset of {name, url,
// events, enabled} may be supplied; omitted fields keep prior values.
// The secret cannot be rotated — operators rotate by deleting and
// re-creating the subscription (mirrors api_keys flow).
func (h *WebhooksHandler) Update(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	sub, err := h.DB.GetEventSubscription(id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook not found", reqID)
		return
	}
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body", reqID)
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", "name cannot be empty", reqID)
			return
		}
		sub.Name = name
	}
	if req.URL != nil {
		u := strings.TrimSpace(*req.URL)
		if err := validateWebhookURL(u); err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
			return
		}
		sub.URL = u
	}
	if req.Events != nil {
		events := normalizeEvents(req.Events)
		if err := validateEventNames(events); err != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", err.Error(), reqID)
			return
		}
		sub.Events = events
	}
	if req.Enabled != nil {
		sub.Enabled = *req.Enabled
	}
	if err := h.DB.UpdateEventSubscription(sub); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "update webhook failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, sub)
}

func (h *WebhooksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if _, err := h.DB.GetEventSubscription(id); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook not found", reqID)
		return
	}
	if err := h.DB.DeleteEventSubscription(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "delete webhook failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// Test handles POST /api/v1/webhooks/{id}/test. Inserts a synthetic
// `webhook.test` delivery so the operator can verify URL + secret
// without waiting for a real event. The scheduler picks it up on the
// next tick (≤ 5s).
func (h *WebhooksHandler) Test(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	sub, err := h.DB.GetEventSubscription(id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook not found", reqID)
		return
	}
	envelope := map[string]any{
		"id":       "evt_test_" + database.NewSubscriptionID()[4:],
		"type":     "webhook.test",
		"fired_at": time.Now().UTC().Format(time.RFC3339Nano),
		"data": map[string]any{
			"subscription_id": sub.ID,
			"name":            sub.Name,
			"message":         "synthetic test event from /api/v1/webhooks/{id}/test",
		},
	}
	body, _ := json.Marshal(envelope)
	d := &database.WebhookDelivery{
		SubscriptionID: sub.ID,
		EventName:      "webhook.test",
		Payload:        body,
		Status:         "pending",
		MaxAttempts:    1, // test events shouldn't retry on flaky URLs
	}
	if err := h.DB.InsertDelivery(d); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "queue test delivery failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusAccepted, map[string]string{
		"status":      "queued",
		"delivery_id": d.ID,
	})
}

// ListDeliveries handles GET /api/v1/webhooks/{id}/deliveries.
func (h *WebhooksHandler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	deliveries, err := h.DB.ListDeliveriesForSubscription(id, 100)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "list deliveries failed: "+err.Error(), reqID)
		return
	}
	if deliveries == nil {
		deliveries = []*database.WebhookDelivery{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}

// RetryDelivery handles POST /api/v1/webhooks/deliveries/{id}/retry.
// Resets a terminal delivery back to pending with attempts=0.
func (h *WebhooksHandler) RetryDelivery(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	id := r.PathValue("id")
	if _, err := h.DB.GetDelivery(id); err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "delivery not found", reqID)
		return
	}
	if err := h.DB.RetryDelivery(id); err != nil {
		respond.Error(w, http.StatusInternalServerError, "INTERNAL", "retry failed: "+err.Error(), reqID)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{"status": "pending", "id": id})
}

// ── helpers ────────────────────────────────────────────────────────

// allowedEvents is the closed set of event names operators can
// subscribe to. "*" is special — matches everything. New events MUST
// be added here so a typo doesn't silently create a subscription that
// never fires.
var allowedEvents = map[string]struct{}{
	"*":                    {},
	"deployment.succeeded": {},
	"deployment.failed":    {},
	"function.created":     {}, // POST /functions
	"function.updated":     {}, // PUT /functions/{id} (excludes silent status flips)
	"function.deleted":     {},
	"execution.error":      {},
	"cron.failed":          {},
	"job.succeeded":        {},
	"job.failed":           {},
	"webhook.test":         {}, // never used in subscriptions, but harmless
}

func normalizeEvents(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	if len(out) == 0 {
		out = []string{"*"}
	}
	return out
}

func validateEventNames(events []string) error {
	for _, e := range events {
		if _, ok := allowedEvents[e]; !ok {
			return errors.New("unknown event name: " + e + " (see GET /api/v1/webhooks/events for catalog)")
		}
	}
	return nil
}

// validateWebhookURL ensures we have a full http(s) URL pointing to a
// hostname (not a bare path). The delivery worker uses the operator's
// firewall blocklist at egress so we don't re-validate against private
// ranges here — operators may legitimately POST to internal endpoints.
func validateWebhookURL(s string) error {
	if s == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(s)
	if err != nil {
		return errors.New("invalid url: " + err.Error())
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must be http:// or https://")
	}
	if u.Host == "" {
		return errors.New("url must include a host")
	}
	return nil
}
