package server

import (
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

func mountManagedWebhooks(mux *http.ServeMux, db *database.Database, ops *managedRouteDeps) {
	webhooks := &handlers.WebhooksHandler{DB: db}
	gateRoute(mux, db, ops, "GET /api/v1/webhooks", managedRead, webhooks.List)
	gateRoute(mux, db, ops, "POST /api/v1/webhooks", managedWrite, webhooks.Create)
	gateRoute(mux, db, ops, "GET /api/v1/webhooks/{id}", managedRead, webhooks.Get)
	gateRoute(mux, db, ops, "PUT /api/v1/webhooks/{id}", managedWrite, webhooks.Update)
	gateRoute(mux, db, ops, "DELETE /api/v1/webhooks/{id}", managedWrite, webhooks.Delete)
	gateRoute(mux, db, ops, "POST /api/v1/webhooks/{id}/test", managedWrite, webhooks.Test)
	gateRoute(mux, db, ops, "GET /api/v1/webhooks/{id}/deliveries", managedRead, webhooks.ListDeliveries)
	gateRoute(mux, db, ops, "POST /api/v1/webhooks/deliveries/{id}/retry", managedWrite, webhooks.RetryDelivery)
}
