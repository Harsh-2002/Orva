package server

import (
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

func mountManagedOperatorKV(mux *http.ServeMux, db *database.Database, reg *registry.Registry, ops *managedRouteDeps) {
	h := &handlers.KVOperatorHandler{DB: db, Registry: reg}
	const path = "/api/v1/functions/{fn_id}/kv"
	gateRoute(mux, db, ops, "GET "+path, managedRead, h.List)
	gateRoute(mux, db, ops, "GET "+path+"/{key}", managedRead, h.Get)
	gateRoute(mux, db, ops, "PUT "+path+"/{key}", managedWrite, h.Put)
	gateRoute(mux, db, ops, "DELETE "+path+"/{key}", managedWrite, h.Delete)
	gateRoute(mux, db, ops, "POST "+path+"/{key}/incr", managedWrite, h.Incr)
	gateRoute(mux, db, ops, "POST "+path+"/{key}/cas", managedWrite, h.CAS)
}
