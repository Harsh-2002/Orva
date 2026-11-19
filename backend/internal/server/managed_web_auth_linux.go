package server

import (
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

func mountManagedWebAuth(mux *http.ServeMux, db *database.Database, ops *managedRouteDeps) {
	webAuth := &handlers.AuthHandler{DB: db, InvalidateSession: ops.InvalidateSession}
	mux.HandleFunc("GET /api/v1/auth/status", webAuth.Status)
	mux.HandleFunc("POST /api/v1/auth/onboard", webAuth.Onboard)
	mux.HandleFunc("POST /api/v1/auth/login", webAuth.Login)
	mux.HandleFunc("POST /api/v1/auth/logout", webAuth.Logout)
	mux.HandleFunc("GET /api/v1/auth/me", webAuth.Me)
	mux.HandleFunc("POST /api/v1/auth/refresh", webAuth.Refresh)
	gateSessionRoute(mux, db, ops, "POST /api/v1/auth/change-password", managedWrite, webAuth.ChangePassword)
}
