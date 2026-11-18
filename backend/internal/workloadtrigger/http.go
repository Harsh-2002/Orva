package workloadtrigger

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

func HTTPError(w http.ResponseWriter, err error) {
	status, code, msg := 503, "IAM_UNAVAILABLE", "Exposure mutation unavailable"
	switch {
	case errors.Is(err, database.ErrOperatorDenied):
		status, code, msg = 403, "FORBIDDEN", "Current operator authority required"
	case errors.Is(err, database.ErrExposureConflict):
		status, code, msg = 409, "IAM_CONFLICT", "Exposure or authority changed; inspect again"
	case errors.Is(err, database.ErrExposureInvalid):
		status, code, msg = 400, "VALIDATION", "Invalid exposure request"
	}
	respond.Error(w, status, code, msg, "")
}

// HTTP receives trusted URL selectors from the optional handler dependency.
func (s *Service) HTTP(w http.ResponseWriter, r *http.Request, op, owner, key string) {
	w.Header().Set("Cache-Control", "no-store")
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		HTTPError(w, database.ErrExposureInvalid)
		return
	}
	m, err := object(raw)
	if err != nil {
		HTTPError(w, err)
		return
	}
	inject := map[string]string{}
	if strings.HasPrefix(op, "inbound_") {
		inject["function_id"] = owner
		if op != "inbound_create" {
			inject["id"] = key
		}
	}
	if op == "route_delete" {
		inject["path"] = key
	}
	for k, v := range inject {
		if m[k] != nil {
			HTTPError(w, database.ErrExposureInvalid)
			return
		}
		m[k], _ = json.Marshal(v)
	}
	body, _ := json.Marshal(m)
	out, err := s.Execute(r.Context(), op, body)
	if err != nil {
		HTTPError(w, err)
		return
	}
	status := http.StatusOK
	if op == "route_set" || op == "inbound_create" {
		status = http.StatusCreated
	}
	if op == "inbound_update" {
		respond.JSON(w, status, out["inbound_webhook"])
		return
	}
	respond.JSON(w, status, out)
}

// HTTPAdapter installs inspection only when explicitly registered by a fixture.
type HTTPAdapter struct {
	Service      *Service
	Authenticate func(*http.Request) (*auth.Principal, error)
}

func (h *HTTPAdapter) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/exposures", h)
}
func (h *HTTPAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || h.Service == nil || h.Authenticate == nil {
		HTTPError(w, database.ErrExposureUnavailable)
		return
	}
	p, err := h.Authenticate(r)
	if err != nil || p == nil || p.Kind == auth.KindChannel {
		HTTPError(w, database.ErrOperatorDenied)
		return
	}
	raw, _ := json.Marshal(map[string]string{"kind": r.URL.Query().Get("kind"), "key": r.URL.Query().Get("key"), "function_id": r.URL.Query().Get("function_id")})
	out, err := h.Service.Execute(auth.WithOperator(r.Context(), p), "inspect", raw)
	if err != nil {
		HTTPError(w, err)
		return
	}
	respond.JSON(w, 200, out)
}
