//go:build linux

package server

import (
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/firewall"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

// mountManagedFirewall exposes the sandbox egress policy over the manager the
// managed server already owns, so an edit here is the generation a managed
// worker loads. Every route needs admin, as on the legacy router: one rule
// change widens or narrows what every egress function can reach. A nil manager
// leaves them unmounted instead of answering from a manager that does not
// exist — "nothing is blocked" is indistinguishable from a healthy empty policy.
func mountManagedFirewall(mux *http.ServeMux, db *database.Database, fw *firewall.Manager, ops *managedRouteDeps) {
	if fw == nil {
		return
	}
	h := &handlers.FirewallHandler{DB: db, Manager: fw}
	gateRoute(mux, db, ops, "GET /api/v1/firewall/rules", managedAdmin, h.List)
	gateRoute(mux, db, ops, "POST /api/v1/firewall/rules", managedAdmin, h.Create)
	gateRoute(mux, db, ops, "PUT /api/v1/firewall/rules/{rule_id}", managedAdmin, h.Update)
	gateRoute(mux, db, ops, "DELETE /api/v1/firewall/rules/{rule_id}", managedAdmin, h.Delete)
	gateRoute(mux, db, ops, "GET /api/v1/firewall/status", managedAdmin, h.Status)
	gateRoute(mux, db, ops, "POST /api/v1/firewall/resolve", managedAdmin, h.Resolve)
	gateRoute(mux, db, ops, "GET /api/v1/firewall/dns", managedAdmin, h.GetDNS)
	gateRoute(mux, db, ops, "PUT /api/v1/firewall/dns", managedAdmin, h.PutDNS)
}
