package server

import (
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

func mountManagedFixtures(mux *http.ServeMux, db *database.Database, reg *registry.Registry, ops *managedRouteDeps) {
	fixtures := &handlers.FixtureHandler{DB: db, Registry: reg}
	gateRoute(mux, db, ops, "GET /api/v1/functions/{fn_id}/fixtures", managedRead, fixtures.List)
	gateRoute(mux, db, ops, "POST /api/v1/functions/{fn_id}/fixtures", managedWrite, fixtures.Create)
	gateRoute(mux, db, ops, "GET /api/v1/functions/{fn_id}/fixtures/{name}", managedRead, fixtures.Get)
	gateRoute(mux, db, ops, "PUT /api/v1/functions/{fn_id}/fixtures/{name}", managedWrite, fixtures.Upsert)
	gateRoute(mux, db, ops, "DELETE /api/v1/functions/{fn_id}/fixtures/{name}", managedWrite, fixtures.Delete)
}
