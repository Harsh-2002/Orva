package server

import (
	"log/slog"
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/google/uuid"
)

func purgeManagedBuildCache(db *database.Database, reg *registry.Registry, manager *pool.Manager, dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		reqID := r.Header.Get("X-Request-ID")
		missing := func() { respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID) }
		id := r.PathValue("fn_id")
		if _, err := uuid.Parse(id); err != nil {
			fn, err := db.GetFunctionByName(id)
			if err != nil {
				missing()
				return
			}
			id = fn.ID
		}
		u, err := uuid.Parse(id)
		if err != nil || u.String() != id || u.Variant() != uuid.RFC4122 || u.Version() != 7 {
			missing()
			return
		}
		if _, err := db.GetFunction(id); err != nil {
			missing()
			return
		}

		buildLock := manager.FunctionLock(id)
		buildLock.Lock()
		defer buildLock.Unlock()
		ownerLock := reg.MutationLock(id)
		ownerLock.Lock()
		defer ownerLock.Unlock()
		fn, err := db.GetFunction(id)
		if err != nil || fn.Status == database.FunctionStatusDeleting {
			missing()
			return
		}
		ref, ok := auth.OperatorFromContext(ctx)
		if !ok || db.CheckOperatorPermission(ctx, ref, string(managedWrite)) != nil {
			respond.Error(w, http.StatusForbidden, "FORBIDDEN", "Current operator authority required", reqID)
			return
		}
		if err := builder.PurgeBuildCache(dataDir, id); err != nil {
			slog.Warn("purge managed build cache failed", "fn", id, "err", err)
			respond.Error(w, http.StatusInternalServerError, "INTERNAL", "failed to purge build cache", reqID)
			return
		}
		respond.JSON(w, http.StatusOK, map[string]string{"status": "purged", "id": id})
	}
}
