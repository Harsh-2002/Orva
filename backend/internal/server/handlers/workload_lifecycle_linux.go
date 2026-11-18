//go:build linux

package handlers

import (
	"errors"
	"io"
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

// WorkloadLifecycleHandler is registered explicitly, never by the serving router.
type WorkloadLifecycleHandler struct {
	Publication  *builder.PublicationQueue
	Authenticate func(*http.Request) (*auth.Principal, error)
}

func (h *WorkloadLifecycleHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/functions/{id}/pause", h.mutate(false))
	mux.HandleFunc("POST /api/v1/functions/{id}/resume", h.mutate(true))
}

func (h *WorkloadLifecycleHandler) mutate(resume bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		reqID := r.Header.Get("X-Request-ID")
		if h == nil || h.Publication == nil || h.Authenticate == nil {
			respond.Error(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Managed workload lifecycle unavailable", reqID)
			return
		}
		p, err := h.Authenticate(r)
		if err != nil || p == nil || p.Kind == auth.KindChannel {
			respond.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Operator authentication required", reqID)
			return
		}
		ctx := auth.WithOperator(r.Context(), p)
		if _, ok := auth.OperatorFromContext(ctx); !ok {
			respond.Error(w, http.StatusForbidden, "FORBIDDEN", "Current operator authority required", reqID)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, builder.MaxLifecycleRequestBytes))
		in, decodeErr := builder.DecodeLifecycleInput(raw, r.PathValue("id"), false)
		if err != nil || decodeErr != nil {
			respond.Error(w, http.StatusBadRequest, "VALIDATION", builder.ErrLifecycleInput.Error(), reqID)
			return
		}
		out, err := h.Publication.ExecuteLifecycle(ctx, in, resume)
		switch {
		case err == nil:
			respond.JSON(w, http.StatusOK, out)
		case errors.Is(err, database.ErrOperatorDenied):
			respond.Error(w, http.StatusForbidden, "FORBIDDEN", "Current operator authority required", reqID)
		case errors.Is(err, builder.ErrLifecycleConflict):
			respond.Error(w, http.StatusConflict, "CONFLICT", builder.ErrLifecycleConflict.Error(), reqID)
		default:
			respond.Error(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Managed workload lifecycle unavailable", reqID)
		}
	}
}
