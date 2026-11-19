package handlers

import (
	"errors"
	"io"
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

func (h *FunctionHandler) rollbackIAM(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	if h.DeployIAM == nil {
		respond.Error(w, http.StatusServiceUnavailable, "UNAVAILABLE", "managed rollback unavailable", reqID)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, builder.MaxRollbackRequestBytes+1))
	in, decodeErr := builder.DecodeRollbackInput(raw, r.PathValue("fn_id"), false)
	if err != nil || decodeErr != nil {
		respond.Error(w, http.StatusBadRequest, "VALIDATION", builder.ErrRollbackInput.Error(), reqID)
		return
	}
	out, err := h.DeployIAM.PromoteRollback(r.Context(), in.FunctionID, in.DeploymentID)
	switch {
	case err == nil:
		respond.JSON(w, http.StatusOK, out)
	case errors.Is(err, database.ErrOperatorDenied):
		respond.Error(w, http.StatusForbidden, "FORBIDDEN", "managed rollback denied", reqID)
	case errors.Is(err, database.ErrIAMConflict), errors.Is(err, database.ErrWorkloadArtifactConflict),
		errors.Is(err, database.ErrFunctionDeleting), errors.Is(err, database.ErrIAMFunctionNotFound):
		respond.Error(w, http.StatusConflict, "CONFLICT", "managed rollback target or current state is ineligible", reqID)
	default:
		respond.Error(w, http.StatusServiceUnavailable, "UNAVAILABLE", "managed rollback unavailable", reqID)
	}
}
