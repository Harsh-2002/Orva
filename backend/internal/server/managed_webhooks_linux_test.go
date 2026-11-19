package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestManagedOutboundWebhooks(t *testing.T) {
	f := newManagedOpsFixture(t)
	reader, _ := f.key("reader", `["read"]`)
	writer, _ := f.key("writer", `["write"]`)
	const base = "/api/v1/webhooks"
	read := func(path string, dst any) {
		t.Helper()
		f.expect(http.MethodGet, path, "", "", http.StatusUnauthorized)
		f.expect(http.MethodGet, path, writer, "", http.StatusForbidden)
		rec := f.expect(http.MethodGet, path, reader, "", http.StatusOK)
		managedMust(t, json.Unmarshal(rec.Body.Bytes(), dst))
	}
	write := func(method, path, body string, status int, dst any) {
		t.Helper()
		f.expect(method, path, "", body, http.StatusUnauthorized)
		f.expect(method, path, reader, body, http.StatusForbidden)
		rec := f.expect(method, path, writer, body, status)
		if dst != nil {
			managedMust(t, json.Unmarshal(rec.Body.Bytes(), dst))
		}
	}

	// The fixture has no delivery worker. The loopback URL is stored but never dialed.
	var created struct {
		Subscription database.EventSubscription `json:"subscription"`
	}
	write(http.MethodPost, base, `{"name":"test hook","url":"http://127.0.0.1:1/events","events":["job.failed"]}`,
		http.StatusCreated, &created)
	id := created.Subscription.ID
	if id == "" {
		t.Fatal("create did not return a subscription ID")
	}
	path := base + "/" + id
	var listed struct {
		Subscriptions []database.EventSubscription `json:"subscriptions"`
	}
	read(base, &listed)
	if len(listed.Subscriptions) != 1 || listed.Subscriptions[0].ID != id {
		t.Fatal("list did not return the created subscription")
	}
	write(http.MethodPut, path, `{"name":"updated hook"}`, http.StatusOK, nil)
	var got database.EventSubscription
	read(path, &got)
	if got.ID != id || got.Name != "updated hook" {
		t.Fatal("get did not return the updated subscription")
	}

	var queued struct {
		Status     string `json:"status"`
		DeliveryID string `json:"delivery_id"`
	}
	write(http.MethodPost, path+"/test", "", http.StatusAccepted, &queued)
	if queued.Status != "queued" || queued.DeliveryID == "" {
		t.Fatal("test did not acknowledge a queued delivery")
	}
	var deliveries struct {
		Deliveries []database.WebhookDelivery `json:"deliveries"`
	}
	read(path+"/deliveries", &deliveries)
	if len(deliveries.Deliveries) != 1 {
		t.Fatal("expected one test delivery")
	}
	delivery := deliveries.Deliveries[0]
	if delivery.ID != queued.DeliveryID || delivery.SubscriptionID != id ||
		delivery.EventName != "webhook.test" || delivery.Status != "pending" || delivery.MaxAttempts != 1 {
		t.Fatal("expected a pending single-attempt webhook.test delivery")
	}
	managedMust(t, f.db.MarkDeliveryFailure(delivery.ID, "fixture failure", 1, 1, 0))
	write(http.MethodPost, base+"/deliveries/"+delivery.ID+"/retry", "", http.StatusOK, nil)
	retried, err := f.db.GetDelivery(delivery.ID)
	managedMust(t, err)
	if retried.Status != "pending" {
		t.Fatal("retry did not return the failed delivery to the pending queue")
	}

	write(http.MethodDelete, path, "", http.StatusOK, nil)
	f.expect(http.MethodGet, path, reader, "", http.StatusNotFound)
}
