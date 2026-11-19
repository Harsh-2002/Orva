package handlers

import (
	"io"
	"net/http"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

func (h *FunctionHandler) deployInlineIAM(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-ID")
	if h.DeployIAM == nil {
		respond.Error(w, http.StatusServiceUnavailable, "UNAVAILABLE", "managed deployment unavailable", reqID)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, builder.MaxPublicationRequestBytes+1))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid managed deployment input", reqID)
		return
	}
	in, err := builder.DecodePublicationInline(raw, false)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "INVALID_JSON", "invalid managed deployment input", reqID)
		return
	}
	owner, ok := h.resolveFnID(r.PathValue("fn_id"))
	if !ok {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "function not found", reqID)
		return
	}
	id, err := h.DeployIAM.Inline(r.Context(), owner, in)
	if err != nil {
		respond.Error(w, http.StatusForbidden, "FORBIDDEN", "managed deployment denied; fresh submission required", reqID)
		return
	}
	w.Header().Set("Location", "/api/v1/deployments/"+id)
	respond.JSON(w, http.StatusAccepted, map[string]string{"deployment_id": id, "function_id": owner, "status": "queued"})
}
